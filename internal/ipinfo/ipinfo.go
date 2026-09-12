// Package ipinfo resolves the ISP, organisation and ASN behind a peer's current
// endpoint address. Lookups happen only when someone asks for them in the UI,
// and answers are cached so repeated views cost nothing.
package ipinfo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"wgui/internal/config"
	"wgui/internal/store"
)

var (
	ErrDisabled   = errors.New("ip information lookups are disabled")
	ErrNotPublic  = errors.New("address is not a public address")
	ErrNoEndpoint = errors.New("peer has no known endpoint")
)

// Provider looks addresses up through an HTTP service, caching results in the
// database.
type Provider struct {
	store    *store.Store
	settings func() config.IPInfoSettings
	client   *http.Client
}

func New(st *store.Store, settings func() config.IPInfoSettings) *Provider {
	return &Provider{
		store:    st,
		settings: settings,
		client:   &http.Client{Timeout: 10 * time.Second},
	}
}

// response is the shape returned by the configured service.
type response struct {
	OK               bool    `json:"ok"`
	CIDR             string  `json:"cidr"`
	ASNumber         int64   `json:"asNumber"`
	ASName           string  `json:"asName"`
	OrganizationName string  `json:"organizationName"`
	Country          *string `json:"country"`
	CountryCode      string  `json:"countryCode"`
}

// HostOf strips the port from an endpoint such as "1.2.3.4:51820".
func HostOf(endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" || endpoint == "<nil>" {
		return ""
	}
	if host, _, err := net.SplitHostPort(endpoint); err == nil {
		return host
	}
	return endpoint
}

// Lookup returns the information for ip, from the cache when it is still fresh.
func (p *Provider) Lookup(ctx context.Context, ip string) (*store.IPInfo, error) {
	cfg := p.settings()
	if !cfg.Enabled {
		return nil, ErrDisabled
	}

	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return nil, fmt.Errorf("parse %q: %w", ip, err)
	}
	// Asking a public service about a private address tells us nothing and
	// would just leak the shape of the internal network.
	if !addr.IsValid() || addr.IsPrivate() || addr.IsLoopback() ||
		addr.IsLinkLocalUnicast() || addr.IsUnspecified() {
		return nil, ErrNotPublic
	}
	ip = addr.String()

	ttl := time.Duration(cfg.CacheTTLHours) * time.Hour
	if cached, err := p.store.GetIPInfo(ip, ttl); err == nil {
		return cached, nil
	}

	info, err := p.fetch(ctx, cfg, ip)
	if err != nil {
		return nil, err
	}
	if err := p.store.PutIPInfo(*info); err != nil {
		// A cache write failure must not fail the lookup itself.
		info.FetchedAt = time.Now().UnixMilli()
	}
	return info, nil
}

func (p *Provider) fetch(ctx context.Context, cfg config.IPInfoSettings, ip string) (*store.IPInfo, error) {
	url := strings.TrimSuffix(cfg.BaseURL, "/") + "/" + ip

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("accept", "application/json")
	if cfg.APIKey != "" {
		header := cfg.APIKeyHeader
		if header == "" {
			header = "Authorization"
		}
		req.Header.Set(header, cfg.APIKey)
	}

	res, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ip lookup: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ip lookup returned %s", res.Status)
	}

	var body response
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode ip lookup response: %w", err)
	}

	info := &store.IPInfo{
		IP:          ip,
		ASN:         body.ASNumber,
		ASName:      body.ASName,
		Org:         body.OrganizationName,
		CIDR:        body.CIDR,
		CountryCode: body.CountryCode,
		FetchedAt:   time.Now().UnixMilli(),
	}
	if body.Country != nil {
		info.Country = *body.Country
	}
	// The service uses "?" for an unknown country code.
	if info.CountryCode == "?" {
		info.CountryCode = ""
	}
	return info, nil
}
