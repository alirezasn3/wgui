package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strings"

	"github.com/labstack/echo/v4"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

// peer key -> name:allowedIPs:publicKey
// group key -> name:ownerID

func GetPeers(ctx echo.Context) error {
	peerRole := ctx.Get("peerRole").(string)
	peerName := ctx.Get("peerName").(string)

	var err error
	var peers []*Peer

	if peerRole == "admin" {
		peers, err = peersDB.GetAllPeers()
		if err != nil {
			return ctx.String(500, err.Error())
		}
	} else {
		peers, err = peersDB.GetPeerNeighbours(strings.Split(peerName, "-")[0] + "-")
		if err != nil {
			return ctx.String(500, err.Error())
		}
	}

	return ctx.JSON(200, map[string]interface{}{"role": peerRole, "peers": peers})
}

func GetGroups(ctx echo.Context) error {
	peerRole := ctx.Get("peerRole").(string)
	peerID := ctx.Get("peerID").(string)

	var err error
	var groups []*Group

	if peerRole == "admin" {
		groups, err = groupsDB.GetAllGroups()
		if err != nil {
			return ctx.String(500, err.Error())
		}
	} else {
		groups, err = groupsDB.GetOwnedGroups(peerID)
		if err != nil {
			return ctx.String(500, err.Error())
		}
	}

	if groups == nil {
		groups = []*Group{}
	}

	return ctx.JSON(200, groups)
}

func GetPeer(ctx echo.Context) error {
	bypass := ctx.Get("bypass").(bool)
	peerRole := ctx.Get("peerRole").(string)
	peerName := ctx.Get("peerName").(string)

	// decode uri
	key, err := url.QueryUnescape(ctx.Param("key"))
	if err != nil {
		return ctx.NoContent(400)
	}

	// check if peer exists
	peer, err := peersDB.GetPeerByKey(key)
	if err != nil {
		if err.Error() == "peer not found" {
			return ctx.NoContent(404)
		}
		return ctx.String(500, err.Error())
	}

	if !bypass {
		// check if the requested peer is a neighbour of the user
		if peerRole != "admin" {
			if !strings.HasPrefix(peer.Name, strings.Split(peerName, "-")[0]+"-") {
				return ctx.NoContent(403)
			}
		}
	}

	ssis, err := ssisDB.GetSSIS(peer.PublicKey)
	if err != nil {
		return ctx.String(500, err.Error())
	}

	// return peer
	return ctx.JSON(200, map[string]interface{}{"peer": peer, "ssis": ssis})
}

func GetGroup(ctx echo.Context) error {
	peerRole := ctx.Get("peerRole").(string)
	peerID := ctx.Get("peerID").(string)
	groupName := ctx.Param("name")

	// check if group exists
	if groupName != "" {
		key, group, err := groupsDB.GetGroupByName(groupName)
		if err != nil {
			return ctx.NoContent(404)
		}
		// check read rights
		if peerRole != "admin" {
			if strings.Split(key, ":")[1] != peerID {
				return ctx.NoContent(403)
			}
		}
		return ctx.JSON(200, group)
	} else {
		return ctx.NoContent(400)
	}
}

func PostPeers(ctx echo.Context) error {
	bypass := ctx.Get("bypass").(bool)
	peerRole := ctx.Get("peerRole").(string)
	peerName := ctx.Get("peerName").(string)

	if !bypass {
		if peerRole == "user" {
			return ctx.NoContent(403)
		}
	}

	neighboursPrefix := strings.Split(peerName, "-")[0]

	// get peer info from request body
	var newPeer Peer
	err := json.NewDecoder(ctx.Request().Body).Decode(&newPeer)
	if err != nil {
		return ctx.String(400, err.Error())
	}

	// check for duplicate name
	duplicateName, err := peersDB.KeyPatternExists(newPeer.Name + ":*")
	if err != nil {
		return ctx.String(500, err.Error())
	}
	if duplicateName {
		return ctx.String(400, "duplicate name")
	}

	if !bypass {
		// check if the requested peer is a neighbour of the user
		if peerRole == "distributor" {
			if !strings.HasPrefix(newPeer.Name, neighboursPrefix+"-") {
				return ctx.NoContent(403)
			}
		}
	}

	// create private and public keys
	privateKey, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		return ctx.String(500, err.Error())
	}
	publicKey := privateKey.PublicKey()
	newPeer.PrivateKey = privateKey.String()
	newPeer.PublicKey = publicKey.String()

	// find unused ip
	var ip IPAddress
	err = ip.Parse(config.InterfaceAddress)
	if err != nil {
		return ctx.String(500, err.Error())
	}
	ip.Increment()

findIP:
	// update device
	device, err := wgc.Device(config.InterfaceName)
	if err != nil {
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
		return ctx.String(500, err.Error())
	}
	newPeer.AllowedIPs = ip.ToString() + "/32"

	// check for duplicate allowedIPs
	duplicateAllowedIPs, err := peersDB.KeyPatternExists("*:" + newPeer.AllowedIPs + ":*")
	if err != nil {
		return ctx.String(500, err.Error())
	}
	if duplicateAllowedIPs {
		ip.Increment()
		goto findIP
	}

	// add peer to database
	err = peersDB.CreatePeer(newPeer)
	if err != nil {
		if err.Error() == "duplicate peer name" {
			return ctx.String(400, "duplicate name")
		}
		return ctx.String(500, err.Error())
	}

	// add peer to device
	err = wgc.ConfigureDevice(config.InterfaceName, wgtypes.Config{Peers: []wgtypes.PeerConfig{{
		PublicKey:  publicKey,
		AllowedIPs: []net.IPNet{*allowedIPs},
	}}})
	if err != nil {
		return ctx.String(500, err.Error())
	}

	return ctx.String(201, newPeer.Name+":"+newPeer.AllowedIPs+":"+newPeer.PublicKey)
}

func PostGroups(ctx echo.Context) error {
	peerRole := ctx.Get("peerRole").(string)
	peerName := ctx.Get("peerName").(string)
	peerID := ctx.Get("peerID").(string)

	if peerRole == "user" {
		return ctx.NoContent(403)
	}

	// get group info from request body
	var data Group
	err := json.NewDecoder(ctx.Request().Body).Decode(&data)
	if err != nil {
		return ctx.String(400, err.Error())
	}

	// check if the requested peer is a neighbour of the user
	neighboursPrefix := strings.Split(peerName, "-")[0]
	if peerRole == "distributor" {
		if !strings.HasPrefix(data.Name, neighboursPrefix+"-") {
			return ctx.NoContent(403)
		}
	}

	// add group to database
	err = groupsDB.CreateGroup(data, peerID)
	if err != nil {
		if err.Error() == "duplicate group name" {
			return ctx.String(400, "duplicate name")
		}
	} else {
		return ctx.String(500, err.Error())
	}

	return ctx.String(201, data.Name+":"+peerID)
}

func DeletePeers(ctx echo.Context) error {
	// var peer Peer
	// err := peersCollection.FindOne(context.TODO(), bson.M{"allowedIPs": ctx.Get("peerIP").(string) + "/32"}).Decode(&peer)
	// if err != nil {
	// 	return ctx.String(500, err.Error())
	// }

	// neighboursPrefix := strings.Split(peer.Name, "-")[0]

	// if peer.Role == "user" {
	// 	return ctx.NoContent(403)
	// }

	// // decode uri
	// id, err := url.QueryUnescape(ctx.Param("id"))
	// if err != nil {
	// 	return ctx.NoContent(400)
	// }

	// // check if peer exists
	// p, ok := peers.peers[id]
	// if !ok {
	// 	return ctx.NoContent(400)
	// }

	// // check if the requested peer is a neighbour the user
	// if peer.Role == "distributor" {
	// 	if !strings.HasPrefix(p.Name, neighboursPrefix+"-") {
	// 		return ctx.NoContent(403)
	// 	}
	// }

	// // parse peer public key
	// pk, err := wgtypes.ParseKey(p.PublicKey)
	// if err != nil {
	// 	logger.Error(err.Error(), slog.String("peer", p.Name))
	// 	return ctx.String(500, err.Error())
	// }

	// // remove peer from device
	// err = wgc.ConfigureDevice(config.InterfaceName, wgtypes.Config{Peers: []wgtypes.PeerConfig{{
	// 	PublicKey: pk,
	// 	Remove:    true,
	// }}})
	// if err != nil {
	// 	logger.Error(err.Error(), slog.String("peer", p.Name))
	// 	return ctx.String(500, err.Error())
	// }

	// // delete peer from database
	// _, err = peersCollection.DeleteOne(context.TODO(), bson.M{"name": p.Name})
	// if err != nil {
	// 	logger.Error(err.Error(), slog.String("peer", p.Name))
	// 	return ctx.String(500, err.Error())
	// }
	// _, err = groupsCollection.UpdateByID(context.TODO(), p.GroupID, bson.M{"$pull": bson.M{"peerIDs": p.ID}})
	// if err != nil {
	// 	logger.Error(err.Error(), slog.String("peer", peer.Name))
	// 	return ctx.String(500, err.Error())
	// }

	// logger.Info("Peer removed", slog.String("peer", p.Name))

	// peers.mu.Lock()
	// defer peers.mu.Unlock()
	// // delete peer from local map
	// delete(peers.peers, p.ID)

	// return ctx.NoContent(200)

	return nil
}

func DeleteGroup(ctx echo.Context) error {
	// var peer Peer
	// err := peersCollection.FindOne(context.TODO(), bson.M{"allowedIPs": ctx.Get("peerIP").(string) + "/32"}).Decode(&peer)
	// if err != nil {
	// 	return ctx.String(500, err.Error())
	// }

	// if peer.Role == "user" {
	// 	return ctx.NoContent(403)
	// }

	// // parse id
	// objectID, err := primitive.ObjectIDFromHex(ctx.Param("id"))
	// if err != nil {
	// 	return ctx.NoContent(400)
	// }

	// // check if group exists
	// var group Group
	// err = groupsCollection.FindOne(context.TODO(), bson.M{"_id": objectID}).Decode(&group)
	// if err != nil {
	// 	return ctx.NoContent(404)
	// }

	// // check read rights
	// if peer.Role != "admin" && group.OwnerID != peer.ID {
	// 	if group.OwnerID != peer.ID {
	// 		return ctx.NoContent(403)
	// 	}
	// }

	// // delete group from database
	// _, err = groupsCollection.DeleteOne(context.TODO(), bson.M{"_id": group.ID})
	// if err != nil {
	// 	return ctx.String(500, err.Error())
	// }

	// return ctx.NoContent(200)

	return nil
}

func DeletePeerFromGroup(ctx echo.Context) error {
	// var peer Peer
	// err := peersCollection.FindOne(context.TODO(), bson.M{"allowedIPs": ctx.Get("peerIP").(string) + "/32"}).Decode(&peer)
	// if err != nil {
	// 	return ctx.String(500, err.Error())
	// }

	// if peer.Role == "user" {
	// 	return ctx.NoContent(403)
	// }

	// // parse group id
	// groupObjectID, err := primitive.ObjectIDFromHex(ctx.Param("groupID"))
	// if err != nil {
	// 	return ctx.NoContent(400)
	// }

	// // check if group exists
	// var group Group
	// err = groupsCollection.FindOne(context.TODO(), bson.M{"_id": groupObjectID}).Decode(&group)
	// if err != nil {
	// 	return ctx.NoContent(404)
	// }

	// // check read rights
	// if peer.Role != "admin" && group.OwnerID != peer.ID {
	// 	if group.OwnerID != peer.ID {
	// 		return ctx.NoContent(403)
	// 	}
	// }

	// // parse peer id
	// peerID, err := url.QueryUnescape(ctx.Param("peerID"))
	// if err != nil {
	// 	return ctx.NoContent(400)
	// }

	// // delete peer from group
	// _, err = peersCollection.UpdateByID(context.TODO(), peerID, bson.M{"$set": bson.M{"groupID": primitive.NilObjectID}})
	// if err != nil {
	// 	logger.Error(err.Error(), slog.String("peer", peer.Name))
	// 	return ctx.String(500, err.Error())
	// }
	// _, err = groupsCollection.UpdateByID(context.TODO(), groupObjectID, bson.M{"$pull": bson.M{"peerIDs": peerID}})
	// if err != nil {
	// 	logger.Error(err.Error(), slog.String("peer", peer.Name))
	// 	return ctx.String(500, err.Error())
	// }

	// return ctx.NoContent(200)

	return nil
}

func PatchPeers(ctx echo.Context) error {
	// var peer Peer
	// err := peersCollection.FindOne(context.TODO(), bson.M{"allowedIPs": ctx.Get("peerIP").(string) + "/32"}).Decode(&peer)
	// if err != nil {
	// 	return ctx.String(500, err.Error())
	// }

	// if peer.Role == "user" {
	// 	return ctx.NoContent(403)
	// }

	// // decode uri
	// id, err := url.QueryUnescape(ctx.Param("id"))
	// if err != nil {
	// 	return ctx.NoContent(400)
	// }

	// // check if peer exists
	// p, ok := peers.peers[id]
	// if !ok {
	// 	return ctx.NoContent(400)
	// }

	// neighboursPrefix := strings.Split(peer.Name, "-")[0]

	// // check if the requested peer is a neighbour of the user
	// if peer.Role == "distributor" {
	// 	if !strings.HasPrefix(p.Name, neighboursPrefix+"-") {
	// 		return ctx.NoContent(403)
	// 	}
	// }

	// data := make(map[string]interface{})
	// err = json.NewDecoder(ctx.Request().Body).Decode(&data)
	// if err != nil {
	// 	return ctx.String(400, err.Error())
	// }

	// var updates []mongo.WriteModel
	// newPeerConfig := wgtypes.PeerConfig{UpdateOnly: true}

	// // parse peer public key
	// pk, err := wgtypes.ParseKey(p.PublicKey)
	// if err != nil {
	// 	logger.Error(err.Error(), slog.String("peer", p.Name))
	// 	return ctx.String(500, err.Error())
	// }
	// newPeerConfig.PublicKey = pk

	// if preferredEndpoint, ok := data["preferredEndpoint"].(string); ok {
	// 	update := mongo.NewUpdateOneModel()
	// 	update.SetFilter(bson.M{"publicKey": p.PublicKey})
	// 	if preferredEndpoint == "" {
	// 		update.SetUpdate(bson.M{"$set": bson.M{"preferredEndpoint": ""}})
	// 		newPeerConfig.Endpoint = nil
	// 	} else {
	// 		udpAddress, err := net.ResolveUDPAddr("udp4", preferredEndpoint)
	// 		if err != nil {
	// 			return ctx.String(400, err.Error())
	// 		}
	// 		update.SetUpdate(bson.M{"$set": bson.M{"preferredEndpoint": udpAddress.String()}})
	// 		newPeerConfig.Endpoint = udpAddress
	// 	}
	// 	updates = append(updates, update)

	// 	err = wgc.ConfigureDevice(config.InterfaceName, wgtypes.Config{Peers: []wgtypes.PeerConfig{newPeerConfig}})
	// 	if err != nil {
	// 		logger.Error(err.Error(), slog.String("peer", p.Name))
	// 		return ctx.String(500, err.Error())
	// 	}
	// 	peers.mu.Lock()
	// 	p.PreferredEndpoint = preferredEndpoint
	// 	peers.mu.Unlock()
	// }

	// if allowedUsage, ok := data["allowedUsage"].(float64); ok {
	// 	update := mongo.NewUpdateOneModel()
	// 	update.SetFilter(bson.M{"publicKey": p.PublicKey})
	// 	update.SetUpdate(bson.M{"$set": bson.M{"allowedUsage": int64(allowedUsage)}})
	// 	updates = append(updates, update)
	// 	peers.mu.Lock()
	// 	p.AllowedUsage = int64(allowedUsage)
	// 	peers.mu.Unlock()
	// }

	// if expiresAt, ok := data["expiresAt"].(float64); ok {
	// 	update := mongo.NewUpdateOneModel()
	// 	update.SetFilter(bson.M{"publicKey": p.PublicKey})
	// 	update.SetUpdate(bson.M{"$set": bson.M{"expiresAt": int64(expiresAt)}})
	// 	updates = append(updates, update)
	// 	peers.mu.Lock()
	// 	p.ExpiresAt = int64(expiresAt)
	// 	peers.mu.Unlock()
	// }

	// if role, ok := data["role"].(string); ok {
	// 	update := mongo.NewUpdateOneModel()
	// 	update.SetFilter(bson.M{"publicKey": p.PublicKey})
	// 	update.SetUpdate(bson.M{"$set": bson.M{"role": role}})
	// 	updates = append(updates, update)
	// 	peers.mu.Lock()
	// 	p.Role = role
	// 	peers.mu.Unlock()
	// }

	// if name, ok := data["name"].(string); ok {
	// 	update := mongo.NewUpdateOneModel()
	// 	update.SetFilter(bson.M{"publicKey": p.PublicKey})
	// 	update.SetUpdate(bson.M{"$set": bson.M{"name": name}})
	// 	updates = append(updates, update)
	// 	peers.mu.Lock()
	// 	p.Name = name
	// 	peers.mu.Unlock()
	// }

	// // update database
	// if len(updates) > 0 {
	// 	_, err := peersCollection.BulkWrite(context.TODO(), updates, &options.BulkWriteOptions{})
	// 	if err != nil {
	// 		logger.Error(err.Error(), slog.String("peer", p.Name))
	// 		return ctx.String(500, err.Error())
	// 	}
	// }

	// return ctx.NoContent(200)

	return nil
}

func PatchGroups(ctx echo.Context) error {
	// var peer Peer
	// err := peersCollection.FindOne(context.TODO(), bson.M{"allowedIPs": ctx.Get("peerIP").(string) + "/32"}).Decode(&peer)
	// if err != nil {
	// 	return ctx.String(500, err.Error())
	// }

	// if peer.Role == "user" {
	// 	return ctx.NoContent(403)
	// }

	// // parse id
	// objectID, err := primitive.ObjectIDFromHex(ctx.Param("id"))
	// if err != nil {
	// 	return ctx.NoContent(400)
	// }

	// // check if group exists
	// var group Group
	// err = groupsCollection.FindOne(context.TODO(), bson.M{"_id": objectID}).Decode(&group)
	// if err != nil {
	// 	return ctx.NoContent(404)
	// }

	// // check read rights
	// if peer.Role != "admin" && group.OwnerID != peer.ID {
	// 	if group.OwnerID != peer.ID {
	// 		return ctx.NoContent(403)
	// 	}
	// }

	// data := make(map[string]interface{})
	// err = json.NewDecoder(ctx.Request().Body).Decode(&data)
	// if err != nil {
	// 	return ctx.String(400, err.Error())
	// }

	// var groupUpdates []mongo.WriteModel
	// var peerUpdates []mongo.WriteModel

	// if allowedUsage, ok := data["allowedUsage"].(float64); ok {
	// 	groupUpdate := mongo.NewUpdateOneModel()
	// 	groupUpdate.SetFilter(bson.M{"_id": group.ID})
	// 	groupUpdate.SetUpdate(bson.M{"$set": bson.M{"allowedUsage": int64(allowedUsage)}})
	// 	groupUpdates = append(groupUpdates, groupUpdate)
	// 	for _, peerID := range group.PeerIDs {
	// 		peerUpdate := mongo.NewUpdateOneModel()
	// 		peerUpdate.SetFilter(bson.M{"_id": peerID})
	// 		peerUpdate.SetUpdate(bson.M{"$set": bson.M{"allowedUsage": int64(allowedUsage)}})
	// 		peerUpdates = append(peerUpdates, peerUpdate)
	// 		peers.mu.Lock()
	// 		peers.peers[peerID].AllowedUsage = int64(allowedUsage)
	// 		peers.mu.Unlock()
	// 	}
	// }

	// if expiresAt, ok := data["expiresAt"].(float64); ok {
	// 	groupUpdate := mongo.NewUpdateOneModel()
	// 	groupUpdate.SetFilter(bson.M{"_id": group.ID})
	// 	groupUpdate.SetUpdate(bson.M{"$set": bson.M{"expiresAt": int64(expiresAt)}})
	// 	groupUpdates = append(groupUpdates, groupUpdate)
	// 	for _, peerID := range group.PeerIDs {
	// 		peerUpdate := mongo.NewUpdateOneModel()
	// 		peerUpdate.SetFilter(bson.M{"_id": peerID})
	// 		peerUpdate.SetUpdate(bson.M{"$set": bson.M{"expiresAt": int64(expiresAt)}})
	// 		peerUpdates = append(peerUpdates, peerUpdate)
	// 		peers.mu.Lock()
	// 		peers.peers[peerID].ExpiresAt = int64(expiresAt)
	// 		peers.mu.Unlock()
	// 	}
	// }

	// if name, ok := data["name"].(string); ok {
	// 	groupUpdate := mongo.NewUpdateOneModel()
	// 	groupUpdate.SetFilter(bson.M{"_id": group.ID})
	// 	groupUpdate.SetUpdate(bson.M{"$set": bson.M{"name": name}})
	// 	groupUpdates = append(groupUpdates, groupUpdate)
	// }

	// // update database
	// if len(groupUpdates) > 0 {
	// 	_, err := groupsCollection.BulkWrite(context.TODO(), groupUpdates, &options.BulkWriteOptions{})
	// 	if err != nil {
	// 		logger.Error(err.Error(), slog.String("peer", peer.Name))
	// 		return ctx.String(500, err.Error())
	// 	}
	// }
	// if len(peerUpdates) > 0 {
	// 	_, err := peersCollection.BulkWrite(context.TODO(), peerUpdates, &options.BulkWriteOptions{})
	// 	if err != nil {
	// 		logger.Error(err.Error(), slog.String("peer", peer.Name))
	// 		return ctx.String(500, err.Error())
	// 	}
	// }

	// return ctx.NoContent(200)

	return nil
}

// put method is used to reset the usage of a peer
func PutPeers(ctx echo.Context) error {
	peerName := ctx.Get("peerName").(string)
	peerRole := ctx.Get("peerRole").(string)

	if peerRole == "user" {
		return ctx.NoContent(403)
	}

	// decode uri
	key, err := url.QueryUnescape(ctx.Param("id"))
	if err != nil {
		return ctx.NoContent(400)
	}

	// check if the requested peer is a neighbour of the user
	if peerRole == "distributor" {
		if !strings.HasPrefix(strings.Split(key, ":")[0], strings.Split(peerName, "-")[0]+"-") {
			return ctx.NoContent(403)
		}
	}

	err = peersDB.ResetPeerUsage(key)
	if err != nil {
		return ctx.String(500, err.Error())
	}

	return ctx.NoContent(200)
}

// put method is used to reset the usage of a group
func PutGroups(ctx echo.Context) error {
	// var peer Peer
	// err := peersCollection.FindOne(context.TODO(), bson.M{"allowedIPs": ctx.Get("peerIP").(string) + "/32"}).Decode(&peer)
	// if err != nil {
	// 	return ctx.String(500, err.Error())
	// }

	// if peer.Role == "user" {
	// 	return ctx.NoContent(403)
	// }

	// // parse id
	// objectID, err := primitive.ObjectIDFromHex(ctx.Param("id"))
	// if err != nil {
	// 	return ctx.NoContent(400)
	// }

	// // check if group exists
	// var group Group
	// err = groupsCollection.FindOne(context.TODO(), bson.M{"_id": objectID}).Decode(&group)
	// if err != nil {
	// 	return ctx.NoContent(404)
	// }

	// // check read rights
	// if peer.Role != "admin" && group.OwnerID != peer.ID {
	// 	if group.OwnerID != peer.ID {
	// 		return ctx.NoContent(403)
	// 	}
	// }

	// _, err = groupsCollection.UpdateByID(context.TODO(), group.ID, bson.M{"$set": bson.M{
	// 	"totalTX": int64(0), "totalRX": int64(0),
	// }})
	// if err != nil {
	// 	logger.Error(err.Error(), slog.String("peer", peer.Name))
	// 	return ctx.String(500, err.Error())
	// }

	// for _, peerID := range group.PeerIDs {
	// 	_, err = peersCollection.UpdateByID(context.TODO(), peerID, bson.M{"$set": bson.M{
	// 		"totalTX": int64(0), "totalRX": int64(0), "allowedUsage": group.AllowedUsage,
	// 	}})
	// 	if err != nil {
	// 		logger.Error(err.Error(), slog.String("peer", peer.Name))
	// 		return ctx.String(500, err.Error())
	// 	}
	// }

	// return ctx.NoContent(200)

	return nil
}

func PutPeerToGroup(ctx echo.Context) error {
	// var peer Peer
	// err := peersCollection.FindOne(context.TODO(), bson.M{"allowedIPs": ctx.Get("peerIP").(string) + "/32"}).Decode(&peer)
	// if err != nil {
	// 	return ctx.String(500, err.Error())
	// }

	// // parse group id
	// groupObjectID, err := primitive.ObjectIDFromHex(ctx.Param("groupID"))
	// if err != nil {
	// 	return ctx.NoContent(400)
	// }

	// // parse target peer id
	// peerID, err := url.QueryUnescape(ctx.Param("peerID"))
	// if err != nil {
	// 	return ctx.NoContent(400)
	// }

	// // check if group exists
	// var group Group
	// err = groupsCollection.FindOne(context.TODO(), bson.M{"_id": groupObjectID}).Decode(&group)
	// if err != nil {
	// 	return ctx.NoContent(404)
	// }

	// // check read rights
	// if peer.Role != "admin" {
	// 	if group.OwnerID != peer.ID {
	// 		return ctx.NoContent(403)
	// 	}
	// }

	// // add peer to group
	// _, err = groupsCollection.UpdateByID(context.TODO(), groupObjectID, bson.M{"$push": bson.M{"peerIDs": peerID}})
	// if err != nil {
	// 	return ctx.String(500, err.Error())
	// }

	// // add group id to peer
	// _, err = peersCollection.UpdateByID(context.TODO(), peerID, bson.M{"$set": bson.M{"groupID": groupObjectID, "totalTX": int64(0), "totalRX": int64(0), "allowedUsage": group.AllowedUsage, "expiresAt": group.ExpiresAt}})
	// if err != nil {
	// 	return ctx.String(500, err.Error())
	// }

	// // return peer
	// return ctx.NoContent(200)

	return nil
}

func GetConfig(ctx echo.Context) error {
	device, err := wgc.Device(config.InterfaceName)

	if err != nil {
		return ctx.String(500, err.Error())
	}

	return ctx.JSON(200, map[string]interface{}{"serverPublicKey": device.PublicKey.String(), "serverAddress": fmt.Sprintf("%s:%d", config.PublicAddress, device.ListenPort), "endpoints": config.Endpoints, "telegramBotID": config.TelegramBotID})
}

func GetMe(ctx echo.Context) error {
	peerRole := ctx.Get("peerRole").(string)
	peerName := ctx.Get("peerName").(string)

	if peerRole == "distributor" {
		return ctx.JSON(200, map[string]interface{}{"role": peerRole, "prefix": strings.Split(peerName, "-")[0]})
	}

	return ctx.JSON(200, map[string]interface{}{"role": peerRole, "prefix": ""})
}
