package main

type Peer struct {
	ID                 string                `json:"ID" bson:"_id"`
	Role               string                `json:"Role" bson:"role"`
	Name               string                `json:"Name" bson:"name"`
	PreferredEndpoint  string                `json:"PreferredEndpoint" bson:"preferredEndpoint"`
	AllowedIPs         string                `json:"AllowedIPs" bson:"allowedIPs"`
	PublicKey          string                `json:"PublicKey" bson:"publicKey"`
	PrivateKey         string                `json:"PrivateKey" bson:"privateKey"`
	Disabled           bool                  `json:"Disabled" bson:"disabled"`
	AllowedUsage       int64                 `json:"AllowedUsage" bson:"allowedUsage"`
	ExpiresAt          int64                 `json:"ExpiresAt" bson:"expiresAt"`
	Endpoint           string                `json:"-" bson:"-"`
	LastHandshakeTime  string                `json:"-" bson:"-"`
	TempTX             int64                 `json:"-" bson:"-"`
	TempRX             int64                 `json:"-" bson:"-"`
	CurrentTX          int64                 `json:"-" bson:"-"`
	CurrentRX          int64                 `json:"-" bson:"-"`
	TotalTX            int64                 `json:"TotalTX" bson:"totalTX"`
	TotalRX            int64                 `json:"TotalRX" bson:"totalRX"`
	ServerSpecificInfo []*ServerSpecificInfo `json:"ServerSpecificInfo" bson:"serverSpecificInfo"`
	TelegramChatID     int64                 `json:"TelegramChatID" bson:"telegramChatID"`
}

type ServerSpecificInfo struct {
	Address           string `json:"address" bson:"Address"`
	LastHandshakeTime string `json:"lastHandshakeTime" bson:"LastHandshakeTime"`
	Endpoint          string `json:"endpoint" bson:"Endpoint"`
	CurrentTX         int64  `json:"currentTX" bson:"CurrentTX"`
	CurrentRX         int64  `json:"currentRX" bson:"CurrentRX"`
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
