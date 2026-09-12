package mongomig

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"wgui/internal/store"
)

func newFixture(t *testing.T) (Options, string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return Options{Store: st, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}, t.TempDir()
}

// write drops an export file into dir and returns its path.
func write(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

// This is the shape mongoexport actually produces: one document per line, with
// values wrapped in Extended JSON type markers.
func TestImportReadsMongoexportLines(t *testing.T) {
	opt, dir := newFixture(t)
	now := time.Now().UnixMilli()

	opt.PeersFile = write(t, dir, "peers.json", `
{"_id":"key-a","role":"user","name":"anna","allowedIPs":"10.0.0.2/32","publicKey":"key-a","privateKey":"priv-a","disabled":false,"allowedUsage":{"$numberLong":"5368709120"},"expiresAt":{"$numberLong":"`+itoa(now+86_400_000)+`"},"totalTX":{"$numberLong":"300"},"totalRX":{"$numberLong":"200"},"preferredEndpoint":"198.51.100.1:51820","groupID":{"$oid":"000000000000000000000000"}}
{"_id":"key-b","role":"admin","name":"root","allowedIPs":"10.0.0.3/32","publicKey":"key-b","privateKey":"priv-b","disabled":false,"allowedUsage":{"$numberLong":"0"},"expiresAt":{"$numberLong":"0"},"totalTX":{"$numberLong":"0"},"totalRX":{"$numberLong":"0"}}
`)

	if err := Run(opt); err != nil {
		t.Fatalf("run: %v", err)
	}

	p, err := opt.Store.GetPeer("key-a", now)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if p.Name != "anna" || p.PrivateKey != "priv-a" || p.AllowedIPs != "10.0.0.2/32" {
		t.Errorf("identity was not carried over: %+v", p)
	}
	if p.AllowedUsage != 5368709120 {
		t.Errorf("quota = %d, want the $numberLong value unwrapped", p.AllowedUsage)
	}
	if p.Usage != 500 {
		t.Errorf("usage = %d, want 500 carried over from the old totals", p.Usage)
	}
	if p.PreferredEndpoint != "198.51.100.1:51820" {
		t.Errorf("pinned endpoint = %q, want it preserved", p.PreferredEndpoint)
	}
	if p.GroupID != 0 {
		t.Errorf("group = %d, want none: the all-zero ObjectID means no group", p.GroupID)
	}

	// An unlimited admin must survive as unlimited, not as expired.
	admin, err := opt.Store.GetPeer("key-b", now)
	if err != nil {
		t.Fatalf("get admin: %v", err)
	}
	if admin.Role != "admin" || admin.AllowedUsage != 0 || admin.ExpiresAt != 0 || admin.Status != store.StatusActive {
		t.Errorf("admin = %+v, want an active unlimited admin", admin)
	}
}

// mongoexport --jsonArray produces a single array instead.
func TestImportReadsJSONArray(t *testing.T) {
	opt, dir := newFixture(t)

	opt.PeersFile = write(t, dir, "peers.json", `[
	  {"_id":"key-a","role":"user","name":"anna","allowedIPs":"10.0.0.2/32","publicKey":"key-a"},
	  {"_id":"key-b","role":"user","name":"bijan","allowedIPs":"10.0.0.3/32","publicKey":"key-b"}
	]`)

	if err := Run(opt); err != nil {
		t.Fatalf("run: %v", err)
	}
	_, total, err := opt.Store.ListPeers(store.PeerFilter{Scope: store.Scope{Role: "admin"}, Now: time.Now().UnixMilli()})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 2 {
		t.Fatalf("imported %d peers, want 2", total)
	}
}

func TestImportMapsGroupMembership(t *testing.T) {
	opt, dir := newFixture(t)
	now := time.Now().UnixMilli()

	opt.GroupsFile = write(t, dir, "groups.json",
		`{"_id":{"$oid":"6512aaaabbbbccccdddd0001"},"name":"team","peerIDs":["key-a","key-b"],"allowedUsage":{"$numberLong":"10000"},"expiresAt":{"$numberLong":"`+itoa(now+86_400_000)+`"}}`)
	opt.PeersFile = write(t, dir, "peers.json", `
{"_id":"key-a","role":"user","name":"a","allowedIPs":"10.0.0.2/32","publicKey":"key-a","allowedUsage":{"$numberLong":"10000"},"totalTX":{"$numberLong":"4000"}}
{"_id":"key-b","role":"user","name":"b","allowedIPs":"10.0.0.3/32","publicKey":"key-b","allowedUsage":{"$numberLong":"10000"},"totalTX":{"$numberLong":"4000"}}
{"_id":"key-c","role":"user","name":"c","allowedIPs":"10.0.0.4/32","publicKey":"key-c","allowedUsage":{"$numberLong":"7000"}}
`)

	if err := Run(opt); err != nil {
		t.Fatalf("run: %v", err)
	}

	for _, id := range []string{"key-a", "key-b"} {
		p, err := opt.Store.GetPeer(id, now)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		if p.GroupID == 0 {
			t.Errorf("%s was not put into the group", id)
		}
		// The old code copied the group's allowance onto every member; keeping
		// those copies would limit them twice over.
		if p.AllowedUsage != 0 {
			t.Errorf("%s kept its copied allowance (%d)", id, p.AllowedUsage)
		}
		if p.GroupAllowedUsage != 10000 {
			t.Errorf("%s group allowance = %d, want 10000", id, p.GroupAllowedUsage)
		}
	}

	if p, _ := opt.Store.GetPeer("key-c", now); p.AllowedUsage != 7000 || p.GroupID != 0 {
		t.Errorf("non-member = quota %d, group %d; want 7000 and no group", p.AllowedUsage, p.GroupID)
	}
}

// Some exports record membership only on the peer, so the peer's own groupID has
// to be honoured when the group document does not list it.
func TestImportUsesPeerGroupIDWhenGroupListIsEmpty(t *testing.T) {
	opt, dir := newFixture(t)
	now := time.Now().UnixMilli()

	opt.GroupsFile = write(t, dir, "groups.json",
		`{"_id":{"$oid":"6512aaaabbbbccccdddd0001"},"name":"team","peerIDs":[],"allowedUsage":{"$numberLong":"500"}}`)
	opt.PeersFile = write(t, dir, "peers.json",
		`{"_id":"key-a","role":"user","name":"a","allowedIPs":"10.0.0.2/32","publicKey":"key-a","groupID":{"$oid":"6512aaaabbbbccccdddd0001"}}`)

	if err := Run(opt); err != nil {
		t.Fatalf("run: %v", err)
	}
	p, _ := opt.Store.GetPeer("key-a", now)
	if p.GroupID == 0 || p.GroupAllowedUsage != 500 {
		t.Errorf("peer = group %d, group allowance %d; want it joined to the group", p.GroupID, p.GroupAllowedUsage)
	}
}

// A name prefix is shared by whoever is named that way, so an admin or a second
// distributor can sit under a distributor's prefix. Handing those over would
// give the distributor power over an account above it.
func TestImportNeverHandsPrivilegedPeersToADistributor(t *testing.T) {
	opt, dir := newFixture(t)
	now := time.Now().UnixMilli()

	opt.PeersFile = write(t, dir, "peers.json", `
{"_id":"key-dis","role":"distributor","name":"sMirzaie-Dis","allowedIPs":"10.0.0.2/32","publicKey":"key-dis"}
{"_id":"key-admin","role":"admin","name":"sMirzaie-Phone","allowedIPs":"10.0.0.3/32","publicKey":"key-admin"}
{"_id":"key-dis2","role":"distributor","name":"sMirzaie-Pc","allowedIPs":"10.0.0.4/32","publicKey":"key-dis2"}
{"_id":"key-user","role":"user","name":"sMirzaie-Client","allowedIPs":"10.0.0.5/32","publicKey":"key-user"}
`)

	if err := Run(opt); err != nil {
		t.Fatalf("run: %v", err)
	}

	// Two distributors claim this prefix, so nobody is handed anything: the
	// alternative is guessing which reseller owns the customers.
	if p, _ := opt.Store.GetPeer("key-user", now); p.OwnerID != "" {
		t.Errorf("user owner = %q, want empty while the prefix is ambiguous", p.OwnerID)
	}
	// The admin and the second distributor are not handed over either.
	for _, id := range []string{"key-admin", "key-dis2"} {
		p, _ := opt.Store.GetPeer(id, now)
		if p.OwnerID != "" {
			t.Errorf("%s (%s) owner = %q, want empty: a distributor must not be given a privileged peer",
				id, p.Role, p.OwnerID)
		}
	}
}

// With one distributor per prefix the handover is unambiguous and happens.
func TestImportHandsUsersToAnUnambiguousDistributor(t *testing.T) {
	opt, dir := newFixture(t)
	now := time.Now().UnixMilli()

	opt.PeersFile = write(t, dir, "peers.json", `
{"_id":"key-dis","role":"distributor","name":"VY-Vahid","allowedIPs":"10.0.0.2/32","publicKey":"key-dis"}
{"_id":"key-admin","role":"admin","name":"VY-Admin","allowedIPs":"10.0.0.3/32","publicKey":"key-admin"}
{"_id":"key-user","role":"user","name":"VY-Client","allowedIPs":"10.0.0.4/32","publicKey":"key-user"}
`)

	if err := Run(opt); err != nil {
		t.Fatalf("run: %v", err)
	}
	if p, _ := opt.Store.GetPeer("key-user", now); p.OwnerID != "key-dis" {
		t.Errorf("user owner = %q, want the one distributor claiming the prefix", p.OwnerID)
	}
	if p, _ := opt.Store.GetPeer("key-admin", now); p.OwnerID != "" {
		t.Errorf("admin owner = %q, want empty", p.OwnerID)
	}
}

func TestImportDerivesOwnershipFromNamePrefix(t *testing.T) {
	opt, dir := newFixture(t)
	now := time.Now().UnixMilli()

	opt.PeersFile = write(t, dir, "peers.json", `
{"_id":"key-karim","role":"distributor","name":"Karim-main","allowedIPs":"10.0.0.2/32","publicKey":"key-karim"}
{"_id":"key-c1","role":"user","name":"Karim-client1","allowedIPs":"10.0.0.3/32","publicKey":"key-c1"}
{"_id":"key-loose","role":"user","name":"standalone","allowedIPs":"10.0.0.5/32","publicKey":"key-loose"}
{"_id":"key-other","role":"user","name":"Other-client","allowedIPs":"10.0.0.6/32","publicKey":"key-other"}
`)

	if err := Run(opt); err != nil {
		t.Fatalf("run: %v", err)
	}

	if p, _ := opt.Store.GetPeer("key-c1", now); p.OwnerID != "key-karim" {
		t.Errorf("owner = %q, want the distributor sharing its name prefix", p.OwnerID)
	}
	// The distributor must not end up owned by itself, and a name with no
	// matching distributor is left for an admin to manage.
	for _, id := range []string{"key-karim", "key-loose", "key-other"} {
		if p, _ := opt.Store.GetPeer(id, now); p.OwnerID != "" {
			t.Errorf("%s owner = %q, want empty", id, p.OwnerID)
		}
	}
}

// The old schema had one disabled flag covering both "switched off by hand" and
// "out of quota". Only the first is stored now, so it has to be inferred.
func TestImportSeparatesManualDisableFromExhaustion(t *testing.T) {
	opt, dir := newFixture(t)
	now := time.Now().UnixMilli()

	opt.PeersFile = write(t, dir, "peers.json", `
{"_id":"manual","role":"user","name":"manual","allowedIPs":"10.0.0.2/32","publicKey":"manual","disabled":true,"allowedUsage":{"$numberLong":"10000"},"totalTX":{"$numberLong":"10"},"expiresAt":{"$numberLong":"`+itoa(now+86_400_000)+`"}}
{"_id":"spent","role":"user","name":"spent","allowedIPs":"10.0.0.3/32","publicKey":"spent","disabled":true,"allowedUsage":{"$numberLong":"100"},"totalTX":{"$numberLong":"150"}}
{"_id":"stale","role":"user","name":"stale","allowedIPs":"10.0.0.4/32","publicKey":"stale","disabled":true,"expiresAt":{"$numberLong":"`+itoa(now-86_400_000)+`"}}
`)

	if err := Run(opt); err != nil {
		t.Fatalf("run: %v", err)
	}

	if p, _ := opt.Store.GetPeer("manual", now); !p.ManuallyDisabled || p.Status != store.StatusDisabled {
		t.Errorf("a peer disabled while inside its limits = %+v, want manually disabled", p)
	}
	// These two were switched off by the old enforcement loop, so the new one
	// must be free to switch them back on if their limits are raised.
	if p, _ := opt.Store.GetPeer("spent", now); p.ManuallyDisabled || p.Status != store.StatusQuota {
		t.Errorf("an out-of-quota peer = manual %v, status %s; want not manual, status quota",
			p.ManuallyDisabled, p.Status)
	}
	if p, _ := opt.Store.GetPeer("stale", now); p.ManuallyDisabled || p.Status != store.StatusExpired {
		t.Errorf("an expired peer = manual %v, status %s; want not manual, status expired",
			p.ManuallyDisabled, p.Status)
	}
}

func TestImportSkipsUnusableRows(t *testing.T) {
	opt, dir := newFixture(t)
	now := time.Now().UnixMilli()

	opt.PeersFile = write(t, dir, "peers.json", `
{"_id":"ok","role":"user","name":"fine","allowedIPs":"10.0.0.2/32","publicKey":"ok"}
{"_id":"no-name","role":"user","name":"","allowedIPs":"10.0.0.3/32","publicKey":"no-name"}
{"_id":"no-key","role":"user","name":"keyless","allowedIPs":"10.0.0.4/32","publicKey":""}
{"_id":"no-ip","role":"user","name":"addressless","allowedIPs":"","publicKey":"no-ip"}
{"_id":"dupe","role":"user","name":"clashing","allowedIPs":"10.0.0.2/32","publicKey":"dupe"}
{"_id":"weird","role":"superuser","name":"weird-role","allowedIPs":"10.0.0.9/32","publicKey":"weird"}
`)

	if err := Run(opt); err != nil {
		t.Fatalf("import must not fail on bad rows: %v", err)
	}

	_, total, err := opt.Store.ListPeers(store.PeerFilter{Scope: store.Scope{Role: "admin"}, Now: now})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 2 {
		t.Fatalf("imported %d peers, want only the two usable rows", total)
	}
	if p, _ := opt.Store.GetPeer("weird", now); p.Role != "user" {
		t.Errorf("unknown role became %q, want user", p.Role)
	}
}

func TestImportRefusesToRunTwiceWithoutForce(t *testing.T) {
	opt, dir := newFixture(t)
	opt.PeersFile = write(t, dir, "peers.json",
		`{"_id":"key-a","role":"user","name":"a","allowedIPs":"10.0.0.2/32","publicKey":"key-a"}`)

	if err := Run(opt); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if err := Run(opt); err == nil {
		t.Fatal("a second import must be refused unless --force is passed")
	}

	// With --force it proceeds; the duplicate rows are skipped individually.
	opt.Force = true
	if err := Run(opt); err != nil {
		t.Fatalf("forced run: %v", err)
	}
}

func TestRunRequiresAPeersFile(t *testing.T) {
	opt, _ := newFixture(t)
	if err := Run(opt); err == nil {
		t.Fatal("expected an error when no peers file is given")
	}
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }
