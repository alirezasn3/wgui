package store

// Status is the effective state of a peer or group, derived from the manual
// disable flag, expiry and quota. It is computed in SQL so it can also be
// filtered and sorted on.
const (
	StatusActive   = "active"
	StatusDisabled = "disabled" // switched off by hand, on the peer or its group
	StatusExpired  = "expired"
	StatusQuota    = "quota" // usage reached the peer's or the group's allowance
)

// Peer is a row of the peers table plus the fields derived from its group and
// owner. Live counters (speed, online) are not stored and are merged in by the
// API from the engine.
type Peer struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Role              string `json:"role"`
	OwnerID           string `json:"ownerId"`
	OwnerName         string `json:"ownerName"`
	GroupID           int64  `json:"groupId"` // 0 = no group
	GroupName         string `json:"groupName"`
	PublicKey         string `json:"publicKey"`
	PrivateKey        string `json:"privateKey,omitempty"`
	AllowedIPs        string `json:"allowedIps"`
	ClientEndpoint    string `json:"clientEndpoint"`
	PreferredEndpoint string `json:"preferredEndpoint"`
	AllowedUsage      int64  `json:"allowedUsage"` // 0 = unlimited
	ExpiresAt         int64  `json:"expiresAt"`    // 0 = never
	TotalTX           int64  `json:"totalTx"`
	TotalRX           int64  `json:"totalRx"`
	ManuallyDisabled  bool   `json:"manuallyDisabled"`
	LastHandshakeAt   int64  `json:"lastHandshakeAt"`
	LastEndpoint      string `json:"lastEndpoint"`
	Note              string `json:"note"`
	CreatedAt         int64  `json:"createdAt"`
	UpdatedAt         int64  `json:"updatedAt"`

	// UsageResetAt is when this peer's counters were last cleared, so a server
	// still reporting counts from before then cannot restore them.
	UsageResetAt int64 `json:"usageResetAt"`

	// Derived.
	Usage             int64  `json:"usage"` // TotalTX + TotalRX
	Status            string `json:"status"`
	GroupAllowedUsage int64  `json:"groupAllowedUsage"`
	GroupExpiresAt    int64  `json:"groupExpiresAt"`
	GroupUsage        int64  `json:"groupUsage"`
	// SharedCount is how many other peers this one has been granted sight of.
	SharedCount int64 `json:"sharedCount"`
	// SeenOn names the server that saw this peer most recently, and SeenOnID
	// identifies it. The API blanks the name when it is the server answering,
	// so the panel only points elsewhere when there is somewhere else to point.
	SeenOnID string `json:"seenOnId"`
	SeenOn   string `json:"seenOn"`
	// LocalHandshakeAt is when this server last saw the peer, as opposed to
	// LastHandshakeAt, which is when any server did.
	LocalHandshakeAt int64 `json:"localHandshakeAt"`

	// Merged in by the API from the engine, never persisted.
	TXSpeed int64 `json:"txSpeed"`
	RXSpeed int64 `json:"rxSpeed"`
	Online  bool  `json:"online"`
}

// EffectiveAllowedUsage is the allowance the peer is actually held to: its
// group's while it belongs to one, otherwise its own. 0 means unlimited.
func (p *Peer) EffectiveAllowedUsage() int64 {
	if p.GroupID != 0 {
		return p.GroupAllowedUsage
	}
	return p.AllowedUsage
}

// EffectiveExpiresAt is the expiry the peer is actually held to: its group's
// while it belongs to one, otherwise its own. 0 means never.
func (p *Peer) EffectiveExpiresAt() int64 {
	if p.GroupID != 0 {
		return p.GroupExpiresAt
	}
	return p.ExpiresAt
}

// EffectiveUsage is what has been spent out of that allowance — the group's
// combined total for a member, since that is what depletes the shared pot.
func (p *Peer) EffectiveUsage() int64 {
	if p.GroupID != 0 {
		return p.GroupUsage
	}
	return p.Usage
}

// Group is a row of the groups table plus its aggregate usage and member count.
type Group struct {
	ID               int64  `json:"id"`
	Name             string `json:"name"`
	OwnerID          string `json:"ownerId"`
	OwnerName        string `json:"ownerName"`
	AllowedUsage     int64  `json:"allowedUsage"` // 0 = unlimited
	ExpiresAt        int64  `json:"expiresAt"`    // 0 = never
	ManuallyDisabled bool   `json:"manuallyDisabled"`
	Note             string `json:"note"`
	CreatedAt        int64  `json:"createdAt"`
	UpdatedAt        int64  `json:"updatedAt"`

	// Derived.
	PeerCount int64  `json:"peerCount"`
	TotalTX   int64  `json:"totalTx"`
	TotalRX   int64  `json:"totalRx"`
	Usage     int64  `json:"usage"`
	Status    string `json:"status"`
}

// UsageDelta is a batch of accumulated counter increments flushed by the engine.
// All usage mutation goes through this one type so that moving to per-server
// accounting later stays contained.
type UsageDelta struct {
	PeerID          string
	TX              int64
	RX              int64
	LastHandshakeAt int64
	LastEndpoint    string
}

// IPInfo is one cached answer from the IP information provider.
type IPInfo struct {
	IP          string `json:"ip"`
	ASN         int64  `json:"asn"`
	ASName      string `json:"asName"`
	Org         string `json:"org"`
	Country     string `json:"country"`
	CountryCode string `json:"countryCode"`
	CIDR        string `json:"cidr"`
	FetchedAt   int64  `json:"fetchedAt"`
	Cached      bool   `json:"cached"`
}
