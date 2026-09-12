// Package config holds the bootstrap configuration that must be known before the
// database is open. Everything that can be changed at runtime lives in the
// settings table instead, see Settings.
package config

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
)

type TLS struct {
	// Cert and Key point at the operator's own certificate. Left empty, wgui
	// generates a self-signed one; either way nothing is written to disk.
	Cert string `json:"cert"`
	Key  string `json:"key"`
	// Ephemeral regenerates the self-signed certificate on every start rather
	// than keeping it in the database. Nothing is then persisted at all, at the
	// cost of a fingerprint that changes at every restart, which makes browsers
	// warn again each time.
	Ephemeral bool `json:"ephemeral"`
}

// Master points this server at another wgui to take its peers from. Setting it
// is what makes a server a node: leave it empty and the server is a master,
// answering /api/sync for its own nodes.
type Master struct {
	// URL is the master's panel, e.g. https://panel.example.com. Empty means
	// this server is a master rather than a node.
	URL string `json:"url"`
	// Secret is the master's sync key, presented as a bearer token.
	Secret string `json:"secret"`
	// Fingerprint pins the master's certificate — the SHA-256 of its DER, as
	// the panel prints it. Set it when the master serves the self-signed
	// certificate wgui generates, which no CA will vouch for. Empty means the
	// certificate is verified the ordinary way.
	Fingerprint string `json:"fingerprint"`
	// Insecure accepts any certificate. A last resort; prefer Fingerprint.
	Insecure bool `json:"insecure"`
	// IntervalSeconds is how often to sync. 0 uses the default.
	IntervalSeconds int `json:"intervalSeconds"`
}

// IsNode reports whether this server takes its peers from a master.
func (m Master) IsNode() bool { return m.URL != "" }

// Config is read from config.json next to the executable. Changing any of it
// requires a restart; runtime-tunable options belong in Settings.
type Config struct {
	DBPath               string `json:"dbPath"`
	InterfaceName        string `json:"interfaceName"`
	InterfaceAddress     string `json:"interfaceAddress"`
	InterfaceAddressCIDR string `json:"interfaceAddressCIDR"`
	ListenAddress        string `json:"listenAddress"`
	TLS                  TLS    `json:"tls"`
	BypassKey            string `json:"bypassKey"`
	Master               Master `json:"master"`

	dir        string
	deviceCIDR *net.IPNet
}

// Dir is the directory config.json was loaded from; relative paths resolve here.
func (c *Config) Dir() string { return c.dir }

// DeviceCIDR is the parsed InterfaceAddressCIDR, used to decide whether a request
// came from inside the tunnel.
func (c *Config) DeviceCIDR() *net.IPNet { return c.deviceCIDR }

// Resolve turns a possibly relative path from the config into an absolute one.
func (c *Config) Resolve(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(c.dir, p)
}

func defaults() Config {
	return Config{
		DBPath:               "wgui.db",
		InterfaceName:        "wg0",
		InterfaceAddress:     "10.0.0.1",
		InterfaceAddressCIDR: "10.0.0.1/24",
		ListenAddress:        "0.0.0.0:443",
		// Left empty on purpose: with no certificate configured, a
		// self-signed one is generated on first start.
		TLS: TLS{},
	}
}

// Load reads config.json from dir and validates the fields the rest of the app
// assumes are well formed.
func Load(dir string) (*Config, error) {
	c := defaults()
	c.dir = dir

	path := filepath.Join(dir, "config.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	if net.ParseIP(c.InterfaceAddress) == nil {
		return nil, fmt.Errorf("interfaceAddress %q is not an IP address", c.InterfaceAddress)
	}
	if _, c.deviceCIDR, err = net.ParseCIDR(c.InterfaceAddressCIDR); err != nil {
		return nil, fmt.Errorf("interfaceAddressCIDR: %w", err)
	}
	if c.InterfaceName == "" {
		return nil, fmt.Errorf("interfaceName must not be empty")
	}
	return &c, nil
}
