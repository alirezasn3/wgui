package store

import (
	"database/sql"
	"errors"
	"strings"
)

// Script is a shell script an operator saved to run from the panel.
type Script struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string `json:"body"`
	TimeoutSec  int    `json:"timeoutSec"`
	CreatedAt   int64  `json:"createdAt"`
	UpdatedAt   int64  `json:"updatedAt"`

	// Derived: the most recent run, so a list can show state without a second
	// request per row.
	LastRun *ScriptRun `json:"lastRun,omitempty"`
	Running bool       `json:"running"`
}

// ScriptRun is one execution.
type ScriptRun struct {
	ID        int64  `json:"id"`
	ScriptID  int64  `json:"scriptId"`
	StartedAt int64  `json:"startedAt"`
	EndedAt   int64  `json:"endedAt"`
	ExitCode  int    `json:"exitCode"`
	Output    string `json:"output"`
	Error     string `json:"error"`
	// Source says what set it off: a button, or a monitor.
	Source string `json:"source"`
	Actor  string `json:"actor"`
}

// runHistory is how many runs are kept per script. This is a panel, not a log
// store; the last handful is what anybody looks at.
const runHistory = 20

func (s *Store) ListScripts() ([]*Script, error) {
	rows, err := s.db.Query(`
		SELECT id, name, description, body, timeout_sec, created_at, updated_at
		FROM scripts ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*Script{}
	for rows.Next() {
		var v Script
		if err := rows.Scan(&v.ID, &v.Name, &v.Description, &v.Body, &v.TimeoutSec,
			&v.CreatedAt, &v.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for _, script := range out {
		run, err := s.LastScriptRun(script.ID)
		if err != nil {
			return nil, err
		}
		script.LastRun = run
	}
	return out, nil
}

func (s *Store) GetScript(id int64) (*Script, error) {
	var v Script
	err := s.db.QueryRow(`
		SELECT id, name, description, body, timeout_sec, created_at, updated_at
		FROM scripts WHERE id = ?`, id).
		Scan(&v.ID, &v.Name, &v.Description, &v.Body, &v.TimeoutSec, &v.CreatedAt, &v.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if v.LastRun, err = s.LastScriptRun(v.ID); err != nil {
		return nil, err
	}
	return &v, nil
}

func (s *Store) CreateScript(v *Script) error {
	ts := now()
	v.CreatedAt, v.UpdatedAt = ts, ts

	res, err := s.db.Exec(
		`INSERT INTO scripts (name, description, body, timeout_sec, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		v.Name, v.Description, v.Body, v.TimeoutSec, v.CreatedAt, v.UpdatedAt)
	if isUnique(err) {
		return ErrDuplicate
	}
	if err != nil {
		return err
	}
	v.ID, err = res.LastInsertId()
	return err
}

// ScriptPatch is a partial update; nil fields are left alone.
type ScriptPatch struct {
	Name        *string
	Description *string
	Body        *string
	TimeoutSec  *int
}

func (s *Store) UpdateScript(id int64, patch ScriptPatch) error {
	var sets []string
	var args []any
	add := func(col string, v any) { sets = append(sets, col+" = ?"); args = append(args, v) }

	if patch.Name != nil {
		add("name", *patch.Name)
	}
	if patch.Description != nil {
		add("description", *patch.Description)
	}
	if patch.Body != nil {
		add("body", *patch.Body)
	}
	if patch.TimeoutSec != nil {
		add("timeout_sec", *patch.TimeoutSec)
	}
	if len(sets) == 0 {
		return nil
	}

	sets = append(sets, "updated_at = ?")
	args = append(args, now(), id)

	res, err := s.db.Exec(`UPDATE scripts SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
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

func (s *Store) DeleteScript(id int64) error {
	res, err := s.db.Exec(`DELETE FROM scripts WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// RecordScriptRun saves a finished run and prunes the history for that script.
func (s *Store) RecordScriptRun(run *ScriptRun) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.Exec(
		`INSERT INTO script_runs (script_id, started_at, ended_at, exit_code, output, error, source, actor)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ScriptID, run.StartedAt, run.EndedAt, run.ExitCode, run.Output, run.Error, run.Source, run.Actor)
	if err != nil {
		return err
	}
	if run.ID, err = res.LastInsertId(); err != nil {
		return err
	}

	if _, err := tx.Exec(`
		DELETE FROM script_runs WHERE script_id = ? AND id NOT IN (
			SELECT id FROM script_runs WHERE script_id = ? ORDER BY started_at DESC, id DESC LIMIT ?
		)`, run.ScriptID, run.ScriptID, runHistory); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) LastScriptRun(scriptID int64) (*ScriptRun, error) {
	runs, err := s.ScriptRuns(scriptID, 1)
	if err != nil || len(runs) == 0 {
		return nil, err
	}
	return runs[0], nil
}

func (s *Store) ScriptRuns(scriptID int64, limit int) ([]*ScriptRun, error) {
	if limit <= 0 || limit > runHistory {
		limit = runHistory
	}
	rows, err := s.db.Query(`
		SELECT id, script_id, started_at, ended_at, exit_code, output, error, source, actor
		FROM script_runs WHERE script_id = ?
		ORDER BY started_at DESC, id DESC LIMIT ?`, scriptID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*ScriptRun{}
	for rows.Next() {
		var r ScriptRun
		if err := rows.Scan(&r.ID, &r.ScriptID, &r.StartedAt, &r.EndedAt, &r.ExitCode,
			&r.Output, &r.Error, &r.Source, &r.Actor); err != nil {
			return nil, err
		}
		out = append(out, &r)
	}
	return out, rows.Err()
}
