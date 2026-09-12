package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"wgui/internal/config"
)

// asNode issues a request the way a node forwards one: authenticated by the
// sync secret, carried on behalf of one of its operators.
func (f *fixture) asNode(secret, onBehalf, method, target, body string) *httptest.ResponseRecorder {
	f.t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	// Deliberately from outside the tunnel: a node is another server, not a peer.
	req.RemoteAddr = "203.0.113.9:40000"
	req.Header.Set("Authorization", "Bearer "+secret)
	req.Header.Set(OnBehalfHeader, onBehalf)
	if body != "" {
		req.Header.Set("content-type", "application/json")
	}
	rec := httptest.NewRecorder()
	f.echo.ServeHTTP(rec, req)
	return rec
}

// A node carries its operators' writes to the master, which is the only thing
// that makes a node's panel usable rather than merely readable. The authority
// applied has to be the operator's own: a node holds the sync secret, so if it
// could name anyone and be believed without the master checking that peer's
// role, every user on a node would be an admin on the master.
func TestForwardedWriteUsesTheOperatorsOwnAuthority(t *testing.T) {
	f := newFixture(t)
	admin := f.peer("admin", "adminkey", "10.0.0.1/32", config.RoleAdmin, nil)
	user := f.peer("user", "userkey", "10.0.0.5/32", config.RoleUser, nil)
	target := f.peer("target", "targetkey", "10.0.0.6/32", config.RoleUser, nil)

	secret, err := f.store.SyncSecret()
	if err != nil {
		t.Fatalf("secret: %v", err)
	}

	body := `{"note":"forwarded"}`
	path := "/api/peers/" + target.ID

	// Without the secret the header is just a claim, and claims are worth
	// nothing.
	req := httptest.NewRequest(http.MethodPatch, path, strings.NewReader(body))
	req.RemoteAddr = "203.0.113.9:40000"
	req.Header.Set(OnBehalfHeader, admin.ID)
	req.Header.Set("content-type", "application/json")
	rec := httptest.NewRecorder()
	f.echo.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("a forged on-behalf header was answered %d, want 401", rec.Code)
	}

	// With the secret, but on behalf of a plain user, the master applies that
	// user's own scope — which cannot even see the peer, let alone change it.
	if rec := f.asNode(secret, user.ID, http.MethodPatch, path, body); rec.Code == http.StatusOK {
		t.Errorf("a plain user's forwarded write succeeded: %s", rec.Body.String())
	}
	if p, _ := f.store.GetPeer(target.ID, nowMS()); p.Note != "" {
		t.Errorf("the write landed anyway: note = %q", p.Note)
	}

	// On behalf of an admin it is allowed, because that admin could have made
	// the same change here directly.
	if rec := f.asNode(secret, admin.ID, http.MethodPatch, path, body); rec.Code != http.StatusOK {
		t.Fatalf("an admin's forwarded write was answered %d: %s", rec.Code, rec.Body.String())
	}
	p, err := f.store.GetPeer(target.ID, nowMS())
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if p.Note != "forwarded" {
		t.Errorf("note = %q, want the forwarded change to have landed", p.Note)
	}

	// A peer the master has never heard of buys nothing either.
	if rec := f.asNode(secret, "nobody", http.MethodPatch, path, body); rec.Code != http.StatusForbidden {
		t.Errorf("an unknown peer was answered %d, want 403", rec.Code)
	}
}

// A refusal has to stop the handler, not merely write a status. The helpers
// that load a peer or a script report one by returning no object — and used to
// return a nil error alongside it, so every caller's `if err != nil` let them
// through to dereference the nil.
func TestARefusalStopsTheHandler(t *testing.T) {
	f := newFixture(t)
	f.peer("admin", "adminkey", "10.0.0.1/32", config.RoleAdmin, nil)

	for _, tc := range []struct {
		name, method, target, body string
		want                       int
	}{
		{"a peer that does not exist", http.MethodGet, "/api/peers/nosuchkey", "", http.StatusNotFound},
		{"its configuration", http.MethodGet, "/api/peers/nosuchkey/config", "", http.StatusNotFound},
		{"its presence", http.MethodGet, "/api/peers/nosuchkey/presence", "", http.StatusNotFound},
		{"editing it", http.MethodPatch, "/api/peers/nosuchkey", `{"note":"x"}`, http.StatusNotFound},
		{"a script that does not exist", http.MethodGet, "/api/scripts/9999", "", http.StatusNotFound},
		{"a monitor that does not exist", http.MethodGet, "/api/monitors/9999", "", http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.as("10.0.0.1", tc.method, tc.target, tc.body)
			if rec.Code != tc.want {
				t.Errorf("answered %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
			}
			// A handler that carried on past the refusal appends its own reply
			// to one already written, so the body stops being a single object.
			body := strings.TrimSpace(rec.Body.String())
			if strings.Count(body, `{"error"`) > 1 || strings.Contains(body, "}{") {
				t.Errorf("the handler ran on past the refusal: %s", body)
			}
		})
	}
}

// A server's name is what an operator picks it out by on another server's
// dashboard, so it has to distinguish one box from another. The tunnel address
// cannot: peers carry fixed addresses, so every server in a fleet has the same
// one, and a list of servers all called 10.0.0.1 is no list at all.
func TestLocalNamePrefersSomethingDistinctive(t *testing.T) {
	f := newFixture(t)

	// With nothing set, the hostname stands in for the tunnel address.
	name := f.srv.LocalName()
	if name == "10.0.0.1" {
		t.Error("a server named itself after its tunnel address, which its whole fleet shares")
	}
	host, err := os.Hostname()
	if err == nil && host != "" && host != "localhost" && name != host {
		t.Errorf("name = %q, want the hostname %q", name, host)
	}

	// A public address is what peers actually reach it at, so it wins.
	settings := f.srv.Settings()
	settings.PublicAddress = "node.example.com"
	if err := f.store.SaveSettings(settings); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := f.srv.ReloadSettings(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := f.srv.LocalName(); got != "node.example.com" {
		t.Errorf("name = %q, want the public address", got)
	}
}
