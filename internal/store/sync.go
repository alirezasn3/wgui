package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"

	"wgui/internal/config"
)

// syncSecretKey holds the bearer token nodes present to their master. It lives
// among the secrets so it is never returned by an ordinary API response, and it
// is deliberately not replicated: a node's copy of the database must not let it
// impersonate the master.
const syncSecretKey = "sync_secret"

// SyncSecret returns the master's sync key, creating one the first time it is
// asked for. Every node presents this, so changing it locks all of them out
// until they are given the new one.
func (s *Store) SyncSecret() (string, error) {
	v, ok, err := s.Secret(syncSecretKey)
	if err != nil {
		return "", err
	}
	if ok && len(v) > 0 {
		return string(v), nil
	}
	return s.RotateSyncSecret()
}

// RotateSyncSecret replaces the sync key and returns the new one.
func (s *Store) RotateSyncSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	secret := hex.EncodeToString(buf)
	if err := s.PutSecret(syncSecretKey, []byte(secret)); err != nil {
		return "", err
	}
	return secret, nil
}

// UsageRow is one server's count for one peer, labelled with whose it is.
type UsageRow struct {
	ServerID        string `json:"serverId"`
	PeerID          string `json:"peerId"`
	TX              int64  `json:"tx"`
	RX              int64  `json:"rx"`
	LastHandshakeAt int64  `json:"lastHandshakeAt"`
	LastEndpoint    string `json:"lastEndpoint"`
}

// UsageExcept returns every server's counts except one. A master answers a
// node with this, so the node ends up holding the same per-server breakdown the
// master has and measures peers against the same total.
func (s *Store) UsageExcept(serverID string) ([]UsageRow, error) {
	rows, err := s.db.Query(
		`SELECT server_id, peer_id, tx, rx, last_handshake_at, last_endpoint
		 FROM peer_usage WHERE server_id <> ?`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []UsageRow{}
	for rows.Next() {
		var u UsageRow
		if err := rows.Scan(&u.ServerID, &u.PeerID, &u.TX, &u.RX,
			&u.LastHandshakeAt, &u.LastEndpoint); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// ApplyRemoteUsage makes rows this database's record of what every other server
// has counted. Rows this server owns are never touched — nothing but the engine
// writes those — and neither are peers reset since takenAt, whose counts would
// otherwise be restored from a snapshot that predates the reset.
func (s *Store) ApplyRemoteUsage(takenAt int64, rows []UsageRow) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM peer_usage WHERE server_id <> ?`, s.serverID); err != nil {
		return err
	}

	stmt, err := tx.Prepare(`INSERT INTO peer_usage
	        (peer_id, server_id, tx, rx, updated_at, last_handshake_at, last_endpoint)
	    SELECT id, ?, ?, ?, ?, ?, ? FROM peers WHERE id = ? AND usage_reset_at <= ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	touch, err := tx.Prepare(
		`UPDATE peers SET last_handshake_at = MAX(last_handshake_at, ?) WHERE id = ?`)
	if err != nil {
		return err
	}
	defer touch.Close()

	at := now()
	for _, r := range rows {
		if r.ServerID == s.serverID {
			return errors.New("a master returned rows this server owns")
		}
		if r.TX == 0 && r.RX == 0 && r.LastHandshakeAt == 0 {
			continue
		}
		if _, err := stmt.Exec(r.ServerID, r.TX, r.RX, at,
			r.LastHandshakeAt, r.LastEndpoint, r.PeerID, takenAt); err != nil {
			return err
		}
		if _, err := touch.Exec(r.LastHandshakeAt, r.PeerID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Grant is one row of the sharing table, carried between servers with the rest
// of the definitions.
type Grant struct {
	ViewerID string `json:"viewerId"`
	TargetID string `json:"targetId"`
}

// Definitions is everything a master tells its nodes to serve. Usage is not in
// here: each server counts its own and they are exchanged separately.
//
// Monitors are deliberately absent. A monitor watches something from where it
// is standing and runs a script when it stops answering, so it is about the
// server it runs on rather than about the peers — replicating one would have
// every node react to a master's view of the network.
type Definitions struct {
	Peers      []*Peer          `json:"peers"`
	Groups     []*Group         `json:"groups"`
	Visibility []Grant          `json:"visibility"`
	Scripts    []*Script        `json:"scripts"`
	Settings   *config.Settings `json:"settings,omitempty"`
}

// ApplyDefinitions makes the master's peers and groups this database's own.
//
// The master is authoritative, so anything it does not list is removed —
// including peers a node had created for itself before it was enrolled. What
// survives is usage: the rows are keyed by peer, and peers are updated in place
// rather than deleted and recreated, so a node's own counts are never lost to a
// sync. A peer the master has reset since this server last heard has its local
// count cleared too, which is how a reset reaches every server.
func (s *Store) ApplyDefinitions(d Definitions) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Peers reference groups, and owners reference other peers, so the order
	// within the batch cannot satisfy every key as it goes. Checking at commit
	// instead means the set only has to be consistent as a whole.
	if _, err := tx.Exec(`PRAGMA defer_foreign_keys = ON`); err != nil {
		return err
	}

	// Remove what the master no longer lists *before* inserting what it does.
	// A node starts life with an admin peer of its own, and the master's admin
	// holds the same name and tunnel address under a different key: inserting
	// first collides on both unique columns. Peers the master still lists are
	// updated in place rather than deleted, so no usage row is ever cascaded
	// away by this.
	groupIDs := make([]any, 0, len(d.Groups))
	for _, g := range d.Groups {
		groupIDs = append(groupIDs, g.ID)
	}
	peerIDs := make([]any, 0, len(d.Peers))
	for _, p := range d.Peers {
		peerIDs = append(peerIDs, p.ID)
	}
	if err := deleteMissing(tx, "peers", "id", peerIDs); err != nil {
		return err
	}
	if err := deleteMissing(tx, "groups", "id", groupIDs); err != nil {
		return err
	}

	for _, g := range d.Groups {
		if _, err := tx.Exec(`
		    INSERT INTO groups (id, name, owner_id, allowed_usage, expires_at,
		                        manually_disabled, note, created_at, updated_at)
		         VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		    ON CONFLICT (id) DO UPDATE SET
		        name = excluded.name, owner_id = excluded.owner_id,
		        allowed_usage = excluded.allowed_usage, expires_at = excluded.expires_at,
		        manually_disabled = excluded.manually_disabled, note = excluded.note,
		        updated_at = excluded.updated_at`,
			g.ID, g.Name, nullString(g.OwnerID), g.AllowedUsage, g.ExpiresAt,
			g.ManuallyDisabled, g.Note, g.CreatedAt, g.UpdatedAt); err != nil {
			return err
		}
	}

	for _, p := range d.Peers {
		// A reset on the master clears what this server counted as well. It is
		// recognised by the reset being newer than the one this database held,
		// which has to be read before the row is overwritten below. Comparing
		// against when the count was last written would miss it: a peer this
		// server carries has its row touched at every flush, so by the time the
		// reset arrives the row nearly always looks newer than it.
		if _, err := tx.Exec(
			`DELETE FROM peer_usage WHERE peer_id = ?
			   AND ? > (SELECT usage_reset_at FROM peers WHERE id = ?)`,
			p.ID, p.UsageResetAt, p.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(`
		    INSERT INTO peers (id, name, role, owner_id, group_id, public_key, private_key,
		                       allowed_ips, client_endpoint, preferred_endpoint, allowed_usage,
		                       expires_at, manually_disabled, last_handshake_at, last_endpoint,
		                       note, usage_reset_at, created_at, updated_at)
		         VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, '', ?, ?, ?, ?)
		    ON CONFLICT (id) DO UPDATE SET
		        name = excluded.name, role = excluded.role, owner_id = excluded.owner_id,
		        group_id = excluded.group_id, public_key = excluded.public_key,
		        private_key = excluded.private_key, allowed_ips = excluded.allowed_ips,
		        client_endpoint = excluded.client_endpoint,
		        preferred_endpoint = excluded.preferred_endpoint,
		        allowed_usage = excluded.allowed_usage, expires_at = excluded.expires_at,
		        manually_disabled = excluded.manually_disabled, note = excluded.note,
		        usage_reset_at = excluded.usage_reset_at, updated_at = excluded.updated_at`,
			p.ID, p.Name, p.Role, nullString(p.OwnerID), nullInt(p.GroupID), p.PublicKey,
			p.PrivateKey, p.AllowedIPs, p.ClientEndpoint, p.PreferredEndpoint, p.AllowedUsage,
			p.ExpiresAt, p.ManuallyDisabled, p.Note, p.UsageResetAt, p.CreatedAt,
			p.UpdatedAt); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(`DELETE FROM peer_visibility`); err != nil {
		return err
	}
	for _, g := range d.Visibility {
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO peer_visibility (viewer_id, target_id) VALUES (?, ?)`,
			g.ViewerID, g.TargetID); err != nil {
			return err
		}
	}

	// Scripts follow the peers: an operator should be able to reach for the
	// same script on whichever panel they happen to have open. Their run
	// history does not — that is a record of what happened on one machine, and
	// it goes when a script the master has dropped goes.
	scriptIDs := make([]any, 0, len(d.Scripts))
	for _, v := range d.Scripts {
		scriptIDs = append(scriptIDs, v.ID)
	}
	if err := deleteMissing(tx, "scripts", "id", scriptIDs); err != nil {
		return err
	}
	for _, v := range d.Scripts {
		if _, err := tx.Exec(`
		    INSERT INTO scripts (id, name, description, body, timeout_sec, created_at, updated_at)
		         VALUES (?, ?, ?, ?, ?, ?, ?)
		    ON CONFLICT (id) DO UPDATE SET
		        name = excluded.name, description = excluded.description,
		        body = excluded.body, timeout_sec = excluded.timeout_sec,
		        updated_at = excluded.updated_at`,
			v.ID, v.Name, v.Description, v.Body, v.TimeoutSec, v.CreatedAt, v.UpdatedAt); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	// Settings are key-value rather than a table to reconcile, and only the
	// shared half of them is taken: see Settings.AdoptShared.
	if d.Settings != nil {
		local, err := s.LoadSettings()
		if err != nil {
			return err
		}
		return s.SaveSettings(local.AdoptShared(*d.Settings))
	}
	return nil
}

// deleteMissing removes every row of table whose key is not in keep. An empty
// keep clears the table, which is what a master with nothing to serve means.
func deleteMissing(tx *sql.Tx, table, key string, keep []any) error {
	if len(keep) == 0 {
		_, err := tx.Exec(`DELETE FROM ` + table)
		return err
	}
	_, err := tx.Exec(
		`DELETE FROM `+table+` WHERE `+key+` NOT IN (`+placeholders(len(keep))+`)`, keep...)
	return err
}

// AllDefinitions is everything a master hands its nodes.
func (s *Store) AllDefinitions(now int64) (Definitions, error) {
	var d Definitions
	peers, _, err := s.ListPeers(PeerFilter{Scope: Scope{Role: "admin"}, Now: now})
	if err != nil {
		return d, err
	}
	d.Peers = peers

	groups, _, err := s.ListGroups(GroupFilter{Scope: Scope{Role: "admin"}, Now: now})
	if err != nil {
		return d, err
	}
	d.Groups = groups

	rows, err := s.db.Query(`SELECT viewer_id, target_id FROM peer_visibility`)
	if err != nil {
		return d, err
	}
	defer rows.Close()
	d.Visibility = []Grant{}
	for rows.Next() {
		var g Grant
		if err := rows.Scan(&g.ViewerID, &g.TargetID); err != nil {
			return d, err
		}
		d.Visibility = append(d.Visibility, g)
	}
	if err := rows.Err(); err != nil {
		return d, err
	}

	if d.Scripts, err = s.ListScripts(); err != nil {
		return d, err
	}
	settings, err := s.LoadSettings()
	if err != nil {
		return d, err
	}
	d.Settings = &settings
	return d, nil
}

// masterIDSecret remembers which master this database belongs to. Its absence
// on a database that already has peers is what tells wgui the data was a
// standalone server's rather than a master's, and must be set aside.
const masterIDSecret = "master_id"

// MasterID is the master this database was last filled from, or "" if it has
// never synced.
func (s *Store) MasterID() (string, error) {
	v, _, err := s.Secret(masterIDSecret)
	return string(v), err
}

// SetMasterID records whose peers this database now holds.
func (s *Store) SetMasterID(id string) error {
	return s.PutSecret(masterIDSecret, []byte(id))
}
