package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"slices"
	"strings"

	"github.com/labstack/echo/v4"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	"google.golang.org/protobuf/proto"
)

func GetPeers(ctx echo.Context) error {
	var peer Peer
	err := collection.FindOne(context.Background(), bson.M{"allowedIPs": ctx.Get("peerIP").(string) + "/32"}).Decode(&peer)
	if err != nil {
		return ctx.String(500, err.Error())
	}

	pbPeers := make([]*PBPeer, 0, len(peers.peers))

	if peer.Role == "admin" {
		// return all peers
		peers.mu.RLock()
		defer peers.mu.RUnlock()
		for _, p := range peers.peers {
			pbPeer := &PBPeer{
				ID:                 p.ID,
				Name:               p.Name,
				AllowedIPs:         p.AllowedIPs,
				Disabled:           p.Disabled,
				AllowedUsage:       p.AllowedUsage,
				ExpiresAt:          p.ExpiresAt,
				TotalTX:            p.TotalTX,
				TotalRX:            p.TotalRX,
				ServerSpecificInfo: []*PBServerSpecificInfo{},
			}
			for _, ssi := range p.ServerSpecificInfo {
				pbPeer.ServerSpecificInfo = append(pbPeer.ServerSpecificInfo, &PBServerSpecificInfo{
					Address:           ssi.Address,
					LastHandshakeTime: ssi.LastHandshakeTime,
					Endpoint:          ssi.Endpoint,
					CurrentTX:         ssi.CurrentTX,
					CurrentRX:         ssi.CurrentRX,
				})
			}
			pbPeers = append(pbPeers, pbPeer)
		}
	} else {
		// return only group's peers if user is not admin
		group := strings.Split(peer.Name, "-")[0]
		peers.mu.RLock()
		defer peers.mu.RUnlock()
		for _, p := range peers.peers {
			if strings.HasPrefix(p.Name, group) {

				pbPeer := &PBPeer{
					ID:                 p.ID,
					Name:               p.Name,
					AllowedIPs:         p.AllowedIPs,
					Disabled:           p.Disabled,
					AllowedUsage:       p.AllowedUsage,
					ExpiresAt:          p.ExpiresAt,
					TotalTX:            p.TotalTX,
					TotalRX:            p.TotalRX,
					ServerSpecificInfo: []*PBServerSpecificInfo{},
				}
				for _, ssi := range p.ServerSpecificInfo {
					pbPeer.ServerSpecificInfo = append(pbPeer.ServerSpecificInfo, &PBServerSpecificInfo{
						Address:           ssi.Address,
						LastHandshakeTime: ssi.LastHandshakeTime,
						Endpoint:          ssi.Endpoint,
						CurrentTX:         ssi.CurrentTX,
						CurrentRX:         ssi.CurrentRX,
					})
				}
				pbPeers = append(pbPeers, pbPeer)
			}
		}
	}

	b, err := proto.Marshal(&PBPeers{Peers: pbPeers, Role: peer.Role})
	if err != nil {
		return ctx.String(500, err.Error())
	}
	return ctx.Blob(200, "application/x-protobuf", b)
}

func GetPeer(ctx echo.Context) error {
	var peer Peer
	err := collection.FindOne(context.Background(), bson.M{"allowedIPs": ctx.Get("peerIP").(string) + "/32"}).Decode(&peer)
	if err != nil {
		return ctx.String(500, err.Error())
	}

	group := strings.Split(peer.Name, "-")[0]

	// decode uri
	id, err := url.QueryUnescape(ctx.Param("id"))
	if err != nil {
		return ctx.NoContent(400)
	}

	// check if peer exists
	p, ok := peers.peers[id]
	if !ok {
		return ctx.NoContent(404)
	}

	// check if the requested peer is in the same group as the user
	if peer.Role != "admin" {
		if !strings.HasPrefix(p.Name, group) {
			return ctx.NoContent(403)
		}
	}

	// return peer
	return ctx.JSON(200, p)
}

func PostPeers(ctx echo.Context) error {
	var peer Peer
	err := collection.FindOne(context.Background(), bson.M{"allowedIPs": ctx.Get("peerIP").(string) + "/32"}).Decode(&peer)
	if err != nil {
		return ctx.String(500, err.Error())
	}

	group := strings.Split(peer.Name, "-")[0]

	if peer.Role == "user" {
		return ctx.NoContent(403)
	}

	// get peer info from request body
	var data Peer
	err = json.NewDecoder(ctx.Request().Body).Decode(&data)
	if err != nil {
		return ctx.String(400, err.Error())
	}

	// check if the requested peer is in the same group as the user
	if peer.Role == "distributor" {
		if !strings.HasPrefix(data.Name, group) {
			return ctx.NoContent(403)
		}
	}

	// create private and public keys
	privateKey, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		logger.Error(err.Error(), slog.String("peer", data.Name))
		return ctx.String(500, err.Error())
	}
	publicKey := privateKey.PublicKey()
	data.PrivateKey = privateKey.String()
	data.PublicKey = publicKey.String()

	// set id
	data.ID = publicKey.String()

	// find unused ip
	var ip IPAddress
	err = ip.Parse(config.InterfaceAddress)
	if err != nil {
		logger.Error(err.Error(), slog.String("peer", data.Name))
		return ctx.String(500, err.Error())
	}
	ip.Increment()

	peers.mu.Lock()
	defer peers.mu.Unlock()

findIP:
	// update device
	device, err = wgc.Device(config.InterfaceName)
	if err != nil {
		logger.Error(err.Error(), slog.String("peer", data.Name))
		return ctx.String(500, err.Error())
	}

	for slices.ContainsFunc(device.Peers, func(p wgtypes.Peer) bool {
		for _, aip := range p.AllowedIPs {
			if aip.String() == ip.ToString()+"/32" {
				return true
			}
		}
		return false
	}) {
		ip.Increment()
	}

	_, allowedIPs, err := net.ParseCIDR(ip.ToString() + "/32")
	if err != nil {
		logger.Error(err.Error(), slog.String("peer", data.Name))
		return ctx.String(500, err.Error())
	}
	data.AllowedIPs = ip.ToString() + "/32"

	var udpAddress *net.UDPAddr = nil

	if len(data.Endpoint) > 0 {
		udpAddress, err = net.ResolveUDPAddr("udp4", data.Endpoint)
		if err != nil {
			return ctx.String(400, err.Error())
		}
	}

	data.ServerSpecificInfo = []*ServerSpecificInfo{{Address: config.PublicAddress}}

	// add peer to local map
	peers.peers[data.PublicKey] = &data

	// add peer to database
	_, err = collection.InsertOne(context.TODO(), data)
	if err != nil {
		// Check if the error is a duplicate key error
		if writeException, ok := err.(mongo.WriteException); ok {
			for _, writeError := range writeException.WriteErrors {
				if writeError.Code == 11000 {
					logger.Error("duplicate key error when inserting into database", slog.String("peer", data.Name))
					ip.Increment()
					goto findIP
				}
			}
		} else {
			delete(peers.peers, data.PublicKey)
			logger.Error(err.Error(), slog.String("peer", data.Name))
			return ctx.String(500, err.Error())
		}
	}

	// add peer to device
	err = wgc.ConfigureDevice(config.InterfaceName, wgtypes.Config{Peers: []wgtypes.PeerConfig{{
		PublicKey:  publicKey,
		AllowedIPs: []net.IPNet{*allowedIPs},
		Endpoint:   udpAddress,
	}}})
	if err != nil {
		delete(peers.peers, data.PublicKey)
		logger.Error(err.Error(), slog.String("peer", data.Name))
		return ctx.String(500, err.Error())
	}

	logger.Info("Peer Created", slog.String("peer", data.Name))

	return ctx.String(201, data.PublicKey)
}

func DeletePeers(ctx echo.Context) error {
	var peer Peer
	err := collection.FindOne(context.Background(), bson.M{"allowedIPs": ctx.Get("peerIP").(string) + "/32"}).Decode(&peer)
	if err != nil {
		return ctx.String(500, err.Error())
	}

	group := strings.Split(peer.Name, "-")[0]

	if peer.Role == "user" {
		return ctx.NoContent(403)
	}

	// decode uri
	id, err := url.QueryUnescape(ctx.Param("id"))
	if err != nil {
		return ctx.NoContent(400)
	}

	// check if peer exists
	p, ok := peers.peers[id]
	if !ok {
		return ctx.NoContent(400)
	}

	// check if the requested peer is in the same group as the user
	if peer.Role == "distributor" {
		if !strings.HasPrefix(p.Name, group) {
			return ctx.NoContent(403)
		}
	}

	// parse peer public key
	pk, err := wgtypes.ParseKey(p.PublicKey)
	if err != nil {
		logger.Error(err.Error(), slog.String("peer", p.Name))
		return ctx.String(500, err.Error())
	}

	// remove peer from device
	err = wgc.ConfigureDevice(config.InterfaceName, wgtypes.Config{Peers: []wgtypes.PeerConfig{{
		PublicKey: pk,
		Remove:    true,
	}}})
	if err != nil {
		logger.Error(err.Error(), slog.String("peer", p.Name))
		return ctx.String(500, err.Error())
	}

	// delete peer from database
	_, err = collection.DeleteOne(context.TODO(), bson.M{"name": p.Name})
	if err != nil {
		logger.Error(err.Error(), slog.String("peer", p.Name))
		return ctx.String(500, err.Error())
	}

	logger.Info("Peer removed", slog.String("peer", p.Name))

	peers.mu.Lock()
	defer peers.mu.Unlock()
	// delete peer from local map
	delete(peers.peers, p.ID)

	return ctx.NoContent(200)
}

func PatchPeers(ctx echo.Context) error {
	var peer Peer
	err := collection.FindOne(context.Background(), bson.M{"allowedIPs": ctx.Get("peerIP").(string) + "/32"}).Decode(&peer)
	if err != nil {
		return ctx.String(500, err.Error())
	}

	if peer.Role == "user" {
		return ctx.NoContent(403)
	}

	// decode uri
	id, err := url.QueryUnescape(ctx.Param("id"))
	if err != nil {
		return ctx.NoContent(400)
	}

	// check if peer exists
	p, ok := peers.peers[id]
	if !ok {
		return ctx.NoContent(400)
	}

	group := strings.Split(peer.Name, "-")[0]

	// check if the requested peer is in the same group as the user
	if peer.Role == "distributor" {
		if !strings.HasPrefix(p.Name, group) {
			return ctx.NoContent(403)
		}
	}

	data := make(map[string]interface{})
	err = json.NewDecoder(ctx.Request().Body).Decode(&data)
	if err != nil {
		return ctx.String(400, err.Error())
	}

	var updates []mongo.WriteModel
	newPeerConfig := wgtypes.PeerConfig{UpdateOnly: true}

	// parse peer public key
	pk, err := wgtypes.ParseKey(p.PublicKey)
	if err != nil {
		logger.Error(err.Error(), slog.String("peer", p.Name))
		return ctx.String(500, err.Error())
	}
	newPeerConfig.PublicKey = pk

	if preferredEndpoint, ok := data["preferredEndpoint"].(string); ok {
		update := mongo.NewUpdateOneModel()
		update.SetFilter(bson.M{"publicKey": p.PublicKey})
		if preferredEndpoint == "" {
			update.SetUpdate(bson.M{"$set": bson.M{"preferredEndpoint": ""}})
			newPeerConfig.Endpoint = nil
		} else {
			udpAddress, err := net.ResolveUDPAddr("udp4", preferredEndpoint)
			if err != nil {
				return ctx.String(400, err.Error())
			}
			update.SetUpdate(bson.M{"$set": bson.M{"preferredEndpoint": udpAddress.String()}})
			newPeerConfig.Endpoint = udpAddress
		}
		updates = append(updates, update)

		err = wgc.ConfigureDevice(config.InterfaceName, wgtypes.Config{Peers: []wgtypes.PeerConfig{newPeerConfig}})
		if err != nil {
			logger.Error(err.Error(), slog.String("peer", p.Name))
			return ctx.String(500, err.Error())
		}
		peers.mu.Lock()
		p.PreferredEndpoint = preferredEndpoint
		peers.mu.Unlock()
	}

	if allowedUsage, ok := data["allowedUsage"].(float64); ok {
		update := mongo.NewUpdateOneModel()
		update.SetFilter(bson.M{"publicKey": p.PublicKey})
		update.SetUpdate(bson.M{"$set": bson.M{"allowedUsage": int64(allowedUsage)}})
		updates = append(updates, update)
		peers.mu.Lock()
		p.AllowedUsage = int64(allowedUsage)
		peers.mu.Unlock()
	}

	if expiresAt, ok := data["expiresAt"].(float64); ok {
		update := mongo.NewUpdateOneModel()
		update.SetFilter(bson.M{"publicKey": p.PublicKey})
		update.SetUpdate(bson.M{"$set": bson.M{"expiresAt": int64(expiresAt)}})
		updates = append(updates, update)
		peers.mu.Lock()
		p.ExpiresAt = int64(expiresAt)
		peers.mu.Unlock()
	}

	if role, ok := data["role"].(string); ok {
		update := mongo.NewUpdateOneModel()
		update.SetFilter(bson.M{"publicKey": p.PublicKey})
		update.SetUpdate(bson.M{"$set": bson.M{"role": role}})
		updates = append(updates, update)
		peers.mu.Lock()
		p.Role = role
		peers.mu.Unlock()
	}

	if name, ok := data["name"].(string); ok {
		update := mongo.NewUpdateOneModel()
		update.SetFilter(bson.M{"publicKey": p.PublicKey})
		update.SetUpdate(bson.M{"$set": bson.M{"name": name}})
		updates = append(updates, update)
		peers.mu.Lock()
		p.Name = name
		peers.mu.Unlock()
	}

	// update database
	if len(updates) > 0 {
		_, err := collection.BulkWrite(context.TODO(), updates, &options.BulkWriteOptions{})
		if err != nil {
			logger.Error(err.Error(), slog.String("peer", p.Name))
			return ctx.String(500, err.Error())
		}
	}

	return ctx.NoContent(200)
}

func PutPeers(ctx echo.Context) error {
	var peer Peer
	err := collection.FindOne(context.Background(), bson.M{"allowedIPs": ctx.Get("peerIP").(string) + "/32"}).Decode(&peer)
	if err != nil {
		return ctx.String(500, err.Error())
	}

	if peer.Role == "user" {
		return ctx.NoContent(403)
	}

	// decode uri
	id, err := url.QueryUnescape(ctx.Param("id"))
	if err != nil {
		return ctx.NoContent(400)
	}

	// check if peer exists
	p, ok := peers.peers[id]
	if !ok {
		return ctx.NoContent(400)
	}

	group := strings.Split(peer.Name, "-")[0]

	// check if the requested peer is in the same group as the user
	if peer.Role == "distributor" {
		if !strings.HasPrefix(p.Name, group) {
			return ctx.NoContent(403)
		}
	}

	_, err = collection.UpdateByID(context.TODO(), p.ID, bson.M{"$set": bson.M{
		"totalTX": int64(0), "totalRX": int64(0),
	}})
	if err != nil {
		logger.Error(err.Error(), slog.String("peer", p.Name))
		return ctx.String(500, err.Error())
	}

	peers.mu.Lock()
	defer peers.mu.Unlock()
	p.TotalTX = 0
	p.TotalRX = 0

	return nil
}

func GetConfig(ctx echo.Context) error {
	return ctx.JSON(200, map[string]interface{}{"serverPublicKey": device.PublicKey.String(), "serverAddress": fmt.Sprintf("%s:%d", config.PublicAddress, device.ListenPort), "endpoints": config.Endpoints, "telegramBotID": config.TelegramBotID})
}

func GetMe(ctx echo.Context) error {
	var peer Peer
	err := collection.FindOne(context.Background(), bson.M{"allowedIPs": ctx.Get("peerIP").(string) + "/32"}).Decode(&peer)
	if err != nil {
		return ctx.String(500, err.Error())
	}

	if peer.Role == "distributor" {
		return ctx.JSON(200, map[string]interface{}{"role": peer.Role, "prefix": strings.Split(peer.Name, "-")[0]})
	}
	return ctx.JSON(200, map[string]interface{}{"role": peer.Role, "prefix": ""})
}

func GetLogs(ctx echo.Context) error {
	var peer Peer
	err := collection.FindOne(context.Background(), bson.M{"allowedIPs": ctx.Get("peerIP").(string) + "/32"}).Decode(&peer)
	if err != nil {
		return ctx.String(500, err.Error())
	}

	if peer.Role != "admin" {
		return ctx.NoContent(403)
	}

	var logs []Log
	cursor, err := ioWriter.Collection.Find(context.Background(), bson.M{})
	if err != nil {
		logger.Error(err.Error())
		return ctx.String(500, err.Error())
	}
	if err = cursor.All(context.TODO(), &logs); err != nil {
		logger.Error(err.Error())
		return ctx.String(500, err.Error())
	}

	return ctx.JSON(200, logs)
}
