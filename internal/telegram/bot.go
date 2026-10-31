package telegram

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"wgui/internal/config"
	"wgui/internal/store"
)

var (
	// ErrNotConfigured is returned when there is no bot to build a link to.
	ErrNotConfigured = errors.New("the Telegram bot is not set up; an admin can add its token in Settings")
	// ErrNoUsername is returned when a token is stored but Telegram has not yet
	// said which bot it is.
	ErrNoUsername = errors.New("the bot has not reached Telegram yet, so there is no link to give out; see Settings")
	// ErrBadToken is returned for something that cannot be a bot token.
	ErrBadToken = errors.New("that does not look like a bot token; BotFather gives one like 123456:ABC-def…")
)

const (
	day  = int64(24 * time.Hour / time.Millisecond)
	hour = int64(time.Hour / time.Millisecond)
)

// RemoteError is Telegram, or the way to it, being the problem rather than
// anything wrong with the request, so the panel can tell the two apart.
type RemoteError struct{ Msg string }

func (e *RemoteError) Error() string { return e.Msg }

// Options wires a Bot to the server it runs in.
type Options struct {
	Store *store.Store
	Log   *slog.Logger
	// Settings returns the current settings, so a change to a threshold or a
	// message applies to the next warning without a restart.
	Settings func() config.Settings

	// For tests; each has a sensible default.
	API         string
	Client      *http.Client
	PollTimeout int           // seconds Telegram may hold a request open
	FirstPass   time.Duration // how long after starting the first warning pass runs
	PassEvery   time.Duration // how often warnings are looked for
	SendGap     time.Duration // pause between messages, to stay under Telegram's rate limits
}

// Status is what the panel shows about the bot.
type Status struct {
	// Configured is whether a token has been set.
	Configured bool   `json:"configured"`
	Username   string `json:"username"`
	// Running is whether the bot is in touch with Telegram right now.
	Running bool `json:"running"`
	// Error is why it is not, in words that say what to do.
	Error string `json:"error,omitempty"`
	// Linked is how many peers are connected to a chat.
	Linked int `json:"linked"`
}

// Bot is the warning bot. It runs on the master only: two servers reading one
// bot's messages take them from each other, and the warnings would go out twice.
type Bot struct {
	opt Options

	mu      sync.Mutex
	running bool
	lastErr string

	changed chan struct{} // the token was set or removed
	kick    chan struct{} // look for warnings now
}

func New(opt Options) *Bot {
	if opt.Log == nil {
		opt.Log = slog.New(slog.DiscardHandler)
	}
	if opt.PollTimeout == 0 {
		opt.PollTimeout = 30
	}
	if opt.FirstPass == 0 {
		opt.FirstPass = 5 * time.Second
	}
	if opt.PassEvery == 0 {
		opt.PassEvery = time.Minute
	}
	if opt.SendGap == 0 {
		// Telegram allows about thirty messages a second across a bot.
		opt.SendGap = 50 * time.Millisecond
	}
	return &Bot{opt: opt, changed: make(chan struct{}, 1), kick: make(chan struct{}, 1)}
}

func signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

func (b *Bot) setState(running bool, err string) {
	b.mu.Lock()
	b.running, b.lastErr = running, err
	b.mu.Unlock()
}

// Status reports whether the bot is set up and working.
func (b *Bot) Status() Status {
	token, username, _ := b.opt.Store.TelegramToken()
	linked, _ := b.opt.Store.CountTelegramLinks()
	st := Status{Configured: token != "", Username: username, Linked: linked}
	if st.Configured {
		b.mu.Lock()
		st.Running, st.Error = b.running, b.lastErr
		b.mu.Unlock()
	}
	return st
}

// SetToken makes token the bot's, after asking Telegram whether it is one. An
// empty token removes the bot. A token Telegram does not accept is not stored,
// so the operator hears about a typo now rather than finding the bot silent.
func (b *Bot) SetToken(ctx context.Context, token string) (Status, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		if err := b.opt.Store.SetTelegramToken("", ""); err != nil {
			return Status{}, err
		}
		b.setState(false, "")
		signal(b.changed)
		return b.Status(), nil
	}
	if !ValidToken(token) {
		return Status{}, ErrBadToken
	}

	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	me, err := newClient(b.opt.API, token, b.opt.Client).getMe(ctx)
	if err != nil {
		return Status{}, &RemoteError{Msg: describe(err)}
	}
	if err := b.opt.Store.SetTelegramToken(token, me.Username); err != nil {
		return Status{}, err
	}
	b.setState(false, "")
	signal(b.changed)
	return b.Status(), nil
}

// NewLink makes the link a customer opens to connect their chat to a peer.
func (b *Bot) NewLink(peerID string) (link string, expiresAt int64, err error) {
	token, username, err := b.opt.Store.TelegramToken()
	if err != nil {
		return "", 0, err
	}
	if token == "" {
		return "", 0, ErrNotConfigured
	}
	if username == "" {
		return "", 0, ErrNoUsername
	}
	code, expiresAt, err := b.opt.Store.CreateLinkCode(peerID)
	if err != nil {
		return "", 0, err
	}
	return "https://t.me/" + username + "?start=" + code, expiresAt, nil
}

// Run keeps the bot going until ctx is cancelled. It follows the token: with
// none set it waits, and setting, replacing or removing one in the panel takes
// effect straight away.
func (b *Bot) Run(ctx context.Context) {
	for ctx.Err() == nil {
		token, _, err := b.opt.Store.TelegramToken()
		if err != nil {
			b.opt.Log.Error("reading the Telegram token failed", "error", err)
		}
		if token == "" {
			b.setState(false, "")
			select {
			case <-ctx.Done():
				return
			case <-b.changed:
			case <-time.After(10 * time.Second):
			}
			continue
		}

		sctx, cancel := context.WithCancel(ctx)
		go func() {
			select {
			case <-b.changed:
				cancel()
			case <-sctx.Done():
			}
		}()
		b.session(sctx, newClient(b.opt.API, token, b.opt.Client))
		cancel()
	}
}

// session is the bot while one token is in force.
func (b *Bot) session(ctx context.Context, c *client) {
	// Say which bot this is: the username is what links are built from, and it
	// is worth refreshing in case the bot was renamed.
	if me, err := c.getMe(ctx); err == nil {
		if token, _, _ := b.opt.Store.TelegramToken(); token == c.token {
			_ = b.opt.Store.SetTelegramToken(c.token, me.Username)
		}
		b.opt.Log.Info("telegram bot ready", "bot", me.Username)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		b.warnLoop(ctx, c)
	}()
	b.pollLoop(ctx, c)
	wg.Wait()
}

func (b *Bot) pollLoop(ctx context.Context, c *client) {
	var offset int64
	backoff := time.Second
	for ctx.Err() == nil {
		updates, err := c.getUpdates(ctx, offset, b.opt.PollTimeout)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			msg := describe(err)
			if b.errorChanged(msg) {
				b.opt.Log.Warn("telegram is not answering", "error", msg)
			}
			b.setState(false, msg)

			wait := backoff
			var apiErr *APIError
			if errors.As(err, &apiErr) && (apiErr.Code == http.StatusUnauthorized || apiErr.Code == http.StatusConflict) {
				// Neither goes away by asking again soon.
				wait = time.Minute
			}
			if !sleep(ctx, wait) {
				return
			}
			backoff = min(backoff*2, time.Minute)
			continue
		}
		backoff = time.Second
		b.setState(true, "")

		for _, u := range updates {
			offset = u.UpdateID + 1
			b.handle(ctx, c, u)
		}
	}
}

func (b *Bot) errorChanged(msg string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.lastErr != msg
}

// handle answers one thing a customer sent.
func (b *Bot) handle(ctx context.Context, c *client, u update) {
	m := u.Message
	// Only a private chat is ever connected. In a group the link would attach
	// the whole group — everyone in it, now and later — to one peer's usage.
	if m == nil || m.Chat.Type != "private" {
		return
	}
	msgs := b.opt.Settings().Telegram.Messages

	fields := strings.Fields(m.Text)
	cmd := ""
	if len(fields) > 0 {
		cmd = command(fields[0])
	}
	switch {
	case cmd == "/list":
		b.list(ctx, c, m.Chat.ID)
		return
	case cmd != "/start" || len(fields) < 2:
		b.reply(ctx, c, m.Chat.ID, msgs.Help)
		return
	}

	name, err := b.opt.Store.RedeemLinkCode(fields[1], m.Chat.ID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		b.reply(ctx, c, m.Chat.ID, msgs.Invalid)
	case err != nil:
		b.opt.Log.Error("connecting a chat failed", "error", err)
	default:
		b.opt.Log.Info("telegram chat connected", "peer", name)
		b.reply(ctx, c, m.Chat.ID, render(msgs.Linked, map[string]string{"name": name}))
		// Someone connecting with little left should hear so now, not at the
		// next pass.
		signal(b.kick)
	}
}

// maxChunk keeps a reply well inside Telegram's limit of 4096 characters.
const maxChunk = 3500

// list tells a customer where each subscription connected to their chat stands.
//
// What it calls "used" is the customer's own use, and "left" is what will cut
// them off: for a group member those are different numbers, the member's share
// and the group's remainder, and the panel shows the same split.
func (b *Bot) list(ctx context.Context, c *client, chatID int64) {
	msgs := b.opt.Settings().Telegram.Messages
	now := time.Now().UnixMilli()

	standings, err := b.opt.Store.ChatStandings(now, chatID)
	if err != nil {
		b.opt.Log.Error("listing a chat's subscriptions failed", "error", err)
		return
	}
	if len(standings) == 0 {
		b.reply(ctx, c, chatID, msgs.ListEmpty)
		return
	}

	// Cut between subscriptions, never through one, so a customer with many is
	// not sent half a block.
	var chunk []string
	size := 0
	flush := func() {
		if len(chunk) > 0 {
			b.reply(ctx, c, chatID, strings.Join(chunk, "\n\n"))
			chunk, size = nil, 0
		}
	}
	for _, st := range standings {
		item := render(msgs.List, listValues(st, now))
		n := len([]rune(item)) + 2 // and the blank line that separates it
		if size+n > maxChunk && len(chunk) > 0 {
			flush()
		}
		chunk = append(chunk, item)
		size += n
	}
	flush()
}

// unlimited is how a missing limit reads in /list: a symbol, so it needs no
// translating whatever language the rest of the message is in.
const unlimited = "∞"

func listValues(st store.Standing, now int64) map[string]string {
	v := map[string]string{
		"name":      st.Name,
		"group":     st.GroupName,
		"used":      formatBytes(st.OwnUsed),
		"allowed":   unlimited,
		"remaining": unlimited,
		"expires":   unlimited,
		"days":      unlimited,
	}
	if st.Allowed > 0 {
		v["allowed"] = formatBytes(st.Allowed)
		v["remaining"] = formatBytes(max(st.Allowed-st.Used, 0))
	}
	if st.ExpiresAt > 0 {
		v["expires"] = time.UnixMilli(st.ExpiresAt).Format("2006-01-02 15:04 MST")
		v["days"] = strconv.FormatInt(ceilDiv(max(st.ExpiresAt-now, 0), day), 10)
	}
	return v
}

// command is the command a message begins with, without the @bot suffix
// Telegram adds in groups and some clients add everywhere.
func command(word string) string {
	if !strings.HasPrefix(word, "/") {
		return ""
	}
	word, _, _ = strings.Cut(word, "@")
	return strings.ToLower(word)
}

func (b *Bot) reply(ctx context.Context, c *client, chatID int64, text string) {
	if err := c.sendMessage(ctx, chatID, text); err != nil && ctx.Err() == nil {
		b.opt.Log.Warn("replying on telegram failed", "error", describe(err))
	}
}

// warnLoop looks for warnings that are due.
func (b *Bot) warnLoop(ctx context.Context, c *client) {
	timer := time.NewTimer(b.opt.FirstPass)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case <-b.kick:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}
		if err := b.warn(ctx, c); err != nil && ctx.Err() == nil {
			b.opt.Log.Error("sending telegram warnings failed", "error", describe(err))
		}
		timer.Reset(b.opt.PassEvery)
	}
}

// warn sends every warning that is due, each once.
//
// What counts as the same warning is the situation it was about, not the time:
// an expiry warning is about one expiry date, and a usage warning about one
// allowance counted from one reset. Renewing a subscription or resetting its
// usage changes the situation, so the next time it runs low it is warned again,
// with nothing here having to notice the renewal.
func (b *Bot) warn(ctx context.Context, c *client) error {
	cfg := b.opt.Settings().Telegram
	if cfg.WarnDays == 0 && cfg.WarnUsageBytes == 0 {
		return nil
	}
	now := time.Now().UnixMilli()
	standings, err := b.opt.Store.Standings(now)
	if err != nil {
		return err
	}
	warned, err := b.opt.Store.Warned()
	if err != nil {
		return err
	}

	gone := map[int64]bool{}
	for _, st := range standings {
		// A peer that is already cut off has nothing left to be warned about,
		// and one switched off by hand is not running out of anything.
		if st.Status != store.StatusActive || gone[st.ChatID] {
			continue
		}

		var due []warning
		if cfg.WarnDays > 0 && st.ExpiresAt > now && st.ExpiresAt-now <= int64(cfg.WarnDays)*day {
			left := st.ExpiresAt - now
			due = append(due, warning{
				store.WarnExpiry, strconv.FormatInt(st.ExpiresAt, 10),
				render(cfg.Messages.ExpiryWarning, map[string]string{
					"name":    st.Name,
					"days":    strconv.FormatInt(ceilDiv(left, day), 10),
					"hours":   strconv.FormatInt(ceilDiv(left, hour), 10),
					"expires": time.UnixMilli(st.ExpiresAt).Format("2006-01-02 15:04 MST"),
				})})
		}
		if remaining := st.Allowed - st.Used; cfg.WarnUsageBytes > 0 && st.Allowed > 0 &&
			remaining > 0 && remaining <= cfg.WarnUsageBytes {
			due = append(due, warning{
				store.WarnUsage, fmt.Sprintf("%d:%d:%d", st.Allowed, st.ResetAt, st.GroupID),
				render(cfg.Messages.UsageWarning, map[string]string{
					"name":      st.Name,
					"remaining": formatBytes(remaining),
				})})
		}

		for _, w := range due {
			if warned[store.WarnedKey{PeerID: st.PeerID, Kind: w.kind}] == w.marker {
				continue
			}
			err := c.sendMessage(ctx, st.ChatID, w.text)
			var apiErr *APIError
			switch {
			case err == nil:
				// Recorded only once it has gone, so a failure is tried again.
				if err := b.opt.Store.MarkWarned(st.PeerID, w.kind, w.marker); err != nil {
					return err
				}
				b.opt.Log.Info("telegram warning sent", "peer", st.Name, "kind", w.kind)
			case errors.As(err, &apiErr) && apiErr.blocked():
				// The customer blocked the bot. Keep trying and every pass
				// would fail again, so connect them again to hear from us.
				n, _ := b.opt.Store.UnlinkChat(st.ChatID)
				b.opt.Log.Info("telegram chat is closed, disconnecting it", "peers", n)
				gone[st.ChatID] = true
			case errors.As(err, &apiErr) && apiErr.Code == http.StatusTooManyRequests:
				// Told to slow down: stop here and pick up at the next pass.
				if !sleep(ctx, time.Duration(min(max(apiErr.RetryAfter, 1), 30))*time.Second) {
					return ctx.Err()
				}
				return nil
			default:
				// Most likely Telegram cannot be reached, in which case every
				// remaining message would fail the same way.
				return err
			}
			if !sleep(ctx, b.opt.SendGap) {
				return ctx.Err()
			}
		}
	}
	return nil
}

// warning is one message that is due: what kind it is, the situation it is
// about, and what it says.
type warning struct{ kind, marker, text string }

// render fills {placeholders} in a message. One it does not know is left as it
// was written, which shows up where an operator will see it.
func render(tmpl string, vals map[string]string) string {
	pairs := make([]string, 0, len(vals)*2)
	for k, v := range vals {
		pairs = append(pairs, "{"+k+"}", v)
	}
	return strings.NewReplacer(pairs...).Replace(tmpl)
}

func ceilDiv(a, b int64) int64 { return (a + b - 1) / b }

// formatBytes writes a size the way the panel does, so a customer reads the
// same figure in a warning as the operator sees on the screen.
func formatBytes(n int64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB"}
	v, i := float64(n), 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.2f %s", v, units[i])
}

// sleep waits for d, and reports false if ctx ended first.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// describe turns what went wrong into a sentence about what to do, since the
// error itself says what failed and nothing about which end to look at.
func describe(err error) string {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Code {
		case http.StatusUnauthorized:
			return "Telegram rejected the bot's token; it may have been revoked. Set it again in Settings."
		case http.StatusConflict:
			return "Something else is reading this bot's messages — another program, or a webhook set on the bot. " +
				"Only one thing can at a time: stop it, or use a bot of its own for wgui."
		}
		return fmt.Sprintf("Telegram refused the request (%d): %s", apiErr.Code, apiErr.Description)
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "Telegram did not answer in time. This server needs to reach api.telegram.org; set HTTPS_PROXY if it needs a proxy."
	}
	return "Could not reach Telegram: " + err.Error() +
		". This server needs to reach api.telegram.org; set HTTPS_PROXY if it needs a proxy."
}
