package main

type Peer struct {
	ID                 string                `json:"_id" bson:"_id"`
	Role               string                `json:"role" bson:"role"`
	Name               string                `json:"name" bson:"name"`
	PreferredEndpoint  string                `json:"preferredEndpoint" bson:"preferredEndpoint"`
	AllowedIPs         string                `json:"allowedIPs" bson:"allowedIPs"`
	PublicKey          string                `json:"publicKey" bson:"publicKey"`
	PrivateKey         string                `json:"privateKey" bson:"privateKey"`
	Disabled           bool                  `json:"disabled" bson:"disabled"`
	AllowedUsage       int64                 `json:"allowedUsage" bson:"allowedUsage"`
	ExpiresAt          int64                 `json:"expiresAt" bson:"expiresAt"`
	Endpoint           string                `json:"-" bson:"-"`
	LastHandshakeTime  string                `json:"-" bson:"-"`
	TempTX             int64                 `json:"-" bson:"-"`
	TempRX             int64                 `json:"-" bson:"-"`
	CurrentTX          int64                 `json:"-" bson:"-"`
	CurrentRX          int64                 `json:"-" bson:"-"`
	TotalTX            int64                 `json:"totalTX" bson:"totalTX"`
	TotalRX            int64                 `json:"totalRX" bson:"totalRX"`
	ServerSpecificInfo []*ServerSpecificInfo `json:"serverSpecificInfo" bson:"serverSpecificInfo"`
}

type ServerSpecificInfo struct {
	Address           string `json:"address" bson:"address"`
	LastHandshakeTime string `json:"lastHandshakeTime" bson:"lastHandshakeTime"`
	Endpoint          string `json:"endpoint" bson:"endpoint"`
	CurrentTX         int64  `json:"currentTX" bson:"currentTX"`
	CurrentRX         int64  `json:"currentRX" bson:"currentRX"`
}

func (peer *Peer) FindSSIByAddress(address string) *ServerSpecificInfo {
	peers.mu.RLock()
	defer peers.mu.RUnlock()
	for _, ssi := range peer.ServerSpecificInfo {
		if ssi.Address == address {
			return ssi
		}
	}
	return nil
}
