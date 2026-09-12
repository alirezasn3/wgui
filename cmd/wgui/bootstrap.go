package main

import (
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"

	"wgui/internal/config"
	"wgui/internal/netutil"
	"wgui/internal/store"
	"wgui/internal/wgdev"
)

// bootstrapAdmin creates the first admin peer on a fresh database and writes its
// client configuration next to the binary, since there is no other way to reach
// a panel that only answers requests from inside the tunnel.
func bootstrapAdmin(st *store.Store, cfg *config.Config, dev wgdev.Device, log *slog.Logger) error {
	// A node has no admin of its own to create: it is given the master's peers,
	// the admin among them, within seconds of starting. Creating one here would
	// take a tunnel address the master has already given to somebody else, and
	// write an admin.conf that stops working at the first sync.
	if cfg.Master.IsNode() {
		log.Info("this server is a node; its peers come from the master", "master", cfg.Master.URL)
		return nil
	}

	empty, err := st.IsEmpty()
	if err != nil || !empty {
		return err
	}

	settings, err := st.LoadSettings()
	if err != nil {
		return err
	}

	subnet, err := netip.ParsePrefix(cfg.InterfaceAddressCIDR)
	if err != nil {
		return err
	}
	server, err := netip.ParseAddr(cfg.InterfaceAddress)
	if err != nil {
		return err
	}
	address, err := netutil.NextFreeAddress(subnet, server, map[string]bool{})
	if err != nil {
		return err
	}

	privateKey, publicKey, err := wgdev.GenerateKeypair()
	if err != nil {
		return err
	}

	peer := &store.Peer{
		ID:         publicKey,
		Name:       "admin",
		Role:       config.RoleAdmin,
		PublicKey:  publicKey,
		PrivateKey: privateKey,
		AllowedIPs: address,
		// The first admin is deliberately unlimited: locking yourself out of
		// the only way in would be unrecoverable.
		AllowedUsage: 0,
		ExpiresAt:    0,
	}
	if err := st.CreatePeer(peer); err != nil {
		return fmt.Errorf("create first admin: %w", err)
	}

	info, err := dev.Info()
	if err != nil {
		return err
	}

	endpoint := settings.DefaultEndpoint
	if endpoint == "" {
		host := settings.PublicAddress
		if host == "" {
			host = "SERVER-ADDRESS"
		}
		endpoint = fmt.Sprintf("%s:%d", host, info.ListenPort)
	}

	conf := fmt.Sprintf(
		"[Interface]\nPrivateKey = %s\nAddress = %s\nDNS = %s\n\n[Peer]\nPublicKey = %s\nAllowedIPs = %s\nEndpoint = %s\n",
		peer.PrivateKey, peer.AllowedIPs, settings.PeerDefaults.DNS,
		info.PublicKey, settings.PeerDefaults.AllowedIPs, endpoint,
	)

	path := filepath.Join(cfg.Dir(), "admin.conf")
	if err := os.WriteFile(path, []byte(conf), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	log.Info("created the first admin peer", "name", peer.Name, "address", peer.AllowedIPs, "config", path)
	if settings.PublicAddress == "" {
		log.Warn("set the public address and endpoints in Settings, then regenerate this config", "config", path)
	}
	return nil
}
