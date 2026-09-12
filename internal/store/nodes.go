package store

import "time"

// Node is another wgui that takes its peers from this one. A master learns
// about its nodes by being contacted: there is nothing to register by hand, and
// a node that stops syncing simply stops being seen.
type Node struct {
	ID string `json:"id"`
	// Role is "node" for a server that syncs to this one, "master" for the one
	// this server syncs with, and "local" for this server itself.
	Role        string `json:"role"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Address     string `json:"address"`
	PeerCount   int64  `json:"peerCount"`
	FirstSeenAt int64  `json:"firstSeenAt"`
	LastSeenAt  int64  `json:"lastSeenAt"`
	// OnlinePeers and the live speeds are what that server reported about
	// itself at its last sync. They are carried, never stored, so a server that
	// has gone quiet shows its last word rather than a fabricated zero.
	OnlinePeers int64 `json:"onlinePeers"`
	TXSpeed     int64 `json:"txSpeed"`
	RXSpeed     int64 `json:"rxSpeed"`
	// Recent is what that server carried over the last hour, day, week and
	// month, as it last reported.
	Recent UsageWindows `json:"recent"`
	// Online is derived, not stored: a node that has not been heard from in
	// several sync intervals is treated as gone.
	Online bool `json:"online"`
}

// nodeOnlineWindowMS is how long after its last sync a node still counts as
// online — several intervals, so one missed or slow sync is not an outage.
const nodeOnlineWindowMS = 90_000

// SeeNode records that a node just synced, creating its row the first time.
func (s *Store) SeeNode(n Node) error {
	at := time.Now().UnixMilli()
	role := n.Role
	if role == "" {
		role = "node"
	}
	_, err := s.db.Exec(`
	    INSERT INTO nodes (id, role, name, version, address, peer_count,
	                       online_peers, tx_speed, rx_speed,
	                       usage_hour, usage_day, usage_week, usage_month,
	                       first_seen_at, last_seen_at)
	         VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	    ON CONFLICT (id) DO UPDATE SET
	        role = excluded.role, name = excluded.name, version = excluded.version,
	        address = excluded.address, peer_count = excluded.peer_count,
	        online_peers = excluded.online_peers, tx_speed = excluded.tx_speed,
	        rx_speed = excluded.rx_speed,
	        usage_hour = excluded.usage_hour, usage_day = excluded.usage_day,
	        usage_week = excluded.usage_week, usage_month = excluded.usage_month,
	        last_seen_at = excluded.last_seen_at`,
		n.ID, role, n.Name, n.Version, n.Address, n.PeerCount,
		n.OnlinePeers, n.TXSpeed, n.RXSpeed,
		n.Recent.Hour, n.Recent.Day, n.Recent.Week, n.Recent.Month, at, at)
	return err
}

// Nodes lists every node this server has heard from, most recently seen first.
func (s *Store) Nodes() ([]Node, error) {
	rows, err := s.db.Query(`SELECT id, role, name, version, address, peer_count,
	                                online_peers, tx_speed, rx_speed,
	                                usage_hour, usage_day, usage_week, usage_month,
	                                first_seen_at, last_seen_at
	                         FROM nodes ORDER BY last_seen_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cutoff := time.Now().UnixMilli() - nodeOnlineWindowMS
	out := []Node{}
	for rows.Next() {
		var n Node
		if err := rows.Scan(&n.ID, &n.Role, &n.Name, &n.Version, &n.Address, &n.PeerCount,
			&n.OnlinePeers, &n.TXSpeed, &n.RXSpeed,
			&n.Recent.Hour, &n.Recent.Day, &n.Recent.Week, &n.Recent.Month,
			&n.FirstSeenAt, &n.LastSeenAt); err != nil {
			return nil, err
		}
		n.Online = n.LastSeenAt >= cutoff
		out = append(out, n)
	}
	return out, rows.Err()
}

// ForgetNode drops a node and everything it had counted, which is what makes
// its share of every peer's usage go away with it.
func (s *Store) ForgetNode(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM peer_usage WHERE server_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM nodes WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// PeerPresence is one server's view of a peer: what it carried, and when and
// from where it last saw it.
type PeerPresence struct {
	ServerID        string `json:"serverId"`
	ServerName      string `json:"serverName"`
	Role            string `json:"role"`
	IsLocal         bool   `json:"isLocal"`
	TX              int64  `json:"tx"`
	RX              int64  `json:"rx"`
	Usage           int64  `json:"usage"`
	LastHandshakeAt int64  `json:"lastHandshakeAt"`
	LastEndpoint    string `json:"lastEndpoint"`
	Online          bool   `json:"online"`
}

// PeerPresence reports where a peer has been seen across the whole fleet, newest
// first, so an operator can tell which server it is actually using.
func (s *Store) PeerPresence(peerID string, now int64) ([]PeerPresence, error) {
	rows, err := s.db.Query(`
	    SELECT u.server_id, COALESCE(n.name, ''), COALESCE(n.role, ''),
	           u.tx, u.rx, u.last_handshake_at, u.last_endpoint
	    FROM peer_usage u LEFT JOIN nodes n ON n.id = u.server_id
	    WHERE u.peer_id = ?
	    ORDER BY u.last_handshake_at DESC`, peerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []PeerPresence{}
	for rows.Next() {
		var p PeerPresence
		if err := rows.Scan(&p.ServerID, &p.ServerName, &p.Role,
			&p.TX, &p.RX, &p.LastHandshakeAt, &p.LastEndpoint); err != nil {
			return nil, err
		}
		p.IsLocal = p.ServerID == s.serverID
		if p.IsLocal {
			p.Role = "local"
		}
		p.Usage = p.TX + p.RX
		p.Online = p.LastHandshakeAt >= now-onlineWindowMS
		out = append(out, p)
	}
	return out, rows.Err()
}
