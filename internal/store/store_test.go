package store

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wgui/internal/config"
)

const day = int64(24 * time.Hour / time.Millisecond)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func mustPeer(t *testing.T, s *Store, p *Peer) *Peer {
	t.Helper()
	if p.Role == "" {
		p.Role = "user"
	}
	p.PublicKey = p.ID
	if err := s.CreatePeer(p); err != nil {
		t.Fatalf("create peer %s: %v", p.Name, err)
	}
	return p
}

func adminScope() Scope { return Scope{PeerID: "admin", Role: "admin"} }

func TestCreateAndGetPeer(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UnixMilli()

	mustPeer(t, s, &Peer{ID: "k1", Name: "alice", AllowedIPs: "10.0.0.2/32"})

	got, err := s.GetPeer("k1", now)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "alice" || got.Status != StatusActive {
		t.Fatalf("got %+v, want name alice with status active", got)
	}
	if got.AllowedUsage != 0 || got.ExpiresAt != 0 {
		t.Fatal("a peer created without limits must be unlimited and never expire")
	}

	if _, err := s.GetPeer("missing", now); err != ErrNotFound {
		t.Fatalf("missing peer returned %v, want ErrNotFound", err)
	}
}

func TestDuplicatesRejected(t *testing.T) {
	s := newTestStore(t)
	mustPeer(t, s, &Peer{ID: "k1", Name: "alice", AllowedIPs: "10.0.0.2/32"})

	err := s.CreatePeer(&Peer{ID: "k2", PublicKey: "k2", Name: "alice", AllowedIPs: "10.0.0.3/32", Role: "user"})
	if err != ErrDuplicate {
		t.Fatalf("duplicate name gave %v, want ErrDuplicate", err)
	}
	err = s.CreatePeer(&Peer{ID: "k3", PublicKey: "k3", Name: "bob", AllowedIPs: "10.0.0.2/32", Role: "user"})
	if err != ErrDuplicate {
		t.Fatalf("duplicate address gave %v, want ErrDuplicate", err)
	}
}

func TestStatusDerivation(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UnixMilli()

	mustPeer(t, s, &Peer{ID: "active", Name: "active", AllowedIPs: "10.0.0.2/32", ExpiresAt: now + day})
	mustPeer(t, s, &Peer{ID: "expired", Name: "expired", AllowedIPs: "10.0.0.3/32", ExpiresAt: now - day})
	mustPeer(t, s, &Peer{ID: "manual", Name: "manual", AllowedIPs: "10.0.0.4/32", ManuallyDisabled: true})
	mustPeer(t, s, &Peer{ID: "quota", Name: "quota", AllowedIPs: "10.0.0.5/32", AllowedUsage: 100})
	mustPeer(t, s, &Peer{ID: "forever", Name: "forever", AllowedIPs: "10.0.0.6/32"})

	// Push the quota peer over its allowance the same way the engine would.
	if err := s.ApplyUsageDeltas([]UsageDelta{{PeerID: "quota", TX: 60, RX: 60}}); err != nil {
		t.Fatalf("apply deltas: %v", err)
	}

	want := map[string]string{
		"active":  StatusActive,
		"expired": StatusExpired,
		"manual":  StatusDisabled,
		"quota":   StatusQuota,
		"forever": StatusActive,
	}
	for id, expect := range want {
		p, err := s.GetPeer(id, now)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		if p.Status != expect {
			t.Errorf("peer %s status = %s, want %s", id, p.Status, expect)
		}
	}
}

func TestGroupQuotaAppliesToMembers(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UnixMilli()

	g := &Group{Name: "team", AllowedUsage: 1000}
	if err := s.CreateGroup(g); err != nil {
		t.Fatalf("create group: %v", err)
	}

	mustPeer(t, s, &Peer{ID: "a", Name: "a", AllowedIPs: "10.0.0.2/32", GroupID: g.ID})
	mustPeer(t, s, &Peer{ID: "b", Name: "b", AllowedIPs: "10.0.0.3/32", GroupID: g.ID})
	mustPeer(t, s, &Peer{ID: "outside", Name: "outside", AllowedIPs: "10.0.0.4/32"})

	// Neither member is over its own (unlimited) quota, but together they exceed
	// the group's, so both must be cut off while the outsider is untouched.
	if err := s.ApplyUsageDeltas([]UsageDelta{
		{PeerID: "a", TX: 400, RX: 200},
		{PeerID: "b", TX: 400, RX: 200},
	}); err != nil {
		t.Fatalf("apply deltas: %v", err)
	}

	for _, id := range []string{"a", "b"} {
		p, err := s.GetPeer(id, now)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		if p.Status != StatusQuota {
			t.Errorf("member %s status = %s, want %s", id, p.Status, StatusQuota)
		}
		if p.GroupUsage != 1200 {
			t.Errorf("member %s sees group usage %d, want 1200", id, p.GroupUsage)
		}
	}
	if p, _ := s.GetPeer("outside", now); p.Status != StatusActive {
		t.Errorf("non-member status = %s, want active", p.Status)
	}

	got, err := s.GetGroup(g.ID, now)
	if err != nil {
		t.Fatalf("get group: %v", err)
	}
	if got.Usage != 1200 || got.PeerCount != 2 || got.Status != StatusQuota {
		t.Errorf("group = usage %d, peers %d, status %s; want 1200, 2, quota",
			got.Usage, got.PeerCount, got.Status)
	}
}

func TestGroupExpiryDisablesMembers(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UnixMilli()

	g := &Group{Name: "expired", ExpiresAt: now - day}
	if err := s.CreateGroup(g); err != nil {
		t.Fatalf("create group: %v", err)
	}
	// The peer's own expiry is far in the future; the group's is what bites.
	mustPeer(t, s, &Peer{ID: "a", Name: "a", AllowedIPs: "10.0.0.2/32", GroupID: g.ID, ExpiresAt: now + 365*day})

	p, err := s.GetPeer("a", now)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if p.Status != StatusExpired {
		t.Fatalf("status = %s, want expired", p.Status)
	}
	if p.EffectiveExpiresAt() != g.ExpiresAt {
		t.Fatalf("effective expiry = %d, want the group's %d", p.EffectiveExpiresAt(), g.ExpiresAt)
	}
}

func TestUsageIsCountedPerServer(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UnixMilli()

	mustPeer(t, s, &Peer{ID: "a", Name: "a", AllowedIPs: "10.0.0.2/32", AllowedUsage: 1000})

	// What this server counted, the way the engine reports it.
	if err := s.ApplyUsageDeltas([]UsageDelta{{PeerID: "a", TX: 300, RX: 100}}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if p, _ := s.GetPeer("a", now); p.Usage != 400 || p.Status != StatusActive {
		t.Fatalf("own usage = %d, status %s; want 400 and active", p.Usage, p.Status)
	}

	// What another server counted for the same peer adds to it, and is what
	// takes the peer over a limit neither server would have breached alone.
	if err := s.ReplaceServerUsage("other", now, []ServerUsage{{PeerID: "a", TX: 500, RX: 200}}); err != nil {
		t.Fatalf("replace: %v", err)
	}
	p, _ := s.GetPeer("a", now)
	if p.Usage != 1100 || p.TotalTX != 800 || p.TotalRX != 300 {
		t.Fatalf("combined usage = %d (tx %d, rx %d); want 1100 (800, 300)", p.Usage, p.TotalTX, p.TotalRX)
	}
	if p.Status != StatusQuota {
		t.Errorf("status = %s, want quota once both servers are counted", p.Status)
	}

	// A snapshot is absolute, not a delta: sending it again changes nothing.
	if err := s.ReplaceServerUsage("other", now, []ServerUsage{{PeerID: "a", TX: 500, RX: 200}}); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if p, _ := s.GetPeer("a", now); p.Usage != 1100 {
		t.Errorf("usage after a repeated snapshot = %d, want 1100", p.Usage)
	}

	// A server may only speak for itself.
	if err := s.ReplaceServerUsage(s.ServerID(), now, nil); err == nil {
		t.Error("a server was allowed to overwrite its own usage as if it were remote")
	}
}

func TestUsageFlushSurvivesADeletedPeer(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UnixMilli()

	mustPeer(t, s, &Peer{ID: "a", Name: "a", AllowedIPs: "10.0.0.2/32"})

	// The engine samples the device, a peer is deleted, and the flush that
	// follows still carries what it had counted for the one that went. That
	// must not cost the surviving peers their traffic — a flush that rolls back
	// here would roll back on every tick from then on.
	if _, err := s.DeletePeers([]string{"gone"}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := s.ApplyUsageDeltas([]UsageDelta{
		{PeerID: "gone", TX: 900, RX: 900},
		{PeerID: "a", TX: 100, RX: 50},
	}); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if p, _ := s.GetPeer("a", now); p.Usage != 150 {
		t.Errorf("usage = %d, want 150: a delta for a deleted peer took the flush down with it", p.Usage)
	}
}

func TestResetClearsEveryServerAndHoldsAgainstLateCounts(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UnixMilli()

	mustPeer(t, s, &Peer{ID: "a", Name: "a", AllowedIPs: "10.0.0.2/32"})
	if err := s.ApplyUsageDeltas([]UsageDelta{{PeerID: "a", TX: 400, RX: 0}}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := s.ReplaceServerUsage("other", now, []ServerUsage{{PeerID: "a", TX: 600, RX: 0}}); err != nil {
		t.Fatalf("replace: %v", err)
	}

	if _, err := s.UpdatePeers([]string{"a"}, PeerPatch{ResetUsage: true}); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if p, _ := s.GetPeer("a", now); p.Usage != 0 {
		t.Fatalf("usage after reset = %d, want 0 across every server", p.Usage)
	}

	// The other server is still reporting what it had read before the reset.
	// Accepting it would silently undo the operator's reset.
	if err := s.ReplaceServerUsage("other", now-1, []ServerUsage{{PeerID: "a", TX: 600, RX: 0}}); err != nil {
		t.Fatalf("stale replace: %v", err)
	}
	if p, _ := s.GetPeer("a", now); p.Usage != 0 {
		t.Errorf("usage = %d, want 0: a snapshot from before the reset restored it", p.Usage)
	}

	// Once that server has read the peer again, its counts land normally.
	if err := s.ReplaceServerUsage("other", time.Now().UnixMilli()+1, []ServerUsage{{PeerID: "a", TX: 20, RX: 0}}); err != nil {
		t.Fatalf("fresh replace: %v", err)
	}
	if p, _ := s.GetPeer("a", now); p.Usage != 20 {
		t.Errorf("usage = %d, want 20 from the post-reset snapshot", p.Usage)
	}
}

func TestGroupLimitsReplaceMemberLimits(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UnixMilli()

	// A roomy group, and a member whose own limits are both already breached.
	g := &Group{Name: "roomy", AllowedUsage: 10_000, ExpiresAt: now + 365*day}
	if err := s.CreateGroup(g); err != nil {
		t.Fatalf("create group: %v", err)
	}
	mustPeer(t, s, &Peer{
		ID: "a", Name: "a", AllowedIPs: "10.0.0.2/32", GroupID: g.ID,
		AllowedUsage: 100, ExpiresAt: now - day,
	})
	mustPeer(t, s, &Peer{ID: "b", Name: "b", AllowedIPs: "10.0.0.3/32", GroupID: g.ID})
	if err := s.ApplyUsageDeltas([]UsageDelta{
		{PeerID: "a", TX: 400, RX: 100},
		{PeerID: "b", TX: 200, RX: 0},
	}); err != nil {
		t.Fatalf("apply: %v", err)
	}

	p, err := s.GetPeer("a", now)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	// Its own 100-byte allowance and yesterday's expiry are both ignored while
	// the group holds the reins.
	if p.Status != StatusActive {
		t.Errorf("status = %s, want active: a member's own limits must not bite", p.Status)
	}
	if got := p.EffectiveAllowedUsage(); got != g.AllowedUsage {
		t.Errorf("effective allowance = %d, want the group's %d", got, g.AllowedUsage)
	}
	if got := p.EffectiveExpiresAt(); got != g.ExpiresAt {
		t.Errorf("effective expiry = %d, want the group's %d", got, g.ExpiresAt)
	}
	// What is spent out of a shared allowance is what everyone spent.
	if got := p.EffectiveUsage(); got != 700 {
		t.Errorf("effective usage = %d, want the group's 700", got)
	}
	if p.Usage != 500 {
		t.Errorf("own usage = %d, want 500 — the member's own counter is untouched", p.Usage)
	}

	// Leaving the group hands the member back to its own limits, unchanged.
	var none int64
	if _, err := s.UpdatePeers([]string{"a"}, PeerPatch{GroupID: &none}); err != nil {
		t.Fatalf("detach: %v", err)
	}
	p, err = s.GetPeer("a", now)
	if err != nil {
		t.Fatalf("get after detach: %v", err)
	}
	if p.AllowedUsage != 100 || p.ExpiresAt != now-day {
		t.Fatalf("own limits were not preserved: allowance %d, expiry %d", p.AllowedUsage, p.ExpiresAt)
	}
	if p.Status != StatusExpired {
		t.Errorf("status after leaving = %s, want expired on its own expiry", p.Status)
	}
}

func TestListFilterAndSort(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UnixMilli()

	mustPeer(t, s, &Peer{ID: "k1", Name: "charlie", AllowedIPs: "10.0.0.4/32", ExpiresAt: now + 3*day})
	mustPeer(t, s, &Peer{ID: "k2", Name: "alice", AllowedIPs: "10.0.0.2/32", ExpiresAt: now + day})
	mustPeer(t, s, &Peer{ID: "k3", Name: "bob", AllowedIPs: "10.0.0.3/32", ManuallyDisabled: true})

	names := func(f PeerFilter) []string {
		f.Scope, f.Now = adminScope(), now
		peers, total, err := s.ListPeers(f)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if f.PageSize <= 0 && total != len(peers) {
			t.Fatalf("total %d disagrees with %d returned rows", total, len(peers))
		}
		out := make([]string, len(peers))
		for i, p := range peers {
			out[i] = p.Name
		}
		return out
	}

	if got := names(PeerFilter{Sort: "name"}); !equal(got, []string{"alice", "bob", "charlie"}) {
		t.Errorf("sort by name = %v", got)
	}
	if got := names(PeerFilter{Sort: "name", Desc: true}); !equal(got, []string{"charlie", "bob", "alice"}) {
		t.Errorf("sort by name desc = %v", got)
	}
	// "never expires" must sort after everything with a real expiry date.
	if got := names(PeerFilter{Sort: "expiry"}); !equal(got, []string{"alice", "charlie", "bob"}) {
		t.Errorf("sort by expiry = %v, want the peer that never expires last", got)
	}
	if got := names(PeerFilter{Search: "ali"}); !equal(got, []string{"alice"}) {
		t.Errorf("search by name = %v", got)
	}
	if got := names(PeerFilter{Search: "10.0.0.3"}); !equal(got, []string{"bob"}) {
		t.Errorf("search by address = %v", got)
	}
	if got := names(PeerFilter{Status: StatusDisabled}); !equal(got, []string{"bob"}) {
		t.Errorf("filter by status = %v", got)
	}
	if got := names(PeerFilter{Status: "never"}); len(got) != 3 {
		t.Errorf("never-connected filter = %v, want all three", got)
	}
}

func TestListPagination(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UnixMilli()
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		mustPeer(t, s, &Peer{ID: n, Name: n, AllowedIPs: "10.0.0." + n + "/32"})
	}

	peers, total, err := s.ListPeers(PeerFilter{Scope: adminScope(), Now: now, Sort: "name", Page: 2, PageSize: 2})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 5 {
		t.Errorf("total = %d, want 5 regardless of the page size", total)
	}
	if len(peers) != 2 || peers[0].Name != "c" || peers[1].Name != "d" {
		t.Errorf("page 2 = %v", peers)
	}
}

func TestScopeVisibility(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UnixMilli()

	mustPeer(t, s, &Peer{ID: "dist", Name: "dist", Role: "distributor", AllowedIPs: "10.0.0.2/32"})
	mustPeer(t, s, &Peer{ID: "owned", Name: "owned", AllowedIPs: "10.0.0.3/32", OwnerID: "dist"})
	mustPeer(t, s, &Peer{ID: "other", Name: "other", AllowedIPs: "10.0.0.4/32"})
	mustPeer(t, s, &Peer{ID: "plain", Name: "plain", AllowedIPs: "10.0.0.5/32"})

	list := func(sc Scope) []string {
		peers, _, err := s.ListPeers(PeerFilter{Scope: sc, Now: now, Sort: "name"})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		out := make([]string, len(peers))
		for i, p := range peers {
			out[i] = p.Name
		}
		return out
	}

	if got := list(adminScope()); len(got) != 4 {
		t.Errorf("admin sees %v, want all four", got)
	}
	if got := list(Scope{PeerID: "dist", Role: "distributor"}); !equal(got, []string{"dist", "owned"}) {
		t.Errorf("distributor sees %v, want itself and what it owns", got)
	}
	if got := list(Scope{PeerID: "plain", Role: "user"}); !equal(got, []string{"plain"}) {
		t.Errorf("user sees %v, want only itself", got)
	}

	// Sharing "other" with "plain" must widen exactly one user's view.
	if err := s.SetVisibility("plain", []string{"other"}); err != nil {
		t.Fatalf("set visibility: %v", err)
	}
	if got := list(Scope{PeerID: "plain", Role: "user"}); !equal(got, []string{"other", "plain"}) {
		t.Errorf("after sharing, user sees %v", got)
	}
	if got := list(Scope{PeerID: "other", Role: "user"}); !equal(got, []string{"other"}) {
		t.Errorf("sharing must not be reciprocal, but %q sees %v", "other", got)
	}

	// The grant count rides along on the row so the list can show it without a
	// query per peer, and it counts one way only, like the grant itself.
	if p, _ := s.GetPeer("plain", now); p.SharedCount != 1 {
		t.Errorf("shared count for the viewer = %d, want 1", p.SharedCount)
	}
	if p, _ := s.GetPeer("other", now); p.SharedCount != 0 {
		t.Errorf("shared count for the target = %d, want 0", p.SharedCount)
	}

	ok, err := s.CanSee(Scope{PeerID: "plain", Role: "user"}, "owned")
	if err != nil {
		t.Fatalf("can see: %v", err)
	}
	if ok {
		t.Error("user must not see a peer that was never shared with it")
	}
}

func TestBulkUpdateAndDelete(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UnixMilli()
	for _, n := range []string{"a", "b", "c"} {
		mustPeer(t, s, &Peer{ID: n, Name: n, AllowedIPs: "10.0.0." + n + "/32"})
	}
	if err := s.ApplyUsageDeltas([]UsageDelta{{PeerID: "a", TX: 10, RX: 10}}); err != nil {
		t.Fatalf("apply: %v", err)
	}

	limit := int64(5000)
	disabled := true
	n, err := s.UpdatePeers([]string{"a", "b"}, PeerPatch{AllowedUsage: &limit, ManuallyDisabled: &disabled, ResetUsage: true})
	if err != nil {
		t.Fatalf("bulk update: %v", err)
	}
	if n != 2 {
		t.Fatalf("updated %d rows, want 2", n)
	}

	for _, id := range []string{"a", "b"} {
		p, _ := s.GetPeer(id, now)
		if p.AllowedUsage != 5000 || !p.ManuallyDisabled || p.Usage != 0 {
			t.Errorf("peer %s = %+v, want quota 5000, disabled, usage reset", id, p)
		}
	}
	if p, _ := s.GetPeer("c", now); p.AllowedUsage != 0 || p.ManuallyDisabled {
		t.Error("bulk update touched a peer that was not selected")
	}

	deleted, err := s.DeletePeers([]string{"a", "b"})
	if err != nil || deleted != 2 {
		t.Fatalf("delete = %d, %v; want 2, nil", deleted, err)
	}
	if _, _, err := s.ListPeers(PeerFilter{Scope: adminScope(), Now: now}); err != nil {
		t.Fatalf("list after delete: %v", err)
	}
}

// Deleting a peer must not delete the peers it owned, only detach them, and must
// clear any sharing grants that referenced it.
func TestDeleteDetachesRatherThanCascades(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UnixMilli()

	mustPeer(t, s, &Peer{ID: "dist", Name: "dist", Role: "distributor", AllowedIPs: "10.0.0.2/32"})
	mustPeer(t, s, &Peer{ID: "owned", Name: "owned", AllowedIPs: "10.0.0.3/32", OwnerID: "dist"})
	if err := s.SetVisibility("owned", []string{"dist"}); err != nil {
		t.Fatalf("set visibility: %v", err)
	}

	if _, err := s.DeletePeers([]string{"dist"}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	p, err := s.GetPeer("owned", now)
	if err != nil {
		t.Fatalf("owned peer was removed along with its owner: %v", err)
	}
	if p.OwnerID != "" {
		t.Errorf("owner_id = %q, want empty after the owner was deleted", p.OwnerID)
	}
	grants, err := s.Visibility("owned")
	if err != nil {
		t.Fatalf("visibility: %v", err)
	}
	if len(grants) != 0 {
		t.Errorf("grants = %v, want none after the target was deleted", grants)
	}
}

func TestDeleteGroupDetachesMembers(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UnixMilli()

	g := &Group{Name: "team", AllowedUsage: 100}
	if err := s.CreateGroup(g); err != nil {
		t.Fatalf("create group: %v", err)
	}
	mustPeer(t, s, &Peer{ID: "a", Name: "a", AllowedIPs: "10.0.0.2/32", GroupID: g.ID})
	if err := s.ApplyUsageDeltas([]UsageDelta{{PeerID: "a", TX: 500, RX: 0}}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if p, _ := s.GetPeer("a", now); p.Status != StatusQuota {
		t.Fatalf("expected the member to be over the group quota first, got %s", p.Status)
	}

	if _, err := s.DeleteGroups([]int64{g.ID}); err != nil {
		t.Fatalf("delete group: %v", err)
	}
	p, err := s.GetPeer("a", now)
	if err != nil {
		t.Fatalf("member was deleted with its group: %v", err)
	}
	if p.GroupID != 0 || p.Status != StatusActive {
		t.Errorf("member = group %d, status %s; want detached and active on its own unlimited quota",
			p.GroupID, p.Status)
	}
}

func TestEnginePeersReportsDesiredState(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UnixMilli()

	mustPeer(t, s, &Peer{ID: "on", Name: "on", AllowedIPs: "10.0.0.2/32"})
	mustPeer(t, s, &Peer{ID: "off", Name: "off", AllowedIPs: "10.0.0.3/32", ExpiresAt: now - day})

	peers, err := s.EnginePeers(now)
	if err != nil {
		t.Fatalf("engine peers: %v", err)
	}
	if len(peers) != 2 {
		t.Fatalf("got %d peers, want 2", len(peers))
	}
	for _, p := range peers {
		want := p.ID == "on"
		if p.ShouldBeEnabled() != want {
			t.Errorf("peer %s enabled = %v, want %v", p.ID, p.ShouldBeEnabled(), want)
		}
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	s := newTestStore(t)

	loaded, err := s.LoadSettings()
	if err != nil {
		t.Fatalf("load defaults: %v", err)
	}
	if loaded.UsageFlushSeconds != 10 {
		t.Fatalf("default flush = %d, want 10", loaded.UsageFlushSeconds)
	}

	loaded.PublicAddress = "vpn.example.com"
	loaded.Endpoints = []string{"de.example.com:51820", "nl.example.com:51820"}
	loaded.PeerDefaults.ExpiryDays = 90
	if err := s.SaveSettings(loaded); err != nil {
		t.Fatalf("save: %v", err)
	}

	again, err := s.LoadSettings()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if again.PublicAddress != "vpn.example.com" || again.PeerDefaults.ExpiryDays != 90 {
		t.Errorf("round trip lost data: %+v", again)
	}
	if again.DefaultEndpoint != "de.example.com:51820" {
		t.Errorf("default endpoint = %q, want the first configured endpoint", again.DefaultEndpoint)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestCountPeers(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UnixMilli()

	mustPeer(t, s, &Peer{ID: "a", Name: "a", AllowedIPs: "10.0.0.2/32"})
	mustPeer(t, s, &Peer{ID: "b", Name: "b", AllowedIPs: "10.0.0.3/32", ExpiresAt: now - day})
	mustPeer(t, s, &Peer{ID: "c", Name: "c", AllowedIPs: "10.0.0.4/32", ManuallyDisabled: true})
	mustPeer(t, s, &Peer{ID: "d", Name: "d", AllowedIPs: "10.0.0.5/32", AllowedUsage: 100})

	if err := s.ApplyUsageDeltas([]UsageDelta{
		{PeerID: "d", TX: 150, RX: 0, LastHandshakeAt: now},
		{PeerID: "a", TX: 50, RX: 25, LastHandshakeAt: now},
	}); err != nil {
		t.Fatalf("apply: %v", err)
	}

	got, err := s.CountPeers(adminScope(), now)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	want := StatusCounts{Total: 4, Active: 1, Disabled: 1, Expired: 1, Quota: 1, Online: 2, OnlineAnywhere: 2, Usage: 225}
	if got != want {
		t.Errorf("counts = %+v, want %+v", got, want)
	}

	// Counting must respect the caller's scope, not the whole server.
	scoped, err := s.CountPeers(Scope{PeerID: "a", Role: "user"}, now)
	if err != nil {
		t.Fatalf("count scoped: %v", err)
	}
	if scoped.Total != 1 || scoped.Usage != 75 {
		t.Errorf("scoped counts = %+v, want just the caller's own row", scoped)
	}
}

func TestCountGroups(t *testing.T) {
	s := newTestStore(t)

	mustPeer(t, s, &Peer{ID: "dist", Name: "dist", Role: "distributor", AllowedIPs: "10.0.0.2/32"})
	for _, g := range []*Group{
		{Name: "mine", OwnerID: "dist"},
		{Name: "theirs"},
	} {
		if err := s.CreateGroup(g); err != nil {
			t.Fatalf("create group: %v", err)
		}
	}

	if n, err := s.CountGroups(adminScope()); err != nil || n != 2 {
		t.Errorf("admin sees %d groups (%v), want 2", n, err)
	}
	if n, err := s.CountGroups(Scope{PeerID: "dist", Role: "distributor"}); err != nil || n != 1 {
		t.Errorf("distributor sees %d groups (%v), want only the one it owns", n, err)
	}
	// A plain user has no business browsing groups at all.
	if n, err := s.CountGroups(Scope{PeerID: "dist", Role: "user"}); err != nil || n != 0 {
		t.Errorf("user sees %d groups (%v), want 0", n, err)
	}
}

func TestSecretsRoundTripAndStayOutOfSettings(t *testing.T) {
	s := newTestStore(t)

	if _, ok, err := s.Secret("tls.cert"); err != nil || ok {
		t.Fatalf("missing secret = ok %v, err %v; want not found", ok, err)
	}

	if err := s.PutSecret("tls.cert", []byte("-----BEGIN CERTIFICATE-----")); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, ok, err := s.Secret("tls.cert")
	if err != nil || !ok || string(got) != "-----BEGIN CERTIFICATE-----" {
		t.Fatalf("got %q, ok %v, err %v", got, ok, err)
	}

	// Overwriting replaces rather than failing on the primary key.
	if err := s.PutSecret("tls.cert", []byte("second")); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	if got, _, _ := s.Secret("tls.cert"); string(got) != "second" {
		t.Errorf("got %q after overwrite, want \"second\"", got)
	}

	// Secrets live in their own table, so nothing here can reach the settings
	// the API hands to admins.
	settings, err := s.LoadSettings()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	if b, _ := json.Marshal(settings); bytes.Contains(b, []byte("BEGIN CERTIFICATE")) ||
		bytes.Contains(b, []byte("second")) {
		t.Error("a secret leaked into the settings the API exposes")
	}
}

func TestApplyDefinitionsReplacesEverythingButKeepsUsage(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UnixMilli()

	// A server standing on its own, with peers and a script of its own.
	mustPeer(t, s, &Peer{ID: "mine", Name: "mine", AllowedIPs: "10.0.0.9/32"})
	if err := s.CreateScript(&Script{Name: "local", Body: "echo local"}); err != nil {
		t.Fatalf("create script: %v", err)
	}
	if err := s.ApplyUsageDeltas([]UsageDelta{{PeerID: "mine", TX: 700, RX: 300}}); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// The master's set arrives. It keeps one of this server's peers and knows
	// nothing of the other, which must go along with its script.
	shared := config.DefaultSettings()
	shared.PeerDefaults.DNS = "9.9.9.9"
	shared.PublicAddress = "master.example.com"
	if err := s.ApplyDefinitions(Definitions{
		Peers: []*Peer{
			{ID: "mine", Name: "mine", PublicKey: "mine", AllowedIPs: "10.0.0.9/32", Role: "user"},
			{ID: "theirs", Name: "theirs", PublicKey: "theirs", AllowedIPs: "10.0.0.8/32", Role: "user"},
		},
		Scripts:  []*Script{{ID: 50, Name: "from-master", Body: "echo master", TimeoutSec: 10}},
		Settings: &shared,
	}); err != nil {
		t.Fatalf("apply definitions: %v", err)
	}

	names := func() []string {
		peers, _, err := s.ListPeers(PeerFilter{Scope: adminScope(), Now: now})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		out := make([]string, len(peers))
		for i, p := range peers {
			out[i] = p.Name
		}
		return out
	}
	if got := names(); len(got) != 2 || got[0] != "mine" || got[1] != "theirs" {
		t.Errorf("peers = %v, want exactly the master's set", got)
	}

	// A peer the master still lists is updated rather than recreated, so what
	// this server had counted for it is untouched.
	if p, _ := s.GetPeer("mine", now); p.Usage != 1000 {
		t.Errorf("usage = %d, want 1000 — a sync must not cost a node its own counts", p.Usage)
	}

	scripts, err := s.ListScripts()
	if err != nil {
		t.Fatalf("list scripts: %v", err)
	}
	if len(scripts) != 1 || scripts[0].Name != "from-master" {
		t.Errorf("scripts = %v, want only the master's", scripts)
	}

	// Policy is taken; this server's own address is not, or every config
	// downloaded from a node would send its peer to the master.
	got, err := s.LoadSettings()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	if got.PeerDefaults.DNS != "9.9.9.9" {
		t.Errorf("DNS = %q, want the master's", got.PeerDefaults.DNS)
	}
	if got.PublicAddress == "master.example.com" {
		t.Error("the master's public address was adopted; a node must keep its own")
	}
}

// A database whose indexes no longer agree with its contents is what copying
// the file out from under a running server leaves behind. It does not announce
// itself — it quietly answers questions wrongly, and one of the questions the
// panel asks is which tunnel addresses are free — so opening one must fail
// plainly rather than several statements later on a foreign key.
//
// The damage is reproduced the way it happens: rows are written while the index
// is not attached, so its b-tree never learns about them, and the index is then
// put back exactly as it was.
func TestOpenRefusesADatabaseWithStaleIndexes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "torn.db")

	s, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	mustPeer(t, s, &Peer{ID: "a", Name: "a", AllowedIPs: "10.0.0.2/32"})

	var rootpage int
	var ddl string
	if err := s.DB().QueryRow(
		`SELECT rootpage, sql FROM sqlite_schema WHERE name = 'idx_peers_owner'`).
		Scan(&rootpage, &ddl); err != nil {
		t.Fatalf("read index definition: %v", err)
	}
	if _, err := s.DB().Exec(`PRAGMA writable_schema = ON;
	    DELETE FROM sqlite_schema WHERE name = 'idx_peers_owner';
	    PRAGMA writable_schema = OFF;`); err != nil {
		t.Skipf("this SQLite build will not let the test damage the file: %v", err)
	}
	s.Close()

	// Reopened, the index is not in the schema, so this row never reaches it.
	raw, err := sql.Open("sqlite", dsnFor(path))
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if _, err := raw.Exec(`INSERT INTO peers
	    (id, name, role, public_key, allowed_ips, created_at, updated_at)
	    VALUES ('b', 'b', 'user', 'b', '10.0.0.3/32', 1, 1)`); err != nil {
		t.Fatalf("insert behind the index: %v", err)
	}
	// Put the index back exactly as it was, pointing at the b-tree that has
	// now missed a row.
	if _, err := raw.Exec(`PRAGMA writable_schema = ON;
	    INSERT INTO sqlite_schema (type, name, tbl_name, rootpage, sql)
	         VALUES ('index', 'idx_peers_owner', 'peers', ?, ?);
	    PRAGMA writable_schema = OFF;`, rootpage, ddl); err != nil {
		t.Fatalf("restore the index: %v", err)
	}
	raw.Close()

	_, err = Open(path)
	var corrupt *ErrCorrupt
	if !errors.As(err, &corrupt) {
		t.Fatalf("open returned %v, want an ErrCorrupt naming the damage", err)
	}
	if len(corrupt.Problems) == 0 {
		t.Error("the error names no problems")
	}
	if !strings.Contains(corrupt.Error(), "--repair") {
		t.Error("the error does not say how to fix it")
	}

	// Rebuilding the indexes from the table makes it openable again, with both
	// peers intact — the table was never the damaged part.
	rs, err := OpenForRepair(path)
	if err != nil {
		t.Fatalf("open for repair: %v", err)
	}
	if _, err := rs.Repair(); err != nil {
		t.Fatalf("repair: %v", err)
	}
	rs.Close()

	fixed, err := Open(path)
	if err != nil {
		t.Fatalf("open after repair: %v", err)
	}
	defer fixed.Close()
	now := time.Now().UnixMilli()
	for _, id := range []string{"a", "b"} {
		if p, err := fixed.GetPeer(id, now); err != nil || p.ID != id {
			t.Errorf("peer %q did not survive the repair: %v", id, err)
		}
	}
}

func TestPresenceSaysWhichServerAPeerIsOn(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UnixMilli()

	mustPeer(t, s, &Peer{ID: "a", Name: "a", AllowedIPs: "10.0.0.2/32"})
	if err := s.SeeNode(Node{ID: "far", Role: "node", Name: "panel-b"}); err != nil {
		t.Fatalf("see node: %v", err)
	}

	// This server carried it a while ago.
	if err := s.ApplyUsageDeltas([]UsageDelta{
		{PeerID: "a", TX: 100, RX: 50, LastHandshakeAt: now - 10*60_000, LastEndpoint: "1.1.1.1:5"},
	}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	// The other server is carrying it now.
	if err := s.ReplaceServerUsage("far", now, []ServerUsage{
		{PeerID: "a", TX: 900, RX: 100, LastHandshakeAt: now - 5_000, LastEndpoint: "2.2.2.2:6"},
	}); err != nil {
		t.Fatalf("replace: %v", err)
	}

	rows, err := s.PeerPresence("a", now)
	if err != nil {
		t.Fatalf("presence: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d servers, want 2", len(rows))
	}
	// Newest first, so the server the peer is actually using leads.
	if rows[0].ServerID != "far" || rows[0].ServerName != "panel-b" {
		t.Errorf("first row = %+v, want the server that saw it most recently", rows[0])
	}
	if !rows[0].Online || rows[0].LastEndpoint != "2.2.2.2:6" || rows[0].Usage != 1000 {
		t.Errorf("remote row = %+v, want online, its own endpoint and its own share", rows[0])
	}
	if !rows[1].IsLocal || rows[1].Online {
		t.Errorf("local row = %+v, want this server, no longer online", rows[1])
	}

	// "Last seen" on the peer itself means last seen anywhere, which is what a
	// fleet's operator is asking and what sorting on it should answer.
	p, _ := s.GetPeer("a", now)
	if p.LastHandshakeAt != now-5_000 {
		t.Errorf("peer handshake = %d, want the newest from any server", p.LastHandshakeAt)
	}
	if p.SeenOnID != "far" || p.SeenOn != "panel-b" {
		t.Errorf("seen on = %q/%q, want the server carrying it", p.SeenOnID, p.SeenOn)
	}
}

func TestTrafficWindowsMeasureTheRecentPast(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UnixMilli()

	// One bucket at each age; each window takes the buckets that started
	// inside it and nothing older.
	for _, b := range []struct {
		ago   time.Duration
		bytes int64
	}{
		{10 * time.Minute, 1},
		{59 * time.Minute, 2},
		{61 * time.Minute, 4},
		{23 * time.Hour, 8},
		{25 * time.Hour, 16},
		{6 * 24 * time.Hour, 32},
		{8 * 24 * time.Hour, 64},
		{29 * 24 * time.Hour, 128},
		{30*24*time.Hour + time.Hour, 256},
	} {
		if _, err := s.DB().Exec(`INSERT INTO traffic (minute, tx, rx) VALUES (?, ?, 0)`,
			now-b.ago.Milliseconds(), b.bytes); err != nil {
			t.Fatalf("bucket: %v", err)
		}
	}

	w, err := s.TrafficWindows(now)
	if err != nil {
		t.Fatalf("windows: %v", err)
	}
	want := UsageWindows{Hour: 1 + 2, Day: 1 + 2 + 4 + 8, Week: 1 + 2 + 4 + 8 + 16 + 32,
		Month: 1 + 2 + 4 + 8 + 16 + 32 + 64 + 128}
	if w != want {
		t.Errorf("windows = %+v, want %+v", w, want)
	}

	// Recording prunes what no window reaches any more.
	if _, err := s.DB().Exec(`INSERT INTO traffic (minute, tx, rx) VALUES (?, 1, 0)`,
		now-(40*24*time.Hour).Milliseconds()); err != nil {
		t.Fatalf("old bucket: %v", err)
	}
	mustPeer(t, s, &Peer{ID: "a", Name: "a", AllowedIPs: "10.0.0.2/32"})
	if err := s.ApplyUsageDeltas([]UsageDelta{{PeerID: "a", TX: 1}}); err != nil {
		t.Fatalf("flush: %v", err)
	}
	var old int
	s.DB().QueryRow(`SELECT COUNT(*) FROM traffic WHERE minute < ?`,
		now-(31*24*time.Hour).Milliseconds()).Scan(&old)
	if old != 0 {
		t.Errorf("%d buckets older than the retention survived a flush", old)
	}
}

// What a server carried is a record of bytes that crossed its device, so
// nothing done to the peers afterwards can take any of it back — and usage that
// never crossed this device, like totals imported from the old panel, was never
// part of it.
func TestTrafficIsWhatCrossedTheDevice(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UnixMilli()
	mustPeer(t, s, &Peer{ID: "a", Name: "a", AllowedIPs: "10.0.0.2/32"})
	mustPeer(t, s, &Peer{ID: "b", Name: "b", AllowedIPs: "10.0.0.3/32"})

	// Usage that arrived from elsewhere: another server's count, and the kind
	// of total an import writes straight into this server's row.
	if err := s.ReplaceServerUsage("other", now, []ServerUsage{{PeerID: "a", TX: 5_000}}); err != nil {
		t.Fatalf("remote: %v", err)
	}
	if _, err := s.DB().Exec(`INSERT INTO peer_usage (peer_id, server_id, tx, rx, updated_at)
	                          VALUES ('b', ?, 9000000, 0, 0)`, s.ServerID()); err != nil {
		t.Fatalf("import: %v", err)
	}

	// What the device actually carried, including for a peer that has since
	// been deleted.
	if err := s.ApplyUsageDeltas([]UsageDelta{
		{PeerID: "a", TX: 300, RX: 100},
		{PeerID: "b", TX: 20, RX: 5},
		{PeerID: "gone", TX: 7, RX: 0},
	}); err != nil {
		t.Fatalf("flush: %v", err)
	}
	check := func(when string) {
		t.Helper()
		w, err := s.TrafficWindows(time.Now().UnixMilli())
		if err != nil {
			t.Fatalf("windows: %v", err)
		}
		if w.Hour != 432 || w.Month != 432 {
			t.Errorf("%s: hour %d, month %d; want 432 carried here", when, w.Hour, w.Month)
		}
	}
	check("after the flush")

	// Resetting and deleting peers changes their quotas, not the server's past.
	if _, err := s.UpdatePeers([]string{"a"}, PeerPatch{ResetUsage: true}); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if _, err := s.DeletePeers([]string{"b"}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	check("after a peer reset and a delete")
}

func TestTrafficResetStartsFromZero(t *testing.T) {
	s := newTestStore(t)
	mustPeer(t, s, &Peer{ID: "a", Name: "a", AllowedIPs: "10.0.0.2/32", AllowedUsage: 1 << 40})
	if err := s.ApplyUsageDeltas([]UsageDelta{{PeerID: "a", TX: 900}}); err != nil {
		t.Fatalf("flush: %v", err)
	}

	if err := s.ResetTraffic(); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if w, _ := s.TrafficWindows(time.Now().UnixMilli()); w != (UsageWindows{}) {
		t.Errorf("windows after a reset = %+v, want zero", w)
	}
	// The peer's usage is its quota, and is not the server's to clear.
	if p, _ := s.GetPeer("a", time.Now().UnixMilli()); p.Usage != 900 {
		t.Errorf("peer usage = %d after a traffic reset, want 900 untouched", p.Usage)
	}
	if err := s.ApplyUsageDeltas([]UsageDelta{{PeerID: "a", TX: 40}}); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if w, _ := s.TrafficWindows(time.Now().UnixMilli()); w.Hour != 40 {
		t.Errorf("hour = %d after counting resumed, want 40", w.Hour)
	}
}

// A master asks a node to reset with every sync until the node says it has, so
// the node must act on a given request once and then leave its counters alone.
func TestAMastersTrafficResetIsAppliedOnce(t *testing.T) {
	master, node := newTestStore(t), newTestStore(t)
	mustPeer(t, node, &Peer{ID: "a", Name: "a", AllowedIPs: "10.0.0.2/32"})
	count := func(n int64) {
		t.Helper()
		if err := node.ApplyUsageDeltas([]UsageDelta{{PeerID: "a", TX: n}}); err != nil {
			t.Fatalf("flush: %v", err)
		}
	}
	hour := func() int64 {
		w, _ := node.TrafficWindows(time.Now().UnixMilli())
		return w.Hour
	}

	if err := master.SeeNode(Node{ID: "n1", Name: "n1", Recent: UsageWindows{Hour: 500, Day: 500}}); err != nil {
		t.Fatalf("see: %v", err)
	}
	if err := master.SeeNode(Node{ID: "m0", Role: "master", Recent: UsageWindows{Hour: 7}}); err != nil {
		t.Fatalf("see: %v", err)
	}
	if err := master.RequestTrafficReset(nil, 1_000); err != nil {
		t.Fatalf("request: %v", err)
	}
	nodes, _ := master.Nodes()
	for _, n := range nodes {
		switch n.ID {
		case "n1":
			if n.Recent != (UsageWindows{}) {
				t.Errorf("node figures = %+v after a reset was requested, want zero at once", n.Recent)
			}
		case "m0":
			if n.Recent.Hour != 7 {
				t.Error("a request meant for nodes reset the figures of the server this one follows")
			}
		}
	}
	if at, _ := master.PendingTrafficReset("n1"); at != 1_000 {
		t.Fatalf("pending reset = %d, want 1000", at)
	}

	count(300)
	if applied, err := node.ApplyTrafficReset(1_000); err != nil || !applied {
		t.Fatalf("first delivery: applied=%v err=%v, want applied", applied, err)
	}
	if hour() != 0 {
		t.Fatalf("hour = %d after the reset, want 0", hour())
	}

	// The same request arrives again with the next sync.
	count(25)
	if applied, _ := node.ApplyTrafficReset(1_000); applied || hour() != 25 {
		t.Errorf("repeat delivery: applied=%v hour=%d; want ignored and 25 kept", applied, hour())
	}
	if seen, _ := node.TrafficResetApplied(); seen != 1_000 {
		t.Errorf("applied = %d, want 1000 reported back", seen)
	}

	// A later request is a new one.
	if applied, _ := node.ApplyTrafficReset(2_000); !applied || hour() != 0 {
		t.Errorf("new request: applied=%v hour=%d; want applied and 0", applied, hour())
	}
}

func TestOnlineCountsSeparateThisServerFromTheFleet(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UnixMilli()

	mustPeer(t, s, &Peer{ID: "here", Name: "here", AllowedIPs: "10.0.0.2/32"})
	mustPeer(t, s, &Peer{ID: "away", Name: "away", AllowedIPs: "10.0.0.3/32"})

	// One connected to this server, one connected to another.
	if err := s.ApplyUsageDeltas([]UsageDelta{
		{PeerID: "here", TX: 1, LastHandshakeAt: now - 1_000},
	}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := s.ReplaceServerUsage("far", now, []ServerUsage{
		{PeerID: "away", TX: 1, LastHandshakeAt: now - 1_000},
	}); err != nil {
		t.Fatalf("replace: %v", err)
	}

	counts, err := s.CountPeers(adminScope(), now)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	// A node's dashboard says how busy the node is, not how busy the fleet is.
	if counts.Online != 1 {
		t.Errorf("online here = %d, want 1", counts.Online)
	}
	if counts.OnlineAnywhere != 2 {
		t.Errorf("online anywhere = %d, want 2", counts.OnlineAnywhere)
	}

	names := func(status string) []string {
		peers, _, err := s.ListPeers(PeerFilter{Scope: adminScope(), Status: status, Now: now})
		if err != nil {
			t.Fatalf("list %s: %v", status, err)
		}
		out := make([]string, len(peers))
		for i, p := range peers {
			out[i] = p.Name
		}
		return out
	}
	if got := names("online-here"); len(got) != 1 || got[0] != "here" {
		t.Errorf("online-here = %v", got)
	}
	if got := names("online-elsewhere"); len(got) != 1 || got[0] != "away" {
		t.Errorf("online-elsewhere = %v", got)
	}
	if got := names("online"); len(got) != 2 {
		t.Errorf("online = %v, want both", got)
	}
}

// A reset made on the master has to reach a node that carries the peer, even
// though that node goes on counting — and touching the peer's row — right up
// to the moment it hears about the reset.
func TestResetReachesANodeThatKeptCounting(t *testing.T) {
	master, node := newTestStore(t), newTestStore(t)
	mustPeer(t, master, &Peer{ID: "a", Name: "a", AllowedIPs: "10.0.0.2/32", AllowedUsage: 1000})

	sync := func() {
		t.Helper()
		snap, takenAt, err := node.ServerUsageSnapshot()
		if err != nil {
			t.Fatalf("snapshot: %v", err)
		}
		if err := master.ReplaceServerUsage(node.ServerID(), takenAt, snap); err != nil {
			t.Fatalf("replace: %v", err)
		}
		defs, err := master.AllDefinitions(time.Now().UnixMilli())
		if err != nil {
			t.Fatalf("definitions: %v", err)
		}
		if err := node.ApplyDefinitions(defs); err != nil {
			t.Fatalf("apply definitions: %v", err)
		}
	}
	sync()

	// The peer runs out on the node, and the master hears about it.
	if err := node.ApplyUsageDeltas([]UsageDelta{{PeerID: "a", TX: 1500, LastHandshakeAt: time.Now().UnixMilli()}}); err != nil {
		t.Fatalf("count: %v", err)
	}
	sync()
	if p, _ := master.GetPeer("a", time.Now().UnixMilli()); p.Status != StatusQuota {
		t.Fatalf("master status = %s, want quota before the reset", p.Status)
	}

	// The operator resets it on the master. Before the node next syncs it
	// flushes again, as it does every few seconds for any peer it has seen.
	time.Sleep(5 * time.Millisecond)
	if _, err := master.UpdatePeers([]string{"a"}, PeerPatch{ResetUsage: true}); err != nil {
		t.Fatalf("reset: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	if err := node.ApplyUsageDeltas([]UsageDelta{{PeerID: "a", LastHandshakeAt: time.Now().UnixMilli()}}); err != nil {
		t.Fatalf("flush: %v", err)
	}
	sync()

	now := time.Now().UnixMilli()
	for name, s := range map[string]*Store{"master": master, "node": node} {
		p, err := s.GetPeer("a", now)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if p.Usage != 0 || p.Status != StatusActive {
			t.Errorf("%s: usage %d, status %s after the reset; want 0 and active", name, p.Usage, p.Status)
		}
	}

	// And counting resumes normally from zero.
	if err := node.ApplyUsageDeltas([]UsageDelta{{PeerID: "a", TX: 30}}); err != nil {
		t.Fatalf("count: %v", err)
	}
	sync()
	if p, _ := master.GetPeer("a", time.Now().UnixMilli()); p.Usage != 30 {
		t.Errorf("master usage = %d after the reset, want the 30 counted since", p.Usage)
	}
}

// A backup is taken from a database in use, so it has to be a complete,
// openable copy rather than whatever pages the file held at that moment.
func TestBackupIsACompleteCopy(t *testing.T) {
	s := newTestStore(t)
	mustPeer(t, s, &Peer{ID: "a", Name: "a", AllowedIPs: "10.0.0.2/32"})
	if err := s.ApplyUsageDeltas([]UsageDelta{{PeerID: "a", TX: 1234}}); err != nil {
		t.Fatalf("flush: %v", err)
	}

	path := filepath.Join(t.TempDir(), "wgui.db.before-v2.1.0")
	if err := os.WriteFile(path, []byte("an older backup"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Backup(path); err != nil {
		t.Fatalf("backup: %v", err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("backup mode = %v, want 0600: it holds every private key", info.Mode().Perm())
	}

	copy, err := Open(path)
	if err != nil {
		t.Fatalf("open the backup: %v", err)
	}
	defer copy.Close()
	if p, err := copy.GetPeer("a", time.Now().UnixMilli()); err != nil || p.Usage != 1234 {
		t.Errorf("peer in the backup = %+v (%v), want it with its usage", p, err)
	}
}
