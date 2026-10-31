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

// TelegramMessages are everything the bot ever says. They are settings rather
// than strings in the code because the people reading them are the operator's
// customers, who rarely share the operator's language, and because a warning
// usually wants a line the operator alone can write — who to ask for a renewal.
//
// Messages are plain text. Telegram turns a bare link into a tappable one, and
// plain text cannot fail to parse: a stray angle bracket in an HTML message
// would make Telegram refuse every warning, not just that one.
//
// Placeholders are written in braces: {name} everywhere a peer is involved, and
// {remaining}, {days}, {hours} and {expires} in the two warnings. The /list
// message is filled in once for each subscription and also has {group},
// {used} and {allowed}; where a subscription has no limit it reads ∞, which
// needs no translating.
type TelegramMessages struct {
	// Linked answers a customer who has just connected a subscription.
	Linked string `json:"linked"`
	// Invalid answers a link that has expired, been used, or never existed.
	Invalid string `json:"invalid"`
	// Help answers everything else a customer might send the bot.
	Help string `json:"help"`
	// ExpiryWarning is sent once, as a subscription's end approaches.
	ExpiryWarning string `json:"expiry"`
	// UsageWarning is sent once, as a subscription's data runs low.
	UsageWarning string `json:"usage"`
	// List is what /list says about each subscription connected to the chat.
	List string `json:"list"`
	// ListEmpty is what /list says when there are none.
	ListEmpty string `json:"listEmpty"`
}

// TelegramSettings configure the warning bot. Its token is a secret and is
// kept out of here: settings are returned by the API and copied to every node.
type TelegramSettings struct {
	// WarnDays warns a peer when this many days or fewer are left before it
	// expires. 0 turns the warning off.
	WarnDays int `json:"warnDays"`
	// WarnUsageBytes warns a peer when this much or less of its allowance is
	// left. 0 turns the warning off.
	WarnUsageBytes int64            `json:"warnUsageBytes"`
	Messages       TelegramMessages `json:"messages"`
}

// maxMessageRunes keeps a message well inside Telegram's 4096-character limit
// once its placeholders have been filled in.
const maxMessageRunes = 1000

// Settings is the runtime-editable configuration, persisted in the settings
// table and changeable from the UI without a restart.
type Settings struct {
	PublicAddress     string           `json:"publicAddress"`
	Endpoints         []string         `json:"endpoints"`
	DefaultEndpoint   string           `json:"defaultEndpoint"`
	PeerDefaults      PeerDefaults     `json:"peerDefaults"`
	GroupDefaults     GroupDefaults    `json:"groupDefaults"`
	QR                QRSettings       `json:"qr"`
	IPInfo            IPInfoSettings   `json:"ipinfo"`
	UsageFlushSeconds int              `json:"usageFlushSeconds"`
	Telegram          TelegramSettings `json:"telegram"`
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
		Telegram: TelegramSettings{
			WarnDays:       1,
			WarnUsageBytes: 1 << 30,
			Messages: TelegramMessages{
				Linked:        "{name} is connected. You will get a message here before its data or time runs out.",
				Invalid:       "That link is not valid any more. Ask for a new one.",
				Help:          "I warn you before your data or time runs out. Send /list to see where your subscriptions stand. To connect a subscription, open the link you were given for it.",
				ExpiryWarning: "{name} expires on {expires}. Renew it before then to stay connected.",
				UsageWarning:  "{name} has only {remaining} of data left. Top it up to stay connected.",
				List:          "{name}\nData: {used} used of {allowed}, {remaining} left\nExpires: {expires}",
				ListEmpty:     "No subscription is connected to this chat. Open the link you were given for one to connect it.",
			},
		},
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

	s.Telegram.Normalize()
}

// Normalize repairs the bot's settings. A message left empty gets its default
// back, so clearing a box and saving never leaves the bot with nothing to say.
func (t *TelegramSettings) Normalize() {
	t.WarnDays = max(0, min(t.WarnDays, 365))
	t.WarnUsageBytes = max(0, t.WarnUsageBytes)

	def := DefaultSettings().Telegram.Messages
	for _, m := range []struct{ got, fallback *string }{
		{&t.Messages.Linked, &def.Linked},
		{&t.Messages.Invalid, &def.Invalid},
		{&t.Messages.Help, &def.Help},
		{&t.Messages.ExpiryWarning, &def.ExpiryWarning},
		{&t.Messages.UsageWarning, &def.UsageWarning},
		{&t.Messages.List, &def.List},
		{&t.Messages.ListEmpty, &def.ListEmpty},
	} {
		text := strings.TrimSpace(*m.got)
		if r := []rune(text); len(r) > maxMessageRunes {
			text = string(r[:maxMessageRunes])
		}
		if text == "" {
			text = *m.fallback
		}
		*m.got = text
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
