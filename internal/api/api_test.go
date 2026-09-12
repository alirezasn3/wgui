package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wgui/internal/config"
	"wgui/internal/engine"
	"wgui/internal/scripts"
	"wgui/internal/store"
	"wgui/internal/wgdev"

	"github.com/labstack/echo/v4"
)

type fixture struct {
	t     *testing.T
	store *store.Store
	srv   *Server
	echo  *echo.Echo
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	cfg, err := config.Load(writeConfig(t))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	settings := config.DefaultSettings()
	settings.Endpoints = []string{"vpn.example.com:51820"}
	settings.Normalize()
	if err := st.SaveSettings(settings); err != nil {
		t.Fatalf("save settings: %v", err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	dev := wgdev.OpenStub("wg0")
	t.Cleanup(func() { dev.Close() })

	srv := New(st, nil, scripts.NewRunner(st, log), cfg, settings, log)
	eng := engine.New(st, dev, log, func() time.Duration { return time.Hour })
	srv.SetEngine(eng)

	f := &fixture{t: t, store: st, srv: srv, echo: srv.Handler()}
	return f
}

// writeConfig creates a config.json in a temp directory and returns that
// directory.
func writeConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	body := `{
	  "interfaceName": "wg0",
	  "interfaceAddress": "10.0.0.1",
	  "interfaceAddressCIDR": "10.0.0.0/24",
	  "bypassKey": "secret"
	}`
	if err := writeFile(filepath.Join(dir, "config.json"), body); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return dir
}

func writeFile(path, body string) error {
	return os.WriteFile(path, []byte(body), 0o600)
}

// peer adds a peer directly to the store and returns it.
func (f *fixture) peer(name, key, ip, role string, mutate func(*store.Peer)) *store.Peer {
	f.t.Helper()
	p := &store.Peer{ID: key, Name: name, Role: role, PublicKey: key, AllowedIPs: ip}
	if mutate != nil {
		mutate(p)
	}
	if err := f.store.CreatePeer(p); err != nil {
		f.t.Fatalf("create peer %s: %v", name, err)
	}
	return p
}

// as issues a request that appears to come from the given tunnel address.
func (f *fixture) as(tunnelIP, method, target, body string) *httptest.ResponseRecorder {
	f.t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	req.RemoteAddr = tunnelIP + ":40000"
	if body != "" {
		req.Header.Set("content-type", "application/json")
	}
	rec := httptest.NewRecorder()
	f.echo.ServeHTTP(rec, req)
	return rec
}

func (f *fixture) decode(rec *httptest.ResponseRecorder, into any) {
	f.t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), into); err != nil {
		f.t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
}

func TestRequestsFromOutsideTheTunnelAreRejected(t *testing.T) {
	f := newFixture(t)
	f.peer("admin", "key-admin", "10.0.0.2/32", config.RoleAdmin, nil)

	rec := f.as("203.0.113.5", http.MethodGet, "/api/peers", "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for an address outside the interface subnet", rec.Code)
	}
}

func TestUnknownTunnelAddressIsRejected(t *testing.T) {
	f := newFixture(t)
	f.peer("admin", "key-admin", "10.0.0.2/32", config.RoleAdmin, nil)

	// Inside the subnet, but no peer owns this address.
	rec := f.as("10.0.0.99", http.MethodGet, "/api/peers", "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for an unregistered address", rec.Code)
	}
}

func TestBypassKeyAuthenticatesAsAdmin(t *testing.T) {
	f := newFixture(t)
	f.peer("admin", "key-admin", "10.0.0.2/32", config.RoleAdmin, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/peers", nil)
	req.RemoteAddr = "203.0.113.5:40000" // outside the tunnel entirely
	req.Header.Set("bypass_key", "secret")
	rec := httptest.NewRecorder()
	f.echo.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with a valid bypass key", rec.Code)
	}

	req.Header.Set("bypass_key", "wrong")
	rec = httptest.NewRecorder()
	f.echo.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 with a wrong bypass key", rec.Code)
	}
}

func TestUserSeesOnlyItselfUntilSomethingIsShared(t *testing.T) {
	f := newFixture(t)
	f.peer("admin", "key-admin", "10.0.0.2/32", config.RoleAdmin, nil)
	f.peer("alice", "key-alice", "10.0.0.3/32", config.RoleUser, nil)
	f.peer("bob", "key-bob", "10.0.0.4/32", config.RoleUser, nil)

	var res peerListResponse
	f.decode(f.as("10.0.0.3", http.MethodGet, "/api/peers", ""), &res)
	if len(res.Peers) != 1 || res.Peers[0].Name != "alice" {
		t.Fatalf("user sees %d peers, want only itself", len(res.Peers))
	}

	// An admin shares bob with alice.
	rec := f.as("10.0.0.2", http.MethodPut, "/api/peers/key-alice/visibility", `{"targets":["key-bob"]}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("share status = %d (%s)", rec.Code, rec.Body.String())
	}

	res = peerListResponse{}
	f.decode(f.as("10.0.0.3", http.MethodGet, "/api/peers", ""), &res)
	if len(res.Peers) != 2 {
		t.Fatalf("after sharing, user sees %d peers, want 2", len(res.Peers))
	}

	// Sharing is one-directional.
	res = peerListResponse{}
	f.decode(f.as("10.0.0.4", http.MethodGet, "/api/peers", ""), &res)
	if len(res.Peers) != 1 {
		t.Fatalf("sharing must not be reciprocal, but bob sees %d peers", len(res.Peers))
	}
}

func TestUsersCannotWrite(t *testing.T) {
	f := newFixture(t)
	f.peer("admin", "key-admin", "10.0.0.2/32", config.RoleAdmin, nil)
	f.peer("alice", "key-alice", "10.0.0.3/32", config.RoleUser, nil)

	if rec := f.as("10.0.0.3", http.MethodPost, "/api/peers", `{"name":"mine"}`); rec.Code != http.StatusForbidden {
		t.Errorf("create as user = %d, want 403", rec.Code)
	}
	// Even on its own row: a user must not be able to lift its own quota.
	if rec := f.as("10.0.0.3", http.MethodPatch, "/api/peers/key-alice", `{"allowedUsage":0}`); rec.Code != http.StatusForbidden {
		t.Errorf("patch self as user = %d, want 403", rec.Code)
	}
	if rec := f.as("10.0.0.3", http.MethodDelete, "/api/peers/key-alice", ""); rec.Code != http.StatusForbidden {
		t.Errorf("delete self as user = %d, want 403", rec.Code)
	}
}

func TestDistributorScope(t *testing.T) {
	f := newFixture(t)
	f.peer("admin", "key-admin", "10.0.0.2/32", config.RoleAdmin, nil)
	f.peer("dist", "key-dist", "10.0.0.3/32", config.RoleDistributor, nil)
	f.peer("mine", "key-mine", "10.0.0.4/32", config.RoleUser, func(p *store.Peer) { p.OwnerID = "key-dist" })
	f.peer("theirs", "key-theirs", "10.0.0.5/32", config.RoleUser, nil)

	var res peerListResponse
	f.decode(f.as("10.0.0.3", http.MethodGet, "/api/peers", ""), &res)
	if len(res.Peers) != 2 {
		t.Fatalf("distributor sees %d peers, want itself and the one it owns", len(res.Peers))
	}

	if rec := f.as("10.0.0.3", http.MethodPatch, "/api/peers/key-mine", `{"note":"ok"}`); rec.Code != http.StatusOK {
		t.Errorf("patch owned peer = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	// A peer it cannot see is reported as missing, not forbidden, so the panel
	// does not confirm that it exists.
	if rec := f.as("10.0.0.3", http.MethodPatch, "/api/peers/key-theirs", `{"note":"no"}`); rec.Code != http.StatusNotFound {
		t.Errorf("patch unowned peer = %d, want 404", rec.Code)
	}

	// Anything a distributor creates belongs to it and is a plain user.
	rec := f.as("10.0.0.3", http.MethodPost, "/api/peers", `{"name":"new","role":"admin"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d (%s)", rec.Code, rec.Body.String())
	}
	var created store.Peer
	f.decode(rec, &created)
	if created.Role != config.RoleUser {
		t.Errorf("role = %q, want user: a distributor must not be able to mint admins", created.Role)
	}
	if created.OwnerID != "key-dist" {
		t.Errorf("owner = %q, want the creating distributor", created.OwnerID)
	}

	// Only an admin may change roles.
	if rec := f.as("10.0.0.3", http.MethodPatch, "/api/peers/key-mine", `{"role":"admin"}`); rec.Code != http.StatusForbidden {
		t.Errorf("role change by distributor = %d, want 403", rec.Code)
	}
}

// Even if the ownership column says otherwise, a distributor must not be able to
// act on an admin or on another distributor.
func TestDistributorCannotActOnPrivilegedPeersItOwns(t *testing.T) {
	f := newFixture(t)
	f.peer("admin", "key-admin", "10.0.0.2/32", config.RoleAdmin, nil)
	f.peer("dist", "key-dist", "10.0.0.3/32", config.RoleDistributor, nil)
	// Ownership rows an import could plausibly produce.
	f.peer("owned-admin", "key-oa", "10.0.0.4/32", config.RoleAdmin,
		func(p *store.Peer) { p.OwnerID = "key-dist" })
	f.peer("owned-dist", "key-od", "10.0.0.5/32", config.RoleDistributor,
		func(p *store.Peer) { p.OwnerID = "key-dist" })
	f.peer("owned-user", "key-ou", "10.0.0.6/32", config.RoleUser,
		func(p *store.Peer) { p.OwnerID = "key-dist" })

	for _, id := range []string{"key-oa", "key-od"} {
		if rec := f.as("10.0.0.3", http.MethodPatch, "/api/peers/"+id, `{"note":"no"}`); rec.Code != http.StatusForbidden {
			t.Errorf("patch %s = %d, want 403", id, rec.Code)
		}
		if rec := f.as("10.0.0.3", http.MethodDelete, "/api/peers/"+id, ""); rec.Code != http.StatusForbidden {
			t.Errorf("delete %s = %d, want 403", id, rec.Code)
		}
		rec := f.as("10.0.0.3", http.MethodPost, "/api/peers/bulk",
			`{"ids":["`+id+`"],"action":"disable"}`)
		if rec.Code != http.StatusForbidden {
			t.Errorf("bulk disable %s = %d, want 403", id, rec.Code)
		}
	}

	// The plain user it owns is still fully manageable.
	if rec := f.as("10.0.0.3", http.MethodPatch, "/api/peers/key-ou", `{"note":"fine"}`); rec.Code != http.StatusOK {
		t.Errorf("patch the owned user = %d (%s), want 200", rec.Code, rec.Body.String())
	}
}

func TestCreatePeerAllocatesSequentialAddresses(t *testing.T) {
	f := newFixture(t)
	f.peer("admin", "key-admin", "10.0.0.2/32", config.RoleAdmin, nil)

	want := []string{"10.0.0.3/32", "10.0.0.4/32", "10.0.0.5/32"}
	for i, expect := range want {
		rec := f.as("10.0.0.2", http.MethodPost, "/api/peers", `{"name":"peer`+string(rune('a'+i))+`"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %d = %d (%s)", i, rec.Code, rec.Body.String())
		}
		var p store.Peer
		f.decode(rec, &p)
		if p.AllowedIPs != expect {
			t.Errorf("address %d = %s, want %s", i, p.AllowedIPs, expect)
		}
	}
}

func TestCreatePeerAppliesDefaults(t *testing.T) {
	f := newFixture(t)
	f.peer("admin", "key-admin", "10.0.0.2/32", config.RoleAdmin, nil)

	settings := f.srv.Settings()
	settings.PeerDefaults.AllowedUsageBytes = 50 << 30
	settings.PeerDefaults.ExpiryDays = 7
	if rec := f.as("10.0.0.2", http.MethodPut, "/api/settings", mustJSON(t, settings)); rec.Code != http.StatusOK {
		t.Fatalf("save settings = %d (%s)", rec.Code, rec.Body.String())
	}

	rec := f.as("10.0.0.2", http.MethodPost, "/api/peers", `{"name":"defaulted"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d (%s)", rec.Code, rec.Body.String())
	}
	var p store.Peer
	f.decode(rec, &p)

	if p.AllowedUsage != 50<<30 {
		t.Errorf("allowed usage = %d, want the configured default", p.AllowedUsage)
	}
	// Seven days out, allowing a generous window for test slowness.
	inSevenDays := time.Now().Add(7 * 24 * time.Hour).UnixMilli()
	if diff := p.ExpiresAt - inSevenDays; diff < -60_000 || diff > 60_000 {
		t.Errorf("expiry = %d, want about %d", p.ExpiresAt, inSevenDays)
	}

	// An explicit value must still win over the default.
	rec = f.as("10.0.0.2", http.MethodPost, "/api/peers", `{"name":"unlimited","allowedUsage":0,"expiryDays":0}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d (%s)", rec.Code, rec.Body.String())
	}
	p = store.Peer{}
	f.decode(rec, &p)
	if p.AllowedUsage != 0 || p.ExpiresAt != 0 {
		t.Errorf("explicit unlimited was overridden by defaults: %+v", p)
	}
}

func TestDuplicateNameIsRejected(t *testing.T) {
	f := newFixture(t)
	f.peer("admin", "key-admin", "10.0.0.2/32", config.RoleAdmin, nil)

	if rec := f.as("10.0.0.2", http.MethodPost, "/api/peers", `{"name":"taken"}`); rec.Code != http.StatusCreated {
		t.Fatalf("first create = %d (%s)", rec.Code, rec.Body.String())
	}
	rec := f.as("10.0.0.2", http.MethodPost, "/api/peers", `{"name":"taken"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate create = %d (%s), want 409", rec.Code, rec.Body.String())
	}
}

func TestBulkActionsAreAllOrNothing(t *testing.T) {
	f := newFixture(t)
	f.peer("admin", "key-admin", "10.0.0.2/32", config.RoleAdmin, nil)
	f.peer("dist", "key-dist", "10.0.0.3/32", config.RoleDistributor, nil)
	f.peer("mine", "key-mine", "10.0.0.4/32", config.RoleUser, func(p *store.Peer) { p.OwnerID = "key-dist" })
	f.peer("theirs", "key-theirs", "10.0.0.5/32", config.RoleUser, nil)

	// The distributor owns one of the two selected peers, so nothing may change.
	rec := f.as("10.0.0.3", http.MethodPost, "/api/peers/bulk",
		`{"ids":["key-mine","key-theirs"],"action":"disable"}`)
	if rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
		t.Fatalf("mixed selection = %d (%s), want the whole action refused", rec.Code, rec.Body.String())
	}
	p, _ := f.store.GetPeer("key-mine", time.Now().UnixMilli())
	if p.ManuallyDisabled {
		t.Fatal("a refused bulk action must not have applied to the peers the caller did own")
	}

	// The same action over only what it owns succeeds.
	rec = f.as("10.0.0.3", http.MethodPost, "/api/peers/bulk", `{"ids":["key-mine"],"action":"disable"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("owned selection = %d (%s)", rec.Code, rec.Body.String())
	}
	p, _ = f.store.GetPeer("key-mine", time.Now().UnixMilli())
	if !p.ManuallyDisabled {
		t.Fatal("bulk disable did not take effect")
	}
}

func TestBulkSetExpiryAndUsage(t *testing.T) {
	f := newFixture(t)
	f.peer("admin", "key-admin", "10.0.0.2/32", config.RoleAdmin, nil)
	f.peer("a", "key-a", "10.0.0.3/32", config.RoleUser, nil)
	f.peer("b", "key-b", "10.0.0.4/32", config.RoleUser, nil)

	rec := f.as("10.0.0.2", http.MethodPost, "/api/peers/bulk",
		`{"ids":["key-a","key-b"],"action":"setUsage","allowedUsage":1073741824}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("setUsage = %d (%s)", rec.Code, rec.Body.String())
	}
	for _, id := range []string{"key-a", "key-b"} {
		p, _ := f.store.GetPeer(id, time.Now().UnixMilli())
		if p.AllowedUsage != 1<<30 {
			t.Errorf("%s quota = %d, want 1 GiB", id, p.AllowedUsage)
		}
	}

	rec = f.as("10.0.0.2", http.MethodPost, "/api/peers/bulk",
		`{"ids":["key-a"],"action":"setExpiry","expiryDays":0}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("setExpiry = %d (%s)", rec.Code, rec.Body.String())
	}
	p, _ := f.store.GetPeer("key-a", time.Now().UnixMilli())
	if p.ExpiresAt != 0 {
		t.Errorf("expiry = %d, want 0 meaning never", p.ExpiresAt)
	}
}

func TestDeletingYourOwnPeerIsRefused(t *testing.T) {
	f := newFixture(t)
	f.peer("admin", "key-admin", "10.0.0.2/32", config.RoleAdmin, nil)

	if rec := f.as("10.0.0.2", http.MethodDelete, "/api/peers/key-admin", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("self delete = %d, want 400: it would lock the admin out", rec.Code)
	}
	rec := f.as("10.0.0.2", http.MethodPost, "/api/peers/bulk", `{"ids":["key-admin"],"action":"delete"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("self delete in bulk = %d, want 400", rec.Code)
	}
}

// Disabling the peer you are connected through would drop the tunnel this
// request arrived on, so it is refused the same way self-deletion is.
func TestDisablingYourOwnPeerIsRefused(t *testing.T) {
	f := newFixture(t)
	f.peer("admin", "key-admin", "10.0.0.2/32", config.RoleAdmin, nil)
	f.peer("other", "key-other", "10.0.0.3/32", config.RoleUser, nil)

	if rec := f.as("10.0.0.2", http.MethodPatch, "/api/peers/key-admin", `{"manuallyDisabled":true}`); rec.Code != http.StatusBadRequest {
		t.Errorf("self disable = %d, want 400", rec.Code)
	}
	rec := f.as("10.0.0.2", http.MethodPost, "/api/peers/bulk",
		`{"ids":["key-admin","key-other"],"action":"disable"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("self disable in bulk = %d, want 400", rec.Code)
	}
	// The other peer in that selection must be untouched, since the action is
	// refused as a whole.
	p, _ := f.store.GetPeer("key-other", time.Now().UnixMilli())
	if p.ManuallyDisabled {
		t.Error("a refused bulk disable must not have applied to the rest of the selection")
	}

	// Disabling someone else is still fine.
	if rec := f.as("10.0.0.2", http.MethodPatch, "/api/peers/key-other", `{"manuallyDisabled":true}`); rec.Code != http.StatusOK {
		t.Errorf("disabling another peer = %d, want 200", rec.Code)
	}
}

func TestPrivateKeyIsNotLeaked(t *testing.T) {
	f := newFixture(t)
	f.peer("admin", "key-admin", "10.0.0.2/32", config.RoleAdmin, nil)
	f.peer("alice", "key-alice", "10.0.0.3/32", config.RoleUser, func(p *store.Peer) { p.PrivateKey = "alice-secret" })
	f.peer("bob", "key-bob", "10.0.0.4/32", config.RoleUser, func(p *store.Peer) { p.PrivateKey = "bob-secret" })

	if err := f.store.SetVisibility("key-alice", []string{"key-bob"}); err != nil {
		t.Fatalf("share: %v", err)
	}

	var res peerListResponse
	f.decode(f.as("10.0.0.3", http.MethodGet, "/api/peers", ""), &res)
	for _, p := range res.Peers {
		switch p.Name {
		case "alice":
			if p.PrivateKey == "" {
				t.Error("a peer must still receive its own private key")
			}
		case "bob":
			if p.PrivateKey != "" {
				t.Error("a shared peer's private key must not be exposed to the viewer")
			}
		}
	}

	// The same rule applies to the rendered configuration.
	if rec := f.as("10.0.0.3", http.MethodGet, "/api/peers/key-bob/config", ""); rec.Code != http.StatusForbidden {
		t.Errorf("config of a shared peer = %d, want 403", rec.Code)
	}
	rec := f.as("10.0.0.3", http.MethodGet, "/api/peers/key-alice/config", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "alice-secret") {
		t.Errorf("own config = %d, body %q", rec.Code, rec.Body.String())
	}
}

func TestConfigUsesPerPeerEndpointOverride(t *testing.T) {
	f := newFixture(t)
	f.peer("admin", "key-admin", "10.0.0.2/32", config.RoleAdmin, func(p *store.Peer) { p.PrivateKey = "k" })
	f.peer("pinned", "key-pinned", "10.0.0.3/32", config.RoleUser, func(p *store.Peer) {
		p.PrivateKey = "k2"
		p.ClientEndpoint = "de.example.com:51820"
	})

	body := f.as("10.0.0.2", http.MethodGet, "/api/peers/key-admin/config", "").Body.String()
	if !strings.Contains(body, "Endpoint = vpn.example.com:51820") {
		t.Errorf("default endpoint missing from config:\n%s", body)
	}
	body = f.as("10.0.0.2", http.MethodGet, "/api/peers/key-pinned/config", "").Body.String()
	if !strings.Contains(body, "Endpoint = de.example.com:51820") {
		t.Errorf("per-peer endpoint override was ignored:\n%s", body)
	}
}

func TestOnlyAdminsReadAndWriteSettings(t *testing.T) {
	f := newFixture(t)
	f.peer("admin", "key-admin", "10.0.0.2/32", config.RoleAdmin, nil)
	f.peer("dist", "key-dist", "10.0.0.3/32", config.RoleDistributor, nil)

	if rec := f.as("10.0.0.3", http.MethodGet, "/api/settings", ""); rec.Code != http.StatusForbidden {
		t.Errorf("distributor reading settings = %d, want 403", rec.Code)
	}
	if rec := f.as("10.0.0.2", http.MethodGet, "/api/settings", ""); rec.Code != http.StatusOK {
		t.Errorf("admin reading settings = %d, want 200", rec.Code)
	}
}

func TestGroupQuotaFlowsThroughTheAPI(t *testing.T) {
	f := newFixture(t)
	f.peer("admin", "key-admin", "10.0.0.2/32", config.RoleAdmin, nil)

	rec := f.as("10.0.0.2", http.MethodPost, "/api/groups", `{"name":"team","allowedUsage":1000,"expiryDays":0}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create group = %d (%s)", rec.Code, rec.Body.String())
	}
	var group store.Group
	f.decode(rec, &group)

	f.peer("member", "key-member", "10.0.0.3/32", config.RoleUser, func(p *store.Peer) { p.GroupID = group.ID })
	if err := f.store.ApplyUsageDeltas([]store.UsageDelta{{PeerID: "key-member", TX: 900, RX: 200}}); err != nil {
		t.Fatalf("usage: %v", err)
	}

	var res peerListResponse
	f.decode(f.as("10.0.0.2", http.MethodGet, "/api/peers?search=member", ""), &res)
	if len(res.Peers) != 1 || res.Peers[0].Status != store.StatusQuota {
		t.Fatalf("member status = %v, want quota once the group allowance is spent", res.Peers)
	}
}

func TestSortAndFilterParameters(t *testing.T) {
	f := newFixture(t)
	f.peer("admin", "key-admin", "10.0.0.2/32", config.RoleAdmin, nil)
	f.peer("zulu", "key-z", "10.0.0.3/32", config.RoleUser, nil)
	f.peer("alpha", "key-a", "10.0.0.4/32", config.RoleUser, func(p *store.Peer) { p.ManuallyDisabled = true })

	var res peerListResponse
	f.decode(f.as("10.0.0.2", http.MethodGet, "/api/peers?sort=name&order=desc", ""), &res)
	if len(res.Peers) != 3 || res.Peers[0].Name != "zulu" {
		t.Errorf("descending name sort gave %v", names(res.Peers))
	}

	res = peerListResponse{}
	f.decode(f.as("10.0.0.2", http.MethodGet, "/api/peers?status=disabled", ""), &res)
	if len(res.Peers) != 1 || res.Peers[0].Name != "alpha" {
		t.Errorf("status filter gave %v", names(res.Peers))
	}

	// Sorting by live speed takes a different path, so check it still paginates.
	res = peerListResponse{}
	f.decode(f.as("10.0.0.2", http.MethodGet, "/api/peers?sort=speed&pageSize=2&page=1", ""), &res)
	if len(res.Peers) != 2 || res.Total != 3 {
		t.Errorf("speed sort page = %d peers of %d total, want 2 of 3", len(res.Peers), res.Total)
	}
}

func names(peers []*store.Peer) []string {
	out := make([]string, len(peers))
	for i, p := range peers {
		out[i] = p.Name
	}
	return out
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

// The sync endpoint is the one route a node reaches from outside the tunnel,
// so it sits outside the authenticated group. A POST must therefore get as far
// as the secret check, and everything else must be told what the endpoint is
// for rather than inheriting the group's complaint about the tunnel — that
// message is true of every node as well, and reading it in a master's log
// sends an operator hunting the wrong end.
func TestSyncEndpointByMethod(t *testing.T) {
	f := newFixture(t)
	secret, err := f.store.SyncSecret()
	if err != nil {
		t.Fatalf("sync secret: %v", err)
	}

	for _, tc := range []struct {
		method, auth string
		want         int
	}{
		{http.MethodPost, "Bearer " + secret, http.StatusBadRequest}, // reached the handler
		{http.MethodPost, "Bearer wrong", http.StatusUnauthorized},   // reached the secret check
		{http.MethodGet, "Bearer " + secret, http.StatusMethodNotAllowed},
		{http.MethodHead, "", http.StatusMethodNotAllowed},
		{http.MethodPut, "", http.StatusMethodNotAllowed},
		{http.MethodDelete, "", http.StatusMethodNotAllowed},
	} {
		req := httptest.NewRequest(tc.method, "/api/sync", strings.NewReader(`{}`))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		if tc.auth != "" {
			req.Header.Set("Authorization", tc.auth)
		}
		// A node is another server on the internet, never inside the tunnel.
		req.RemoteAddr = "87.107.115.58:55416"
		rec := httptest.NewRecorder()
		f.echo.ServeHTTP(rec, req)

		if rec.Code != tc.want {
			t.Errorf("%s: got %d want %d: %s", tc.method, rec.Code, tc.want, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "inside the tunnel") {
			t.Errorf("%s: blamed the tunnel: %s", tc.method, rec.Body.String())
		}
	}
}
