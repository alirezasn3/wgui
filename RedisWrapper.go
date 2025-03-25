package main

import (
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"
)

func CreateRedisClient(url string) (*redis.Client, error) {
	// create redis client
	options, err := redis.ParseURL(url)
	if err != nil {
		return nil, err
	}
	client := redis.NewClient(options)

	// check redis connection
	if client.Ping(ctx).Val() == "PONG" {
		return client, nil
	} else {
		return nil, errors.New("failed to connect to redis, did not receive PONG response")
	}
}

type PeersDB struct {
	client          *redis.Client
	allowedIPsIndex *redis.Client
	nameIndex       *redis.Client
}

func (pdb *PeersDB) Connect(url string) error {
	client, err := CreateRedisClient(url + "/0")
	if err != nil {
		return err
	}
	pdb.client = client

	client, err = CreateRedisClient(url + "/8")
	if err != nil {
		return err
	}
	pdb.allowedIPsIndex = client

	client, err = CreateRedisClient(url + "/9")
	if err != nil {
		return err
	}
	pdb.nameIndex = client

	return nil
}

func (pdb *PeersDB) GetAllPeers() ([]*Peer, error) {
	var peers []*Peer
	keys, err := pdb.client.Keys(ctx, "*").Result()
	if err != nil {
		return nil, err
	}
	var p Peer
	for _, k := range keys {
		err = pdb.client.HGetAll(ctx, k).Scan(&p)
		if err != nil {
			return nil, err
		}
		peers = append(peers, &p)
	}
	return peers, nil
}

func (pdb *PeersDB) GetPeerNeighbours(prefix string) ([]*Peer, error) {
	var peers []*Peer
	keys, err := pdb.client.Keys(ctx, prefix+"*").Result()
	if err != nil {
		return nil, err
	}
	var p Peer
	for _, k := range keys {
		err = pdb.client.HGetAll(ctx, k).Scan(&p)
		if err != nil {
			return nil, err
		}
		peers = append(peers, &p)
	}
	return peers, nil
}

func (pdb *PeersDB) KeyExists(key string) (bool, error) {
	count, err := pdb.client.Exists(ctx, key).Result()
	if err != nil {
		return false, err
	}
	if count > 0 {
		return true, nil
	}
	return false, nil
}

func (pdb *PeersDB) GetPeerByName(name string) (*Peer, error) {
	key, err := pdb.nameIndex.Get(ctx, name).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, errors.New("peer not found")
		}
		return nil, err
	}
	var p Peer
	err = pdb.client.HGetAll(ctx, key).Scan(&p)
	if err != nil {
		return nil, err
	} else if p == (Peer{}) {
		return nil, errors.New("peer not found")
	}
	return &p, err
}

func (pdb *PeersDB) GetPeerByKey(key string) (*Peer, error) {
	var p Peer
	err := pdb.client.HGetAll(ctx, key).Scan(&p)
	if err != nil {
		return nil, err
	} else if p == (Peer{}) {
		return nil, errors.New("peer not found")
	}
	return &p, err
}

func (pdb *PeersDB) GetPeerByAllowedIPs(allowedIPs string) (*Peer, error) {
	key, err := pdb.allowedIPsIndex.Get(ctx, allowedIPs).Result()
	if err != nil {
		return nil, err
	}
	var p Peer
	err = pdb.client.HGetAll(ctx, key).Scan(&p)
	if err != nil {
		return nil, err
	} else if p == (Peer{}) {
		return nil, errors.New("peer not found")
	}
	return &p, err
}

func (pdb *PeersDB) CreatePeer(p Peer) error {
	_, err := pdb.GetPeerByName(p.Name)
	if err == nil {
		fmt.Println(1)
		return errors.New("duplicate peer name")
	}
	if err.Error() == "peer not found" {
		fmt.Println(2)
		err = pdb.client.HSet(ctx, p.Name+":"+p.AllowedIPs+":"+p.PublicKey, p).Err()
		if err != nil {
			fmt.Println(3)
			return err
		}
		err = pdb.allowedIPsIndex.Set(ctx, p.AllowedIPs, p.Name+":"+p.AllowedIPs+":"+p.PublicKey, 0).Err()
		if err != nil {
			fmt.Println(4)
			return err
		}
		err = pdb.nameIndex.Set(ctx, p.Name, p.Name+":"+p.AllowedIPs+":"+p.PublicKey, 0).Err()
		if err != nil {
			fmt.Println(5)
			return err
		}
		return nil
	}
	fmt.Println(6)
	return err
}

func (pdb *PeersDB) ResetPeerUsage(key string) error {
	return pdb.client.HSet(ctx, key, map[string]interface{}{"totalTX": 0, "totalRX": 0}).Err()
}

type GroupsDB struct {
	client *redis.Client
}

func (pdb *GroupsDB) Connect(url string) error {
	client, err := CreateRedisClient(url + "/1")
	if err != nil {
		return err
	}
	pdb.client = client
	return nil
}

func (pdb *GroupsDB) GetAllGroups() ([]*Group, error) {
	var groups []*Group
	keys, err := pdb.client.Keys(ctx, "*").Result()
	if err != nil {
		return nil, err
	}
	var g Group
	for _, k := range keys {
		err = pdb.client.HGetAll(ctx, k).Scan(&g)
		if err != nil {
			return nil, err
		}
		groups = append(groups, &g)
	}
	return groups, nil
}

type SSISDB struct {
	client *redis.Client
}

func (pdb *SSISDB) Connect(url string) error {
	client, err := CreateRedisClient(url + "/2")
	if err != nil {
		return err
	}
	pdb.client = client
	return nil
}
