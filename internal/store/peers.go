package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// onlineWindowMS is how long after a handshake a peer still counts as online,
// matching the interval WireGuard itself uses to consider a session live.
const onlineWindowMS = 180_000

// peerCTE derives everything that filtering and sorting need, so that a peer's
// status is defined in exactly one place rather than recomputed per caller.
//
// It takes two bind parameters, both the current time in unix ms.
const peerCTE = `
WITH peer_totals AS (
    -- A peer's usage is what every server carrying it has counted. Each server
    -- owns one row and writes only its own, so this sum is the whole truth
    -- however the syncs between them happen to interleave.
    SELECT peer_id, SUM(tx) AS tx, SUM(rx) AS rx FROM peer_usage GROUP BY peer_id
),
group_usage AS (
    SELECT p.group_id AS gid, SUM(COALESCE(t.tx, 0) + COALESCE(t.rx, 0)) AS used
    FROM peers p LEFT JOIN peer_totals t ON t.peer_id = p.id
    WHERE p.group_id IS NOT NULL GROUP BY p.group_id
),
local_seen AS (
    -- What this server itself last saw, as opposed to the fleet. The id is read
    -- from the secrets table rather than bound, so every caller of this CTE
    -- keeps the two parameters it already passes.
    SELECT peer_id, last_handshake_at FROM peer_usage
    WHERE server_id = (SELECT CAST(value AS TEXT) FROM secrets WHERE key = 'server_id')
),
last_seen AS (
    -- Which server saw the peer most recently, so the panel can say where a
    -- peer actually is rather than only that it is somewhere.
    SELECT u.peer_id, u.server_id, COALESCE(n.name, '') AS server_name,
           ROW_NUMBER() OVER (PARTITION BY u.peer_id ORDER BY u.last_handshake_at DESC) AS rn
    FROM peer_usage u LEFT JOIN nodes n ON n.id = u.server_id
    WHERE u.last_handshake_at > 0
),
shared_with AS (
    SELECT viewer_id AS vid, COUNT(*) AS n FROM peer_visibility GROUP BY viewer_id
),
v AS (
    SELECT
        p.id, p.name, p.role,
        COALESCE(p.owner_id, '')          AS owner_id,
        COALESCE(o.name, '')              AS owner_name,
        COALESCE(p.group_id, 0)           AS group_id,
        COALESCE(g.name, '')              AS group_name,
        p.public_key, p.private_key, p.allowed_ips,
        p.client_endpoint, p.preferred_endpoint,
        p.allowed_usage, p.expires_at,
        COALESCE(t.tx, 0)                 AS total_tx,
        COALESCE(t.rx, 0)                 AS total_rx,
        p.manually_disabled, p.last_handshake_at, p.last_endpoint, p.note,
        p.usage_reset_at, p.created_at, p.updated_at,
        (COALESCE(t.tx, 0) + COALESCE(t.rx, 0)) AS usage,
        COALESCE(g.allowed_usage, 0)      AS g_allowed_usage,
        COALESCE(g.expires_at, 0)         AS g_expires_at,
        COALESCE(gu.used, 0)              AS g_used,
        -- How many other peers this one has been granted sight of.
        COALESCE(sw.n, 0)                 AS shared_count,
        COALESCE(ls.server_id, '')        AS seen_on_id,
        COALESCE(ls.server_name, '')      AS seen_on,
        COALESCE(loc.last_handshake_at, 0) AS local_handshake_at,
        -- Joining a group hands the group the whole say over data and time: its
        -- allowance and expiry replace the member's own rather than competing
        -- with them, and the usage measured against that allowance is the
        -- group's total. A member's own figures are kept untouched in the row
        -- and take effect again the moment it leaves.
        CASE WHEN p.group_id IS NOT NULL THEN COALESCE(g.allowed_usage, 0)
             ELSE p.allowed_usage END AS eff_allowed_usage,
        CASE WHEN p.group_id IS NOT NULL THEN COALESCE(g.expires_at, 0)
             ELSE p.expires_at END AS eff_expires_at,
        CASE WHEN p.group_id IS NOT NULL THEN COALESCE(gu.used, 0)
             ELSE (COALESCE(t.tx, 0) + COALESCE(t.rx, 0)) END AS eff_usage,
        CASE
            -- Disabling by hand is not a limit, so a member's own switch still
            -- cuts it off even though the group governs its quota and expiry.
            WHEN p.manually_disabled = 1 OR COALESCE(g.manually_disabled, 0) = 1 THEN 'disabled'
            WHEN p.group_id IS NOT NULL THEN
                CASE
                    WHEN COALESCE(g.expires_at, 0) > 0 AND g.expires_at <= ? THEN 'expired'
                    WHEN COALESCE(g.allowed_usage, 0) > 0
                     AND COALESCE(gu.used, 0) >= g.allowed_usage THEN 'quota'
                    ELSE 'active'
                END
            WHEN p.expires_at > 0 AND p.expires_at <= ? THEN 'expired'
            WHEN p.allowed_usage > 0
             AND (COALESCE(t.tx, 0) + COALESCE(t.rx, 0)) >= p.allowed_usage THEN 'quota'
            ELSE 'active'
        END AS status
    FROM peers p
    LEFT JOIN peer_totals t ON t.peer_id = p.id
    LEFT JOIN groups g      ON g.id  = p.group_id
    LEFT JOIN group_usage gu ON gu.gid = p.group_id
    LEFT JOIN shared_with sw ON sw.vid = p.id
    LEFT JOIN last_seen ls   ON ls.peer_id = p.id AND ls.rn = 1
    LEFT JOIN local_seen loc ON loc.peer_id = p.id
    LEFT JOIN peers o       ON o.id  = p.owner_id
)`

const peerColumns = `
    id, name, role, owner_id, owner_name, group_id, group_name,
    public_key, private_key, allowed_ips, client_endpoint, preferred_endpoint,
    allowed_usage, expires_at, total_tx, total_rx, manually_disabled,
    last_handshake_at, last_endpoint, note, usage_reset_at, created_at, updated_at,
    usage, g_allowed_usage, g_expires_at, g_used, shared_count,
    seen_on_id, seen_on, local_handshake_at, status`

func scanPeer(sc interface{ Scan(...any) error }) (*Peer, error) {
	var p Peer
	err := sc.Scan(
		&p.ID, &p.Name, &p.Role, &p.OwnerID, &p.OwnerName, &p.GroupID, &p.GroupName,
		&p.PublicKey, &p.PrivateKey, &p.AllowedIPs, &p.ClientEndpoint, &p.PreferredEndpoint,
		&p.AllowedUsage, &p.ExpiresAt, &p.TotalTX, &p.TotalRX, &p.ManuallyDisabled,
		&p.LastHandshakeAt, &p.LastEndpoint, &p.Note, &p.UsageResetAt, &p.CreatedAt, &p.UpdatedAt,
		&p.Usage, &p.GroupAllowedUsage, &p.GroupExpiresAt, &p.GroupUsage, &p.SharedCount,
		&p.SeenOnID, &p.SeenOn, &p.LocalHandshakeAt, &p.Status,
	)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// Scope limits a query to what the requesting peer is allowed to see.
type Scope struct {
	PeerID string
	Role   string
}

// where returns the visibility predicate for this scope, or "" for an admin.
func (s Scope) where() (string, []any) {
	switch s.Role {
	case "admin":
		return "", nil
	case "distributor":
		// Its own row, anything it owns, and the members of the groups it owns.
		return `(id = ? OR owner_id = ? OR group_id IN (SELECT id FROM groups WHERE owner_id = ?))`,
			[]any{s.PeerID, s.PeerID, s.PeerID}
	default:
		// A plain user sees itself plus whatever has been shared with it.
		return `(id = ? OR id IN (SELECT target_id FROM peer_visibility WHERE viewer_id = ?))`,
			[]any{s.PeerID, s.PeerID}
	}
}

// PeerFilter describes one page of the peer list.
type PeerFilter struct {
	Scope    Scope
	Search   string // matches name, allowed IP or public key
	Status   string // active | disabled | expired | quota | online | offline | never
	GroupID  *int64 // -1 selects peers with no group
	Role     string
	OwnerID  string
	Sort     string // name | expiry | usage | remaining | handshake | status | created
	Desc     bool
	Page     int // 1-based
	PageSize int // <= 0 returns every match, used when sorting on live speed
	Now      int64
}

// peerSortColumns maps a sort key to the ORDER BY expressions it expands to.
// Unlimited quota and "never expires" are pushed to the end of an ascending sort
// by a leading flag expression, so "expiring soonest" does not put the peers
// that never expire first.
var peerSortColumns = map[string][]string{
	"name":      {"name COLLATE NOCASE"},
	"expiry":    {"CASE WHEN eff_expires_at = 0 THEN 1 ELSE 0 END", "eff_expires_at"},
	"usage":     {"usage"},
	"remaining": {"CASE WHEN eff_allowed_usage = 0 THEN 1 ELSE 0 END", "(eff_allowed_usage - eff_usage)"},
	"handshake": {"last_handshake_at"},
	"status":    {"status"},
	"created":   {"created_at"},
	"group":     {"group_name COLLATE NOCASE"},
	"ip":        {"allowed_ips"},
}

// SortableFields lists the sort keys the API accepts. "speed" is handled above
// the store because live speed is never persisted.
func SortableFields() []string {
	out := make([]string, 0, len(peerSortColumns)+1)
	for k := range peerSortColumns {
		out = append(out, k)
	}
	return append(out, "speed")
}

// ListPeers returns one page of peers plus the total number of matches.
func (s *Store) ListPeers(f PeerFilter) ([]*Peer, int, error) {
	args := []any{f.Now, f.Now}
	var conds []string

	if w, a := f.Scope.where(); w != "" {
		conds = append(conds, w)
		args = append(args, a...)
	}
	if q := strings.TrimSpace(f.Search); q != "" {
		like := "%" + q + "%"
		conds = append(conds, `(name LIKE ? OR allowed_ips LIKE ? OR public_key LIKE ? OR note LIKE ?)`)
		args = append(args, like, like, like, like)
	}
	switch f.Status {
	case StatusActive, StatusDisabled, StatusExpired, StatusQuota:
		conds = append(conds, `status = ?`)
		args = append(args, f.Status)
	case "online":
		// Anywhere in the fleet, which is what "who is connected" means once
		// there is more than one server.
		conds = append(conds, `last_handshake_at >= ?`)
		args = append(args, f.Now-onlineWindowMS)
	case "online-here":
		conds = append(conds, `local_handshake_at >= ?`)
		args = append(args, f.Now-onlineWindowMS)
	case "online-elsewhere":
		conds = append(conds, `last_handshake_at >= ? AND local_handshake_at < ?`)
		args = append(args, f.Now-onlineWindowMS, f.Now-onlineWindowMS)
	case "offline":
		conds = append(conds, `last_handshake_at < ?`)
		args = append(args, f.Now-onlineWindowMS)
	case "never":
		conds = append(conds, `last_handshake_at = 0`)
	}
	if f.GroupID != nil {
		if *f.GroupID < 0 {
			conds = append(conds, `group_id = 0`)
		} else {
			conds = append(conds, `group_id = ?`)
			args = append(args, *f.GroupID)
		}
	}
	if f.Role != "" {
		conds = append(conds, `role = ?`)
		args = append(args, f.Role)
	}
	if f.OwnerID != "" {
		conds = append(conds, `owner_id = ?`)
		args = append(args, f.OwnerID)
	}

	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}

	exprs, ok := peerSortColumns[f.Sort]
	if !ok {
		exprs = peerSortColumns["name"]
	}
	dir := "ASC"
	if f.Desc {
		dir = "DESC"
	}
	terms := make([]string, 0, len(exprs)+1)
	for _, e := range exprs {
		terms = append(terms, e+" "+dir)
	}
	// Break ties on name so paging is stable.
	terms = append(terms, "name COLLATE NOCASE ASC")
	orderBy := " ORDER BY " + strings.Join(terms, ", ")

	limit := ""
	if f.PageSize > 0 {
		page := f.Page
		if page < 1 {
			page = 1
		}
		limit = " LIMIT ? OFFSET ?"
		args = append(args, f.PageSize, (page-1)*f.PageSize)
	}

	query := peerCTE + ` SELECT ` + peerColumns + `, COUNT(*) OVER () AS total
	                     FROM v` + where + orderBy + limit

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list peers: %w", err)
	}
	defer rows.Close()

	out := []*Peer{}
	total := 0
	for rows.Next() {
		var p Peer
		err := rows.Scan(
			&p.ID, &p.Name, &p.Role, &p.OwnerID, &p.OwnerName, &p.GroupID, &p.GroupName,
			&p.PublicKey, &p.PrivateKey, &p.AllowedIPs, &p.ClientEndpoint, &p.PreferredEndpoint,
			&p.AllowedUsage, &p.ExpiresAt, &p.TotalTX, &p.TotalRX, &p.ManuallyDisabled,
			&p.LastHandshakeAt, &p.LastEndpoint, &p.Note, &p.UsageResetAt, &p.CreatedAt, &p.UpdatedAt,
			&p.Usage, &p.GroupAllowedUsage, &p.GroupExpiresAt, &p.GroupUsage, &p.SharedCount,
			&p.SeenOnID, &p.SeenOn, &p.LocalHandshakeAt, &p.Status,
			&total,
		)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, &p)
	}
	return out, total, rows.Err()
}

// GetPeer looks a peer up by id (its public key).
func (s *Store) GetPeer(id string, now int64) (*Peer, error) {
	row := s.db.QueryRow(peerCTE+` SELECT `+peerColumns+` FROM v WHERE id = ?`, now, now, id)
	p, err := scanPeer(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

// GetPeerByAllowedIP resolves the caller of a request to the peer that owns the
// tunnel address the request came from.
func (s *Store) GetPeerByAllowedIP(allowedIPs string, now int64) (*Peer, error) {
	row := s.db.QueryRow(peerCTE+` SELECT `+peerColumns+` FROM v WHERE allowed_ips = ?`, now, now, allowedIPs)
	p, err := scanPeer(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

// CanSee reports whether viewer is allowed to read target.
func (s *Store) CanSee(viewer Scope, targetID string) (bool, error) {
	if viewer.Role == "admin" || viewer.PeerID == targetID {
		return true, nil
	}
	w, args := viewer.where()
	args = append(args, targetID)
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM (
		SELECT id, COALESCE(owner_id,'') AS owner_id, COALESCE(group_id,0) AS group_id FROM peers
	) WHERE `+w+` AND id = ?`, args...).Scan(&n)
	return n > 0, err
}

// UsedAllowedIPs returns every tunnel address already handed out, so the
// allocator can pick the next free one.
func (s *Store) UsedAllowedIPs() (map[string]bool, error) {
	rows, err := s.db.Query(`SELECT allowed_ips FROM peers`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]bool{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out[v] = true
	}
	return out, rows.Err()
}

// CreatePeer inserts a peer. ErrDuplicate is returned when the name or tunnel
// address is already taken.
func (s *Store) CreatePeer(p *Peer) error {
	ts := now()
	p.CreatedAt, p.UpdatedAt = ts, ts

	_, err := s.db.Exec(
		`INSERT INTO peers (id, name, role, owner_id, group_id, public_key, private_key,
		                    allowed_ips, client_endpoint, preferred_endpoint, allowed_usage,
		                    expires_at, manually_disabled,
		                    last_handshake_at, last_endpoint, note, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, '', ?, ?, ?)`,
		p.ID, p.Name, p.Role, nullString(p.OwnerID), nullInt(p.GroupID), p.PublicKey, p.PrivateKey,
		p.AllowedIPs, p.ClientEndpoint, p.PreferredEndpoint, p.AllowedUsage,
		p.ExpiresAt, p.ManuallyDisabled, p.Note, p.CreatedAt, p.UpdatedAt,
	)
	if isUnique(err) {
		return ErrDuplicate
	}
	return err
}

// PeerPatch is a partial update; nil fields are left alone.
type PeerPatch struct {
	Name              *string
	Role              *string
	OwnerID           *string // "" detaches the owner
	GroupID           *int64  // 0 detaches the group
	ClientEndpoint    *string
	PreferredEndpoint *string
	AllowedUsage      *int64
	ExpiresAt         *int64
	ManuallyDisabled  *bool
	Note              *string
	ResetUsage        bool
}

func (p PeerPatch) assignments() ([]string, []any) {
	var sets []string
	var args []any
	add := func(col string, v any) { sets = append(sets, col+" = ?"); args = append(args, v) }

	if p.Name != nil {
		add("name", *p.Name)
	}
	if p.Role != nil {
		add("role", *p.Role)
	}
	if p.OwnerID != nil {
		add("owner_id", nullString(*p.OwnerID))
	}
	if p.GroupID != nil {
		add("group_id", nullInt(*p.GroupID))
	}
	if p.ClientEndpoint != nil {
		add("client_endpoint", *p.ClientEndpoint)
	}
	if p.PreferredEndpoint != nil {
		add("preferred_endpoint", *p.PreferredEndpoint)
	}
	if p.AllowedUsage != nil {
		add("allowed_usage", *p.AllowedUsage)
	}
	if p.ExpiresAt != nil {
		add("expires_at", *p.ExpiresAt)
	}
	if p.ManuallyDisabled != nil {
		add("manually_disabled", *p.ManuallyDisabled)
	}
	if p.Note != nil {
		add("note", *p.Note)
	}
	return sets, args
}

// UpdatePeers applies the same patch to every id in one transaction, so a bulk
// edit either lands completely or not at all.
func (s *Store) UpdatePeers(ids []string, patch PeerPatch) (int64, error) {
	sets, args := patch.assignments()
	if len(ids) == 0 || (len(sets) == 0 && !patch.ResetUsage) {
		return 0, nil
	}
	sets = append(sets, "updated_at = ?")
	args = append(args, now())

	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	if patch.ResetUsage {
		if err := resetUsage(tx, ids); err != nil {
			return 0, err
		}
		sets = append(sets, "usage_reset_at = ?")
		args = append(args, now())
	}

	query := `UPDATE peers SET ` + strings.Join(sets, ", ") +
		` WHERE id IN (` + placeholders(len(ids)) + `)`
	for _, id := range ids {
		args = append(args, id)
	}

	res, err := tx.Exec(query, args...)
	if isUnique(err) {
		return 0, ErrDuplicate
	}
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return n, tx.Commit()
}

// resetUsage clears what every server has counted for these peers, not just
// this one — the operator is zeroing the peer, not this server's share of it.
// The caller stamps usage_reset_at alongside, which is what stops a server that
// is still reporting pre-reset counters from restoring them.
func resetUsage(tx *sql.Tx, ids []string) error {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	_, err := tx.Exec(`DELETE FROM peer_usage WHERE peer_id IN (`+placeholders(len(ids))+`)`, args...)
	return err
}

// DeletePeers removes peers and, through the foreign keys, any sharing grants
// that referenced them.
func (s *Store) DeletePeers(ids []string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	res, err := s.db.Exec(`DELETE FROM peers WHERE id IN (`+placeholders(len(ids))+`)`, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ApplyUsageDeltas folds the engine's accumulated counters into the stored
// totals. This is the only place peer usage is mutated, which is the seam that
// per-server accounting would replace.
func (s *Store) ApplyUsageDeltas(deltas []UsageDelta) error {
	if len(deltas) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Counters go into this server's own row, which nothing else ever writes.
	//
	// Selecting the peer rather than naming it means a delta for one that has
	// since been deleted is dropped instead of failing the key — and a single
	// stale delta must never cost every other peer its traffic, which is what a
	// rolled-back flush would do on every tick from then on.
	usage, err := tx.Prepare(`INSERT INTO peer_usage
	        (peer_id, server_id, tx, rx, updated_at, last_handshake_at, last_endpoint)
	    SELECT id, ?, ?, ?, ?, ?, ? FROM peers WHERE id = ?
	    ON CONFLICT (peer_id, server_id) DO UPDATE SET
	        tx = tx + excluded.tx, rx = rx + excluded.rx, updated_at = excluded.updated_at,
	        last_handshake_at = MAX(last_handshake_at, excluded.last_handshake_at),
	        last_endpoint = CASE WHEN excluded.last_endpoint <> ''
	                             THEN excluded.last_endpoint ELSE last_endpoint END`)
	if err != nil {
		return err
	}
	defer usage.Close()

	// The peer row carries the newest handshake from anywhere, which is what a
	// fleet's "last seen" means and what sorting on it should answer. Which
	// server saw it is in the row above.
	seen, err := tx.Prepare(`UPDATE peers SET
	    last_handshake_at = MAX(last_handshake_at, ?),
	    last_endpoint = CASE WHEN ? <> '' THEN ? ELSE last_endpoint END
	    WHERE id = ?`)
	if err != nil {
		return err
	}
	defer seen.Close()

	at := now()
	var sent, received int64
	for _, d := range deltas {
		// Everything the device carried counts towards the server's traffic,
		// including a peer deleted since: the bytes crossed the wire either way.
		sent += d.TX
		received += d.RX
		if d.TX != 0 || d.RX != 0 || d.LastHandshakeAt != 0 {
			if _, err := usage.Exec(s.serverID, d.TX, d.RX, at,
				d.LastHandshakeAt, d.LastEndpoint, d.PeerID); err != nil {
				return err
			}
		}
		if _, err := seen.Exec(d.LastHandshakeAt, d.LastEndpoint, d.LastEndpoint, d.PeerID); err != nil {
			return err
		}
	}
	if err := recordTraffic(tx, at, sent, received); err != nil {
		return err
	}
	return tx.Commit()
}

// EnginePeer is the slice of a peer the enforcement loop needs: which key to
// configure, whether it should be reachable, and where to send its traffic.
type EnginePeer struct {
	ID                string
	PublicKey         string
	AllowedIPs        string
	PreferredEndpoint string
	Status            string
}

func (e EnginePeer) ShouldBeEnabled() bool { return e.Status == StatusActive }

// EnginePeers returns every peer with its effective status evaluated.
func (s *Store) EnginePeers(now int64) ([]EnginePeer, error) {
	rows, err := s.db.Query(peerCTE+
		` SELECT id, public_key, allowed_ips, preferred_endpoint, status FROM v`, now, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []EnginePeer
	for rows.Next() {
		var e EnginePeer
		if err := rows.Scan(&e.ID, &e.PublicKey, &e.AllowedIPs, &e.PreferredEndpoint, &e.Status); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// -- sharing ----------------------------------------------------------------

// Visibility lists the peers a user has been granted read access to.
func (s *Store) Visibility(viewerID string) ([]string, error) {
	rows, err := s.db.Query(`SELECT target_id FROM peer_visibility WHERE viewer_id = ?`, viewerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// SetVisibility replaces the grant list for a viewer.
func (s *Store) SetVisibility(viewerID string, targetIDs []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM peer_visibility WHERE viewer_id = ?`, viewerID); err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO peer_visibility (viewer_id, target_id) VALUES (?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, t := range targetIDs {
		if t == viewerID {
			continue // a peer always sees itself
		}
		if _, err := stmt.Exec(viewerID, t); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// -- helpers ----------------------------------------------------------------

func nullString(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func nullInt(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// StatusCounts is the breakdown the dashboard shows.
type StatusCounts struct {
	Total    int
	Active   int
	Disabled int
	Expired  int
	Quota    int
	// Online is how many are connected to this server. OnlineAnywhere counts
	// those connected to any server in the fleet, which on a single server is
	// the same number and on a node is emphatically not.
	Online         int
	OnlineAnywhere int
	Usage          int64
}

// CountPeers aggregates the peer list in SQL rather than returning every row for
// the caller to tally, since the dashboard asks for this every few seconds.
func (s *Store) CountPeers(scope Scope, now int64) (StatusCounts, error) {
	var c StatusCounts

	// Parameters bind by their position in the statement, so they are assembled
	// in the order they appear in it: the CTE first, then the two windows in
	// the select list, then whatever the scope adds to the where clause.
	args := []any{now, now, now - onlineWindowMS, now - onlineWindowMS}
	where := ""
	if w, a := scope.where(); w != "" {
		where = " WHERE " + w
		args = append(args, a...)
	}

	err := s.db.QueryRow(peerCTE+`
		SELECT
			COUNT(*),
			COALESCE(SUM(status = 'active'), 0),
			COALESCE(SUM(status = 'disabled'), 0),
			COALESCE(SUM(status = 'expired'), 0),
			COALESCE(SUM(status = 'quota'), 0),
			COALESCE(SUM(local_handshake_at >= ?), 0),
			COALESCE(SUM(last_handshake_at >= ?), 0),
			COALESCE(SUM(usage), 0)
		FROM v`+where, args...,
	).Scan(&c.Total, &c.Active, &c.Disabled, &c.Expired, &c.Quota,
		&c.Online, &c.OnlineAnywhere, &c.Usage)
	return c, err
}

// CountGroups returns how many groups the caller can see.
func (s *Store) CountGroups(scope Scope) (int, error) {
	where, args, allowed := groupScopeWhere(scope)
	if !allowed {
		return 0, nil
	}
	query := `SELECT COUNT(*) FROM groups`
	if where != "" {
		query += " WHERE " + where
	}
	var n int
	err := s.db.QueryRow(query, args...).Scan(&n)
	return n, err
}

// ServerUsage is one server's count for one peer: absolute totals, not a
// delta, so a snapshot that is late, repeated or missed entirely costs nothing.
type ServerUsage struct {
	PeerID string `json:"peerId"`
	TX     int64  `json:"tx"`
	RX     int64  `json:"rx"`
	// Where and when this server last carried the peer, so another server can
	// say which one it is actually connected to.
	LastHandshakeAt int64  `json:"lastHandshakeAt"`
	LastEndpoint    string `json:"lastEndpoint"`
	// ResetAt is the latest reset of this peer the reporting server knew about
	// when it read the count. A count is only as new as the reset it was
	// measured from: a server that has not yet heard of a reset is still
	// reporting traffic from before it, however recently it read the number.
	// Nil for a server too old to say, which is judged by the snapshot's time.
	ResetAt *int64 `json:"resetAt,omitempty"`
}

// ReplaceServerUsage takes another server's whole view of what it has counted
// and makes it this database's record for that server. Only rows labelled with
// serverID are touched, so servers can never overwrite each other's counts.
//
// A count is skipped when it was measured from before the peer's latest reset:
// applying it would quietly restore usage an operator had cleared. What decides
// that is the reset the reporting server says it had seen, not when it read the
// count — a node goes on counting and reporting old traffic right up to the
// sync that tells it about the reset. takenAt stands in for servers that do not
// say.
func (s *Store) ReplaceServerUsage(serverID string, takenAt int64, rows []ServerUsage) error {
	if serverID == "" || serverID == s.serverID {
		return errors.New("a server may only be given another server's usage")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM peer_usage WHERE server_id = ?`, serverID); err != nil {
		return err
	}

	stmt, err := tx.Prepare(`INSERT INTO peer_usage
	        (peer_id, server_id, tx, rx, updated_at, last_handshake_at, last_endpoint)
	    SELECT id, ?, ?, ?, ?, ?, ? FROM peers WHERE id = ? AND usage_reset_at <= ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	// A handshake seen elsewhere is still the peer's newest, so the row that
	// answers "last seen" follows it.
	touch, err := tx.Prepare(
		`UPDATE peers SET last_handshake_at = MAX(last_handshake_at, ?) WHERE id = ?`)
	if err != nil {
		return err
	}
	defer touch.Close()

	at := now()
	for _, r := range rows {
		if r.TX == 0 && r.RX == 0 && r.LastHandshakeAt == 0 {
			continue
		}
		measuredFrom := takenAt
		if r.ResetAt != nil {
			measuredFrom = *r.ResetAt
		}
		if _, err := stmt.Exec(serverID, r.TX, r.RX, at,
			r.LastHandshakeAt, r.LastEndpoint, r.PeerID, measuredFrom); err != nil {
			return err
		}
		if _, err := touch.Exec(r.LastHandshakeAt, r.PeerID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ServerUsageSnapshot is everything this server has counted, ready to be sent
// to another one, with the time it was read.
func (s *Store) ServerUsageSnapshot() ([]ServerUsage, int64, error) {
	takenAt := now()
	rows, err := s.db.Query(
		`SELECT u.peer_id, u.tx, u.rx, u.last_handshake_at, u.last_endpoint, p.usage_reset_at
		 FROM peer_usage u JOIN peers p ON p.id = u.peer_id
		 WHERE u.server_id = ?`, s.serverID)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := []ServerUsage{}
	for rows.Next() {
		var u ServerUsage
		var resetAt int64
		if err := rows.Scan(&u.PeerID, &u.TX, &u.RX, &u.LastHandshakeAt, &u.LastEndpoint, &resetAt); err != nil {
			return nil, 0, err
		}
		u.ResetAt = &resetAt
		out = append(out, u)
	}
	return out, takenAt, rows.Err()
}
