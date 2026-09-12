package store

import (
	"database/sql"
	"errors"
	"strings"
)

// Monitor watches a destination and runs a script when it stops answering.
type Monitor struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	Target         string `json:"target"`
	IntervalSec    int    `json:"intervalSec"`
	TimeoutSec     int    `json:"timeoutSec"`
	FailuresBefore int    `json:"failuresBefore"`
	ScriptID       int64  `json:"scriptId"` // 0 = no script, just watch
	ScriptName     string `json:"scriptName"`
	// RearmOnRecovery keeps a fired monitor quiet until the target answers
	// again, so one outage runs the script once rather than on every tick.
	RearmOnRecovery bool `json:"rearmOnRecovery"`
	Enabled         bool `json:"enabled"`

	ConsecutiveFailures int    `json:"consecutiveFailures"`
	LastCheckedAt       int64  `json:"lastCheckedAt"`
	LastOKAt            int64  `json:"lastOkAt"`
	LastRTTMS           int64  `json:"lastRttMs"`
	LastError           string `json:"lastError"`
	FiredAt             int64  `json:"firedAt"`
	CreatedAt           int64  `json:"createdAt"`
	UpdatedAt           int64  `json:"updatedAt"`
}

// Healthy reports whether the destination is currently answering.
func (m *Monitor) Healthy() bool { return m.ConsecutiveFailures == 0 && m.LastCheckedAt > 0 }

const monitorColumns = `
	m.id, m.name, m.target, m.interval_sec, m.timeout_sec, m.failures_before,
	COALESCE(m.script_id, 0), COALESCE(s.name, ''), m.rearm_on_recovery, m.enabled,
	m.consecutive_failures, m.last_checked_at, m.last_ok_at, m.last_rtt_ms,
	m.last_error, m.fired_at, m.created_at, m.updated_at`

func scanMonitor(sc interface{ Scan(...any) error }) (*Monitor, error) {
	var m Monitor
	err := sc.Scan(&m.ID, &m.Name, &m.Target, &m.IntervalSec, &m.TimeoutSec, &m.FailuresBefore,
		&m.ScriptID, &m.ScriptName, &m.RearmOnRecovery, &m.Enabled,
		&m.ConsecutiveFailures, &m.LastCheckedAt, &m.LastOKAt, &m.LastRTTMS,
		&m.LastError, &m.FiredAt, &m.CreatedAt, &m.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (s *Store) ListMonitors() ([]*Monitor, error) {
	rows, err := s.db.Query(`SELECT ` + monitorColumns + `
		FROM monitors m LEFT JOIN scripts s ON s.id = m.script_id
		ORDER BY m.name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*Monitor{}
	for rows.Next() {
		m, err := scanMonitor(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) GetMonitor(id int64) (*Monitor, error) {
	row := s.db.QueryRow(`SELECT `+monitorColumns+`
		FROM monitors m LEFT JOIN scripts s ON s.id = m.script_id WHERE m.id = ?`, id)
	m, err := scanMonitor(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return m, err
}

func (s *Store) CreateMonitor(m *Monitor) error {
	ts := now()
	m.CreatedAt, m.UpdatedAt = ts, ts

	res, err := s.db.Exec(
		`INSERT INTO monitors (name, target, interval_sec, timeout_sec, failures_before,
		                       script_id, rearm_on_recovery, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.Name, m.Target, m.IntervalSec, m.TimeoutSec, m.FailuresBefore,
		nullInt(m.ScriptID), m.RearmOnRecovery, m.Enabled, m.CreatedAt, m.UpdatedAt)
	if isUnique(err) {
		return ErrDuplicate
	}
	if err != nil {
		return err
	}
	m.ID, err = res.LastInsertId()
	return err
}

// MonitorPatch is a partial update; nil fields are left alone.
type MonitorPatch struct {
	Name            *string
	Target          *string
	IntervalSec     *int
	TimeoutSec      *int
	FailuresBefore  *int
	ScriptID        *int64
	RearmOnRecovery *bool
	Enabled         *bool
	// ResetState clears the failure count and the fired marker, which is what
	// "try again now" means.
	ResetState bool
}

func (s *Store) UpdateMonitor(id int64, patch MonitorPatch) error {
	var sets []string
	var args []any
	add := func(col string, v any) { sets = append(sets, col+" = ?"); args = append(args, v) }

	if patch.Name != nil {
		add("name", *patch.Name)
	}
	if patch.Target != nil {
		add("target", *patch.Target)
	}
	if patch.IntervalSec != nil {
		add("interval_sec", *patch.IntervalSec)
	}
	if patch.TimeoutSec != nil {
		add("timeout_sec", *patch.TimeoutSec)
	}
	if patch.FailuresBefore != nil {
		add("failures_before", *patch.FailuresBefore)
	}
	if patch.ScriptID != nil {
		add("script_id", nullInt(*patch.ScriptID))
	}
	if patch.RearmOnRecovery != nil {
		add("rearm_on_recovery", *patch.RearmOnRecovery)
	}
	if patch.Enabled != nil {
		add("enabled", *patch.Enabled)
	}
	if patch.ResetState {
		add("consecutive_failures", 0)
		add("fired_at", int64(0))
		add("last_error", "")
	}
	if len(sets) == 0 {
		return nil
	}

	sets = append(sets, "updated_at = ?")
	args = append(args, now(), id)

	res, err := s.db.Exec(`UPDATE monitors SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
	if isUnique(err) {
		return ErrDuplicate
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteMonitor(id int64) error {
	res, err := s.db.Exec(`DELETE FROM monitors WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// MonitorResult is the outcome of one check, written back by the prober.
type MonitorResult struct {
	ID        int64
	OK        bool
	RTTMS     int64
	Error     string
	CheckedAt int64
	// Failures is the running count after this check.
	Failures int
	// Fired marks the moment the script was set off.
	Fired bool
}

// RecordMonitorResult saves the outcome of a check.
func (s *Store) RecordMonitorResult(r MonitorResult) error {
	if r.Fired {
		_, err := s.db.Exec(`UPDATE monitors SET
			consecutive_failures = ?, last_checked_at = ?, last_rtt_ms = ?, last_error = ?, fired_at = ?
			WHERE id = ?`, r.Failures, r.CheckedAt, r.RTTMS, r.Error, r.CheckedAt, r.ID)
		return err
	}
	if r.OK {
		_, err := s.db.Exec(`UPDATE monitors SET
			consecutive_failures = 0, last_checked_at = ?, last_ok_at = ?, last_rtt_ms = ?,
			last_error = '', fired_at = 0
			WHERE id = ?`, r.CheckedAt, r.CheckedAt, r.RTTMS, r.ID)
		return err
	}
	_, err := s.db.Exec(`UPDATE monitors SET
		consecutive_failures = ?, last_checked_at = ?, last_rtt_ms = 0, last_error = ?
		WHERE id = ?`, r.Failures, r.CheckedAt, r.Error, r.ID)
	return err
}
