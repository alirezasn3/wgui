// Package engine owns the once-per-second reconciliation between the database
// and the WireGuard interface: it accounts for traffic, derives live speeds, and
// switches peers on and off as their quota and expiry change.
package engine

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"wgui/internal/store"
	"wgui/internal/wgdev"
)

// onlineWindow matches the store's definition of "recently handshaked".
const onlineWindow = 180 * time.Second

// Live is the per-peer state that is deliberately never written to the
// database: it is derived from the device on every tick and lost on restart.
type Live struct {
	TXSpeed       int64 `json:"txSpeed"` // bytes per second
	RXSpeed       int64 `json:"rxSpeed"`
	Endpoint      string
	LastHandshake int64 // unix ms, 0 = never
	Online        bool
}

// Totals is the aggregate shown on the dashboard.
type Totals struct {
	TXSpeed int64 `json:"txSpeed"`
	RXSpeed int64 `json:"rxSpeed"`
	Online  int   `json:"online"`
}

// sample is the previous counter reading for a peer, used to turn WireGuard's
// cumulative counters into per-tick deltas.
type sample struct {
	tx, rx int64
	at     time.Time
}

// applied is the last configuration actually pushed to the device, so the engine
// only issues a syscall when something really changed. Re-pushing a disabled
// peer would mint a fresh preshared key for no reason.
type applied struct {
	disabled   bool
	endpoint   string
	allowedIPs string
}

type Engine struct {
	store *store.Store
	dev   wgdev.Device
	log   *slog.Logger

	mu      sync.RWMutex
	live    map[string]Live              // by public key
	pending map[string]*store.UsageDelta // unflushed counters, by peer id
	samples map[string]sample            // last counter reading, by public key
	state   map[string]applied           // last pushed device config, by public key
	info    *wgdev.Info

	flushEvery func() time.Duration
	kick       chan struct{}
}

func New(st *store.Store, dev wgdev.Device, log *slog.Logger, flushEvery func() time.Duration) *Engine {
	return &Engine{
		store:      st,
		dev:        dev,
		log:        log,
		live:       map[string]Live{},
		pending:    map[string]*store.UsageDelta{},
		samples:    map[string]sample{},
		state:      map[string]applied{},
		flushEvery: flushEvery,
		kick:       make(chan struct{}, 1),
	}
}

// Sync makes the device's peer set match the database and seeds the counter
// baseline from the device.
//
// Seeding matters: without it the first tick would treat each peer's entire
// cumulative counter as traffic that happened in the last second, so restarting
// wgui against a live interface would inflate everyone's usage.
func (e *Engine) Sync() error {
	// Read the device before touching it. What is already there is the whole
	// point: a peer the kernel is holding exactly as the database wants it is
	// left alone, so restarting wgui no longer drops every live session for the
	// second it takes to write them all back.
	info, err := e.dev.Info()
	if err != nil {
		return err
	}
	onDevice := make(map[string]wgdev.PeerState, len(info.Peers))
	for _, p := range info.Peers {
		onDevice[p.PublicKey] = p
	}

	peers, err := e.store.EnginePeers(time.Now().UnixMilli())
	if err != nil {
		return err
	}

	state := make(map[string]applied, len(peers))
	wanted := make(map[string]bool, len(peers))
	written, skipped := 0, 0

	for _, p := range peers {
		want := wgdev.PeerConfig{
			PublicKey:  p.PublicKey,
			AllowedIPs: p.AllowedIPs,
			Endpoint:   p.PreferredEndpoint,
			Disabled:   !p.ShouldBeEnabled(),
		}
		wanted[p.PublicKey] = true

		if have, ok := onDevice[p.PublicKey]; ok && have.Matches(want) {
			skipped++
		} else {
			if err := e.apply(want); err != nil {
				e.log.Error("configure peer failed", "peer", p.ID, "error", err)
				continue
			}
			written++
		}
		state[p.PublicKey] = applied{
			disabled:   want.Disabled,
			endpoint:   want.Endpoint,
			allowedIPs: want.AllowedIPs,
		}
	}

	// Anything the kernel is holding that the database does not know about.
	removed := 0
	for key := range onDevice {
		if wanted[key] {
			continue
		}
		if err := e.dev.Remove(key); err != nil {
			e.log.Error("remove peer failed", "publicKey", key, "error", err)
			continue
		}
		removed++
	}

	e.mu.Lock()
	e.state = state
	// Re-read rather than reusing the first read: peers just written are not in
	// it, and their counters are the baseline every later delta is measured
	// from.
	e.mu.Unlock()

	info, err = e.dev.Info()
	if err != nil {
		return err
	}
	e.mu.Lock()
	e.info = info
	now := time.Now()
	for _, p := range info.Peers {
		e.samples[p.PublicKey] = sample{tx: p.TransmitBytes, rx: p.ReceiveBytes, at: now}
	}
	e.mu.Unlock()

	e.log.Info("device synchronised", "interface", info.Name, "peers", len(peers),
		"written", written, "unchanged", skipped, "removed", removed)
	return nil
}

// Kick asks for an immediate reconcile, used after the API changes something
// that affects whether a peer should be reachable.
func (e *Engine) Kick() {
	select {
	case e.kick <- struct{}{}:
	default: // one pending kick is enough
	}
}

// Run ticks until ctx is cancelled, then flushes what it has accumulated.
func (e *Engine) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	lastFlush := time.Now()
	for {
		select {
		case <-ctx.Done():
			if err := e.flush(); err != nil {
				e.log.Error("final usage flush failed", "error", err)
			}
			return

		case <-e.kick:
			if err := e.reconcile(); err != nil {
				e.log.Error("reconcile failed", "error", err)
			}

		case <-ticker.C:
			if err := e.poll(); err != nil {
				e.log.Error("device poll failed", "error", err)
				continue
			}
			if time.Since(lastFlush) < e.flushEvery() {
				continue
			}
			lastFlush = time.Now()
			e.settle()
		}
	}
}

// settle writes down what has been counted and then brings the device in line
// with the database.
//
// The two are independent on purpose. A flush that keeps failing — a stale
// delta, a full disk, a damaged index — costs the traffic it could not record,
// but it must not also stop enforcement: expiry, a manual switch and whatever
// another server has counted all still need to reach the device.
func (e *Engine) settle() {
	if err := e.flush(); err != nil {
		e.log.Error("usage flush failed", "error", err)
	}
	if err := e.reconcile(); err != nil {
		e.log.Error("reconcile failed", "error", err)
	}
}

// apply writes one peer to the device.
//
// A peer being switched off is taken off the device first. The preshared key
// that disables it only governs the next handshake: the kernel goes on honouring
// the session already open until its keys age out, which is up to three minutes
// (REJECT_AFTER_TIME). Removing the peer drops those keys with it, so it is cut
// off now rather than then. Removing a peer that is not there is a no-op.
func (e *Engine) apply(c wgdev.PeerConfig) error {
	if c.Disabled {
		if err := e.dev.Remove(c.PublicKey); err != nil {
			return err
		}
	}
	return e.dev.Set(c)
}

// poll reads the device and turns the cumulative counters into per-tick deltas,
// updating live speeds and accumulating usage to be flushed later.
func (e *Engine) poll() error {
	info, err := e.dev.Info()
	if err != nil {
		return err
	}
	now := time.Now()

	e.mu.Lock()
	defer e.mu.Unlock()
	e.info = info

	seen := make(map[string]bool, len(info.Peers))
	for _, p := range info.Peers {
		seen[p.PublicKey] = true

		prev, known := e.samples[p.PublicKey]
		e.samples[p.PublicKey] = sample{tx: p.TransmitBytes, rx: p.ReceiveBytes, at: now}

		var dtx, drx int64
		if known {
			dtx, drx = p.TransmitBytes-prev.tx, p.ReceiveBytes-prev.rx
			// A counter that went backwards means the interface was recreated,
			// so what is on it now is all traffic since the reset.
			if dtx < 0 {
				dtx = p.TransmitBytes
			}
			if drx < 0 {
				drx = p.ReceiveBytes
			}
		}

		elapsed := now.Sub(prev.at).Seconds()
		l := Live{Endpoint: p.Endpoint}
		if known && elapsed > 0 {
			l.TXSpeed = int64(float64(dtx) / elapsed)
			l.RXSpeed = int64(float64(drx) / elapsed)
		}
		if !p.LastHandshake.IsZero() {
			l.LastHandshake = p.LastHandshake.UnixMilli()
			l.Online = now.Sub(p.LastHandshake) < onlineWindow
		}
		e.live[p.PublicKey] = l

		if dtx == 0 && drx == 0 && l.LastHandshake == 0 {
			continue
		}
		d, ok := e.pending[p.PublicKey]
		if !ok {
			d = &store.UsageDelta{PeerID: p.PublicKey}
			e.pending[p.PublicKey] = d
		}
		d.TX += dtx
		d.RX += drx
		if l.LastHandshake > d.LastHandshakeAt {
			d.LastHandshakeAt = l.LastHandshake
		}
		if p.Endpoint != "" {
			d.LastEndpoint = p.Endpoint
		}
	}

	// Forget peers that are no longer on the device.
	for key := range e.samples {
		if !seen[key] {
			delete(e.samples, key)
			delete(e.live, key)
		}
	}
	return nil
}

// flush writes the accumulated counters to the database in one transaction.
func (e *Engine) flush() error {
	e.mu.Lock()
	if len(e.pending) == 0 {
		e.mu.Unlock()
		return nil
	}
	deltas := make([]store.UsageDelta, 0, len(e.pending))
	for _, d := range e.pending {
		deltas = append(deltas, *d)
	}
	e.pending = map[string]*store.UsageDelta{}
	e.mu.Unlock()

	if err := e.store.ApplyUsageDeltas(deltas); err != nil {
		// Put the deltas back so the traffic is not lost on a transient error.
		e.mu.Lock()
		for i := range deltas {
			d := deltas[i]
			if existing, ok := e.pending[d.PeerID]; ok {
				existing.TX += d.TX
				existing.RX += d.RX
			} else {
				e.pending[d.PeerID] = &d
			}
		}
		e.mu.Unlock()
		return err
	}
	return nil
}

// reconcile pushes the database's view of who should be reachable onto the
// device, touching only the peers whose desired configuration actually changed.
func (e *Engine) reconcile() error {
	desired, err := e.store.EnginePeers(time.Now().UnixMilli())
	if err != nil {
		return err
	}

	e.mu.Lock()
	onDevice := map[string]bool{}
	if e.info != nil {
		for _, p := range e.info.Peers {
			onDevice[p.PublicKey] = true
		}
	}
	state := make(map[string]applied, len(e.state))
	for k, v := range e.state {
		state[k] = v
	}
	e.mu.Unlock()

	wanted := make(map[string]bool, len(desired))
	changes := map[string]applied{}

	for _, p := range desired {
		wanted[p.PublicKey] = true
		want := applied{
			disabled:   !p.ShouldBeEnabled(),
			endpoint:   p.PreferredEndpoint,
			allowedIPs: p.AllowedIPs,
		}
		if prev, ok := state[p.PublicKey]; ok && prev == want && onDevice[p.PublicKey] {
			continue
		}
		if err := e.apply(wgdev.PeerConfig{
			PublicKey:  p.PublicKey,
			AllowedIPs: p.AllowedIPs,
			Endpoint:   p.PreferredEndpoint,
			Disabled:   want.disabled,
		}); err != nil {
			e.log.Error("configure peer failed", "peer", p.ID, "error", err)
			continue
		}
		changes[p.PublicKey] = want
		if _, existed := state[p.PublicKey]; existed {
			e.log.Info("peer state changed", "peer", p.ID, "status", p.Status)
		}
	}

	var removed []string
	for key := range onDevice {
		if wanted[key] {
			continue
		}
		if err := e.dev.Remove(key); err != nil {
			e.log.Error("remove peer failed", "publicKey", key, "error", err)
			continue
		}
		removed = append(removed, key)
	}

	e.mu.Lock()
	for k, v := range changes {
		e.state[k] = v
	}
	for _, k := range removed {
		delete(e.state, k)
		delete(e.live, k)
		delete(e.samples, k)
	}
	e.mu.Unlock()
	return nil
}

// -- readers ----------------------------------------------------------------

// Live returns the live counters for one peer.
func (e *Engine) Live(publicKey string) Live {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.live[publicKey]
}

// Pending returns the traffic recorded since the last flush, so the API can show
// usage that is current rather than up to one flush interval stale.
func (e *Engine) Pending(peerID string) (tx, rx int64) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if d, ok := e.pending[peerID]; ok {
		return d.TX, d.RX
	}
	return 0, 0
}

// Decorate merges live speed and unflushed usage into peers loaded from the
// database.
func (e *Engine) Decorate(peers []*store.Peer) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, p := range peers {
		if l, ok := e.live[p.PublicKey]; ok {
			p.TXSpeed, p.RXSpeed, p.Online = l.TXSpeed, l.RXSpeed, l.Online
			if l.LastHandshake > p.LastHandshakeAt {
				p.LastHandshakeAt = l.LastHandshake
			}
			if l.Endpoint != "" {
				p.LastEndpoint = l.Endpoint
			}
		}
		if d, ok := e.pending[p.ID]; ok {
			p.TotalTX += d.TX
			p.TotalRX += d.RX
			p.Usage = p.TotalTX + p.TotalRX
		}
	}
}

// Totals aggregates the live counters for the dashboard.
func (e *Engine) Totals() Totals {
	e.mu.RLock()
	defer e.mu.RUnlock()

	var t Totals
	for _, l := range e.live {
		t.TXSpeed += l.TXSpeed
		t.RXSpeed += l.RXSpeed
		if l.Online {
			t.Online++
		}
	}
	return t
}

// Info returns the last device reading.
func (e *Engine) Info() *wgdev.Info {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.info
}
