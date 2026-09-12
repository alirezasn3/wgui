package store

import (
	"database/sql"
	"strconv"
	"time"
)

// trafficBucket is the resolution traffic is recorded at. A minute makes "the
// last hour" exact to within one bucket, and a month of them is a few tens of
// thousands of rows that sum in milliseconds.
const trafficBucket = time.Minute

// keepTrafficFor covers the longest window with a day to spare. Nothing asks
// about anything older.
const keepTrafficFor = 31 * 24 * time.Hour

// The windows a dashboard asks about.
const (
	windowHour  = time.Hour
	windowDay   = 24 * time.Hour
	windowWeek  = 7 * 24 * time.Hour
	windowMonth = 30 * 24 * time.Hour
)

// UsageWindows is how much a server carried over the recent past.
type UsageWindows struct {
	Hour  int64 `json:"hour"`
	Day   int64 `json:"day"`
	Week  int64 `json:"week"`
	Month int64 `json:"month"`
}

// recordTraffic adds what the device carried to the bucket for the minute it
// was counted in.
//
// This is the only honest source for "how much did this server carry lately".
// The per-peer totals cannot answer it: they go down whenever a peer or a group
// is reset or deleted, they include usage imported from another panel, and on a
// node they are cleared by the master — a window measured as the difference
// between two readings of them is wrong by all of that. Traffic recorded as it
// happens only ever grows, and only by bytes that actually crossed the device.
func recordTraffic(tx *sql.Tx, at, sent, received int64) error {
	if sent == 0 && received == 0 {
		return nil
	}
	minute := at - at%trafficBucket.Milliseconds()
	if _, err := tx.Exec(`
	    INSERT INTO traffic (minute, tx, rx) VALUES (?, ?, ?)
	    ON CONFLICT (minute) DO UPDATE SET tx = tx + excluded.tx, rx = rx + excluded.rx`,
		minute, sent, received); err != nil {
		return err
	}
	_, err := tx.Exec(`DELETE FROM traffic WHERE minute < ?`, at-keepTrafficFor.Milliseconds())
	return err
}

// TrafficWindows reports what this server carried over the last hour, day,
// week and month, as of now.
//
// A bucket belongs to a window when the minute it covers started inside it, so
// each window is exact to within one minute.
func (s *Store) TrafficWindows(now int64) (UsageWindows, error) {
	var w UsageWindows
	since := func(d time.Duration) int64 { return now - d.Milliseconds() }
	err := s.db.QueryRow(`
	    SELECT COALESCE(SUM(CASE WHEN minute >= ? THEN tx + rx END), 0),
	           COALESCE(SUM(CASE WHEN minute >= ? THEN tx + rx END), 0),
	           COALESCE(SUM(CASE WHEN minute >= ? THEN tx + rx END), 0),
	           COALESCE(SUM(tx + rx), 0)
	    FROM traffic WHERE minute >= ?`,
		since(windowHour), since(windowDay), since(windowWeek), since(windowMonth),
	).Scan(&w.Hour, &w.Day, &w.Week, &w.Month)
	return w, err
}

// ResetTraffic starts this server's traffic counters again from zero. Peer
// usage is untouched: this is the server's own record of what it carried, not
// anybody's quota.
func (s *Store) ResetTraffic() error {
	_, err := s.db.Exec(`DELETE FROM traffic`)
	return err
}

// trafficResetKey remembers the last reset a master asked this server for, so
// a request that arrives with every sync is acted on once rather than wiping
// the counters over and over.
const trafficResetKey = "traffic_reset_applied"

// TrafficResetApplied is the master's reset request this server last carried
// out, 0 if it has never been asked.
func (s *Store) TrafficResetApplied() (int64, error) {
	v, ok, err := s.Secret(trafficResetKey)
	if err != nil || !ok {
		return 0, err
	}
	n, _ := strconv.ParseInt(string(v), 10, 64)
	return n, nil
}

// ApplyTrafficReset carries out a master's request to reset this server's
// counters, if it is one this server has not already acted on. The request is
// the master's own timestamp and is only ever compared with the last one
// applied, never with this server's clock, so skew between the two machines
// cannot make a request look stale.
func (s *Store) ApplyTrafficReset(requestedAt int64) (bool, error) {
	if requestedAt == 0 {
		return false, nil
	}
	applied, err := s.TrafficResetApplied()
	if err != nil || requestedAt <= applied {
		return false, err
	}
	if err := s.ResetTraffic(); err != nil {
		return false, err
	}
	return true, s.PutSecret(trafficResetKey, []byte(strconv.FormatInt(requestedAt, 10)))
}

// RequestTrafficReset asks nodes to reset their counters at their next sync.
// Their figures here are cleared at once, so a dashboard reflects the request
// straight away instead of showing the old numbers until the node catches up.
// No ids means every node.
func (s *Store) RequestTrafficReset(ids []string, at int64) error {
	query := `UPDATE nodes SET traffic_reset_at = ?,
	              usage_hour = 0, usage_day = 0, usage_week = 0, usage_month = 0
	          WHERE role = 'node'`
	args := []any{at}
	if len(ids) > 0 {
		query += ` AND id IN (` + placeholders(len(ids)) + `)`
		for _, id := range ids {
			args = append(args, id)
		}
	}
	_, err := s.db.Exec(query, args...)
	return err
}

// PendingTrafficReset is the reset this server has asked of a node, 0 if none.
func (s *Store) PendingTrafficReset(nodeID string) (int64, error) {
	var at int64
	err := s.db.QueryRow(`SELECT traffic_reset_at FROM nodes WHERE id = ?`, nodeID).Scan(&at)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return at, err
}
