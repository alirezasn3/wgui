package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

type Config struct {
	MongoURI             string   `json:"mongoURI"`
	DBName               string   `json:"dbName"`
	CollectionName       string   `json:"collectionName"`
	InterfaceName        string   `json:"interfaceName"`
	InterfaceAddress     string   `json:"interfaceAddress"`
	InterfaceAddressCIDR string   `json:"interfaceAddressCIDR"`
	PublicAddress        string   `json:"publicAddress"`
	Endpoints            []string `json:"endpoints"`
	TelegramBotID        string   `json:"telegramBotID"`
}

type Peers struct {
	peers map[string]*Peer
	mu    sync.RWMutex
}

var peers Peers                  // used to intract with peers concurrently
var config Config                // used to store app configuration
var wgc *wgctrl.Client           // used to interact with wireguard interfaces
var device *wgtypes.Device       // actual wireguard interface
var collection *mongo.Collection // peers collection on database
var ioWriter CustomWriter        // io writer that writes to database and stdout
var logger *slog.Logger          // custom logger that writes logs to database and stdout
var deviceCIDR *net.IPNet        // used to check if client is in device subnet
var mongoClient *mongo.Client
var path string

func init() {
	// init local map
	peers.peers = make(map[string]*Peer)

	execPath, err := os.Executable()
	if err != nil {
		panic(err)
	}
	path = filepath.Dir(execPath)

	// load config file
	bytes, err := os.ReadFile(filepath.Join(path, "config.json"))
	if err != nil {
		panic(err)
	}
	err = json.Unmarshal(bytes, &config)
	if err != nil {
		panic(err)
	}
	log.Println("Loaded config from " + filepath.Join(path, "config.json"))

	// check for arguments
	if slices.Contains(os.Args, "reset-ssis") {
		// connect to database
		mongoClient, err = mongo.Connect(context.TODO(), options.Client().ApplyURI(config.MongoURI).SetServerAPIOptions(options.ServerAPI(options.ServerAPIVersion1)))
		if err != nil {
			panic(err)
		}
		log.Println("Connected to database")

		// load mongodb peers collectoin
		collection = mongoClient.Database(config.DBName).Collection(config.CollectionName)

		_, err = collection.UpdateMany(context.Background(), bson.M{}, bson.M{"$set": bson.M{"serverSpecificInfo": []ServerSpecificInfo{}}})
		if err != nil {
			panic(err)
		}
		log.Println("Server specific info entries reset")
		os.Exit(0)
	}

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
	mongoClient, err = mongo.Connect(context.TODO(), options.Client().ApplyURI(config.MongoURI).SetServerAPIOptions(options.ServerAPI(options.ServerAPIVersion1)))
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

	// create ttl index for logs
	indexModel := mongo.IndexModel{
		Keys:    bson.D{{Key: "expireAt", Value: 1}},
		Options: options.Index().SetExpireAfterSeconds(0),
	}
	_, err = mongoClient.Database(config.DBName).Collection("logs").Indexes().CreateOne(context.TODO(), indexModel)
	if err != nil {
		panic(err)
	}

	// setup logger
	ioWriter = CustomWriter{W: os.Stdout, Collection: mongoClient.Database(config.DBName).Collection("logs")}
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
		err = os.WriteFile(filepath.Join(path, "Admin-0.conf"), []byte(fmt.Sprintf("[Interface]\nPrivateKey=%s\nAddress=%s\nDNS=1.1.1.1,8.8.8.8\n[Peer]\nPublicKey=%s\nAllowedIPs=0.0.0.0/0\nEndpoint=%s:%d\n", data.PrivateKey, data.AllowedIPs, device.PublicKey.String(), config.PublicAddress, device.ListenPort)), 0666)
		if err != nil {
			logger.Error(err.Error(), slog.String("peer", data.Name))
			panic(err)
		}
		logger.Info("Saved config to "+filepath.Join(path, "Admin-0.conf"), slog.String("peer", "Admin-0"))
	}

	// store new peer configurations
	var newPeerConfigurations []wgtypes.PeerConfig

	// update device
	device, err = wgc.Device(config.InterfaceName)
	if err != nil {
		logger.Error(err.Error())
		panic(err)
	}

	// ssi udpates
	var updates []mongo.WriteModel

	// add peers from database to device
	for _, pdb := range peers.peers {
		log.Printf("%s from database will be created on %s", pdb.Name, device.Name)

		decodedPrivateKey, err := base64.StdEncoding.DecodeString(pdb.PrivateKey)
		if err != nil {
			panic(err)
		}
		privateKey, err := wgtypes.NewKey([]byte(decodedPrivateKey))
		if err != nil {
			panic(err)
		}
		_, ipNet, err := net.ParseCIDR(pdb.AllowedIPs)
		if err != nil {
			panic(err)
		}

		presharedKey := wgtypes.Key{}
		if pdb.Disabled {
			presharedKey, err = wgtypes.GenerateKey()
			if err != nil {
				panic(err)
			}
		}

		newPeerConfigurations = append(newPeerConfigurations, wgtypes.PeerConfig{
			PublicKey:    privateKey.PublicKey(),
			AllowedIPs:   []net.IPNet{*ipNet},
			PresharedKey: &presharedKey,
		})

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

	// write ssi updates to database
	if len(updates) > 0 {
		if _, err := collection.BulkWrite(context.TODO(), updates, &options.BulkWriteOptions{}); err != nil {
			logger.Error(err.Error())
			panic(err)
		}
	}

	// create missing peers
	err = wgc.ConfigureDevice(device.Name, wgtypes.Config{Peers: newPeerConfigurations, ReplacePeers: true})
	if err != nil {
		logger.Error(err.Error())
		panic(err)
	}

	// log the start of application
	logger.Info("Server started")
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
					panic(err)
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
				panic(e)
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
				panic(e)
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
				panic(e)
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
				panic(e)
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
				panic(e)
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

		var ok bool
		var ssi *ServerSpecificInfo
		var p *Peer
		var m map[string]interface{}

		for changeStream.Next(context.Background()) {
			data = nil

			// parse update
			if e = changeStream.Decode(&data); e != nil {
				logger.Error(e.Error())
				panic(e)
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
							panic(e)
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
							panic(e)
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

	// handle static files
	e.Static("/", filepath.Join(path, "public", "build"))

	// add cors middleware
	e.Use(middleware.CORSWithConfig(middleware.CORSConfig{AllowOrigins: []string{"http://localhost:5173"}, AllowCredentials: true}))

	// check if request is from a peer
	e.Use(Auth)

	e.GET("/api/peers", GetPeers)
	e.GET("/api/peers/:id", GetPeer)
	e.POST("/api/peers", PostPeers)
	e.DELETE("/api/peers/:id", DeletePeers)
	e.PATCH("/api/peers/:id", PatchPeers)
	e.PUT("/api/peers/:id", PutPeers)
	e.GET("/api/config", GetConfig)
	e.GET("/api/me", GetMe)
	e.GET("/api/logs", GetLogs)

	e.Logger.Fatal(e.StartTLS("0.0.0.0:443", filepath.Join(path, "certs", "server.pem"), filepath.Join(path, "certs", "server.key")))
}
