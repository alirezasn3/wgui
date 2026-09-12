package engine

import (
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"wgui/internal/store"
	"wgui/internal/wgdev"
)

// fakeDevice is a WireGuard interface whose counters the test drives directly.
type fakeDevice struct {
	mu      sync.Mutex
	peers   map[string]*wgdev.PeerState
	config  map[string]wgdev.PeerConfig
	removed []string
	sets    int
	// setKeys records which peers were written, since the point of skipping is
	// which ones are left alone rather than how many were touched.
	setKeys []string
}

func newFakeDevice() *fakeDevice {
	return &fakeDevice{peers: map[string]*wgdev.PeerState{}, config: map[string]wgdev.PeerConfig{}}
}

func (d *fakeDevice) Close() error { return nil }

func (d *fakeDevice) Info() (*wgdev.Info, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	info := &wgdev.Info{Name: "wg0", PublicKey: "server", ListenPort: 51820}
	for _, p := range d.peers {
		info.Peers = append(info.Peers, *p)
	}
	return info, nil
}

func (d *fakeDevice) ReplaceAll(peers []wgdev.PeerConfig) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.config = map[string]wgdev.PeerConfig{}
	kept := map[string]*wgdev.PeerState{}
	for _, c := range peers {
		d.config[c.PublicKey] = c
		if existing, ok := d.peers[c.PublicKey]; ok {
			kept[c.PublicKey] = existing
		} else {
			kept[c.PublicKey] = &wgdev.PeerState{PublicKey: c.PublicKey}
		}
	}
	d.peers = kept
	return nil
}

func (d *fakeDevice) Set(p wgdev.PeerConfig) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.sets++
	d.setKeys = append(d.setKeys, p.PublicKey)
	d.config[p.PublicKey] = p
	if _, ok := d.peers[p.PublicKey]; !ok {
		d.peers[p.PublicKey] = &wgdev.PeerState{PublicKey: p.PublicKey}
	}
	return nil
}

func (d *fakeDevice) Remove(publicKey string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.removed = append(d.removed, publicKey)
	delete(d.peers, publicKey)
	delete(d.config, publicKey)
	return nil
}

// counters sets what the device reports for a peer.
func (d *fakeDevice) counters(key string, tx, rx int64, handshake time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	p, ok := d.peers[key]
	if !ok {
		p = &wgdev.PeerState{PublicKey: key}
		d.peers[key] = p
	}
	p.TransmitBytes, p.ReceiveBytes, p.LastHandshake = tx, rx, handshake
	p.Endpoint = "203.0.113.9:51820"
}

func (d *fakeDevice) disabled(key string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.config[key].Disabled
}

func (d *fakeDevice) setCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.sets
}

func newFixture(t *testing.T) (*store.Store, *fakeDevice, *Engine) {
	t.Helper()

	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	dev := newFakeDevice()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return st, dev, New(st, dev, log, func() time.Duration { return time.Hour })
}

func addPeer(t *testing.T, st *store.Store, name, key, ip string, mutate func(*store.Peer)) {
	t.Helper()
	p := &store.Peer{ID: key, Name: name, Role: "user", PublicKey: key, AllowedIPs: ip}
	if mutate != nil {
		mutate(p)
	}
	if err := st.CreatePeer(p); err != nil {
		t.Fatalf("create peer %s: %v", name, err)
	}
}

func usageOf(t *testing.T, st *store.Store, id string) int64 {
	t.Helper()
	p, err := st.GetPeer(id, time.Now().UnixMilli())
	if err != nil {
		t.Fatalf("get peer %s: %v", id, err)
	}
	return p.Usage
}

func TestUsageAccumulatesFromDeltas(t *testing.T) {
	st, dev, eng := newFixture(t)
	addPeer(t, st, "a", "key-a", "10.0.0.2/32", nil)

	if err := eng.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}

	// Two readings of a cumulative counter must record the difference, not the
	// running total, or usage would grow quadratically.
	dev.counters("key-a", 1000, 500, time.Now())
	mustPoll(t, eng)
	dev.counters("key-a", 1600, 900, time.Now())
	mustPoll(t, eng)

	if err := eng.flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if got := usageOf(t, st, "key-a"); got != 2500 {
		t.Fatalf("usage = %d, want 2500 (1600 tx + 900 rx)", got)
	}
}

// Restarting wgui against a live interface must not fold the device's existing
// counters into the peer's total.
func TestSyncSeedsBaselineSoRestartDoesNotDoubleCount(t *testing.T) {
	st, dev, eng := newFixture(t)
	addPeer(t, st, "a", "key-a", "10.0.0.2/32", nil)

	// The interface already carries a large counter from before we started.
	dev.counters("key-a", 5_000_000, 3_000_000, time.Now())

	if err := eng.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}
	mustPoll(t, eng)
	if err := eng.flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}

	if got := usageOf(t, st, "key-a"); got != 0 {
		t.Fatalf("usage = %d, want 0: the counters present at startup are not new traffic", got)
	}

	dev.counters("key-a", 5_000_100, 3_000_000, time.Now())
	mustPoll(t, eng)
	if err := eng.flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if got := usageOf(t, st, "key-a"); got != 100 {
		t.Fatalf("usage = %d, want 100: only traffic after startup counts", got)
	}
}

// If the interface is recreated its counters restart from zero. The engine must
// read that as new traffic rather than a negative delta.
func TestCounterResetIsNotNegative(t *testing.T) {
	st, dev, eng := newFixture(t)
	addPeer(t, st, "a", "key-a", "10.0.0.2/32", nil)

	if err := eng.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}
	dev.counters("key-a", 10_000, 5_000, time.Now())
	mustPoll(t, eng)
	if err := eng.flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if got := usageOf(t, st, "key-a"); got != 15_000 {
		t.Fatalf("usage before reset = %d, want 15000", got)
	}

	// Interface recreated: counters drop back to a small value.
	dev.counters("key-a", 200, 100, time.Now())
	mustPoll(t, eng)
	if err := eng.flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if got := usageOf(t, st, "key-a"); got != 15_300 {
		t.Fatalf("usage after reset = %d, want 15300: the post-reset counters are traffic, not a rollback", got)
	}
}

func TestLiveSpeedIsDerivedButNotStored(t *testing.T) {
	st, dev, eng := newFixture(t)
	addPeer(t, st, "a", "key-a", "10.0.0.2/32", nil)

	if err := eng.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}

	// Backdate the baseline by a second so the delta spans a known interval.
	eng.mu.Lock()
	eng.samples["key-a"] = sample{tx: 0, rx: 0, at: time.Now().Add(-time.Second)}
	eng.mu.Unlock()

	dev.counters("key-a", 1_000_000, 500_000, time.Now())
	mustPoll(t, eng)

	live := eng.Live("key-a")
	if live.TXSpeed < 900_000 || live.TXSpeed > 1_100_000 {
		t.Errorf("tx speed = %d, want roughly 1000000 bytes/sec", live.TXSpeed)
	}
	if !live.Online {
		t.Error("a peer that just handshaked must read as online")
	}

	// The speed must exist only in memory.
	peers, _, err := st.ListPeers(store.PeerFilter{Scope: store.Scope{Role: "admin"}, Now: time.Now().UnixMilli()})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if peers[0].TXSpeed != 0 {
		t.Error("speed was read back from the database; it must never be persisted")
	}

	// It only appears once the engine decorates the rows.
	eng.Decorate(peers)
	if peers[0].TXSpeed == 0 {
		t.Error("Decorate must merge live speed into rows loaded from the database")
	}
}

// Usage that has not been flushed yet must still show up in the API's view, or
// the numbers would visibly lag behind the flush interval.
func TestDecorateIncludesUnflushedUsage(t *testing.T) {
	st, dev, eng := newFixture(t)
	addPeer(t, st, "a", "key-a", "10.0.0.2/32", nil)

	if err := eng.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}
	dev.counters("key-a", 700, 300, time.Now())
	mustPoll(t, eng)

	if got := usageOf(t, st, "key-a"); got != 0 {
		t.Fatalf("stored usage = %d, want 0 before the flush", got)
	}

	peers, _, err := st.ListPeers(store.PeerFilter{Scope: store.Scope{Role: "admin"}, Now: time.Now().UnixMilli()})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	eng.Decorate(peers)
	if peers[0].Usage != 1000 {
		t.Fatalf("decorated usage = %d, want 1000 including what is still pending", peers[0].Usage)
	}
}

func TestReconcileDisablesAndReenables(t *testing.T) {
	st, dev, eng := newFixture(t)
	addPeer(t, st, "a", "key-a", "10.0.0.2/32", func(p *store.Peer) {
		p.AllowedUsage = 1000
	})

	if err := eng.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if dev.disabled("key-a") {
		t.Fatal("a peer within its quota must start enabled")
	}

	// Blow through the quota.
	dev.counters("key-a", 900, 200, time.Now())
	mustPoll(t, eng)
	if err := eng.flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if err := eng.reconcile(); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !dev.disabled("key-a") {
		t.Fatal("a peer over its quota must be disabled on the device")
	}

	// Raising the allowance must bring it back without any manual step.
	limit := int64(10_000)
	if _, err := st.UpdatePeers([]string{"key-a"}, store.PeerPatch{AllowedUsage: &limit}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := eng.reconcile(); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if dev.disabled("key-a") {
		t.Fatal("raising the quota must re-enable the peer")
	}
}

// A manual disable must survive the enforcement loop: the loop owns quota and
// expiry, the admin owns the switch.
func TestManualDisableSurvivesReconcile(t *testing.T) {
	st, dev, eng := newFixture(t)
	addPeer(t, st, "a", "key-a", "10.0.0.2/32", nil)

	if err := eng.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}
	off := true
	if _, err := st.UpdatePeers([]string{"key-a"}, store.PeerPatch{ManuallyDisabled: &off}); err != nil {
		t.Fatalf("update: %v", err)
	}

	for i := 0; i < 3; i++ {
		if err := eng.reconcile(); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
	}
	if !dev.disabled("key-a") {
		t.Fatal("a manually disabled peer must stay disabled")
	}
}

// Re-pushing an unchanged peer would mint a fresh preshared key every tick, so
// reconcile must be a no-op when nothing changed.
func TestReconcileIsIdempotent(t *testing.T) {
	st, dev, eng := newFixture(t)
	addPeer(t, st, "a", "key-a", "10.0.0.2/32", nil)

	if err := eng.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}
	before := dev.setCount()
	for i := 0; i < 5; i++ {
		if err := eng.reconcile(); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
	}
	if got := dev.setCount() - before; got != 0 {
		t.Fatalf("reconcile issued %d device writes with nothing changed, want 0", got)
	}
}

func TestReconcileAddsAndRemovesPeers(t *testing.T) {
	st, dev, eng := newFixture(t)
	addPeer(t, st, "a", "key-a", "10.0.0.2/32", nil)
	if err := eng.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}

	addPeer(t, st, "b", "key-b", "10.0.0.3/32", nil)
	if err := eng.reconcile(); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if _, ok := dev.config["key-b"]; !ok {
		t.Fatal("a newly created peer must be pushed to the device")
	}

	if _, err := st.DeletePeers([]string{"key-a"}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	mustPoll(t, eng) // refresh the engine's view of what is on the device
	if err := eng.reconcile(); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if _, ok := dev.config["key-a"]; ok {
		t.Fatal("a deleted peer must be removed from the device")
	}
}

// A failed flush must not lose the traffic it was trying to record.
func TestFlushKeepsDeltasOnFailure(t *testing.T) {
	st, dev, eng := newFixture(t)
	addPeer(t, st, "a", "key-a", "10.0.0.2/32", nil)
	if err := eng.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}

	dev.counters("key-a", 400, 100, time.Now())
	mustPoll(t, eng)

	st.Close() // make the next write fail
	if err := eng.flush(); err == nil {
		t.Fatal("expected the flush to fail against a closed database")
	}

	tx, rx := eng.Pending("key-a")
	if tx != 400 || rx != 100 {
		t.Fatalf("pending = %d/%d after a failed flush, want 400/100 retained", tx, rx)
	}
}

func mustPoll(t *testing.T, eng *Engine) {
	t.Helper()
	if err := eng.poll(); err != nil {
		t.Fatalf("poll: %v", err)
	}
}

// Restarting wgui must not drop live sessions. A peer the kernel already holds
// exactly as the database wants it is left alone; only what is actually wrong
// is written, and writing a peer is what tears its handshake down.
func TestSyncLeavesCorrectlyConfiguredPeersAlone(t *testing.T) {
	st, dev, eng := newFixture(t)

	addPeer(t, st, "keep", "keep", "10.0.0.2/32", nil)
	addPeer(t, st, "wrong", "wrong", "10.0.0.3/32", nil)

	// The device already holds one of them correctly, one with the wrong
	// address, and one the database has never heard of.
	dev.peers = map[string]*wgdev.PeerState{
		"keep":    {PublicKey: "keep", AllowedIPs: "10.0.0.2/32"},
		"wrong":   {PublicKey: "wrong", AllowedIPs: "10.0.0.99/32"},
		"strange": {PublicKey: "strange", AllowedIPs: "10.0.0.50/32"},
	}

	if err := eng.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}

	for _, key := range dev.setKeys {
		if key == "keep" {
			t.Error("a peer already configured correctly was written again, dropping its session")
		}
	}
	if len(dev.setKeys) != 1 || dev.setKeys[0] != "wrong" {
		t.Errorf("wrote %v, want only the peer whose configuration was wrong", dev.setKeys)
	}
	if len(dev.removed) != 1 || dev.removed[0] != "strange" {
		t.Errorf("removed %v, want only the peer the database does not know", dev.removed)
	}
}

// A preshared key only governs the next handshake: the kernel keeps honouring
// the session already open until its keys age out, up to three minutes later.
// Switching a peer off therefore has to take it off the device first, which
// drops those keys with it.
func TestDisablingCutsTheOpenSession(t *testing.T) {
	st, dev, eng := newFixture(t)
	addPeer(t, st, "a", "key-a", "10.0.0.2/32", func(p *store.Peer) { p.AllowedUsage = 1000 })
	addPeer(t, st, "b", "key-b", "10.0.0.3/32", nil)
	if err := eng.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}

	dev.counters("key-a", 900, 200, time.Now())
	dev.counters("key-b", 10, 10, time.Now())
	mustPoll(t, eng)
	if err := eng.flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if err := eng.reconcile(); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	if !dev.disabled("key-a") {
		t.Fatal("a peer over its quota must be disabled")
	}
	if !contains(dev.removed, "key-a") {
		t.Error("the peer was only given a new preshared key, which leaves its open session running")
	}
	if contains(dev.removed, "key-b") {
		t.Error("a peer that stayed enabled was taken off the device")
	}

	// Switching it back on must not tear anything down: there is no session
	// to end, and removing it again would only cost the reconnect.
	dev.removed = nil
	limit := int64(1 << 30)
	if _, err := st.UpdatePeers([]string{"key-a"}, store.PeerPatch{AllowedUsage: &limit}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := eng.reconcile(); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if dev.disabled("key-a") || len(dev.removed) != 0 {
		t.Errorf("re-enabling: disabled=%v removed=%v, want enabled and nothing removed",
			dev.disabled("key-a"), dev.removed)
	}
}

// The same holds at startup: a peer the kernel is carrying live that should be
// off has to lose its session, not just its next handshake.
func TestSyncCutsASessionThatShouldBeOff(t *testing.T) {
	st, dev, eng := newFixture(t)
	addPeer(t, st, "a", "key-a", "10.0.0.2/32", func(p *store.Peer) { p.ManuallyDisabled = true })
	dev.peers = map[string]*wgdev.PeerState{
		"key-a": {PublicKey: "key-a", AllowedIPs: "10.0.0.2/32", LastHandshake: time.Now()},
	}

	if err := eng.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if !dev.disabled("key-a") || !contains(dev.removed, "key-a") {
		t.Errorf("disabled=%v removed=%v, want the live peer taken off and put back disabled",
			dev.disabled("key-a"), dev.removed)
	}
}

// Recording usage and enforcing limits are separate jobs. A write that keeps
// failing — a stale delta, a full disk, a damaged index — must not also leave
// every expired peer connected.
func TestEnforcementSurvivesAFailingFlush(t *testing.T) {
	st, dev, eng := newFixture(t)
	addPeer(t, st, "a", "key-a", "10.0.0.2/32", nil)
	if err := eng.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}

	if _, err := st.DB().Exec(`CREATE TRIGGER refuse BEFORE INSERT ON peer_usage
	                           BEGIN SELECT RAISE(ABORT, 'refused'); END`); err != nil {
		t.Fatalf("trigger: %v", err)
	}
	past := time.Now().Add(-time.Minute).UnixMilli()
	if _, err := st.UpdatePeers([]string{"key-a"}, store.PeerPatch{ExpiresAt: &past}); err != nil {
		t.Fatalf("expire: %v", err)
	}
	dev.counters("key-a", 100, 100, time.Now())
	mustPoll(t, eng)

	eng.settle()

	if !dev.disabled("key-a") {
		t.Error("an expired peer stayed connected because the usage flush failed")
	}
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
