package wgdev

import (
	"encoding/base64"
	"fmt"
	"net"

	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

type wgDevice struct {
	client *wgctrl.Client
	name   string
}

// Open connects to an existing WireGuard interface.
func Open(name string) (Device, error) {
	c, err := wgctrl.New()
	if err != nil {
		return nil, fmt.Errorf("open wireguard control socket: %w", err)
	}
	if _, err := c.Device(name); err != nil {
		c.Close()
		return nil, fmt.Errorf("read interface %q: %w", name, err)
	}
	return &wgDevice{client: c, name: name}, nil
}

func (d *wgDevice) Close() error { return d.client.Close() }

func (d *wgDevice) Info() (*Info, error) {
	dev, err := d.client.Device(d.name)
	if err != nil {
		return nil, err
	}
	info := &Info{
		Name:       dev.Name,
		PublicKey:  dev.PublicKey.String(),
		ListenPort: dev.ListenPort,
		Peers:      make([]PeerState, 0, len(dev.Peers)),
	}
	for _, p := range dev.Peers {
		endpoint := ""
		if p.Endpoint != nil {
			endpoint = p.Endpoint.String()
		}
		allowed := ""
		if len(p.AllowedIPs) > 0 {
			allowed = p.AllowedIPs[0].String()
		}
		info.Peers = append(info.Peers, PeerState{
			PublicKey:     p.PublicKey.String(),
			Endpoint:      endpoint,
			LastHandshake: p.LastHandshakeTime,
			TransmitBytes: p.TransmitBytes,
			ReceiveBytes:  p.ReceiveBytes,
			AllowedIPs:    allowed,
			// A peer is held unusable by a preshared key it does not have, so
			// a key being set is what "disabled" looks like on the device.
			Disabled: p.PresharedKey != (wgtypes.Key{}),
		})
	}
	return info, nil
}

func (d *wgDevice) ReplaceAll(peers []PeerConfig) error {
	cfgs := make([]wgtypes.PeerConfig, 0, len(peers))
	for _, p := range peers {
		c, err := toPeerConfig(p, false)
		if err != nil {
			return err
		}
		cfgs = append(cfgs, c)
	}
	return d.client.ConfigureDevice(d.name, wgtypes.Config{Peers: cfgs, ReplacePeers: true})
}

func (d *wgDevice) Set(p PeerConfig) error {
	c, err := toPeerConfig(p, false)
	if err != nil {
		return err
	}
	return d.client.ConfigureDevice(d.name, wgtypes.Config{Peers: []wgtypes.PeerConfig{c}})
}

func (d *wgDevice) Remove(publicKey string) error {
	key, err := wgtypes.ParseKey(publicKey)
	if err != nil {
		return fmt.Errorf("parse public key: %w", err)
	}
	return d.client.ConfigureDevice(d.name, wgtypes.Config{
		Peers: []wgtypes.PeerConfig{{PublicKey: key, Remove: true}},
	})
}

func toPeerConfig(p PeerConfig, updateOnly bool) (wgtypes.PeerConfig, error) {
	key, err := wgtypes.ParseKey(p.PublicKey)
	if err != nil {
		return wgtypes.PeerConfig{}, fmt.Errorf("parse public key %q: %w", p.PublicKey, err)
	}

	cfg := wgtypes.PeerConfig{
		PublicKey:         key,
		UpdateOnly:        updateOnly,
		ReplaceAllowedIPs: true,
	}

	if p.AllowedIPs != "" {
		_, ipNet, err := net.ParseCIDR(p.AllowedIPs)
		if err != nil {
			return wgtypes.PeerConfig{}, fmt.Errorf("parse allowed ips %q: %w", p.AllowedIPs, err)
		}
		cfg.AllowedIPs = []net.IPNet{*ipNet}
	}

	if p.Endpoint != "" {
		addr, err := net.ResolveUDPAddr("udp", p.Endpoint)
		if err != nil {
			return wgtypes.PeerConfig{}, fmt.Errorf("resolve endpoint %q: %w", p.Endpoint, err)
		}
		cfg.Endpoint = addr
	}

	// A random preshared key the client does not hold makes the handshake fail,
	// which is how a peer is switched off without deleting it. The zero key
	// means "no preshared key" and switches it back on.
	psk := wgtypes.Key{}
	if p.Disabled {
		psk, err = wgtypes.GenerateKey()
		if err != nil {
			return wgtypes.PeerConfig{}, fmt.Errorf("generate preshared key: %w", err)
		}
	}
	cfg.PresharedKey = &psk

	return cfg, nil
}

// GenerateKeypair returns a new base64 private/public key pair.
func GenerateKeypair() (privateKey, publicKey string, err error) {
	k, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		return "", "", err
	}
	return k.String(), k.PublicKey().String(), nil
}

// PublicKeyFor derives the public key of an existing base64 private key.
func PublicKeyFor(privateKey string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(privateKey)
	if err != nil {
		return "", err
	}
	k, err := wgtypes.NewKey(raw)
	if err != nil {
		return "", err
	}
	return k.PublicKey().String(), nil
}
