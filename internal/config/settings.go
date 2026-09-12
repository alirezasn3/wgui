package config

import "strings"

// PeerDefaults seed the create-peer form and are applied server side when a
// field is omitted from the request.
type PeerDefaults struct {
	AllowedUsageBytes   int64  `json:"allowedUsageBytes"` // 0 = unlimited
	ExpiryDays          int    `json:"expiryDays"`        // 0 = never expires
	Role                string `json:"role"`
	DNS                 string `json:"dns"`
	AllowedIPs          string `json:"allowedIPs"` // AllowedIPs written into the client config
	MTU                 int    `json:"mtu"`        // 0 = omit from the client config
	PersistentKeepalive int    `json:"persistentKeepalive"`
}

type GroupDefaults struct {
	AllowedUsageBytes int64 `json:"allowedUsageBytes"`
	ExpiryDays        int   `json:"expiryDays"`
}

// QRSettings control the shareable QR image: the colour of the code and the
// labels printed around it.
type QRSettings struct {
	// Color is the dark modules and the label text. The default is the deep
	// green the panel has always used.
	Color string `json:"color"`
	// Caption is an optional line under the address, for a support address or
	// whatever else should travel with a shared code.
	Caption string `json:"caption"`
	// ShowName and ShowAddress print the peer's name above the code and its
	// tunnel address below it.
	ShowName    bool `json:"showName"`
	ShowAddress bool `json:"showAddress"`
}

type IPInfoSettings struct {
	Enabled       bool   `json:"enabled"`
	BaseURL       string `json:"baseURL"`
	CacheTTLHours int    `json:"cacheTTLHours"`
	// APIKey is sent verbatim as the value of APIKeyHeader, so a service
	// wanting "Bearer xyz" or a bare token is equally satisfied.
	APIKey       string `json:"apiKey"`
	APIKeyHeader string `json:"apiKeyHeader"`
}

// Settings is the runtime-editable configuration, persisted in the settings
// table and changeable from the UI without a restart.
type Settings struct {
	PublicAddress     string         `json:"publicAddress"`
	Endpoints         []string       `json:"endpoints"`
	DefaultEndpoint   string         `json:"defaultEndpoint"`
	PeerDefaults      PeerDefaults   `json:"peerDefaults"`
	GroupDefaults     GroupDefaults  `json:"groupDefaults"`
	QR                QRSettings     `json:"qr"`
	IPInfo            IPInfoSettings `json:"ipinfo"`
	UsageFlushSeconds int            `json:"usageFlushSeconds"`
}

// DefaultSettings are written to the database on first run.
func DefaultSettings() Settings {
	return Settings{
		PublicAddress:   "",
		Endpoints:       []string{},
		DefaultEndpoint: "",
		PeerDefaults: PeerDefaults{
			AllowedUsageBytes:   0,
			ExpiryDays:          30,
			Role:                "user",
			DNS:                 "1.1.1.1, 8.8.8.8",
			AllowedIPs:          "0.0.0.0/0",
			MTU:                 0,
			PersistentKeepalive: 0,
		},
		GroupDefaults: GroupDefaults{AllowedUsageBytes: 0, ExpiryDays: 30},
		QR: QRSettings{
			Color:       "#023020",
			ShowName:    true,
			ShowAddress: true,
		},
		IPInfo: IPInfoSettings{
			Enabled:       true,
			BaseURL:       "https://ipee-api.alirezasn.workers.dev/v1/info/",
			CacheTTLHours: 168,
			APIKeyHeader:  "Authorization",
		},
		UsageFlushSeconds: 10,
	}
}

// Normalize repairs values that would break callers downstream, so a bad edit in
// the settings UI degrades to a sane default instead of a panic.
func (s *Settings) Normalize() {
	if s.Endpoints == nil {
		s.Endpoints = []string{}
	}
	cleaned := s.Endpoints[:0]
	for _, e := range s.Endpoints {
		if e = strings.TrimSpace(e); e != "" {
			cleaned = append(cleaned, e)
		}
	}
	s.Endpoints = cleaned

	s.DefaultEndpoint = strings.TrimSpace(s.DefaultEndpoint)
	if s.DefaultEndpoint == "" && len(s.Endpoints) > 0 {
		s.DefaultEndpoint = s.Endpoints[0]
	}

	switch s.PeerDefaults.Role {
	case RoleAdmin, RoleDistributor, RoleUser:
	default:
		s.PeerDefaults.Role = RoleUser
	}
	if s.PeerDefaults.AllowedIPs == "" {
		s.PeerDefaults.AllowedIPs = "0.0.0.0/0"
	}
	if s.PeerDefaults.ExpiryDays < 0 {
		s.PeerDefaults.ExpiryDays = 0
	}
	if s.PeerDefaults.AllowedUsageBytes < 0 {
		s.PeerDefaults.AllowedUsageBytes = 0
	}
	if s.PeerDefaults.MTU < 0 || s.PeerDefaults.MTU > 9000 {
		s.PeerDefaults.MTU = 0
	}
	if s.PeerDefaults.PersistentKeepalive < 0 || s.PeerDefaults.PersistentKeepalive > 65535 {
		s.PeerDefaults.PersistentKeepalive = 0
	}
	if s.GroupDefaults.ExpiryDays < 0 {
		s.GroupDefaults.ExpiryDays = 0
	}
	if s.GroupDefaults.AllowedUsageBytes < 0 {
		s.GroupDefaults.AllowedUsageBytes = 0
	}
	// An unreadable colour would produce a code that will not scan, so anything
	// that is not a plain hex colour falls back to the default.
	if !isHexColor(s.QR.Color) {
		s.QR.Color = DefaultSettings().QR.Color
	}
	s.QR.Caption = strings.TrimSpace(s.QR.Caption)
	if len(s.QR.Caption) > 64 {
		s.QR.Caption = s.QR.Caption[:64]
	}
	if s.IPInfo.BaseURL == "" {
		s.IPInfo.BaseURL = DefaultSettings().IPInfo.BaseURL
	}
	if s.IPInfo.CacheTTLHours <= 0 {
		s.IPInfo.CacheTTLHours = 168
	}
	s.IPInfo.APIKey = strings.TrimSpace(s.IPInfo.APIKey)
	s.IPInfo.APIKeyHeader = strings.TrimSpace(s.IPInfo.APIKeyHeader)
	if s.IPInfo.APIKeyHeader == "" {
		s.IPInfo.APIKeyHeader = "Authorization"
	}
	if s.UsageFlushSeconds < 1 {
		s.UsageFlushSeconds = 1
	}
	if s.UsageFlushSeconds > 300 {
		s.UsageFlushSeconds = 300
	}
}

// isHexColor reports whether v looks like #rgb or #rrggbb.
func isHexColor(v string) bool {
	if len(v) != 4 && len(v) != 7 {
		return false
	}
	if v[0] != '#' {
		return false
	}
	for _, c := range v[1:] {
		isDigit := c >= '0' && c <= '9'
		isHex := (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
		if !isDigit && !isHex {
			return false
		}
	}
	return true
}

// Roles. A peer's role decides what it may see and change in the panel.
const (
	RoleAdmin       = "admin"       // everything
	RoleDistributor = "distributor" // peers and groups it owns
	RoleUser        = "user"        // itself, plus peers explicitly shared with it
)

func ValidRole(r string) bool {
	return r == RoleAdmin || r == RoleDistributor || r == RoleUser
}

// AdoptShared takes the policy a master sets for every server and leaves this
// server's own identity alone.
//
// Nearly everything here is policy that should read the same on every panel:
// what a new peer gets, how a QR code looks, which lookup service to use. Two
// fields are not. PublicAddress is the address this particular box answers on,
// and DefaultEndpoint is which of the endpoints its own configurations should
// point at — copy those from the master and every configuration downloaded from
// a node would send its peer to the master instead.
func (s Settings) AdoptShared(from Settings) Settings {
	out := from
	out.PublicAddress = s.PublicAddress
	out.DefaultEndpoint = s.DefaultEndpoint
	// A node keeps its own default but is offered the whole fleet's endpoints,
	// so a peer can be pointed at any server from any panel.
	if out.DefaultEndpoint == "" {
		out.DefaultEndpoint = s.PublicAddress
	}
	out.Normalize()
	return out
}
