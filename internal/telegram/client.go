// Package telegram is the bot that warns customers before their subscription
// runs out, and the small slice of Telegram's Bot API it needs.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// DefaultAPI is where Telegram's Bot API is. It honours HTTPS_PROXY like any
// Go program, which is how a server that cannot reach Telegram directly gets
// there.
const DefaultAPI = "https://api.telegram.org"

// tokenShape is what BotFather hands out: a number, a colon, a secret. The
// token is put into a URL path, so anything outside this alphabet is refused
// before it can change which endpoint a request goes to.
var tokenShape = regexp.MustCompile(`^[0-9]{5,}:[A-Za-z0-9_-]{20,}$`)

// ValidToken reports whether s could be a bot token.
func ValidToken(s string) bool { return tokenShape.MatchString(s) }

// APIError is Telegram refusing a request.
type APIError struct {
	Code        int
	Description string
	// RetryAfter is how many seconds Telegram asks to be left alone, on a 429.
	RetryAfter int
}

func (e *APIError) Error() string {
	return fmt.Sprintf("telegram answered %d: %s", e.Code, e.Description)
}

// blocked reports whether the error means the chat can never be written to
// again — the customer blocked the bot, or deleted the chat — so retrying is
// pointless and the link should be dropped.
func (e *APIError) blocked() bool {
	if e.Code == http.StatusForbidden {
		return true
	}
	return e.Code == http.StatusBadRequest && strings.Contains(strings.ToLower(e.Description), "chat not found")
}

type client struct {
	api   string
	token string
	http  *http.Client
}

func newClient(api, token string, hc *http.Client) *client {
	if api == "" {
		api = DefaultAPI
	}
	if hc == nil {
		hc = &http.Client{Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			TLSHandshakeTimeout:   15 * time.Second,
			ResponseHeaderTimeout: 60 * time.Second,
		}}
	}
	return &client{api: strings.TrimSuffix(api, "/"), token: token, http: hc}
}

// Telegram's own shapes, reduced to what is read.
type (
	user struct {
		ID       int64  `json:"id"`
		Username string `json:"username"`
	}
	chat struct {
		ID   int64  `json:"id"`
		Type string `json:"type"`
	}
	message struct {
		Chat chat   `json:"chat"`
		Text string `json:"text"`
	}
	update struct {
		UpdateID int64    `json:"update_id"`
		Message  *message `json:"message"`
	}
)

// call makes one Bot API request. Whatever comes back, the token is not in it:
// Go's HTTP errors quote the request URL, and the URL is where Telegram wants
// the token, so an error passed up as it came would put the bot's password into
// the log and the settings page.
func (c *client) call(ctx context.Context, method string, params map[string]any, out any) error {
	body, err := json.Marshal(params)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.api+"/bot"+c.token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return c.redact(err)
	}
	req.Header.Set("Content-Type", "application/json")

	res, err := c.http.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return c.redact(fmt.Errorf("%s: %w", method, err))
	}
	defer res.Body.Close()

	var env struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		ErrorCode   int             `json:"error_code"`
		Description string          `json:"description"`
		Parameters  struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(&env); err != nil {
		return c.redact(fmt.Errorf("%s: unreadable answer (%s): %w", method, res.Status, err))
	}
	if !env.OK {
		code := env.ErrorCode
		if code == 0 {
			code = res.StatusCode
		}
		return &APIError{Code: code, Description: env.Description, RetryAfter: env.Parameters.RetryAfter}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(env.Result, out)
}

// redact is the last line of defence: an error that somehow still carries the
// token has it replaced.
func (c *client) redact(err error) error {
	if err == nil || c.token == "" || !strings.Contains(err.Error(), c.token) {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), c.token, "<token>"))
}

func (c *client) getMe(ctx context.Context) (user, error) {
	var u user
	err := c.call(ctx, "getMe", map[string]any{}, &u)
	return u, err
}

// getUpdates long-polls: Telegram holds the request open for up to timeout
// seconds and answers the moment something arrives.
func (c *client) getUpdates(ctx context.Context, offset int64, timeout int) ([]update, error) {
	var out []update
	err := c.call(ctx, "getUpdates", map[string]any{
		"offset":          offset,
		"timeout":         timeout,
		"allowed_updates": []string{"message"},
	}, &out)
	return out, err
}

func (c *client) sendMessage(ctx context.Context, chatID int64, text string) error {
	return c.call(ctx, "sendMessage", map[string]any{
		"chat_id":                  chatID,
		"text":                     text,
		"disable_web_page_preview": true,
	}, nil)
}
