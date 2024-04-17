package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo"
	el "github.com/labstack/gommon/log"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

type ServerSpecificInfo struct {
	Address           string `json:"address" bson:"address"`
	LastHandshakeTime string `json:"lastHandshakeTime" bson:"lastHandshakeTime"`
	Endpoint          string `json:"endpoint" bson:"endpoint"`
	CurrentTX         int64  `json:"currentTX" bson:"currentTX"`
	CurrentRX         int64  `json:"currentRX" bson:"currentRX"`
}

type Config struct {
	MongoURI             string   `json:"mongoURI"`
	DBName               string   `json:"dbName"`
	CollectionName       string   `json:"collectionName"`
	InterfaceName        string   `json:"interfaceName"`
	InterfaceAddress     string   `json:"interfaceAddress"`
	InterfaceAddressCIDR string   `json:"interfaceAddressCIDR"`
	PublicAddress        string   `json:"publicAddress"`
	Endpoints            []string `json:"endpoints"`
}

type Peers struct {
	peers map[string]*Peer
	mu    sync.RWMutex
}

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

type Log struct {
	Time          int64  `json:"time" bson:"time"`
	Level         string `json:"level" bson:"level"`
	Message       string `json:"msg" bson:"msg"`
	Peer          string `json:"peer" bson:"peer"`
	PublicAddress string `json:"publicAddress" bson:"publicAddress"`
}

type customWriter struct {
	w          io.Writer
	collection *mongo.Collection
}

func (e customWriter) Write(p []byte) (int, error) {
	go func() {
		var l Log
		err := json.Unmarshal(p, &l)
		if err != nil {
			fmt.Println(err)
			return
		}
		_, err = e.collection.InsertOne(context.TODO(), l)
		if err != nil {
			fmt.Println(err)
		}
	}()
	return fmt.Println(string(p))
}

var peers Peers
var config Config                // used to store app configuration
var wgc *wgctrl.Client           // used to interact with wireguard interfaces
var device *wgtypes.Device       // actual wireguard interface
var collection *mongo.Collection // peers collection on database
var ioWriter customWriter        // io writer that writes to database and stdout
var logger *slog.Logger          // custom logger that writes logs to database and stdout
var deviceCIDR *net.IPNet        // used to check if requests or from device peers

func init() {
	// init local map
	peers.peers = make(map[string]*Peer)

	// load config file
	configPath := "config.json"
	if len(os.Args) > 1 {
		configPath = os.Args[1] + configPath
	}
	bytes, err := os.ReadFile(configPath)
	if err != nil {
		panic(err)
	}
	err = json.Unmarshal(bytes, &config)
	if err != nil {
		panic(err)
	}
	log.Println("Loaded config from " + configPath)

	// parse device cidr
	_, deviceCIDR, err = net.ParseCIDR(config.InterfaceAddressCIDR)
	if err != nil {
		panic(err)
	}

	// create wireguard client
	wgc, err = wgctrl.New()
	if err != nil {
		panic(err)
	}

	// get the wireguard device(interface)
	device, err = wgc.Device(config.InterfaceName)
	if err != nil {
		panic(err)
	}

	// connect to database
	mongoClient, err := mongo.Connect(context.TODO(), options.Client().ApplyURI(config.MongoURI).SetServerAPIOptions(options.ServerAPI(options.ServerAPIVersion1)))
	if err != nil {
		panic(err)
	}
	log.Println("Connected to database")

	// load mongodb peers collectoin
	collection = mongoClient.Database(config.DBName).Collection(config.CollectionName)

	// create unique index for allowedIPs
	_, err = collection.Indexes().CreateOne(context.Background(), mongo.IndexModel{Keys: bson.M{"allowedIPs": 1}, Options: options.Index().SetUnique(true)})
	if err != nil {
		panic(err)
	}

	// create unique index for names
	_, err = collection.Indexes().CreateOne(context.Background(), mongo.IndexModel{Keys: bson.M{"name": 1}, Options: options.Index().SetUnique(true)})
	if err != nil {
		panic(err)
	}

	// setup logger
	ioWriter = customWriter{w: os.Stdout, collection: mongoClient.Database(config.DBName).Collection("logs")}
	logger = slog.New(slog.NewJSONHandler(ioWriter, &slog.HandlerOptions{AddSource: true, ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
		if a.Key == "time" {
			return slog.Int64("time", time.Now().UnixMilli())
		} else {
			return a
		}
	}})).With(slog.String("publicAddress", config.PublicAddress))

	// get peers from db
	var tempPeers []*Peer
	cursor, err := collection.Find(context.TODO(), bson.D{})
	if err != nil {
		panic(err)
	}
	if err = cursor.All(context.TODO(), &tempPeers); err != nil {
		panic(err)
	}
	for _, p := range tempPeers {
		peers.peers[p.PublicKey] = p
	}

	log.Println("Checking for conflicts...")

	// check if any peer exists
	if len(tempPeers) == 0 {
		var data *Peer = &Peer{
			Name:         "Admin-0",
			AllowedUsage: 1024000000000,
			ExpiresAt:    time.Now().Add(time.Hour * 24 * 365).UnixMilli(),
			Role:         "admin",
		}

		// create private and public keys
		privateKey, err := wgtypes.GeneratePrivateKey()
		if err != nil {
			logger.Error(err.Error(), slog.String("peer", data.Name))
			panic(err)
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
			panic(err)
		}
		ip.Increment()

		// update device
		device, err = wgc.Device(config.InterfaceName)
		if err != nil {
			logger.Error(err.Error(), slog.String("peer", data.Name))
			panic(err)
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
			panic(err)
		}
		data.AllowedIPs = ip.ToString() + "/32"

		var udpAddress *net.UDPAddr = nil

		data.ServerSpecificInfo = []*ServerSpecificInfo{{Address: config.PublicAddress}}

		// add peer to database
		_, err = collection.InsertOne(context.TODO(), data)
		if err != nil {
			panic(err)
		}

		// add peer to device
		err = wgc.ConfigureDevice(config.InterfaceName, wgtypes.Config{Peers: []wgtypes.PeerConfig{{
			PublicKey:  publicKey,
			AllowedIPs: []net.IPNet{*allowedIPs},
			Endpoint:   udpAddress,
		}}})
		if err != nil {
			logger.Error(err.Error(), slog.String("peer", data.Name))
			panic(err)
		}

		// add peer to local map
		peers.peers[data.PublicKey] = data

		// add peer to list of recieved peers from database to be added to device
		tempPeers = append(tempPeers, data)

		// save config file
		err = os.WriteFile(os.Args[1]+"Admin-0.conf", []byte(fmt.Sprintf("[Interface]\nPrivateKey=%s\nAddress=%s\nDNS=1.1.1.1,8.8.8.8\n[Peer]\nPublicKey=%s\nAllowedIPs=0.0.0.0/0\nEndpoint=%s:%d\n", data.PrivateKey, data.AllowedIPs, device.PublicKey.String(), config.PublicAddress, device.ListenPort)), 0666)
		if err != nil {
			logger.Error(err.Error(), slog.String("peer", data.Name))
			panic(err)
		}
		logger.Info("Saved config to "+os.Args[1]+"Admin-0.conf", slog.String("peer", "Admin-0"))
	}

	// store peers that are missing from device
	var missingPeersOnDevice []wgtypes.PeerConfig

	// ssi udpates
	var updates []mongo.WriteModel

	// add peers from database to device
	for _, pdb := range peers.peers {
		log.Printf("%s from database will be created on %s", pdb.Name, device.Name)
		decodedPublicKey, err := base64.StdEncoding.DecodeString(pdb.PrivateKey)
		if err != nil {
			panic(err)
		}
		pk, err := wgtypes.NewKey([]byte(decodedPublicKey))
		if err != nil {
			panic(err)
		}
		_, ipNet, err := net.ParseCIDR(pdb.AllowedIPs)
		if err != nil {
			panic(err)
		}

		if pdb.Disabled {
			presharedKey, err := wgtypes.GenerateKey()
			if err != nil {
				panic(err)
			}
			missingPeersOnDevice = append(missingPeersOnDevice, wgtypes.PeerConfig{
				PublicKey:    pk.PublicKey(),
				AllowedIPs:   []net.IPNet{*ipNet},
				PresharedKey: &presharedKey,
			})
		} else {
			missingPeersOnDevice = append(missingPeersOnDevice, wgtypes.PeerConfig{
				PublicKey:    pk.PublicKey(),
				AllowedIPs:   []net.IPNet{*ipNet},
				PresharedKey: &wgtypes.Key{},
			})
		}

		// check if this server has server specific entry on database
		ssi := pdb.FindSSIByAddress(config.PublicAddress)
		if ssi == nil {
			// add server specific info entry to database
			update := mongo.NewUpdateOneModel()
			update.SetFilter(bson.M{"_id": pdb.ID})
			update.SetUpdate(bson.M{

				"$push": bson.M{"serverSpecificInfo": ServerSpecificInfo{Address: config.PublicAddress}},
			})
			updates = append(updates, update)
		}
	}

	// write updates to database
	if len(updates) > 0 {
		if _, err := collection.BulkWrite(context.TODO(), updates, &options.BulkWriteOptions{}); err != nil {
			logger.Error(err.Error())
			panic(err)
		}
	}

	// create missing peers
	err = wgc.ConfigureDevice(device.Name, wgtypes.Config{Peers: missingPeersOnDevice, ReplacePeers: true})
	if err != nil {
		logger.Error(err.Error())
		panic(err)
	}
}

func main() {
	// update loop
	go func() {
		var e error
		var startTime time.Time
		var publicKey string
		var updates []mongo.WriteModel
		var peer *Peer
		var presharedKey wgtypes.Key
		var p wgtypes.Peer
		var ok bool
		for {
			// set starting time of this iteration
			startTime = time.Now()

			// update device
			device, e = wgc.Device(config.InterfaceName)
			if e != nil {
				logger.Error(e.Error())
				continue
			}

			// update peers' info
			for _, p = range device.Peers {
				// get peer public key
				publicKey = p.PublicKey.String()

				// check if peer exists in map
				peer, ok = peers.peers[publicKey]
				if !ok {
					continue
				}

				// check to see if peer should be disabled
				if startTime.UnixMilli() > peer.ExpiresAt || peer.TotalRX+peer.TotalTX > peer.AllowedUsage {
					if !peer.Disabled {

						// create preshared key to invalidate peer
						presharedKey, e = wgtypes.GenerateKey()
						if e != nil {
							logger.Error(e.Error(), slog.String("peer", peer.Name))
							continue
						}

						// set peer's preshared key
						e = wgc.ConfigureDevice(config.InterfaceName, wgtypes.Config{
							Peers: []wgtypes.PeerConfig{
								{
									PublicKey:    p.PublicKey,
									UpdateOnly:   true,
									PresharedKey: &presharedKey,
								},
							},
						})
						if e != nil {
							logger.Error(e.Error(), slog.String("peer", peer.Name))
							continue
						}

						// update peer on database
						updates = append(updates, mongo.NewUpdateOneModel().SetFilter(bson.M{"_id": publicKey}).SetUpdate(bson.M{"$set": bson.M{"disabled": true}}))

						// disable peer in local map
						peers.mu.Lock()
						peer.Disabled = true
						peers.mu.Unlock()

						logger.Info("Peer Disabled", slog.String("peer", peer.Name))
						continue
					} else {
						continue
					}
				}

				// check to see if peer should be enabled
				if (startTime.UnixMilli() < peer.ExpiresAt && peer.TotalRX+peer.TotalTX < peer.AllowedUsage) && peer.Disabled {
					// remove peer's preshared key to enable it
					e = wgc.ConfigureDevice(config.InterfaceName, wgtypes.Config{
						Peers: []wgtypes.PeerConfig{
							{
								PublicKey:    p.PublicKey,
								UpdateOnly:   true,
								PresharedKey: &wgtypes.Key{},
							},
						},
					})
					if e != nil {
						logger.Error(e.Error())
						continue
					}

					// update peer on database
					updates = append(updates, mongo.NewUpdateOneModel().SetFilter(bson.M{"_id": publicKey}).SetUpdate(bson.M{"$set": bson.M{"disabled": false}}))

					// update peer on local map
					peers.mu.Lock()
					peer.Disabled = false
					peers.mu.Unlock()

					logger.Info("Peer Enabled", slog.String("peer", peer.Name))
					continue
				}

				peers.mu.Lock()

				// calculate and update current tx and rx
				peer.CurrentTX = p.TransmitBytes - peer.TempTX
				peer.CurrentRX = p.ReceiveBytes - peer.TempRX
				peer.TempTX = p.TransmitBytes
				peer.TempRX = p.ReceiveBytes

				// update  current endpoint
				peer.Endpoint = p.Endpoint.String()

				// calculate last handshake time
				if !p.LastHandshakeTime.IsZero() {
					peer.LastHandshakeTime = p.LastHandshakeTime.Sub(startTime).Abs().Round(time.Second).String()
				}

				peers.mu.Unlock()

				// check if current server has ssi entry in local map
				ssiIndex := slices.IndexFunc(peer.ServerSpecificInfo, func(ssi *ServerSpecificInfo) bool { return ssi.Address == config.PublicAddress })

				// create ssi
				ssi := ServerSpecificInfo{
					Address:           config.PublicAddress,
					LastHandshakeTime: peer.LastHandshakeTime,
					Endpoint:          peer.Endpoint,
					CurrentTX:         peer.CurrentTX,
					CurrentRX:         peer.CurrentRX,
				}

				peers.mu.Lock()

				if ssiIndex != -1 {
					// update ssi in local map
					peer.ServerSpecificInfo[ssiIndex] = &ssi
				} else {
					// add ssi to local map
					peer.ServerSpecificInfo = append(peer.ServerSpecificInfo, &ssi)
				}

				peers.mu.Unlock()

				// update ssi on database
				updates = append(updates, mongo.NewUpdateOneModel().SetFilter(bson.M{"_id": publicKey, "serverSpecificInfo.address": config.PublicAddress}).SetUpdate(
					bson.M{"$set": bson.M{"serverSpecificInfo.$": ssi}},
				))

				// update total tx and rx on database
				updates = append(updates, mongo.NewUpdateOneModel().SetFilter(bson.M{"_id": publicKey}).SetUpdate(
					bson.M{"$inc": bson.M{"totalTX": peer.CurrentTX, "totalRX": peer.CurrentRX}},
				))
			}

			// update database
			if len(updates) > 0 {
				_, err := collection.BulkWrite(context.TODO(), updates, &options.BulkWriteOptions{})
				if err != nil {
					logger.Error(err.Error())
				}
			}

			// empty updates slice
			updates = nil

			// sleep if a second has not passed
			time.Sleep(time.Duration(1000-(time.Now().UnixMilli()-startTime.UnixMilli())) * time.Millisecond)
		}
	}()

	// listen for delete events from database
	go func() {
		// create change stream
		changeStream, e := collection.Watch(context.TODO(), mongo.Pipeline{bson.D{{Key: "$match", Value: bson.D{{Key: "operationType", Value: "delete"}}}}})
		if e != nil {
			logger.Error(e.Error())
			panic(e)
		}
		defer changeStream.Close(context.TODO())

		var p *Peer
		var ok bool
		var publicKey wgtypes.Key

		// loop over changes
		for changeStream.Next(context.TODO()) {
			// parse change
			var data struct {
				DocumentKey struct {
					ID string `bson:"_id"`
				} `bson:"documentKey"`
			}
			if e = changeStream.Decode(&data); e != nil {
				logger.Error(e.Error())
				continue
			}

			// check if peer exists
			p, ok = peers.peers[data.DocumentKey.ID]
			if !ok {
				continue
			}

			// parse peer public key
			publicKey, e = wgtypes.ParseKey(p.PublicKey)
			if e != nil {
				logger.Error(e.Error(), slog.String("peer", p.Name))
				continue
			}

			// remove peer from device
			e = wgc.ConfigureDevice(config.InterfaceName, wgtypes.Config{Peers: []wgtypes.PeerConfig{{PublicKey: publicKey, Remove: true}}})
			if e != nil {
				logger.Error(e.Error(), slog.String("peer", p.Name))
				continue
			}

			// delete peer from local map
			peers.mu.Lock()
			delete(peers.peers, p.PublicKey)
			peers.mu.Unlock()

			logger.Info("Peer removed", slog.String("peer", p.Name))
		}
	}()

	// listen for insert events from database
	go func() {
		// create change stream
		changeStream, e := collection.Watch(context.TODO(), mongo.Pipeline{bson.D{{Key: "$match", Value: bson.D{{Key: "operationType", Value: "insert"}}}}})
		if e != nil {
			logger.Error(e.Error())
			panic(e)
		}
		defer changeStream.Close(context.TODO())

		var publicKey wgtypes.Key
		var allowedIPs *net.IPNet

		// loop over changes
		for changeStream.Next(context.TODO()) {
			// parse change
			var data struct {
				FullDocument Peer `bson:"fullDocument"`
			}
			if e = changeStream.Decode(&data); e != nil {
				logger.Error(e.Error())
				continue
			}

			// check if peer already exists
			_, ok := peers.peers[data.FullDocument.ID]
			if ok {
				continue
			}

			// parse public key
			publicKey, e = wgtypes.ParseKey(data.FullDocument.PublicKey)
			if e != nil {
				logger.Error(e.Error(), slog.String("peer", data.FullDocument.Name))
				continue
			}

			// parse allowedIPs
			_, allowedIPs, e = net.ParseCIDR(data.FullDocument.AllowedIPs)
			if e != nil {
				logger.Error(e.Error(), slog.String("peer", data.FullDocument.Name))
				continue
			}

			// add peer to device
			e = wgc.ConfigureDevice(config.InterfaceName, wgtypes.Config{Peers: []wgtypes.PeerConfig{{PublicKey: publicKey, AllowedIPs: []net.IPNet{*allowedIPs}}}})
			if e != nil {
				logger.Error(e.Error(), slog.String("peer", data.FullDocument.Name))
				continue
			}

			// add peer to local map
			peers.mu.Lock()
			peers.peers[data.FullDocument.PublicKey] = &data.FullDocument
			peers.mu.Unlock()

			logger.Info("Peer created", slog.String("peer", data.FullDocument.Name))

			// add server specific info entry to database
			_, e = collection.UpdateByID(context.TODO(), data.FullDocument.ID, bson.M{"$push": bson.M{"serverSpecificInfo": ServerSpecificInfo{Address: config.PublicAddress}}})
			if e != nil {
				logger.Error(e.Error(), slog.String("peer", data.FullDocument.Name))
				continue
			}
		}
	}()

	// listen for update events from database
	go func() {
		changeStream, e := collection.Watch(context.TODO(), mongo.Pipeline{bson.D{{Key: "$match", Value: bson.D{{Key: "operationType", Value: "update"}}}}})
		if e != nil {
			logger.Error(e.Error())
			panic(e)
		}
		var data *struct {
			DocumentKey struct {
				ID string `bson:"_id"`
			} `bson:"documentKey"`
			UpdateDescription struct {
				UpdatedFields map[string]interface{} `bson:"updatedFields"`
			} `bson:"updateDescription"`
		}
		defer changeStream.Close(context.TODO())

		var ok bool
		var ssi *ServerSpecificInfo
		var p *Peer
		var m map[string]interface{}

		for changeStream.Next(context.TODO()) {
			data = nil

			// parse update
			if e = changeStream.Decode(&data); e != nil {
				logger.Error(e.Error())
				continue
			}

			p, ok = peers.peers[data.DocumentKey.ID]
			if !ok {
				logger.Error("Recieved update for a peer that does not exist in local map", slog.String("peer", data.DocumentKey.ID))
				continue
			}

			// check all the updated fields
			for k, v := range data.UpdateDescription.UpdatedFields {
				if k == "totalTX" {
					peers.mu.Lock()
					p.TotalTX = v.(int64)
					peers.mu.Unlock()
				} else if k == "totalRX" {
					peers.mu.Lock()
					p.TotalRX = v.(int64)
					peers.mu.Unlock()
				} else if k == "allowedUsage" {
					peers.mu.Lock()
					p.AllowedUsage = v.(int64)
					peers.mu.Unlock()
				} else if k == "expiresAt" {
					peers.mu.Lock()
					p.ExpiresAt = v.(int64)
					peers.mu.Unlock()
				} else if k == "disabled" {
					// do nothing
				} else if k == "name" {
					peers.mu.Lock()
					p.Name = v.(string)
					peers.mu.Unlock()
				} else if k == "role" {
					peers.mu.Lock()
					p.Role = v.(string)
					peers.mu.Unlock()
				} else if k == "preferredEndpoint" {
					// parse peer public key
					pk, e := wgtypes.ParseKey(p.PublicKey)
					if e != nil {
						logger.Error(e.Error(), slog.String("peer", p.Name))
						continue
					}

					p.PreferredEndpoint = v.(string)
					if v.(string) == "" {
						e = wgc.ConfigureDevice(config.InterfaceName, wgtypes.Config{Peers: []wgtypes.PeerConfig{{PublicKey: pk, Endpoint: nil, UpdateOnly: true}}})
						if e != nil {
							logger.Error(e.Error(), slog.String("peer", p.Name))
							continue
						}
					} else {
						udpAddress, e := net.ResolveUDPAddr("udp4", v.(string))
						if e != nil {
							logger.Error(e.Error(), slog.String("peer", p.Name))
							continue
						}
						e = wgc.ConfigureDevice(config.InterfaceName, wgtypes.Config{Peers: []wgtypes.PeerConfig{{PublicKey: pk, Endpoint: udpAddress, UpdateOnly: true}}})
						if e != nil {
							logger.Error(e.Error(), slog.String("peer", p.Name))
							continue
						}
					}
					peers.mu.Lock()
					p.PreferredEndpoint = v.(string)
					peers.mu.Unlock()
				} else if m, ok = v.(map[string]interface{}); ok {
					if _, ok = m["address"]; ok && m["address"].(string) != config.PublicAddress {
						ssi = p.FindSSIByAddress(m["address"].(string))
						if ssi == nil {
							peers.mu.Lock()
							p.ServerSpecificInfo = append(p.ServerSpecificInfo, &ServerSpecificInfo{
								Address:           m["address"].(string),
								Endpoint:          m["endpoint"].(string),
								LastHandshakeTime: m["lastHandshakeTime"].(string),
								CurrentTX:         m["currentTX"].(int64),
								CurrentRX:         m["currentRX"].(int64),
							})
							peers.mu.Unlock()
						} else {
							peers.mu.Lock()
							ssi.Address = m["address"].(string)
							ssi.Endpoint = m["endpoint"].(string)
							ssi.LastHandshakeTime = m["lastHandshakeTime"].(string)
							ssi.CurrentTX = m["currentTX"].(int64)
							ssi.CurrentRX = m["currentRX"].(int64)
							peers.mu.Unlock()
						}
					}
				}
			}
		}
	}()

	// create echo instance
	e := echo.New()

	// set logs to error only
	e.Logger.SetLevel(el.ERROR)

	// handle static files
	e.Static("/", os.Args[1]+"public/build")

	// check if request is from a peer
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(ctx echo.Context) error {
			if !deviceCIDR.Contains(net.ParseIP(strings.Split(ctx.Request().RemoteAddr, ":")[0])) {
				logger.Warn("Unauthorized request from " + ctx.Request().RemoteAddr)
				return ctx.NoContent(403)
			}
			return next(ctx)
		}
	})

	// check if peer is authenticated
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(ctx echo.Context) error {
			if !strings.Contains(ctx.Request().URL.Path, "/api/") || ctx.Request().URL.Path == "/api/auth" {
				return next(ctx)
			}
			cookie, err := ctx.Cookie("id")
			if err == nil {
				if p, ok := peers.peers[cookie.Value]; ok {
					ctx.Set("role", p.Role)
					ctx.Set("name", p.Name)
					ctx.Set("id", p.ID)
					ctx.Set("group", strings.Split(p.Name, "-")[0]+"-")
					return next(ctx)
				}
			}
			logger.Warn("Unauthenticated request from " + ctx.Request().RemoteAddr)
			return ctx.NoContent(401)
		}
	})

	e.GET("/api/auth", func(ctx echo.Context) error {
		peers.mu.RLock()
		defer peers.mu.RUnlock()
		for _, p := range peers.peers {
			if p.AllowedIPs == strings.Split(ctx.Request().RemoteAddr, ":")[0]+"/32" {
				cookie := new(http.Cookie)
				cookie.Name = "id"
				cookie.Value = p.ID
				cookie.Expires = time.Now().Add(24 * time.Hour * 24)
				cookie.Secure = true
				cookie.HttpOnly = true
				ctx.SetCookie(cookie)
				return ctx.NoContent(200)
			}
		}
		return ctx.NoContent(403)
	})
	e.GET("/api/peers", func(ctx echo.Context) error {
		role := ctx.Get("role").(string)
		group := ctx.Get("group").(string)

		if role == "admin" {
			// return all peers
			return ctx.JSON(200, map[string]interface{}{"role": role, "peers": peers.peers})
		} else {
			// return only group's peers if user is not admin
			var data []*Peer
			peers.mu.RLock()
			defer peers.mu.RUnlock()
			for _, p := range peers.peers {
				if strings.HasPrefix(p.Name, group) {
					data = append(data, p)
				}
			}
			return ctx.JSON(200, map[string]interface{}{"role": role, "peers": data})
		}
	})
	e.GET("/api/peers/:id", func(ctx echo.Context) error {
		role := ctx.Get("role").(string)
		group := ctx.Get("group").(string)

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
		if role != "admin" {
			if !strings.HasPrefix(p.Name, group) {
				return ctx.NoContent(403)
			}
		}

		// return peer
		return ctx.JSON(200, p)
	})
	e.POST("/api/peers", func(ctx echo.Context) error {
		role := ctx.Get("role").(string)
		group := ctx.Get("group").(string)

		if role == "user" {
			return ctx.NoContent(403)
		}

		// get peer info from request body
		var data Peer
		err := json.NewDecoder(ctx.Request().Body).Decode(&data)
		if err != nil {
			return ctx.String(400, err.Error())
		}

		// check if the requested peer is in the same group as the user
		if role == "distributor" {
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
	})
	e.DELETE("/api/peers/:id", func(ctx echo.Context) error {
		role := ctx.Get("role").(string)
		group := ctx.Get("group").(string)

		if role == "user" {
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
		if role == "distributor" {
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
	})
	e.PATCH("/api/peers/:id", func(ctx echo.Context) error {
		role := ctx.Get("role").(string)
		group := ctx.Get("group").(string)

		if role == "user" {
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
		if role == "distributor" {
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
	})
	e.PUT("/api/peers/:id", func(ctx echo.Context) error {
		role := ctx.Get("role").(string)
		group := ctx.Get("group").(string)

		if role == "user" {
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
		if role == "distributor" {
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
	})
	e.GET("/api/config", func(ctx echo.Context) error {
		return ctx.JSON(200, map[string]interface{}{"serverPublicKey": device.PublicKey.String(), "serverAddress": fmt.Sprintf("%s:%d", config.PublicAddress, device.ListenPort), "endpoints": config.Endpoints})
	})
	e.GET("/api/me", func(ctx echo.Context) error {
		role := ctx.Get("role").(string)
		group := ctx.Get("group").(string)
		if role == "distributor" {
			return ctx.JSON(200, map[string]interface{}{"role": role, "prefix": group})
		}
		return ctx.JSON(200, map[string]interface{}{"role": role, "prefix": ""})
	})
	e.GET("/api/logs", func(ctx echo.Context) error {
		role := ctx.Get("role").(string)

		if role != "admin" {
			return ctx.NoContent(403)
		}

		var logs []Log
		cursor, err := ioWriter.collection.Find(context.Background(), bson.M{})
		if err != nil {
			logger.Error(err.Error())
			return ctx.String(500, err.Error())
		}
		if err = cursor.All(context.TODO(), &logs); err != nil {
			logger.Error(err.Error())
			return ctx.String(500, err.Error())
		}

		return ctx.JSON(200, logs)
	})

	if len(os.Args) > 1 {
		e.Logger.Fatal(e.StartTLS("0.0.0.0:443", os.Args[1]+"certs/server.pem", os.Args[1]+"certs/server.key"))
	} else {
		e.Logger.Fatal(e.StartTLS("0.0.0.0:443", "", ""))
	}
}
