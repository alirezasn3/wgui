package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"wgui/internal/config"
	"wgui/internal/store"
)

const testToken = "123456:ABCdefGhIJKlmNoPQRsTUVwxyZ_0123456789"

// fakeTelegram is the Bot API as far as the bot uses it.
type fakeTelegram struct {
	*httptest.Server
	mu      sync.Mutex
	updates []map[string]any
	sent    []sentMessage
	// failChat makes sendMessage to a chat fail with the given Telegram error.
	failChat map[int64]fakeFailure
	nextID   int64
	username string
}

type sentMessage struct {
	Chat int64
	Text string
}

type fakeFailure struct {
	code        int
	description string
	retryAfter  int
}

func newFakeTelegram(t *testing.T) *fakeTelegram {
	t.Helper()
	f := &fakeTelegram{failChat: map[int64]fakeFailure{}, nextID: 1, username: "wgui_test_bot"}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply := func(v any) { json.NewEncoder(w).Encode(v) }
		fail := func(code int, desc string) {
			reply(map[string]any{"ok": false, "error_code": code, "description": desc})
		}
		path := strings.TrimPrefix(r.URL.Path, "/bot")
		tok, method, _ := strings.Cut(path, "/")
		if tok != testToken {
			fail(401, "Unauthorized")
			return
		}
		var params map[string]any
		json.NewDecoder(r.Body).Decode(&params)

		switch method {
		case "getMe":
			reply(map[string]any{"ok": true, "result": map[string]any{"id": 1, "username": f.username}})
		case "getUpdates":
			f.mu.Lock()
			out := f.updates
			f.updates = nil
			f.mu.Unlock()
			if len(out) == 0 {
				time.Sleep(10 * time.Millisecond)
				out = []map[string]any{}
			}
			reply(map[string]any{"ok": true, "result": out})
		case "sendMessage":
			chat := int64(params["chat_id"].(float64))
			f.mu.Lock()
			failure, failing := f.failChat[chat]
			if !failing {
				f.sent = append(f.sent, sentMessage{chat, params["text"].(string)})
			}
			f.mu.Unlock()
			if failing {
				body := map[string]any{"ok": false, "error_code": failure.code, "description": failure.description}
				if failure.retryAfter > 0 {
					body["parameters"] = map[string]any{"retry_after": failure.retryAfter}
				}
				reply(body)
				return
			}
			reply(map[string]any{"ok": true, "result": map[string]any{}})
		default:
			fail(404, "no such method")
		}
	}))
	t.Cleanup(f.Close)
	return f
}

// say queues something a customer sent the bot.
func (f *fakeTelegram) say(chatID int64, chatType, text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	f.updates = append(f.updates, map[string]any{
		"update_id": f.nextID,
		"message":   map[string]any{"chat": map[string]any{"id": chatID, "type": chatType}, "text": text},
	})
}

func (f *fakeTelegram) messages() []sentMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sentMessage(nil), f.sent...)
}

func (f *fakeTelegram) forget() {
	f.mu.Lock()
	f.sent = nil
	f.mu.Unlock()
}

type fixture struct {
	t        *testing.T
	st       *store.Store
	tg       *fakeTelegram
	bot      *Bot
	settings config.Settings
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	f := &fixture{t: t, st: st, tg: newFakeTelegram(t), settings: config.DefaultSettings()}
	f.bot = New(Options{
		Store:       st,
		Settings:    func() config.Settings { return f.settings },
		API:         f.tg.URL,
		PollTimeout: 1,
		FirstPass:   20 * time.Millisecond,
		PassEvery:   time.Hour, // passes in these tests are run by hand or by a kick
		SendGap:     time.Millisecond,
	})
	return f
}

func (f *fixture) peer(id, name, ip string, mutate func(*store.Peer)) {
	f.t.Helper()
	p := &store.Peer{ID: id, Name: name, Role: "user", PublicKey: id, AllowedIPs: ip}
	if mutate != nil {
		mutate(p)
	}
	if err := f.st.CreatePeer(p); err != nil {
		f.t.Fatal(err)
	}
}

// connect links a peer to a chat the way a customer does, without the bot.
func (f *fixture) connect(peerID string, chat int64) {
	f.t.Helper()
	code, _, err := f.st.CreateLinkCode(peerID)
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.st.RedeemLinkCode(code, chat); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) client() *client { return newClient(f.tg.URL, testToken, nil) }

func (f *fixture) warn() {
	f.t.Helper()
	if err := f.bot.warn(context.Background(), f.client()); err != nil {
		f.t.Fatalf("warn: %v", err)
	}
}

// run starts the bot with a token set and returns once it has stopped, which
// the cleanup makes happen.
func (f *fixture) run() {
	f.t.Helper()
	if _, err := f.bot.SetToken(context.Background(), testToken); err != nil {
		f.t.Fatalf("set token: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { f.bot.Run(ctx); close(done) }()
	f.t.Cleanup(func() { cancel(); <-done })
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func texts(msgs []sentMessage) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.Text
	}
	return out
}

func TestACustomerConnectsWithTheLinkTheyWereGiven(t *testing.T) {
	f := newFixture(t)
	f.peer("a", "Sara-phone", "10.0.0.2/32", nil)
	f.run()

	link, _, err := f.bot.NewLink("a")
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	prefix := "https://t.me/wgui_test_bot?start="
	if !strings.HasPrefix(link, prefix) {
		t.Fatalf("link = %q, want one into the bot", link)
	}
	code := strings.TrimPrefix(link, prefix)

	// A group is not a customer: linking it would attach everyone in it.
	f.tg.say(900, "group", "/start "+code)
	// Not a link at all, then a wrong one: neither connects anything.
	f.tg.say(100, "private", "hello")
	f.tg.say(100, "private", "/start nonsense")
	eventually(t, "the replies to a stray message and a bad link", func() bool { return len(f.tg.messages()) >= 2 })
	if linked, _ := f.st.TelegramLinked("a"); linked {
		t.Fatal("a peer was connected by something that was not its link")
	}
	got := f.tg.messages()
	msgs := f.settings.Telegram.Messages
	if got[0].Chat != 100 || got[0].Text != msgs.Help || got[1].Text != msgs.Invalid {
		t.Errorf("replies = %+v, want help then invalid, and nothing for the group", got)
	}

	// The command may carry the bot's name, as it does in some clients.
	f.tg.say(100, "private", "/start@wgui_test_bot "+code)
	eventually(t, "the peer to be connected", func() bool {
		linked, _ := f.st.TelegramLinked("a")
		return linked
	})
	eventually(t, "the confirmation", func() bool { return len(f.tg.messages()) >= 3 })
	if last := f.tg.messages()[2]; last.Chat != 100 || !strings.Contains(last.Text, "Sara-phone") {
		t.Errorf("confirmation = %+v, want one to the customer naming the peer", last)
	}
	if st := f.bot.Status(); !st.Configured || st.Username != "wgui_test_bot" || !st.Running || st.Linked != 1 {
		t.Errorf("status = %+v", st)
	}

	// The link has been used.
	f.tg.say(200, "private", "/start "+code)
	eventually(t, "the answer to a spent link", func() bool { return len(f.tg.messages()) >= 4 })
	if linked := f.tg.messages()[3]; linked.Text != msgs.Invalid {
		t.Errorf("a spent link was answered with %q", linked.Text)
	}
}

func TestWarningsAreSentOncePerSituation(t *testing.T) {
	f := newFixture(t)
	now := time.Now().UnixMilli()
	const day = int64(24 * time.Hour / time.Millisecond)
	const gib = int64(1 << 30)

	f.peer("ending", "ending", "10.0.0.2/32", func(p *store.Peer) { p.ExpiresAt = now + day/2 })
	f.peer("far", "far", "10.0.0.3/32", func(p *store.Peer) { p.ExpiresAt = now + 5*day })
	f.peer("low", "low", "10.0.0.4/32", func(p *store.Peer) { p.AllowedUsage = 10 * gib })
	f.peer("plenty", "plenty", "10.0.0.5/32", func(p *store.Peer) { p.AllowedUsage = 100 * gib })
	f.peer("unlimited", "unlimited", "10.0.0.6/32", nil)
	f.peer("unlinked", "unlinked", "10.0.0.7/32", func(p *store.Peer) { p.ExpiresAt = now + day/2 })
	for i, id := range []string{"ending", "far", "low", "plenty", "unlimited"} {
		f.connect(id, int64(10+i))
	}
	if err := f.st.ApplyUsageDeltas([]store.UsageDelta{
		{PeerID: "low", TX: 9*gib + gib/2}, {PeerID: "plenty", TX: gib}, {PeerID: "unlimited", TX: 500 * gib},
	}); err != nil {
		t.Fatal(err)
	}

	f.warn()
	got := f.tg.messages()
	if len(got) != 2 {
		t.Fatalf("sent %v, want a warning for the one ending and the one running low only", texts(got))
	}
	byChat := map[int64]string{}
	for _, m := range got {
		byChat[m.Chat] = m.Text
	}
	if !strings.Contains(byChat[10], "ending") || !strings.Contains(byChat[10], time.UnixMilli(now+day/2).Format("2006-01-02")) {
		t.Errorf("expiry warning = %q, want the name and the date", byChat[10])
	}
	if !strings.Contains(byChat[12], "low") || !strings.Contains(byChat[12], "512.00 MiB") {
		t.Errorf("usage warning = %q, want the name and what is left", byChat[12])
	}

	// A warning is about one situation, and that situation is still going on.
	f.tg.forget()
	f.warn()
	f.warn()
	if got := f.tg.messages(); len(got) != 0 {
		t.Fatalf("the same warnings were sent again: %v", texts(got))
	}

	// Renewed, and then running out again: a new situation.
	if _, err := f.st.UpdatePeers([]string{"ending"}, store.PeerPatch{ExpiresAt: ptr(now + 30*day)}); err != nil {
		t.Fatal(err)
	}
	f.warn()
	if got := f.tg.messages(); len(got) != 0 {
		t.Fatalf("a renewed peer was warned: %v", texts(got))
	}
	if _, err := f.st.UpdatePeers([]string{"ending"}, store.PeerPatch{ExpiresAt: ptr(now + day/4)}); err != nil {
		t.Fatal(err)
	}
	f.warn()
	if got := f.tg.messages(); len(got) != 1 || got[0].Chat != 10 {
		t.Fatalf("after a renewal that ran out again: %v, want one more warning", texts(got))
	}

	// A reset gives it its allowance back; running low afterwards is new too.
	f.tg.forget()
	if _, err := f.st.UpdatePeers([]string{"low"}, store.PeerPatch{ResetUsage: true}); err != nil {
		t.Fatal(err)
	}
	f.warn()
	if got := f.tg.messages(); len(got) != 0 {
		t.Fatalf("a reset peer was warned: %v", texts(got))
	}
	if err := f.st.ApplyUsageDeltas([]store.UsageDelta{{PeerID: "low", TX: 9*gib + gib/4}}); err != nil {
		t.Fatal(err)
	}
	f.warn()
	if got := f.tg.messages(); len(got) != 1 || got[0].Chat != 12 || !strings.Contains(got[0].Text, "768.00 MiB") {
		t.Fatalf("after a reset and running low again: %v", texts(got))
	}
}

func ptr[T any](v T) *T { return &v }

func TestOnlyAPeerThatIsStillWorkingIsWarned(t *testing.T) {
	f := newFixture(t)
	now := time.Now().UnixMilli()
	const day = int64(24 * time.Hour / time.Millisecond)

	f.peer("expired", "expired", "10.0.0.2/32", func(p *store.Peer) { p.ExpiresAt = now - day })
	f.peer("off", "off", "10.0.0.3/32", func(p *store.Peer) { p.ExpiresAt = now + day/2; p.ManuallyDisabled = true })
	f.peer("spent", "spent", "10.0.0.4/32", func(p *store.Peer) { p.AllowedUsage = 100 })
	f.connect("expired", 1)
	f.connect("off", 2)
	f.connect("spent", 3)
	if err := f.st.ApplyUsageDeltas([]store.UsageDelta{{PeerID: "spent", TX: 100}}); err != nil {
		t.Fatal(err)
	}

	f.warn()
	if got := f.tg.messages(); len(got) != 0 {
		t.Errorf("sent %v to peers that are already cut off or switched off", texts(got))
	}
}

func TestAGroupMemberIsWarnedAboutTheGroupsLimits(t *testing.T) {
	f := newFixture(t)
	now := time.Now().UnixMilli()
	const gib = int64(1 << 30)

	g := &store.Group{Name: "family", AllowedUsage: 10 * gib, ExpiresAt: now + 90*24*60*60*1000}
	if err := f.st.CreateGroup(g); err != nil {
		t.Fatal(err)
	}
	// One member is nowhere near a limit of its own — it has used nothing — but
	// the group is nearly spent, and that is what will cut it off.
	f.peer("big", "big", "10.0.0.2/32", func(p *store.Peer) { p.GroupID = g.ID })
	f.peer("idle", "idle", "10.0.0.3/32", func(p *store.Peer) { p.GroupID = g.ID; p.AllowedUsage = 500 * gib })
	f.connect("big", 1)
	f.connect("idle", 2)
	if err := f.st.ApplyUsageDeltas([]store.UsageDelta{{PeerID: "big", TX: 9*gib + gib/2}}); err != nil {
		t.Fatal(err)
	}

	f.warn()
	got := f.tg.messages()
	if len(got) != 2 {
		t.Fatalf("sent %v, want both members told the group is running low", texts(got))
	}
	for _, m := range got {
		if !strings.Contains(m.Text, "512.00 MiB") {
			t.Errorf("%+v does not say what the group has left", m)
		}
	}
}

func TestAFailedSendIsTriedAgain(t *testing.T) {
	f := newFixture(t)
	f.peer("a", "a", "10.0.0.2/32", func(p *store.Peer) { p.ExpiresAt = time.Now().UnixMilli() + 1000 })
	f.connect("a", 5)

	f.tg.failChat[5] = fakeFailure{code: 500, description: "Internal Server Error"}
	if err := f.bot.warn(context.Background(), f.client()); err == nil {
		t.Error("a failed send was not reported")
	}
	if w, _ := f.st.Warned(); len(w) != 0 {
		t.Fatalf("recorded %v as warned though nothing was delivered", w)
	}

	delete(f.tg.failChat, 5)
	f.warn()
	if got := f.tg.messages(); len(got) != 1 {
		t.Errorf("sent %v, want the warning to go out once Telegram recovered", texts(got))
	}
}

func TestACustomerWhoBlockedTheBotIsDisconnected(t *testing.T) {
	f := newFixture(t)
	soon := time.Now().UnixMilli() + 1000
	f.peer("a", "a", "10.0.0.2/32", func(p *store.Peer) { p.ExpiresAt = soon })
	f.peer("b", "b", "10.0.0.3/32", func(p *store.Peer) { p.ExpiresAt = soon })
	f.peer("c", "c", "10.0.0.4/32", func(p *store.Peer) { p.ExpiresAt = soon })
	f.connect("a", 5)
	f.connect("b", 5) // the same customer's second device
	f.connect("c", 6)
	f.tg.failChat[5] = fakeFailure{code: 403, description: "Forbidden: bot was blocked by the user"}

	f.warn()
	for id, want := range map[string]bool{"a": false, "b": false, "c": true} {
		if linked, _ := f.st.TelegramLinked(id); linked != want {
			t.Errorf("%s linked = %v, want %v", id, linked, want)
		}
	}
	// Everyone else still got theirs.
	if got := f.tg.messages(); len(got) != 1 || got[0].Chat != 6 {
		t.Errorf("sent %v, want the warning for the other customer", texts(got))
	}
}

func TestBeingToldToSlowDownEndsThePassWithoutLosingWarnings(t *testing.T) {
	f := newFixture(t)
	// Not a second away: the pass waits out Telegram's retry-after, and a peer
	// that expired in the meantime would rightly no longer be warned.
	soon := time.Now().UnixMilli() + 2*60*60*1000
	f.peer("a", "a", "10.0.0.2/32", func(p *store.Peer) { p.ExpiresAt = soon })
	f.connect("a", 5)
	f.tg.failChat[5] = fakeFailure{code: 429, description: "Too Many Requests: retry after 1", retryAfter: 1}

	start := time.Now()
	f.warn() // waits out the retry-after, then leaves the rest for the next pass
	if time.Since(start) < 900*time.Millisecond {
		t.Error("did not wait as long as Telegram asked")
	}
	if w, _ := f.st.Warned(); len(w) != 0 {
		t.Errorf("recorded %v as warned though nothing was delivered", w)
	}
	delete(f.tg.failChat, 5)
	f.warn()
	if got := f.tg.messages(); len(got) != 1 {
		t.Errorf("sent %v, want the warning at the next pass", texts(got))
	}
}

func TestThresholdsOfZeroTurnTheWarningsOff(t *testing.T) {
	f := newFixture(t)
	now := time.Now().UnixMilli()
	f.peer("a", "a", "10.0.0.2/32", func(p *store.Peer) { p.ExpiresAt = now + 1000; p.AllowedUsage = 100 })
	f.connect("a", 5)

	f.settings.Telegram.WarnDays, f.settings.Telegram.WarnUsageBytes = 0, 0
	f.warn()
	if got := f.tg.messages(); len(got) != 0 {
		t.Fatalf("sent %v with both warnings off", texts(got))
	}

	// Each can be off on its own.
	f.settings.Telegram.WarnDays, f.settings.Telegram.WarnUsageBytes = 1, 0
	f.warn()
	if got := f.tg.messages(); len(got) != 1 || !strings.Contains(got[0].Text, "expires") {
		t.Fatalf("sent %v with only the expiry warning on", texts(got))
	}
}

func TestMessagesAreWhatTheOperatorWrote(t *testing.T) {
	f := newFixture(t)
	now := time.Now().UnixMilli()
	const hour = int64(time.Hour / time.Millisecond)
	f.peer("a", "اشتراک-من", "10.0.0.2/32", func(p *store.Peer) { p.ExpiresAt = now + 5*hour + hour/2 })
	f.connect("a", 5)

	f.settings.Telegram.Messages.ExpiryWarning = "اشتراک {name} تا {hours} ساعت دیگر ({days} روز) تمام می‌شود. {nope}"
	f.warn()
	got := f.tg.messages()
	if len(got) != 1 {
		t.Fatalf("sent %v", texts(got))
	}
	// Hours and days round up: 5.5 hours left is "6 hours", and the day it falls
	// in is the first. An unknown placeholder is left for the operator to see.
	if want := "اشتراک اشتراک-من تا 6 ساعت دیگر (1 روز) تمام می‌شود. {nope}"; got[0].Text != want {
		t.Errorf("message = %q, want %q", got[0].Text, want)
	}
}

func TestTheTokenIsCheckedBeforeItIsKept(t *testing.T) {
	f := newFixture(t)

	// Not the shape of a token: refused without troubling Telegram, which
	// matters because the token goes into a URL.
	for _, bad := range []string{"hello", "123:abc", "123456:abc/../def", "123456:ABCdefGhIJKlmNoPQRsTUV wxyZ"} {
		if _, err := f.bot.SetToken(context.Background(), bad); !errors.Is(err, ErrBadToken) {
			t.Errorf("%q: %v, want ErrBadToken", bad, err)
		}
	}

	// The right shape, but Telegram does not know it.
	wrong := "999999:ZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZ"
	_, err := f.bot.SetToken(context.Background(), wrong)
	if err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("a token Telegram refuses: %v, want it to say so", err)
	}
	if strings.Contains(err.Error(), wrong) {
		t.Error("the error repeats the token")
	}
	if tok, _, _ := f.st.TelegramToken(); tok != "" {
		t.Error("a token Telegram refused was kept")
	}

	st, err := f.bot.SetToken(context.Background(), "  "+testToken+"\n")
	if err != nil || !st.Configured || st.Username != "wgui_test_bot" {
		t.Fatalf("a good token: %+v, %v", st, err)
	}
	if st, _ := f.bot.SetToken(context.Background(), ""); st.Configured {
		t.Error("the bot is still configured after its token was removed")
	}
	if _, _, err := f.bot.NewLink("a"); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("a link without a bot: %v, want ErrNotConfigured", err)
	}
}

func TestTheTokenNeverReachesAnError(t *testing.T) {
	// A server that is not there: Go's own error would quote the URL, and the
	// token is in the URL.
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	c := newClient(dead.URL, testToken, nil)

	for name, call := range map[string]func() error{
		"getMe":       func() error { _, err := c.getMe(context.Background()); return err },
		"getUpdates":  func() error { _, err := c.getUpdates(context.Background(), 0, 1); return err },
		"sendMessage": func() error { return c.sendMessage(context.Background(), 1, "hi") },
	} {
		err := call()
		if err == nil {
			t.Fatalf("%s reached a server that is not there", name)
		}
		if strings.Contains(err.Error(), testToken) || strings.Contains(describe(err), testToken) {
			t.Errorf("%s leaks the token: %v", name, err)
		}
		if !strings.Contains(describe(err), "api.telegram.org") {
			t.Errorf("%s: %q does not say what to check", name, describe(err))
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	time.Sleep(5 * time.Millisecond)
	if err := c.sendMessage(ctx, 1, "hi"); err == nil || strings.Contains(err.Error(), testToken) {
		t.Errorf("a timeout: %v", err)
	}
}

func TestStatusSaysWhyTheBotIsNotWorking(t *testing.T) {
	f := newFixture(t)
	if st := f.bot.Status(); st.Configured || st.Running || st.Error != "" {
		t.Errorf("before anything is set: %+v", st)
	}

	// Another program polling the same bot: Telegram answers 409 to the second.
	conflict := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/getMe") {
			w.Write([]byte(`{"ok":true,"result":{"id":1,"username":"wgui_test_bot"}}`))
			return
		}
		w.Write([]byte(`{"ok":false,"error_code":409,"description":"Conflict: terminated by other getUpdates request"}`))
	}))
	defer conflict.Close()
	f.bot.opt.API = conflict.URL
	f.run()

	eventually(t, "the conflict to be reported", func() bool { return f.bot.Status().Error != "" })
	st := f.bot.Status()
	if st.Running || !strings.Contains(st.Error, "Only one thing can") || strings.Contains(st.Error, testToken) {
		t.Errorf("status = %+v, want an explanation of the conflict", st)
	}
}

// A customer who comes back — a new phone, or after blocking the bot — is told
// where they stand again, even though the situation has not changed since the
// last warning.
func TestReconnectingWarnsTheNewChatAgain(t *testing.T) {
	f := newFixture(t)
	f.peer("a", "a", "10.0.0.2/32", func(p *store.Peer) { p.ExpiresAt = time.Now().UnixMilli() + 1000 })
	f.connect("a", 5)
	f.warn()
	f.warn()
	if got := f.tg.messages(); len(got) != 1 || got[0].Chat != 5 {
		t.Fatalf("first chat: %v, want one warning", texts(got))
	}

	f.connect("a", 6)
	f.tg.forget()
	f.warn()
	if got := f.tg.messages(); len(got) != 1 || got[0].Chat != 6 {
		t.Fatalf("new chat: %v, want it warned once, and the old one left alone", texts(got))
	}
}

func TestListSaysWhereEachSubscriptionStands(t *testing.T) {
	f := newFixture(t)
	now := time.Now().UnixMilli()
	const gib = int64(1 << 30)
	const day = int64(24 * time.Hour / time.Millisecond)

	g := &store.Group{Name: "family", AllowedUsage: 100 * gib, ExpiresAt: now + 10*day}
	if err := f.st.CreateGroup(g); err != nil {
		t.Fatal(err)
	}
	f.peer("solo", "Sara-phone", "10.0.0.2/32", func(p *store.Peer) { p.AllowedUsage = 30 * gib; p.ExpiresAt = now + 3*day })
	f.peer("kid", "Sara-kid", "10.0.0.3/32", func(p *store.Peer) { p.GroupID = g.ID })
	f.peer("mom", "Sara-mom", "10.0.0.4/32", func(p *store.Peer) { p.GroupID = g.ID })
	f.peer("free", "Sara-tv", "10.0.0.5/32", nil)
	f.peer("else", "Omid-tv", "10.0.0.6/32", nil)
	for _, id := range []string{"solo", "kid", "mom", "free"} {
		f.connect(id, 100)
	}
	f.connect("else", 200)
	if err := f.st.ApplyUsageDeltas([]store.UsageDelta{
		{PeerID: "solo", TX: 5 * gib},
		{PeerID: "kid", TX: 2 * gib},
		{PeerID: "mom", TX: 40 * gib},
	}); err != nil {
		t.Fatal(err)
	}
	// A warning would also be sent to this chat; /list is what is under test.
	f.settings.Telegram.WarnDays, f.settings.Telegram.WarnUsageBytes = 0, 0
	f.settings.Telegram.Messages.List = "{name} [{group}] used {used} of {allowed}, {remaining} left, expires {expires} ({days} days)"
	f.run()

	f.tg.say(100, "private", "/list@wgui_test_bot")
	eventually(t, "the list", func() bool { return len(f.tg.messages()) >= 1 })
	got := f.tg.messages()
	if len(got) != 1 || got[0].Chat != 100 {
		t.Fatalf("sent %v, want one message to the chat that asked", texts(got))
	}
	lines := strings.Split(got[0].Text, "\n\n")
	if len(lines) != 4 {
		t.Fatalf("%d subscriptions in %q, want the four connected to this chat and not the other chat's", len(lines), got[0].Text)
	}
	exp := time.UnixMilli(now + 3*day).Format("2006-01-02 15:04 MST")
	gexp := time.UnixMilli(g.ExpiresAt).Format("2006-01-02 15:04 MST")
	for i, want := range []string{
		// Alphabetical. A member's "used" is its own share of the group's
		// allowance, and what is left is the group's — 42 GiB has gone in all.
		"Sara-kid [family] used 2.00 GiB of 100.00 GiB, 58.00 GiB left, expires " + gexp + " (10 days)",
		"Sara-mom [family] used 40.00 GiB of 100.00 GiB, 58.00 GiB left, expires " + gexp + " (10 days)",
		"Sara-phone [] used 5.00 GiB of 30.00 GiB, 25.00 GiB left, expires " + exp + " (3 days)",
		"Sara-tv [] used 0 B of ∞, ∞ left, expires ∞ (∞ days)",
	} {
		if lines[i] != want {
			t.Errorf("subscription %d:\n got %q\nwant %q", i+1, lines[i], want)
		}
	}
}

func TestListWithNothingConnected(t *testing.T) {
	f := newFixture(t)
	f.run()
	f.tg.say(300, "private", "/list")
	eventually(t, "the answer", func() bool { return len(f.tg.messages()) >= 1 })
	if got := f.tg.messages()[0]; got.Text != f.settings.Telegram.Messages.ListEmpty {
		t.Errorf("answer = %q, want the empty-list message", got.Text)
	}

	// The same command in a group does not answer at all: only a private chat
	// is ever spoken to.
	f.tg.forget()
	f.tg.say(-400, "group", "/list")
	f.tg.say(300, "private", "/help") // a marker that the loop has got past it
	eventually(t, "the marker", func() bool { return len(f.tg.messages()) >= 1 })
	if got := f.tg.messages(); len(got) != 1 || got[0].Chat != 300 {
		t.Errorf("sent %v, want only the private chat answered", texts(got))
	}
}

func TestAPastExpiryAndAnEmptyAllowanceReadAsZero(t *testing.T) {
	f := newFixture(t)
	now := time.Now().UnixMilli()
	f.peer("late", "late", "10.0.0.2/32", func(p *store.Peer) { p.ExpiresAt = now - 3*60*60*1000; p.AllowedUsage = 100 })
	f.connect("late", 1)
	if err := f.st.ApplyUsageDeltas([]store.UsageDelta{{PeerID: "late", TX: 250}}); err != nil {
		t.Fatal(err)
	}
	f.settings.Telegram.Messages.List = "{remaining} left, {days} days"
	f.run()
	f.tg.say(1, "private", "/list")
	eventually(t, "the list", func() bool { return len(f.tg.messages()) >= 1 })
	// Never "-150 B left" or "-1 days": a customer is not told a negative.
	if got := f.tg.messages()[0].Text; got != "0 B left, 0 days" {
		t.Errorf("list = %q, want zeros for what has run out", got)
	}
}

func TestALongListIsSplitBetweenSubscriptionsNotThroughOne(t *testing.T) {
	f := newFixture(t)
	f.settings.Telegram.WarnDays, f.settings.Telegram.WarnUsageBytes = 0, 0
	// Each block is a few hundred characters, so sixty of them cannot go in one
	// message.
	f.settings.Telegram.Messages.List = "{name}\n" + strings.Repeat("padding ", 40)
	var names []string
	for i := 0; i < 60; i++ {
		name := "peer-" + strings.Repeat("x", i%3) + string(rune('a'+i%26)) + string(rune('A'+i/26))
		names = append(names, name)
		f.peer(name, name, "10.0."+string(rune('0'+i/10))+"."+string(rune('0'+i%10))+"/32", nil)
		f.connect(name, 9)
	}
	f.run()
	f.tg.say(9, "private", "/list")
	eventually(t, "every part of the list", func() bool {
		total := 0
		for _, m := range f.tg.messages() {
			total += strings.Count(m.Text, "peer-")
		}
		return total == 60
	})

	got := f.tg.messages()
	if len(got) < 2 {
		t.Fatalf("%d messages, want the list split", len(got))
	}
	seen := map[string]int{}
	for _, m := range got {
		if n := len([]rune(m.Text)); n > 4096 {
			t.Errorf("a message is %d characters, over Telegram's 4096", n)
		}
		// Whole blocks only: each has all of its padding.
		for _, block := range strings.Split(m.Text, "\n\n") {
			if n := strings.Count(block, "padding"); n != 40 {
				t.Errorf("a subscription was cut short: %d of 40 words in %q", n, block[:min(len(block), 30)])
			}
			seen[strings.SplitN(block, "\n", 2)[0]]++
		}
	}
	for _, name := range names {
		if seen[name] != 1 {
			t.Errorf("%s appears %d times, want exactly once", name, seen[name])
		}
	}
}
