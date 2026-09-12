// Package wgdev isolates every interaction with the WireGuard interface behind
// one interface, so the panel can run against a real device, or against a stub
// on a machine that has no WireGuard at all.
package wgdev

import "time"

// PeerState is what the kernel currently reports about one peer.
type PeerState struct {
	PublicKey     string
	Endpoint      string
	LastHandshake time.Time
	TransmitBytes int64
	ReceiveBytes  int64
	// AllowedIPs and Disabled describe how the peer is configured right now,
	// as opposed to what it is doing. They are what lets a restart tell an
	// already-correct peer from one that needs writing, and leave the correct
	// one alone rather than tearing its session down and building it again.
	AllowedIPs string
	Disabled   bool
}

// Matches reports whether the device already holds this exact configuration.
func (s PeerState) Matches(c PeerConfig) bool {
	// An endpoint is only compared when one is pinned: an unpinned peer's
	// endpoint is whatever it last connected from, which is not ours to match.
	if c.Endpoint != "" && s.Endpoint != c.Endpoint {
		return false
	}
	return s.AllowedIPs == c.AllowedIPs && s.Disabled == c.Disabled
}

// Info describes the interface itself.
type Info struct {
	Name       string
	PublicKey  string
	ListenPort int
	Peers      []PeerState
}

// PeerConfig is the desired state of a single peer on the device.
//
// Disabled peers stay configured but are made unusable by giving the device a
// preshared key the client does not have, so the handshake fails without losing
// the peer's address allocation.
type PeerConfig struct {
	PublicKey  string
	AllowedIPs string // CIDR, e.g. 10.0.0.5/32
	Endpoint   string // pinned remote endpoint; empty clears the pin
	Disabled   bool
}

type Device interface {
	// Info reads the current state of the interface.
	Info() (*Info, error)
	// ReplaceAll makes the device's peer set exactly match peers, tearing down
	// whatever was there. Reserved for a device that cannot be reconciled
	// peer by peer; startup does not use it, because replacing a peer drops
	// its session.
	ReplaceAll(peers []PeerConfig) error
	// Set adds or updates a single peer.
	Set(p PeerConfig) error
	// Remove deletes a peer by public key.
	Remove(publicKey string) error
	Close() error
}
