package main

import "go.mongodb.org/mongo-driver/bson/primitive"

type Group struct {
	ID           primitive.ObjectID `json:"ID" bson:"_id"`
	PeerIDs      []string           `json:"PeersIDs" bson:"peerIDs"`
	AllowedUsage int64              `json:"AllowedUsage" bson:"allowedUsage"`
	TotalTX      int64              `json:"TotalTX" bson:"totalTX"`
	TotalRX      int64              `json:"TotalRX" bson:"totalRX"`
	ExpiresAt    int64              `json:"ExpiresAt" bson:"expiresAt"`
}
