package nodesync

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The transport errors a node hits are accurate and unhelpful: they say what
// the standard library saw, not which end of the pair is misconfigured. Each of
// these is a misconfiguration an operator can actually make, so each gets a
// sentence about what to change.
func TestDiagnoseNamesWhatToChange(t *testing.T) {
	for _, tc := range []struct{ err, want string }{
		{"http: server gave HTTP response to HTTPS client", "not speaking TLS"},
		{"x509: certificate signed by unknown authority", "fingerprint"},
		{"the master's certificate does not match the pinned fingerprint", "has changed"},
		{"x509: certificate is valid for man.example.com, not panel.example.com", "different name"},
		{"master answered 401 Unauthorized: the sync secret is missing or wrong", "secret"},
		{"dial tcp: connection refused", "not reachable"},
	} {
		got := diagnose(errors.New(tc.err))
		if got == "" {
			t.Errorf("no hint for %q", tc.err)
			continue
		}
		if !strings.Contains(got, tc.want) {
			t.Errorf("hint for %q was %q, want it to mention %q", tc.err, got, tc.want)
		}
	}

	if got := diagnose(errors.New("something nobody predicted")); got != "" {
		t.Errorf("invented a hint for an unknown error: %q", got)
	}
}

// A proxy in front of the master that redirects — to add a trailing slash, to
// move www to the apex, to send http to https — silently breaks sync rather
// than failing it: the standard library answers a 301, 302 or 303 by reissuing
// the request as a bodyless GET, which the master refuses as a browser request
// from outside the tunnel. The node must refuse the redirect instead, and say
// so, rather than leaving the master to report a puzzle.
func TestSyncRefusesToFollowRedirects(t *testing.T) {
	var got []string
	master := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Method+" "+r.URL.Path)
		if r.URL.Path == "/api/sync" {
			http.Redirect(w, r, "/api/sync/", http.StatusMovedPermanently)
			return
		}
		w.Write([]byte(`{}`))
	}))
	defer master.Close()

	c := &Client{http: &http.Client{CheckRedirect: refuseRedirect}}
	req, err := http.NewRequest(http.MethodPost, master.URL+"/api/sync", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.http.Do(req)
	if err == nil {
		res.Body.Close()
		t.Fatalf("followed the redirect and reached %v", got)
	}
	if !errors.Is(err, errRedirected) {
		t.Fatalf("error was %v, want it to wrap errRedirected", err)
	}
	if len(got) != 1 || got[0] != "POST /api/sync" {
		t.Fatalf("master saw %v, want only the original POST", got)
	}
	if hint := diagnose(err); !strings.Contains(hint, "redirect") {
		t.Fatalf("hint was %q, want it to name the redirect", hint)
	}
}

// A name can resolve to more than one machine, and a network in the way can
// answer in the master's place. Both look the same in the error the standard
// library raises, so the failure has to name the address that actually
// answered — without it an operator cannot tell which end to look at.
func TestSyncFailureNamesTheAddressThatAnswered(t *testing.T) {
	// A master that speaks cleartext where TLS is expected: what a stray
	// second A record, or a middlebox injecting a page, looks like from here.
	impostor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("blocked"))
	}))
	defer impostor.Close()

	c := &Client{http: &http.Client{CheckRedirect: refuseRedirect}}
	req, err := http.NewRequest(http.MethodPost,
		strings.Replace(impostor.URL, "http://", "https://", 1)+"/api/sync", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	req, remote := watchConn(req)
	res, err := c.http.Do(req)
	if err == nil {
		res.Body.Close()
		t.Fatal("a cleartext answer to an HTTPS request was accepted")
	}

	got := withRemote(err, remote).Error()
	want := strings.TrimPrefix(impostor.URL, "http://")
	if !strings.Contains(got, want) {
		t.Errorf("error was %q, want it to name %q", got, want)
	}
	if !errors.Is(withRemote(err, remote), err) {
		t.Error("wrapping lost the original error")
	}
	if hint := diagnose(withRemote(err, remote)); !strings.Contains(hint, "not speaking TLS") {
		t.Errorf("hint was %q", hint)
	}

	// Nothing to name when the connection was never made: the error says that
	// on its own, and an empty address would only add noise.
	if got := withRemote(err, nil); got.Error() != err.Error() {
		t.Errorf("added an address that was never known: %v", got)
	}
}
