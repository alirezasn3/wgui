package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"wgui/internal/config"
	"wgui/internal/store"
	"wgui/internal/telegram"
)

// stubBot stands in for the bot: what is under test here is who may ask it for
// things, not what Telegram does.
type stubBot struct {
	status   telegram.Status
	tokenErr error
	tokens   []string
}

func (b *stubBot) Status() telegram.Status { return b.status }

func (b *stubBot) SetToken(_ context.Context, token string) (telegram.Status, error) {
	b.tokens = append(b.tokens, token)
	if b.tokenErr != nil {
		return telegram.Status{}, b.tokenErr
	}
	b.status.Configured = token != ""
	return b.status, nil
}

func (b *stubBot) NewLink(peerID string) (string, int64, error) {
	if !b.status.Configured {
		return "", 0, telegram.ErrNotConfigured
	}
	return "https://t.me/" + b.status.Username + "?start=code-for-" + peerID, 123, nil
}

func telegramFixture(t *testing.T) (*fixture, *stubBot) {
	t.Helper()
	f := newFixture(t)
	bot := &stubBot{status: telegram.Status{Configured: true, Username: "wgui_bot", Running: true}}
	f.srv.SetTelegram(bot)
	return f, bot
}

func TestOnlyAdminsMakeLinksButMoreCanDisconnect(t *testing.T) {
	f, _ := telegramFixture(t)
	f.peer("admin", "adm", "10.0.0.2/32", config.RoleAdmin, nil)
	f.peer("reseller", "res", "10.0.0.3/32", config.RoleDistributor, nil)
	f.peer("other-reseller", "oth", "10.0.0.4/32", config.RoleDistributor, nil)
	f.peer("mine", "mine", "10.0.0.5/32", config.RoleUser, func(p *store.Peer) { p.OwnerID = "res" })
	f.peer("theirs", "theirs", "10.0.0.6/32", config.RoleUser, func(p *store.Peer) { p.OwnerID = "oth" })
	f.peer("friend", "friend", "10.0.0.7/32", config.RoleUser, nil)
	if err := f.store.SetVisibility("friend", []string{"mine"}); err != nil {
		t.Fatal(err)
	}

	// Making a link: an admin, anyone's. Nobody else, whoever the peer is —
	// including a peer that does not exist, so the answer is the same and
	// reveals nothing.
	for _, tc := range []struct {
		who, peer string
		want      int
	}{
		{"10.0.0.2", "mine", http.StatusOK},
		{"10.0.0.2", "res", http.StatusOK},
		{"10.0.0.3", "mine", http.StatusForbidden},    // a reseller, its own customer
		{"10.0.0.3", "res", http.StatusForbidden},     // a reseller, itself
		{"10.0.0.3", "theirs", http.StatusForbidden},  // another reseller's customer
		{"10.0.0.3", "no-such", http.StatusForbidden}, // nothing at all
		{"10.0.0.5", "mine", http.StatusForbidden},    // a customer, their own
		{"10.0.0.7", "mine", http.StatusForbidden},    // seeing a peer is not owning it
	} {
		rec := f.as(tc.who, http.MethodPost, "/api/peers/"+tc.peer+"/telegram", "")
		if rec.Code != tc.want {
			t.Errorf("%s making a link for %s: got %d, want %d (%s)", tc.who, tc.peer, rec.Code, tc.want, rec.Body.String())
		}
	}

	// Disconnecting: whoever may change the peer, and the peer itself.
	connect := func(id string) {
		t.Helper()
		code, _, _ := f.store.CreateLinkCode(id)
		if _, err := f.store.RedeemLinkCode(code, 1); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		who, peer string
		want      int
	}{
		{"10.0.0.2", "mine", http.StatusNoContent},  // an admin, anyone's
		{"10.0.0.3", "mine", http.StatusNoContent},  // a reseller, its own customer
		{"10.0.0.5", "mine", http.StatusNoContent},  // the customer, their own
		{"10.0.0.3", "theirs", http.StatusNotFound}, // another reseller's customer is not even visible
		{"10.0.0.5", "theirs", http.StatusNotFound}, // nor is a stranger's to a customer
		{"10.0.0.7", "mine", http.StatusForbidden},  // seeing a peer is not owning it
	} {
		connect("mine")
		rec := f.as(tc.who, http.MethodDelete, "/api/peers/"+tc.peer+"/telegram", "")
		if rec.Code != tc.want {
			t.Errorf("%s disconnecting %s: got %d, want %d", tc.who, tc.peer, rec.Code, tc.want)
		}
	}

	var out struct {
		Link      string `json:"link"`
		ExpiresAt int64  `json:"expiresAt"`
	}
	f.decode(f.as("10.0.0.2", http.MethodPost, "/api/peers/mine/telegram", ""), &out)
	if out.Link != "https://t.me/wgui_bot?start=code-for-mine" || out.ExpiresAt != 123 {
		t.Errorf("link = %+v", out)
	}
}

func TestPeerTelegramStateAndAnUnsetBot(t *testing.T) {
	f, bot := telegramFixture(t)
	f.peer("admin", "adm", "10.0.0.2/32", config.RoleAdmin, nil)
	f.peer("c", "c", "10.0.0.3/32", config.RoleUser, nil)

	state := func() (available, linked bool) {
		t.Helper()
		var out struct{ Available, Linked bool }
		rec := f.as("10.0.0.3", http.MethodGet, "/api/peers/c/telegram", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("state: %d %s", rec.Code, rec.Body.String())
		}
		f.decode(rec, &out)
		return out.Available, out.Linked
	}
	if a, l := state(); !a || l {
		t.Errorf("fresh: available=%v linked=%v, want available and not linked", a, l)
	}
	code, _, _ := f.store.CreateLinkCode("c")
	if _, err := f.store.RedeemLinkCode(code, 42); err != nil {
		t.Fatal(err)
	}
	if a, l := state(); !a || !l {
		t.Errorf("connected: available=%v linked=%v", a, l)
	}

	// With no bot, there is nothing to offer and nothing to make a link to.
	bot.status = telegram.Status{}
	if a, _ := state(); a {
		t.Error("a bot that is not set up is offered")
	}
	f.store.UnlinkPeer("c")
	rec := f.as("10.0.0.2", http.MethodPost, "/api/peers/c/telegram", "")
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "Settings") {
		t.Errorf("a link with no bot: %d %s, want 409 pointing at Settings", rec.Code, rec.Body.String())
	}
}

func TestTheBotIsSetUpByAdminsOnly(t *testing.T) {
	f, bot := telegramFixture(t)
	f.peer("admin", "adm", "10.0.0.2/32", config.RoleAdmin, nil)
	f.peer("reseller", "res", "10.0.0.3/32", config.RoleDistributor, nil)

	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/telegram", ""},
		{http.MethodPut, "/api/telegram/token", `{"token":"123456:ABCdefGhIJKlmNoPQRsTUV"}`},
	} {
		if rec := f.as("10.0.0.3", tc.method, tc.path, tc.body); rec.Code != http.StatusForbidden {
			t.Errorf("reseller %s %s: got %d, want 403", tc.method, tc.path, rec.Code)
		}
	}
	if len(bot.tokens) != 0 {
		t.Fatalf("a reseller's request reached the bot: %v", bot.tokens)
	}

	if rec := f.as("10.0.0.2", http.MethodGet, "/api/telegram", ""); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"username":"wgui_bot"`) {
		t.Errorf("status: %d %s", rec.Code, rec.Body.String())
	}

	put := func(body string) (int, string) {
		rec := f.as("10.0.0.2", http.MethodPut, "/api/telegram/token", body)
		return rec.Code, rec.Body.String()
	}
	bot.tokenErr = telegram.ErrBadToken
	if code, _ := put(`{"token":"nonsense"}`); code != http.StatusBadRequest {
		t.Errorf("a malformed token: %d, want 400", code)
	}
	bot.tokenErr = &telegram.RemoteError{Msg: "Telegram rejected the bot's token"}
	if code, body := put(`{"token":"123456:ABCdefGhIJKlmNoPQRsTUV"}`); code != http.StatusBadGateway || !strings.Contains(body, "rejected") {
		t.Errorf("a token Telegram refuses: %d %s, want 502 saying so", code, body)
	}
	bot.tokenErr = errors.New("disk full")
	if code, body := put(`{"token":"123456:ABCdefGhIJKlmNoPQRsTUV"}`); code != http.StatusInternalServerError || strings.Contains(body, "disk full") {
		t.Errorf("a failure of our own: %d %s, want 500 without the detail", code, body)
	}
	bot.tokenErr = nil
	if code, _ := put(`{"token":""}`); code != http.StatusOK || bot.status.Configured {
		t.Errorf("removing the bot: %d, configured=%v", code, bot.status.Configured)
	}
}

// fakeMaster records what a node carries to its master.
type fakeMaster struct {
	mu       sync.Mutex
	requests []string
	syncs    int
}

func (m *fakeMaster) Forward(_ context.Context, r Forwarded) (*ForwardedResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests = append(m.requests, r.Method+" "+r.Path+" as "+r.PeerID)
	return &ForwardedResponse{Status: http.StatusOK, Headers: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{}`)}, nil
}

func (m *fakeMaster) SyncNow(context.Context) {
	m.mu.Lock()
	m.syncs++
	m.mu.Unlock()
}

// The bot is on the master, so a node's panel has to carry every Telegram
// request there — and reading one is not a change worth syncing for.
func TestANodeCarriesTelegramToItsMaster(t *testing.T) {
	f := newFixture(t) // no bot: a node does not run one
	f.peer("admin", "adm", "10.0.0.2/32", config.RoleAdmin, nil)
	f.peer("c", "c", "10.0.0.3/32", config.RoleUser, nil)
	master := &fakeMaster{}
	f.srv.SetForwarder(master)
	f.srv.cfg.Master.URL = "https://master.example.com"

	for _, tc := range []struct{ who, method, path, body string }{
		{"10.0.0.2", http.MethodGet, "/api/telegram", ""},
		{"10.0.0.2", http.MethodPut, "/api/telegram/token", `{"token":""}`},
		{"10.0.0.3", http.MethodGet, "/api/peers/c/telegram", ""},
		{"10.0.0.3", http.MethodPost, "/api/peers/c/telegram", ""},
		{"10.0.0.3", http.MethodDelete, "/api/peers/c/telegram", ""},
	} {
		if rec := f.as(tc.who, tc.method, tc.path, tc.body); rec.Code != http.StatusOK {
			t.Errorf("%s %s on a node: %d %s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}
	want := []string{
		"GET /api/telegram as adm",
		"PUT /api/telegram/token as adm",
		"GET /api/peers/c/telegram as c",
		"POST /api/peers/c/telegram as c",
		"DELETE /api/peers/c/telegram as c",
	}
	if strings.Join(master.requests, "|") != strings.Join(want, "|") {
		t.Errorf("master received %v, want %v", master.requests, want)
	}
	// Only the three that change something are worth pulling the master's
	// state back down for.
	if master.syncs != 3 {
		t.Errorf("synced %d times, want 3: reads should not sync", master.syncs)
	}
}
