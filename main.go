package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	sync "sync"
	"time"

	goSystemd "github.com/alirezasn3/go-systemd"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/redis/go-redis/v9"
	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

type Config struct {
	RedisURL             string   `json:"redisURL"`
	InterfaceName        string   `json:"interfaceName"`
	InterfaceAddress     string   `json:"interfaceAddress"`
	InterfaceAddressCIDR string   `json:"interfaceAddressCIDR"`
	PublicAddress        string   `json:"publicAddress"`
	Endpoints            []string `json:"endpoints"`
	TelegramBotID        string   `json:"telegramBotID"`
	BypassKey            string   `json:"bypassKey"`
}

type Peer struct {
	Role           string `json:"role" redis:"role"`
	Name           string `json:"name" redis:"name"`
	AllowedIPs     string `json:"allowedIPs" redis:"allowedIPs"`
	PublicKey      string `json:"publicKey" redis:"publicKey"`
	PrivateKey     string `json:"privateKey" redis:"privateKey"`
	Disabled       bool   `json:"disabled" redis:"disabled"`
	AllowedUsage   int64  `json:"allowedUsage" redis:"allowedUsage"`
	ExpiresAt      int64  `json:"expiresAt" redis:"expiresAt"`
	TotalTX        int64  `json:"totalTX" redis:"totalTX"`
	TotalRX        int64  `json:"totalRX" redis:"totalRX"`
	TelegramChatID int64  `json:"telegramChatID" redis:"telegramChatID"`
	GroupID        string `json:"groupID" redis:"groupID"`
}

// type Peers struct {
// 	peers map[string]*Peer
// 	mu    sync.RWMutex
// }

type Group struct {
	Name         string   `json:"Name" bson:"name"`
	PeerIDs      []string `json:"PeerIDs" bson:"peerIDs"`
	AllowedUsage int64    `json:"AllowedUsage" bson:"allowedUsage"`
	TotalTX      int64    `json:"TotalTX" bson:"totalTX"`
	TotalRX      int64    `json:"TotalRX" bson:"totalRX"`
	ExpiresAt    int64    `json:"ExpiresAt" bson:"expiresAt"`
	Disabled     bool     `json:"Disabled" bson:"disabled"`
}

type Groups struct {
	groups map[string]string
	mu     sync.RWMutex
}

type RedisWrapper struct {
	client *redis.Client
}

var groups Groups
var config Config          // used to store app configuration
var wgc *wgctrl.Client     // used to interact with wireguard interfaces
var deviceCIDR *net.IPNet  // used to check if client is in device subnet
var peersDB *RedisWrapper  // used to interact with reids database
var groupsDB *RedisWrapper // used to interact with reids database
var ssisDB *RedisWrapper   // used to interact with reids database
var execDir string         // used to store the directory of the executable

func CreateRedisClient(url string) (*RedisWrapper, error) {
	// create redis client
	options, err := redis.ParseURL(url)
	if err != nil {
		return nil, err
	}
	client := redis.NewClient(options)

	// check redis connection
	if client.Ping(context.Background()).Val() == "PONG" {
		return &RedisWrapper{client: client}, nil
	} else {
		return nil, errors.New("failed to connect to redis, did not receive PONG response")
	}
}

func must(returnValues ...interface{}) {
	if returnValues[len(returnValues)-1] != nil {
		panic(returnValues[len(returnValues)-1])
	}
}

func init() {
	groups.groups = make(map[string]string)

	// check for install and uninstall commands on linux
	if runtime.GOOS == "linux" {
		if slices.Contains(os.Args, "--install") {
			execPath, err := os.Executable()
			if err != nil {
				fmt.Println(err)
				os.Exit(1)
			}
			err = goSystemd.CreateService(&goSystemd.Service{Name: "wgui", ExecStart: execPath, Restart: "on-failure", RestartSec: "3s"})
			if err != nil {
				fmt.Println(err)
				os.Exit(1)
			} else {
				fmt.Println("wgui service created")
				os.Exit(0)
			}
		} else if slices.Contains(os.Args, "--uninstall") {
			err := goSystemd.DeleteService("wgui")
			if err != nil {
				fmt.Println(err)
				os.Exit(1)
			} else {
				fmt.Println("wgui service deleted")
				os.Exit(0)
			}
		}
	}

	// get executable directory
	execPath, err := os.Executable()
	must(err)
	execDir = filepath.Dir(execPath)

	// load config file
	bytes, err := os.ReadFile(filepath.Join(execDir, "config.json"))
	must(err)
	must(json.Unmarshal(bytes, &config))
	log.Println("Loaded config from " + filepath.Join(execDir, "config.json"))

	// check for arguments
	if slices.Contains(os.Args, "--reset-ssis") {
		// connect to database
		// mongoClient, err = mongo.Connect(context.TODO(), options.Client().ApplyURI(config.MongoURI).SetServerAPIOptions(options.ServerAPI(options.ServerAPIVersion1)))
		// if err != nil {
		// 	panic(err)
		// }
		// log.Println("Connected to database")

		// load mongodb peers collectoin
		// peersCollection = mongoClient.Database(config.DBName).Collection("peers")

		// _, err = peersCollection.UpdateMany(context.Background(), bson.M{}, bson.M{"$set": bson.M{"serverSpecificInfo": []ServerSpecificInfo{}}})
		// if err != nil {
		// 	panic(err)
		// }
		// log.Println("Server specific info entries reset")
		os.Exit(0)
	}

	_, deviceCIDR, err = net.ParseCIDR(config.InterfaceAddressCIDR)
	must(err)

	// create wireguard client
	wgc, err = wgctrl.New()
	must(err)

	// create redis client for peers
	peersDB, err = CreateRedisClient(config.RedisURL + "/0")
	must(err)
	log.Println("Connected to peers database")

	// create redis client for groups
	groupsDB, err = CreateRedisClient(config.RedisURL + "/1")
	must(err)
	log.Println("Connected to groups database")

	// create redis client for ssis
	ssisDB, err = CreateRedisClient(config.RedisURL + "/2")
	must(err)
	log.Println("Connected to ssis database")

	// get peers from db
	var peers []Peer
	keys, err := peersDB.client.Keys(context.Background(), "*").Result()
	must(err)
	var p Peer
	for _, k := range keys {
		must(peersDB.client.HGetAll(context.Background(), k).Scan(&p))
		peers = append(peers, p)
	}

	// check if any peer exists
	if len(peers) == 0 {
		newPeer := Peer{
			Name:         "Admin-0",
			AllowedUsage: 1024000000000,
			ExpiresAt:    time.Now().Add(time.Hour * 24 * 365).UnixMilli(),
			Role:         "admin",
		}

		// create private and public keys
		privateKey, err := wgtypes.GeneratePrivateKey()
		must(err)
		publicKey := privateKey.PublicKey()
		newPeer.PrivateKey = privateKey.String()
		newPeer.PublicKey = publicKey.String()

		// find unused ip
		var ip IPAddress
		must(ip.Parse(config.InterfaceAddress))
		ip.Increment()

		// update device
		device, err := wgc.Device(config.InterfaceName)
		must(err)

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
		must(err)

		newPeer.AllowedIPs = ip.ToString() + "/32"

		// add peer to database
		must(peersDB.client.HSet(context.Background(), newPeer.PublicKey, newPeer).Result())

		// add peer to device
		must(wgc.ConfigureDevice(config.InterfaceName, wgtypes.Config{Peers: []wgtypes.PeerConfig{{
			PublicKey:  publicKey,
			AllowedIPs: []net.IPNet{*allowedIPs},
			Endpoint:   nil,
		}}}))

		// add peer to list of recieved peers from database to be added to device
		peers = append(peers, newPeer)

		// save config file
		must(os.WriteFile(filepath.Join(execDir, "Admin-0.conf"), []byte(fmt.Sprintf("[Interface]\nPrivateKey=%s\nAddress=%s\nDNS=1.1.1.1,8.8.8.8\n[Peer]\nPublicKey=%s\nAllowedIPs=0.0.0.0/0\nEndpoint=%s:%d\n", newPeer.PrivateKey, newPeer.AllowedIPs, device.PublicKey.String(), config.PublicAddress, device.ListenPort)), 0666))

		log.Println("Saved config to "+filepath.Join(execDir, "Admin-0.conf"), slog.String("peer", "Admin-0"))
	}

	// store new peer configurations
	var newPeerConfigurations []wgtypes.PeerConfig

	// get wireguard interface
	device, err := wgc.Device(config.InterfaceName)
	must(err)

	// add peers from database to device
	for _, pdb := range peers {
		log.Printf("creating %s from database on %s\n", pdb.Name, device.Name)

		decodedPrivateKey, err := base64.StdEncoding.DecodeString(pdb.PrivateKey)
		must(err)
		privateKey, err := wgtypes.NewKey([]byte(decodedPrivateKey))
		must(err)
		_, ipNet, err := net.ParseCIDR(pdb.AllowedIPs)
		must(err)

		presharedKey := wgtypes.Key{}
		if pdb.Disabled {
			presharedKey, err = wgtypes.GenerateKey()
			must(err)
		}

		newPeerConfigurations = append(newPeerConfigurations, wgtypes.PeerConfig{
			PublicKey:    privateKey.PublicKey(),
			AllowedIPs:   []net.IPNet{*ipNet},
			PresharedKey: &presharedKey,
		})
	}

	// create missing peers
	must(wgc.ConfigureDevice(device.Name, wgtypes.Config{Peers: newPeerConfigurations, ReplacePeers: true}))

	// log the start of application
	log.Println("Server started")
}

func main() {
	// peers loop
	go func() {
		var err error
		var startTime time.Time
		var publicKey string
		var p wgtypes.Peer
		var ok bool
		var groupName string
		var currentTX, currentRX int64
		lastUsageMap := make(map[string][2]int64)
		peersPipeline := peersDB.client.Pipeline()
		groupsPipeline := groupsDB.client.Pipeline()
		ssisPipeline := ssisDB.client.Pipeline()

		// populate lastUsageMap
		device, err := wgc.Device(config.InterfaceName)
		must(err)
		for _, p = range device.Peers {
			lastUsageMap[p.PublicKey.String()] = [2]int64{p.TransmitBytes, p.ReceiveBytes}
		}

		for {
			// set starting time of this iteration
			startTime = time.Now()

			// get wireguard interface info
			device, err = wgc.Device(config.InterfaceName)
			must(err)

			// update peers' info
			for _, p = range device.Peers {
				// get peer public key
				publicKey = p.PublicKey.String()

				// calculate and update current tx and rx
				currentTX = p.TransmitBytes - lastUsageMap[publicKey][0]
				currentRX = p.ReceiveBytes - lastUsageMap[publicKey][1]
				ssisPipeline.HSet(context.Background(), publicKey, "tx", currentTX)
				ssisPipeline.HSet(context.Background(), publicKey, "rx", currentRX)
				lastUsageMap[publicKey] = [2]int64{p.TransmitBytes, p.ReceiveBytes}

				// update current endpoint
				ssisPipeline.HSet(context.Background(), publicKey, "endpoint", p.Endpoint.String())

				// update last handshake time
				ssisPipeline.HSet(context.Background(), publicKey, "lastHandshake", p.LastHandshakeTime.Sub(startTime).Abs().Round(time.Second).String())

				// update total tx and rx on database
				peersPipeline.HIncrBy(context.Background(), publicKey, "totalTX", currentTX)
				peersPipeline.HIncrBy(context.Background(), publicKey, "totalRX", currentRX)

				// update group usage if peer is in a group
				groups.mu.RLock()
				if groupName, ok = groups.groups[publicKey]; ok {
					groupsPipeline.HIncrBy(context.Background(), groupName, "totalTX", currentTX)
					groupsPipeline.HIncrBy(context.Background(), groupName, "totalRX", currentRX)
				}
				groups.mu.RUnlock()
			}

			// update peers
			if peersPipeline.Len() > 0 {
				must(peersPipeline.Exec(context.Background()))
			}

			// update groups
			if groupsPipeline.Len() > 0 {
				must(groupsPipeline.Exec(context.Background()))
			}

			// update ssis
			if ssisPipeline.Len() > 0 {
				must(ssisPipeline.Exec(context.Background()))
			}

			// sleep if a second has not passed
			time.Sleep(time.Duration(1000-(time.Now().UnixMilli()-startTime.UnixMilli())) * time.Millisecond)
		}
	}()

	// disable and enable peers
	go func() {
		var err error
		var startTime time.Time
		var keys []string
		var p Peer
		var dp wgtypes.Peer
		var devicePeer *wgtypes.Peer
		var device *wgtypes.Device
		var presharedKey, publicKey wgtypes.Key
		peersPipeline := peersDB.client.Pipeline()

		for {
			// set starting time of this iteration
			startTime = time.Now()

			device, err = wgc.Device(config.InterfaceName)
			must(err)

			keys, err = peersDB.client.Keys(context.Background(), "*").Result()
			must(err)
			for _, k := range keys {
				must(peersDB.client.HGetAll(context.Background(), k).Scan(&p))

				// check if peer is in device
				for _, dp = range device.Peers {
					if dp.PublicKey.String() == p.PublicKey {
						devicePeer = &dp
						break
					}
				}
				if devicePeer == nil {
					continue
				}

				// check to see if peer should be disabled
				if startTime.UnixMilli() > p.ExpiresAt || p.TotalRX+p.TotalTX > p.AllowedUsage {
					if devicePeer.PresharedKey.String() == "" {
						// create preshared key to invalidate peer
						presharedKey, err = wgtypes.GenerateKey()
						must(err)

						// parse public key
						publicKey, err = wgtypes.ParseKey(p.PublicKey)
						must(err)

						// set peer's preshared key
						must(wgc.ConfigureDevice(config.InterfaceName, wgtypes.Config{
							Peers: []wgtypes.PeerConfig{
								{
									PublicKey:    publicKey,
									UpdateOnly:   true,
									PresharedKey: &presharedKey,
								},
							},
						}))

						// update peer on database
						if !p.Disabled {
							peersPipeline.HSet(context.Background(), p.PublicKey, "disabled", "true")
						}
					}

					continue
				}

				// check to see if peer should be enabled
				if startTime.UnixMilli() < p.ExpiresAt && p.TotalRX+p.TotalTX < p.AllowedUsage {
					if devicePeer.PresharedKey.String() != "" {
						// parse public key
						publicKey, err = wgtypes.ParseKey(p.PublicKey)
						must(err)

						// remove peer's preshared key to enable it
						must(wgc.ConfigureDevice(config.InterfaceName, wgtypes.Config{
							Peers: []wgtypes.PeerConfig{
								{
									PublicKey:    publicKey,
									UpdateOnly:   true,
									PresharedKey: &wgtypes.Key{},
								},
							},
						}))

						// update peer on database
						if p.Disabled {
							peersPipeline.HSet(context.Background(), p.PublicKey, "disabled", "false")
						}
					}

					continue
				}
			}

			if peersPipeline.Len() > 0 {
				must(peersPipeline.Exec(context.Background()))
			}

			devicePeer = nil
		}
	}()

	// disable and enable groups
	go func() {
		var err error
		var startTime int64
		peersPipeline := peersDB.client.Pipeline()
		groupsPipeline := peersDB.client.Pipeline()
		var g Group
		var peerID string
		var keys []string
		var groups []Group
		var k string

		for {
			// set starting time of this iteration
			startTime = time.Now().UnixMilli()

			keys, err = groupsDB.client.Keys(context.Background(), "*").Result()
			must(err)
			for _, k = range keys {
				must(groupsDB.client.HGetAll(context.Background(), k).Scan(&g))
				groups = append(groups, g)
			}

			for _, g = range groups {
				if g.Disabled && g.TotalRX+g.TotalTX < g.AllowedUsage && startTime < g.ExpiresAt {
					groupsPipeline.HSet(context.Background(), g.Name, "disabled", "false")
					for _, peerID = range g.PeerIDs {
						peersPipeline.HSet(context.Background(), peerID, "allowedUsage", g.AllowedUsage)
					}
				} else if !g.Disabled && (g.TotalRX+g.TotalTX > g.AllowedUsage || startTime > g.ExpiresAt) {
					groupsPipeline.HSet(context.Background(), g.Name, "disabled", "true")
					for _, peerID = range g.PeerIDs {
						peersPipeline.HSet(context.Background(), peerID, "allowedUsage", 0)
					}
				}
			}

			// update peers
			if peersPipeline.Len() > 0 {
				must(peersPipeline.Exec(context.Background()))
			}

			// update groups
			if groupsPipeline.Len() > 0 {
				must(groupsPipeline.Exec(context.Background()))
			}

			// sleep if a second has not passed
			time.Sleep(time.Duration(1000-(time.Now().UnixMilli()-startTime)) * time.Millisecond)
		}
	}()

	createPeersPubSub := peersDB.client.Subscribe(context.Background(), "createPeers")
	defer createPeersPubSub.Close()
	go func() {
		var publicKey wgtypes.Key
		var allowedIPs *net.IPNet
		var peer Peer
		var err error

		for msg := range createPeersPubSub.Channel() {
			must(json.Unmarshal([]byte(msg.Payload), &peer))

			// parse public key
			publicKey, err = wgtypes.ParseKey(peer.PublicKey)
			must(err)

			// parse allowedIPs
			_, allowedIPs, err = net.ParseCIDR(peer.AllowedIPs)
			must(err)

			// add peer to device
			must(wgc.ConfigureDevice(config.InterfaceName, wgtypes.Config{Peers: []wgtypes.PeerConfig{{PublicKey: publicKey, AllowedIPs: []net.IPNet{*allowedIPs}}}}))
		}
	}()

	deletePeersPubSub := peersDB.client.Subscribe(context.Background(), "deletePeers")
	defer deletePeersPubSub.Close()
	go func() {
		var publicKey wgtypes.Key
		var err error
		for msg := range deletePeersPubSub.Channel() {
			// parse peer public key
			publicKey, err = wgtypes.ParseKey(msg.Payload)
			must(err)

			// remove peer from device
			must(wgc.ConfigureDevice(config.InterfaceName, wgtypes.Config{Peers: []wgtypes.PeerConfig{{PublicKey: publicKey, Remove: true}}}))
		}
	}()

	// create echo instance
	e := echo.New()

	// handle static files
	e.Static("/", filepath.Join(execDir, "public", "build"))

	// add cors middleware
	e.Use(middleware.CORSWithConfig(middleware.CORSConfig{AllowOrigins: []string{"http://localhost:5173"}, AllowCredentials: true}))

	// check if request is from a peer
	e.Use(Auth)

	e.GET("/api/peers", GetPeers)
	e.GET("/api/groups", GetGroups)
	e.GET("/api/peers/:id", GetPeer)
	e.GET("/api/groups/:id", GetGroup)
	e.POST("/api/peers", PostPeers)
	e.POST("/api/groups", PostGroups)
	e.DELETE("/api/peers/:id", DeletePeers)
	e.DELETE("/api/groups/:id", DeleteGroup)
	e.DELETE("/api/groups/:groupID/:peerID", DeletePeerFromGroup)
	e.PATCH("/api/peers/:id", PatchPeers)
	e.PATCH("/api/groups/:id", PatchGroups)
	e.PUT("/api/peers/:id", PutPeers)
	e.PUT("/api/groups/:id", PutGroups)
	e.PUT("/api/groups/:groupID/:peerID", PutPeerToGroup)
	e.GET("/api/config", GetConfig)
	e.GET("/api/me", GetMe)

	e.Logger.Fatal(e.StartTLS("0.0.0.0:443", filepath.Join(execDir, "certs", "server.pem"), filepath.Join(execDir, "certs", "server.key")))
}
