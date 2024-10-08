package main

import "go.mongodb.org/mongo-driver/bson/primitive"

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
	GroupID            primitive.ObjectID    `json:"GroupID" bson:"groupID"`
}

type ServerSpecificInfo struct {
	Address           string `json:"Address" bson:"address"`
	LastHandshakeTime string `json:"LastHandshakeTime" bson:"lastHandshakeTime"`
	Endpoint          string `json:"Endpoint" bson:"endpoint"`
	CurrentTX         int64  `json:"CurrentTX" bson:"currentTX"`
	CurrentRX         int64  `json:"CurrentRX" bson:"currentRX"`
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
