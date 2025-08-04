package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
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
	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

type StringSlice []string

func (ss StringSlice) MarshalBinary() ([]byte, error) {
	if ss == nil {
		ss = []string{}
	}
	return json.Marshal(ss)
}

func (ss *StringSlice) UnmarshalBinary(data []byte) error {
	if len(data) == 0 {
		data = []byte{}
	}
	return json.Unmarshal(data, ss)
}

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
	GroupName      string `json:"groupName" redis:"groupName"`
}

type SSI struct {
	Address       string `json:"address" redis:"address"`
	LastHandshake string `json:"lastHandshake" redis:"lastHandshake"`
	Endpoint      string `json:"endpoint" redis:"endpoint"`
	TX            int64  `json:"tx" redis:"tx"`
	RX            int64  `json:"rx" redis:"rx"`
}

// type Peers struct {
// 	peers map[string]*Peer
// 	mu    sync.RWMutex
// }

type Group struct {
	Name         string      `json:"name" redis:"name"`
	Peers        StringSlice `json:"peers" redis:"peers"`
	AllowedUsage int64       `json:"allowedUsage" redis:"allowedUsage"`
	TotalTX      int64       `json:"totalTX" redis:"totalTX"`
	TotalRX      int64       `json:"totalRX" redis:"totalRX"`
	ExpiresAt    int64       `json:"expiresAt" redis:"expiresAt"`
	Disabled     bool        `json:"disabled" redis:"disabled"`
}

var config Config         // used to store app configuration
var wgc *wgctrl.Client    // used to interact with wireguard interfaces
var deviceCIDR *net.IPNet // used to check if client is in device subnet
var peersDB = PeersDB{}   // used to interact with reids database
var groupsDB = GroupsDB{} // used to interact with reids database
var ssisDB = SSISDB{}     // used to interact with reids database
var execDir string        // used to store the directory of the executable
var ctx = context.TODO()  // default context

var publicKeyToPeerNameMap = make(map[string]string)
var publicKeyToPeerNameMapMutex sync.RWMutex
var publicKeyToGroupNameMap = make(map[string]string)
var publicKeyToGroupNameMapMutex sync.RWMutex

func must(returnValues ...interface{}) {
	if returnValues[len(returnValues)-1] != nil {
		panic(returnValues[len(returnValues)-1])
	}
}

func init() {
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

		// _, err = peersCollection.UpdateMany(ctx, bson.M{}, bson.M{"$set": bson.M{"serverSpecificInfo": []ServerSpecificInfo{}}})
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
	must(peersDB.Connect(config.RedisURL))
	log.Println("Connected to peers database")

	// create redis client for groups
	must(groupsDB.Connect(config.RedisURL))
	log.Println("Connected to groups database")

	// create redis client for ssis
	must(ssisDB.Connect(config.RedisURL))
	log.Println("Connected to ssis database")

	// get peers from db
	peers, err := peersDB.GetAllPeers()
	must(err)

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
		must(peersDB.CreatePeer(newPeer))

		// add peer to device
		must(wgc.ConfigureDevice(config.InterfaceName, wgtypes.Config{Peers: []wgtypes.PeerConfig{{
			PublicKey:  publicKey,
			AllowedIPs: []net.IPNet{*allowedIPs},
			Endpoint:   nil,
		}}}))

		// add peer to list of recieved peers from database to be added to device
		peers = append(peers, &newPeer)

		// save config file
		must(os.WriteFile(filepath.Join(execDir, "Admin-0.conf"), []byte(fmt.Sprintf("[Interface]\nPrivateKey=%s\nAddress=%s\nDNS=1.1.1.1,8.8.8.8\n[Peer]\nPublicKey=%s\nAllowedIPs=0.0.0.0/0\nEndpoint=%s:%d\n", newPeer.PrivateKey, newPeer.AllowedIPs, device.PublicKey.String(), config.PublicAddress, device.ListenPort)), 0666))

		log.Println("Saved config to " + filepath.Join(execDir, "Admin-0.conf"))
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
		var peerName string
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

		// populate public key to name map
		tempPeers, err := peersDB.GetAllPeers()
		must(err)
		publicKeyToPeerNameMapMutex.Lock()
		for _, p := range tempPeers {
			publicKeyToPeerNameMap[p.PublicKey] = p.Name
		}
		publicKeyToPeerNameMapMutex.Unlock()

		// populate public key to group name map
		tempGroups, err := groupsDB.GetAllGroups()
		must(err)
		for _, g := range tempGroups {
			publicKeyToGroupNameMapMutex.Lock()
			for _, pk := range g.Peers {
				publicKeyToGroupNameMap[pk] = g.Name
			}
			publicKeyToGroupNameMapMutex.Unlock()
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
				ssisPipeline.HSet(ctx, publicKey+":"+config.PublicAddress, "tx", currentTX)
				ssisPipeline.HSet(ctx, publicKey+":"+config.PublicAddress, "rx", currentRX)
				lastUsageMap[publicKey] = [2]int64{p.TransmitBytes, p.ReceiveBytes}

				// update current endpoint
				ssisPipeline.HSet(ctx, publicKey+":"+config.PublicAddress, "endpoint", p.Endpoint.String())

				// update last handshake time
				// ssisPipeline.HSet(ctx, publicKey+":"+config.PublicAddress, "lastHandshake", p.LastHandshakeTime.Sub(startTime).Abs().Round(time.Second).String())
				ssisPipeline.HSet(ctx, publicKey+":"+config.PublicAddress, "lastHandshake", p.LastHandshakeTime.UnixMilli())

				// update total tx and rx on database
				if peerName, ok = publicKeyToPeerNameMap[publicKey]; ok {
					peersPipeline.HIncrBy(ctx, peerName+":"+p.AllowedIPs[0].String()+":"+publicKey, "totalTX", currentTX)
					peersPipeline.HIncrBy(ctx, peerName+":"+p.AllowedIPs[0].String()+":"+publicKey, "totalRX", currentRX)
				} else {
					panic("failed to find peer name for public key: " + publicKey)
				}

				// update group usage if peer is in a group
				publicKeyToGroupNameMapMutex.RLock()
				if groupName, ok = publicKeyToGroupNameMap[publicKey]; ok {
					groupsPipeline.HIncrBy(ctx, groupName, "totalTX", currentTX)
					groupsPipeline.HIncrBy(ctx, groupName, "totalRX", currentRX)
				}
				publicKeyToGroupNameMapMutex.RUnlock()
			}

			// update peers
			if peersPipeline.Len() > 0 {
				must(peersPipeline.Exec(ctx))
			}

			// update groups
			if groupsPipeline.Len() > 0 {
				must(groupsPipeline.Exec(ctx))
			}

			// update ssis
			if ssisPipeline.Len() > 0 {
				must(ssisPipeline.Exec(ctx))
			}

			// sleep if a second has not passed
			time.Sleep(time.Duration(1000-(time.Now().UnixMilli()-startTime.UnixMilli())) * time.Millisecond)
		}
	}()

	// disable and enable peers
	go func() {
		var err error
		var startTime time.Time
		var peers []*Peer
		var dbp *Peer       // database peer
		var dp wgtypes.Peer // device peer
		var foundDevicePeer *wgtypes.Peer
		var device *wgtypes.Device
		var presharedKey, publicKey wgtypes.Key
		peersPipeline := peersDB.client.Pipeline()

		for {
			// set starting time of this iteration
			startTime = time.Now()

			device, err = wgc.Device(config.InterfaceName)
			must(err)

			// get peers
			peers, err = peersDB.GetAllPeers()
			must(err)

			// loop over peers
			for _, dbp = range peers {
				// check if peer is in device
				for _, dp = range device.Peers {
					if dp.PublicKey.String() == dbp.PublicKey {
						foundDevicePeer = &dp
						break
					}
				}
				if foundDevicePeer == nil {
					log.Println(dbp.Name + " exists in database but not on device")
					continue
				}

				// check to see if peer should be disabled
				if startTime.UnixMilli() > dbp.ExpiresAt || dbp.TotalRX+dbp.TotalTX > dbp.AllowedUsage {
					if foundDevicePeer.PresharedKey.String() == "" {
						// create preshared key to invalidate peer
						presharedKey, err = wgtypes.GenerateKey()
						must(err)

						// parse public key
						publicKey, err = wgtypes.ParseKey(dbp.PublicKey)
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
						if !dbp.Disabled {
							peersPipeline.HSet(ctx, dbp.PublicKey, "disabled", true)
						}
					}

					continue
				}

				// check to see if peer should be enabled
				if startTime.UnixMilli() < dbp.ExpiresAt && dbp.TotalRX+dbp.TotalTX < dbp.AllowedUsage {
					if foundDevicePeer.PresharedKey.String() != "" {
						// parse public key
						publicKey, err = wgtypes.ParseKey(dbp.PublicKey)
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
						if dbp.Disabled {
							peersPipeline.HSet(ctx, dbp.PublicKey, "disabled", false)
						}
					}

					continue
				}
			}

			if peersPipeline.Len() > 0 {
				must(peersPipeline.Exec(ctx))
			}

			foundDevicePeer = nil

			// sleep if 10 seconda has not passed
			time.Sleep(time.Duration(10000-(time.Now().UnixMilli()-startTime.UnixMilli())) * time.Millisecond)
		}
	}()

	// disable and enable groups
	go func() {
		var err error
		var startTime int64
		peersPipeline := peersDB.client.Pipeline()
		groupsPipeline := peersDB.client.Pipeline()
		var g *Group
		var peerID string
		var groups []*Group

		for {
			// set starting time of this iteration
			startTime = time.Now().UnixMilli()

			groups, err = groupsDB.GetAllGroups()
			must(err)

			for _, g = range groups {
				if g.Disabled && g.TotalRX+g.TotalTX < g.AllowedUsage && startTime < g.ExpiresAt {
					groupsPipeline.HSet(ctx, g.Name, "disabled", false)
					for _, peerID = range g.Peers {
						peersPipeline.HSet(ctx, peerID, "allowedUsage", g.AllowedUsage)
					}
				} else if !g.Disabled && (g.TotalRX+g.TotalTX > g.AllowedUsage || startTime > g.ExpiresAt) {
					groupsPipeline.HSet(ctx, g.Name, "disabled", true)
					for _, peerID = range g.Peers {
						peersPipeline.HSet(ctx, peerID, "allowedUsage", 0)
					}
				}
			}

			// update peers
			if peersPipeline.Len() > 0 {
				must(peersPipeline.Exec(ctx))
			}

			// update groups
			if groupsPipeline.Len() > 0 {
				must(groupsPipeline.Exec(ctx))
			}

			// sleep if 10 seconda has not passed
			time.Sleep(time.Duration(10000-(time.Now().UnixMilli()-startTime)) * time.Millisecond)
		}
	}()

	createPeersPubSub := peersDB.client.Subscribe(ctx, "createPeers")
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

	deletePeersPubSub := peersDB.client.Subscribe(ctx, "deletePeers")
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
	e.GET("/api/peers/:key", GetPeer)
	e.GET("/api/groups/:name", GetGroup)
	e.POST("/api/peers", PostPeers)
	e.POST("/api/groups", PostGroups)
	e.DELETE("/api/peers/:key", DeletePeers)
	e.DELETE("/api/groups/:key", DeleteGroup)
	e.DELETE("/api/groups/:groupID/:peerID", DeletePeerFromGroup)
	e.PATCH("/api/peers/:key", PatchPeers)
	e.PATCH("/api/groups/:key", PatchGroups)
	e.PUT("/api/peers/:key", PutPeers)
	e.PUT("/api/groups/:key", PutGroups)
	e.PUT("/api/groups/:groupID/:peerID", PutPeerToGroup)
	e.GET("/api/config", GetConfig)
	e.GET("/api/me", GetMe)

	e.Logger.Fatal(e.StartTLS("0.0.0.0:443", filepath.Join(execDir, "certs", "server.pem"), filepath.Join(execDir, "certs", "server.key")))
}
