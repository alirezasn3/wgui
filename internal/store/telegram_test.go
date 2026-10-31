package store

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func mustMarshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func contains(haystack, needle string) bool { return strings.Contains(haystack, needle) }

func TestLinkCodesAreSingleUseAndExpire(t *testing.T) {
	s := newTestStore(t)
	mustPeer(t, s, &Peer{ID: "a", Name: "Sara-phone", AllowedIPs: "10.0.0.2/32"})
	mustPeer(t, s, &Peer{ID: "b", Name: "Sara-laptop", AllowedIPs: "10.0.0.3/32"})

	code, expires, err := s.CreateLinkCode("a")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// What goes after /start has to fit Telegram's 64 characters and its
	// alphabet; anything else is silently dropped by the client.
	if len(code) == 0 || len(code) > 64 {
		t.Errorf("code is %d characters, want 1 to 64", len(code))
	}
	for _, r := range code {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			t.Errorf("code %q holds %q, which Telegram does not allow in a start parameter", code, r)
		}
	}
	if want := time.Now().Add(LinkCodeTTL).UnixMilli(); expires < want-5000 || expires > want+5000 {
		t.Errorf("expires at %d, want about %d", expires, want)
	}

	// A database read must not be enough to link a chat.
	var stored string
	s.DB().QueryRow(`SELECT code_hash FROM telegram_codes`).Scan(&stored)
	if stored == "" || stored == code {
		t.Errorf("stored %q for code %q, want only a hash of it", stored, code)
	}

	name, err := s.RedeemLinkCode(code, 4242)
	if err != nil || name != "Sara-phone" {
		t.Fatalf("redeem = %q, %v; want the peer's name", name, err)
	}
	if linked, _ := s.TelegramLinked("a"); !linked {
		t.Error("the peer is not linked after its code was redeemed")
	}
	if _, err := s.RedeemLinkCode(code, 9999); !errors.Is(err, ErrNotFound) {
		t.Errorf("redeeming a spent code: %v, want ErrNotFound", err)
	}
	if st, _ := s.Standings(time.Now().UnixMilli()); len(st) != 1 || st[0].ChatID != 4242 {
		t.Errorf("standings = %+v, want the first chat to still hold the peer", st)
	}

	// Never issued, and expired, look the same from outside.
	if _, err := s.RedeemLinkCode("not-a-real-code", 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("an unknown code: %v, want ErrNotFound", err)
	}
	old, _, _ := s.CreateLinkCode("b")
	if _, err := s.DB().Exec(`UPDATE telegram_codes SET expires_at = ?`, time.Now().Add(-time.Minute).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RedeemLinkCode(old, 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("an expired code: %v, want ErrNotFound", err)
	}
	if linked, _ := s.TelegramLinked("b"); linked {
		t.Error("an expired code linked a chat")
	}
}

func TestAskingForAnotherCodeRetiresTheFirst(t *testing.T) {
	s := newTestStore(t)
	mustPeer(t, s, &Peer{ID: "a", Name: "a", AllowedIPs: "10.0.0.2/32"})

	first, _, _ := s.CreateLinkCode("a")
	second, _, _ := s.CreateLinkCode("a")
	if first == second {
		t.Fatal("two codes were identical")
	}
	// The first may have gone to the wrong person; making a new one is how it
	// is taken back.
	if _, err := s.RedeemLinkCode(first, 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("the retired code: %v, want ErrNotFound", err)
	}
	if _, err := s.RedeemLinkCode(second, 1); err != nil {
		t.Errorf("the current code: %v", err)
	}
}

func TestConnectingAgainMovesThePeerToTheNewChat(t *testing.T) {
	s := newTestStore(t)
	mustPeer(t, s, &Peer{ID: "a", Name: "a", AllowedIPs: "10.0.0.2/32"})
	mustPeer(t, s, &Peer{ID: "b", Name: "b", AllowedIPs: "10.0.0.3/32"})

	for _, c := range []struct {
		peer string
		chat int64
	}{{"a", 1}, {"b", 1}, {"a", 2}} {
		code, _, _ := s.CreateLinkCode(c.peer)
		if _, err := s.RedeemLinkCode(code, c.chat); err != nil {
			t.Fatalf("redeem: %v", err)
		}
	}
	chats := map[string]int64{}
	standings, _ := s.Standings(time.Now().UnixMilli())
	for _, st := range standings {
		chats[st.PeerID] = st.ChatID
	}
	if chats["a"] != 2 || chats["b"] != 1 {
		t.Errorf("chats = %v, want a on the new phone and b where it was", chats)
	}

	// A customer who blocks the bot takes every peer in that chat with them,
	// and nobody else's.
	if n, err := s.UnlinkChat(1); err != nil || n != 1 {
		t.Errorf("unlinking chat 1 = %d, %v; want one peer", n, err)
	}
	if n, _ := s.CountTelegramLinks(); n != 1 {
		t.Errorf("%d links left, want the other chat's", n)
	}
}

func TestDeletingAPeerTakesItsTelegramStateWithIt(t *testing.T) {
	s := newTestStore(t)
	mustPeer(t, s, &Peer{ID: "a", Name: "a", AllowedIPs: "10.0.0.2/32"})
	code, _, _ := s.CreateLinkCode("a")
	if _, err := s.RedeemLinkCode(code, 1); err != nil {
		t.Fatal(err)
	}
	s.CreateLinkCode("a")
	if err := s.MarkWarned("a", WarnUsage, "x"); err != nil {
		t.Fatal(err)
	}

	if _, err := s.DeletePeers([]string{"a"}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	for _, table := range []string{"telegram_links", "telegram_codes", "telegram_warned"} {
		var n int
		s.DB().QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n)
		if n != 0 {
			t.Errorf("%s has %d rows after its peer was deleted", table, n)
		}
	}
}

func TestStandingsAreTheLimitsInForce(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UnixMilli()

	g := &Group{Name: "family", AllowedUsage: 10_000, ExpiresAt: now + 5*day}
	if err := s.CreateGroup(g); err != nil {
		t.Fatal(err)
	}
	// The members' own limits are stale leftovers: a group governs outright.
	mustPeer(t, s, &Peer{ID: "m1", Name: "m1", AllowedIPs: "10.0.0.2/32", GroupID: g.ID, AllowedUsage: 50, ExpiresAt: now + day})
	mustPeer(t, s, &Peer{ID: "m2", Name: "m2", AllowedIPs: "10.0.0.3/32", GroupID: g.ID})
	mustPeer(t, s, &Peer{ID: "solo", Name: "solo", AllowedIPs: "10.0.0.4/32", AllowedUsage: 2_000, ExpiresAt: now + 2*day})
	mustPeer(t, s, &Peer{ID: "bare", Name: "bare", AllowedIPs: "10.0.0.5/32"})
	mustPeer(t, s, &Peer{ID: "unlinked", Name: "unlinked", AllowedIPs: "10.0.0.6/32", AllowedUsage: 1})

	if err := s.ApplyUsageDeltas([]UsageDelta{
		{PeerID: "m1", TX: 3_000}, {PeerID: "m2", TX: 1_000}, {PeerID: "solo", TX: 500},
	}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"m1", "m2", "solo", "bare"} {
		code, _, _ := s.CreateLinkCode(id)
		if _, err := s.RedeemLinkCode(code, 7); err != nil {
			t.Fatal(err)
		}
	}

	got := map[string]Standing{}
	list, err := s.Standings(now)
	if err != nil {
		t.Fatalf("standings: %v", err)
	}
	for _, st := range list {
		got[st.PeerID] = st
	}
	if len(got) != 4 {
		t.Fatalf("%d standings, want the four that are connected", len(got))
	}
	for _, id := range []string{"m1", "m2"} {
		st := got[id]
		// Both members are measured by what the whole group has spent.
		if st.Allowed != 10_000 || st.Used != 4_000 || st.ExpiresAt != g.ExpiresAt || st.GroupID != g.ID {
			t.Errorf("%s: %+v, want the group's 10000 allowed, 4000 used and its expiry", id, st)
		}
	}
	if st := got["solo"]; st.Allowed != 2_000 || st.Used != 500 || st.Status != StatusActive {
		t.Errorf("solo: %+v, want its own limits and active", st)
	}
	if st := got["bare"]; st.Allowed != 0 || st.ExpiresAt != 0 {
		t.Errorf("bare: %+v, want no limits", st)
	}

	// A reset is visible, since it is what makes a warning due a second time.
	if _, err := s.UpdatePeers([]string{"solo"}, PeerPatch{ResetUsage: true}); err != nil {
		t.Fatal(err)
	}
	list, _ = s.Standings(now)
	for _, st := range list {
		if st.PeerID == "solo" && (st.ResetAt == 0 || st.Used != 0) {
			t.Errorf("after a reset: %+v, want a reset time and no usage", st)
		}
	}
}

func TestWarnedMarkersAreKeptPerPeerAndKind(t *testing.T) {
	s := newTestStore(t)
	mustPeer(t, s, &Peer{ID: "a", Name: "a", AllowedIPs: "10.0.0.2/32"})
	mustPeer(t, s, &Peer{ID: "b", Name: "b", AllowedIPs: "10.0.0.3/32"})

	for _, w := range []struct{ peer, kind, marker string }{
		{"a", WarnExpiry, "100"}, {"a", WarnUsage, "u1"}, {"b", WarnExpiry, "200"}, {"a", WarnExpiry, "300"},
	} {
		if err := s.MarkWarned(w.peer, w.kind, w.marker); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Warned()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[WarnedKey{"a", WarnExpiry}] != "300" ||
		got[WarnedKey{"a", WarnUsage}] != "u1" || got[WarnedKey{"b", WarnExpiry}] != "200" {
		t.Errorf("warned = %v, want a later warning to replace an earlier one of the same kind only", got)
	}
}

func TestTheBotsTokenIsASecretNotASetting(t *testing.T) {
	s := newTestStore(t)
	if tok, user, err := s.TelegramToken(); err != nil || tok != "" || user != "" {
		t.Fatalf("a fresh database has token %q / %q (%v)", tok, user, err)
	}
	if err := s.SetTelegramToken("123456:ABCdef", "wgui_bot"); err != nil {
		t.Fatal(err)
	}
	if tok, user, _ := s.TelegramToken(); tok != "123456:ABCdef" || user != "wgui_bot" {
		t.Errorf("token = %q, username = %q", tok, user)
	}

	// Settings are returned by the API and copied to every node; the token must
	// be in neither.
	settings, _ := s.LoadSettings()
	if blob := mustMarshal(t, settings); contains(blob, "123456:ABCdef") {
		t.Error("the token is in the settings")
	}
	rows, _ := s.DB().Query(`SELECT value FROM settings`)
	defer rows.Close()
	for rows.Next() {
		var v string
		rows.Scan(&v)
		if contains(v, "123456:ABCdef") {
			t.Error("the token is in the settings table")
		}
	}

	if err := s.SetTelegramToken("", ""); err != nil {
		t.Fatal(err)
	}
	if tok, user, _ := s.TelegramToken(); tok != "" || user != "" {
		t.Errorf("after removing the bot: %q / %q", tok, user)
	}
}

func TestConnectingAChatStartsFreshForWarnings(t *testing.T) {
	s := newTestStore(t)
	mustPeer(t, s, &Peer{ID: "a", Name: "a", AllowedIPs: "10.0.0.2/32"})
	mustPeer(t, s, &Peer{ID: "b", Name: "b", AllowedIPs: "10.0.0.3/32"})

	code, _, _ := s.CreateLinkCode("a")
	if _, err := s.RedeemLinkCode(code, 1); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		if err := s.MarkWarned(id, WarnUsage, "same-situation"); err != nil {
			t.Fatal(err)
		}
	}

	// The same peer on a new phone: that chat has heard nothing yet.
	code, _, _ = s.CreateLinkCode("a")
	if _, err := s.RedeemLinkCode(code, 2); err != nil {
		t.Fatal(err)
	}
	warned, _ := s.Warned()
	if _, still := warned[WarnedKey{"a", WarnUsage}]; still {
		t.Error("a peer connected to a new chat is still marked as warned")
	}
	// Nobody else's history is touched.
	if _, kept := warned[WarnedKey{"b", WarnUsage}]; !kept {
		t.Error("connecting one peer erased another's warnings")
	}
}
