package store

import (
	"database/sql"
	"errors"
	"strings"
)

// groupCTE mirrors peerCTE: a group's usage is the sum of its members' usage
// rather than a counter of its own, so the two can never drift apart.
//
// It takes two bind parameters, both the current time in unix ms.
const groupCTE = `
WITH peer_totals AS (
    -- Every server's count for each peer, added up; see peerCTE.
    SELECT peer_id, SUM(tx) AS tx, SUM(rx) AS rx FROM peer_usage GROUP BY peer_id
),
member_usage AS (
    SELECT p.group_id AS gid,
           SUM(COALESCE(t.tx, 0)) AS tx, SUM(COALESCE(t.rx, 0)) AS rx, COUNT(*) AS members
    FROM peers p LEFT JOIN peer_totals t ON t.peer_id = p.id
    WHERE p.group_id IS NOT NULL GROUP BY p.group_id
),
v AS (
    SELECT
        g.id, g.name,
        COALESCE(g.owner_id, '')  AS owner_id,
        COALESCE(o.name, '')      AS owner_name,
        g.allowed_usage, g.expires_at, g.manually_disabled, g.note,
        g.created_at, g.updated_at,
        COALESCE(mu.members, 0)   AS peer_count,
        COALESCE(mu.tx, 0)        AS total_tx,
        COALESCE(mu.rx, 0)        AS total_rx,
        COALESCE(mu.tx, 0) + COALESCE(mu.rx, 0) AS usage,
        CASE
            WHEN g.manually_disabled = 1 THEN 'disabled'
            WHEN g.expires_at > 0 AND g.expires_at <= ? THEN 'expired'
            WHEN g.allowed_usage > 0
             AND COALESCE(mu.tx, 0) + COALESCE(mu.rx, 0) >= g.allowed_usage THEN 'quota'
            ELSE 'active'
        END AS status
    FROM groups g
    LEFT JOIN member_usage mu ON mu.gid = g.id
    LEFT JOIN peers o         ON o.id  = g.owner_id
)`

const groupColumns = `
    id, name, owner_id, owner_name, allowed_usage, expires_at, manually_disabled,
    note, created_at, updated_at, peer_count, total_tx, total_rx, usage, status`

func scanGroup(sc interface{ Scan(...any) error }) (*Group, error) {
	var g Group
	err := sc.Scan(
		&g.ID, &g.Name, &g.OwnerID, &g.OwnerName, &g.AllowedUsage, &g.ExpiresAt,
		&g.ManuallyDisabled, &g.Note, &g.CreatedAt, &g.UpdatedAt,
		&g.PeerCount, &g.TotalTX, &g.TotalRX, &g.Usage, &g.Status,
	)
	if err != nil {
		return nil, err
	}
	return &g, nil
}

// GroupFilter describes one page of the group list.
type GroupFilter struct {
	Scope    Scope
	Search   string
	Status   string
	Sort     string // name | usage | expiry | peers | created
	Desc     bool
	Page     int
	PageSize int
	Now      int64
}

var groupSortColumns = map[string][]string{
	"name":    {"name COLLATE NOCASE"},
	"usage":   {"usage"},
	"expiry":  {"CASE WHEN expires_at = 0 THEN 1 ELSE 0 END", "expires_at"},
	"peers":   {"peer_count"},
	"status":  {"status"},
	"created": {"created_at"},
}

// groupScope limits the listing to the groups a caller may see. A plain user has
// no business browsing groups at all.
func groupScopeWhere(s Scope) (string, []any, bool) {
	switch s.Role {
	case "admin":
		return "", nil, true
	case "distributor":
		return `owner_id = ?`, []any{s.PeerID}, true
	default:
		return "", nil, false
	}
}

func (s *Store) ListGroups(f GroupFilter) ([]*Group, int, error) {
	where, args, allowed := groupScopeWhere(f.Scope)
	if !allowed {
		return []*Group{}, 0, nil
	}

	args = append([]any{f.Now}, args...)
	var conds []string
	if where != "" {
		conds = append(conds, where)
	}
	if q := strings.TrimSpace(f.Search); q != "" {
		like := "%" + q + "%"
		conds = append(conds, `(name LIKE ? OR note LIKE ?)`)
		args = append(args, like, like)
	}
	switch f.Status {
	case StatusActive, StatusDisabled, StatusExpired, StatusQuota:
		conds = append(conds, `status = ?`)
		args = append(args, f.Status)
	}

	clause := ""
	if len(conds) > 0 {
		clause = " WHERE " + strings.Join(conds, " AND ")
	}

	exprs, ok := groupSortColumns[f.Sort]
	if !ok {
		exprs = groupSortColumns["name"]
	}
	dir := "ASC"
	if f.Desc {
		dir = "DESC"
	}
	terms := make([]string, 0, len(exprs)+1)
	for _, e := range exprs {
		terms = append(terms, e+" "+dir)
	}
	terms = append(terms, "name COLLATE NOCASE ASC")

	limit := ""
	if f.PageSize > 0 {
		page := f.Page
		if page < 1 {
			page = 1
		}
		limit = " LIMIT ? OFFSET ?"
		args = append(args, f.PageSize, (page-1)*f.PageSize)
	}

	rows, err := s.db.Query(groupCTE+` SELECT `+groupColumns+`, COUNT(*) OVER () AS total
	                                   FROM v`+clause+` ORDER BY `+strings.Join(terms, ", ")+limit, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := []*Group{}
	total := 0
	for rows.Next() {
		var g Group
		err := rows.Scan(
			&g.ID, &g.Name, &g.OwnerID, &g.OwnerName, &g.AllowedUsage, &g.ExpiresAt,
			&g.ManuallyDisabled, &g.Note, &g.CreatedAt, &g.UpdatedAt,
			&g.PeerCount, &g.TotalTX, &g.TotalRX, &g.Usage, &g.Status, &total,
		)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, &g)
	}
	return out, total, rows.Err()
}

func (s *Store) GetGroup(id int64, now int64) (*Group, error) {
	row := s.db.QueryRow(groupCTE+` SELECT `+groupColumns+` FROM v WHERE id = ?`, now, id)
	g, err := scanGroup(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return g, err
}

func (s *Store) CreateGroup(g *Group) error {
	ts := now()
	g.CreatedAt, g.UpdatedAt = ts, ts

	res, err := s.db.Exec(
		`INSERT INTO groups (name, owner_id, allowed_usage, expires_at, manually_disabled,
		                     note, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		g.Name, nullString(g.OwnerID), g.AllowedUsage, g.ExpiresAt, g.ManuallyDisabled,
		g.Note, g.CreatedAt, g.UpdatedAt,
	)
	if isUnique(err) {
		return ErrDuplicate
	}
	if err != nil {
		return err
	}
	g.ID, err = res.LastInsertId()
	return err
}

// GroupPatch is a partial update; nil fields are left alone.
type GroupPatch struct {
	Name             *string
	OwnerID          *string
	AllowedUsage     *int64
	ExpiresAt        *int64
	ManuallyDisabled *bool
	Note             *string
	// ResetUsage zeroes the members' counters, since a group has no counter of
	// its own to reset.
	ResetUsage bool
}

// UpdateGroups applies the same patch to every id in one transaction.
func (s *Store) UpdateGroups(ids []int64, patch GroupPatch) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}

	var sets []string
	var args []any
	add := func(col string, v any) { sets = append(sets, col+" = ?"); args = append(args, v) }

	if patch.Name != nil {
		add("name", *patch.Name)
	}
	if patch.OwnerID != nil {
		add("owner_id", nullString(*patch.OwnerID))
	}
	if patch.AllowedUsage != nil {
		add("allowed_usage", *patch.AllowedUsage)
	}
	if patch.ExpiresAt != nil {
		add("expires_at", *patch.ExpiresAt)
	}
	if patch.ManuallyDisabled != nil {
		add("manually_disabled", *patch.ManuallyDisabled)
	}
	if patch.Note != nil {
		add("note", *patch.Note)
	}

	if len(sets) == 0 && !patch.ResetUsage {
		return 0, nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	idArgs := make([]any, len(ids))
	for i, id := range ids {
		idArgs[i] = id
	}

	var affected int64
	if len(sets) > 0 {
		sets = append(sets, "updated_at = ?")
		args = append(args, now())
		args = append(args, idArgs...)

		res, err := tx.Exec(`UPDATE groups SET `+strings.Join(sets, ", ")+
			` WHERE id IN (`+placeholders(len(ids))+`)`, args...)
		if isUnique(err) {
			return 0, ErrDuplicate
		}
		if err != nil {
			return 0, err
		}
		affected, _ = res.RowsAffected()
	}

	if patch.ResetUsage {
		// Clear what every server counted for the members, and stamp when, so a
		// server still reporting pre-reset counters cannot restore them.
		if _, err := tx.Exec(`DELETE FROM peer_usage WHERE peer_id IN (
		                          SELECT id FROM peers WHERE group_id IN (`+placeholders(len(ids))+`))`,
			idArgs...); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`UPDATE peers SET usage_reset_at = ?, updated_at = ?
		                      WHERE group_id IN (`+placeholders(len(ids))+`)`,
			append([]any{now(), now()}, idArgs...)...); err != nil {
			return 0, err
		}
	}

	return affected, tx.Commit()
}

// DeleteGroups removes groups; members are detached by ON DELETE SET NULL and
// keep their own quota and expiry.
func (s *Store) DeleteGroups(ids []int64) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	res, err := s.db.Exec(`DELETE FROM groups WHERE id IN (`+placeholders(len(ids))+`)`, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
