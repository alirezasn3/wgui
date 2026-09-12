package api

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"wgui/internal/config"
	"wgui/internal/store"

	"github.com/labstack/echo/v4"
)

func nowMS() int64 { return time.Now().UnixMilli() }

// dayMS is the length of a day in milliseconds, used to turn the "expires in N
// days" form input into an absolute timestamp.
const dayMS = int64(24 * time.Hour / time.Millisecond)

// expiryFromDays converts a day count into an absolute expiry. Zero days means
// "never expires", matching the convention used throughout the database.
func expiryFromDays(days int) int64 {
	if days <= 0 {
		return 0
	}
	return nowMS() + int64(days)*dayMS
}

// pathID reads the :id path parameter, which is a URL-escaped public key.
func pathID(c echo.Context) (string, error) {
	return url.QueryUnescape(c.Param("id"))
}

func pathInt64(c echo.Context) (int64, error) {
	return strconv.ParseInt(c.Param("id"), 10, 64)
}

func queryInt(c echo.Context, name string, fallback int) int {
	v := c.QueryParam(name)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

// clientEndpointFor decides which server address a peer's generated config
// should point at: its own override, otherwise the configured default.
func clientEndpointFor(p *store.Peer, s config.Settings) string {
	if p.ClientEndpoint != "" {
		return p.ClientEndpoint
	}
	if s.DefaultEndpoint != "" {
		return s.DefaultEndpoint
	}
	if len(s.Endpoints) > 0 {
		return s.Endpoints[0]
	}
	return s.PublicAddress
}

// renderConfig builds the WireGuard client configuration for a peer.
func renderConfig(p *store.Peer, s config.Settings, serverPublicKey string) string {
	var b strings.Builder

	b.WriteString("[Interface]\n")
	fmt.Fprintf(&b, "PrivateKey = %s\n", p.PrivateKey)
	fmt.Fprintf(&b, "Address = %s\n", p.AllowedIPs)
	if s.PeerDefaults.DNS != "" {
		fmt.Fprintf(&b, "DNS = %s\n", s.PeerDefaults.DNS)
	}
	if s.PeerDefaults.MTU > 0 {
		fmt.Fprintf(&b, "MTU = %d\n", s.PeerDefaults.MTU)
	}

	b.WriteString("\n[Peer]\n")
	fmt.Fprintf(&b, "PublicKey = %s\n", serverPublicKey)
	fmt.Fprintf(&b, "AllowedIPs = %s\n", s.PeerDefaults.AllowedIPs)
	fmt.Fprintf(&b, "Endpoint = %s\n", clientEndpointFor(p, s))
	if s.PeerDefaults.PersistentKeepalive > 0 {
		fmt.Fprintf(&b, "PersistentKeepalive = %d\n", s.PeerDefaults.PersistentKeepalive)
	}
	return b.String()
}

// serverPublicKey reports the interface's own key, which every client config
// needs.
func (s *Server) serverPublicKey() string {
	if info := s.engine.Info(); info != nil {
		return info.PublicKey
	}
	return ""
}

// redactPeer strips the private key from peers the caller has no business
// configuring. A user still gets its own key, since that is its config.
func redactPeer(p *store.Peer, cl *Caller, canModify bool) {
	if p.ID == cl.PeerID || canModify || cl.Bypass {
		return
	}
	p.PrivateKey = ""
}
