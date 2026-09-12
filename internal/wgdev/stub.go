package wgdev

import (
	"hash/crc32"
	"math/rand/v2"
	"sync"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

// stubDevice fakes a WireGuard interface so the panel can be developed and
// exercised on a machine without one. Enabled peers accumulate plausible
// traffic; disabled peers go quiet, which is what the real preshared-key trick
// achieves.
type stubDevice struct {
	mu     sync.Mutex
	name   string
	key    string
	peers  map[string]*stubPeer
	lastAt time.Time
}

type stubPeer struct {
	cfg           PeerConfig
	tx, rx        int64
	lastHandshake time.Time
	rate          float64 // bytes per second this peer trends towards
	endpointIndex int
}

// OpenStub returns an in-memory device. It is selected with --fake-wg.
func OpenStub(name string) Device {
	key, _ := wgtypes.GeneratePrivateKey()
	return &stubDevice{
		name:   name,
		key:    key.PublicKey().String(),
		peers:  map[string]*stubPeer{},
		lastAt: time.Now(),
	}
}

func (d *stubDevice) Close() error { return nil }

func (d *stubDevice) Info() (*Info, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(d.lastAt).Seconds()
	d.lastAt = now

	info := &Info{Name: d.name, PublicKey: d.key, ListenPort: 51820}
	for _, p := range d.peers {
		if !p.cfg.Disabled && elapsed > 0 {
			// Wander the rate around so the speed column visibly moves.
			p.rate += (rand.Float64() - 0.5) * 40_000
			if p.rate < 0 {
				p.rate = 0
			}
			if p.rate > 2_000_000 {
				p.rate = 2_000_000
			}
			p.tx += int64(p.rate * elapsed)
			p.rx += int64(p.rate * elapsed * 0.4)
			p.lastHandshake = now
		}
		info.Peers = append(info.Peers, PeerState{
			PublicKey:     p.cfg.PublicKey,
			Endpoint:      p.endpoint(),
			LastHandshake: p.lastHandshake,
			TransmitBytes: p.tx,
			ReceiveBytes:  p.rx,
		})
	}
	return info, nil
}

// endpoint reports the pinned endpoint if there is one, otherwise a stable fake
// public address so the IP-info lookup has something to resolve.
func (p *stubPeer) endpoint() string {
	if p.cfg.Endpoint != "" {
		return p.cfg.Endpoint
	}
	if p.lastHandshake.IsZero() {
		return ""
	}
	// Stable per peer, so the endpoint does not appear to roam between ticks.
	return fakeEndpoints[p.endpointIndex]
}

var fakeEndpoints = []string{
	"1.1.1.1:51820", "8.8.8.8:43210", "9.9.9.9:33445",
	"208.67.222.222:51820", "139.99.222.10:29384",
}

func (d *stubDevice) ReplaceAll(peers []PeerConfig) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	kept := make(map[string]*stubPeer, len(peers))
	for _, c := range peers {
		if existing, ok := d.peers[c.PublicKey]; ok {
			existing.cfg = c
			kept[c.PublicKey] = existing
			continue
		}
		kept[c.PublicKey] = newStubPeer(c)
	}
	d.peers = kept
	return nil
}

func (d *stubDevice) Set(p PeerConfig) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if existing, ok := d.peers[p.PublicKey]; ok {
		existing.cfg = p
		return nil
	}
	d.peers[p.PublicKey] = newStubPeer(p)
	return nil
}

func newStubPeer(c PeerConfig) *stubPeer {
	return &stubPeer{
		cfg:           c,
		rate:          rand.Float64() * 500_000,
		endpointIndex: int(crc32.ChecksumIEEE([]byte(c.PublicKey))) % len(fakeEndpoints),
	}
}

func (d *stubDevice) Remove(publicKey string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.peers, publicKey)
	return nil
}
