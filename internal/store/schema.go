package store

import (
	"database/sql"
	"fmt"
	"log/slog"
	"sort"
	"strings"
)

// schemaVersion is bumped whenever a new migration is appended to migrations.
const schemaVersion = 8

// migrations[i] upgrades the database from user_version i to i+1.
var migrations = []string{
	// v0 -> v1: initial schema.
	`
CREATE TABLE groups (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    name              TEXT    NOT NULL UNIQUE,
    owner_id          TEXT    REFERENCES peers(id) ON DELETE SET NULL,
    allowed_usage     INTEGER NOT NULL DEFAULT 0,  -- bytes, 0 = unlimited
    expires_at        INTEGER NOT NULL DEFAULT 0,  -- unix ms, 0 = never
    manually_disabled INTEGER NOT NULL DEFAULT 0,
    note              TEXT    NOT NULL DEFAULT '',
    created_at        INTEGER NOT NULL,
    updated_at        INTEGER NOT NULL
);

CREATE TABLE peers (
    id                 TEXT    PRIMARY KEY,           -- equals public_key
    name               TEXT    NOT NULL UNIQUE,
    role               TEXT    NOT NULL DEFAULT 'user',
    owner_id           TEXT    REFERENCES peers(id) ON DELETE SET NULL,
    group_id           INTEGER REFERENCES groups(id) ON DELETE SET NULL,
    public_key         TEXT    NOT NULL UNIQUE,
    private_key        TEXT    NOT NULL DEFAULT '',
    allowed_ips        TEXT    NOT NULL UNIQUE,       -- e.g. 10.0.0.5/32
    client_endpoint    TEXT    NOT NULL DEFAULT '',   -- Endpoint= in the generated config
    preferred_endpoint TEXT    NOT NULL DEFAULT '',   -- pinned remote endpoint on the device
    allowed_usage      INTEGER NOT NULL DEFAULT 0,    -- bytes, 0 = unlimited
    expires_at         INTEGER NOT NULL DEFAULT 0,    -- unix ms, 0 = never
    total_tx           INTEGER NOT NULL DEFAULT 0,
    total_rx           INTEGER NOT NULL DEFAULT 0,
    manually_disabled  INTEGER NOT NULL DEFAULT 0,
    last_handshake_at  INTEGER NOT NULL DEFAULT 0,
    last_endpoint      TEXT    NOT NULL DEFAULT '',
    note               TEXT    NOT NULL DEFAULT '',
    created_at         INTEGER NOT NULL,
    updated_at         INTEGER NOT NULL
);

CREATE INDEX idx_peers_group  ON peers(group_id);
CREATE INDEX idx_peers_owner  ON peers(owner_id);
CREATE INDEX idx_peers_expiry ON peers(expires_at);

-- Read-only sharing: viewer_id may see target_id in the panel.
CREATE TABLE peer_visibility (
    viewer_id TEXT NOT NULL REFERENCES peers(id) ON DELETE CASCADE,
    target_id TEXT NOT NULL REFERENCES peers(id) ON DELETE CASCADE,
    PRIMARY KEY (viewer_id, target_id)
);
CREATE INDEX idx_visibility_target ON peer_visibility(target_id);

CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE ipinfo_cache (
    ip           TEXT PRIMARY KEY,
    asn          INTEGER NOT NULL DEFAULT 0,
    as_name      TEXT    NOT NULL DEFAULT '',
    org          TEXT    NOT NULL DEFAULT '',
    country      TEXT    NOT NULL DEFAULT '',
    country_code TEXT    NOT NULL DEFAULT '',
    cidr         TEXT    NOT NULL DEFAULT '',
    fetched_at   INTEGER NOT NULL
);

-- Exactly one row today. Present so that per-server usage accounting can be
-- added later without reshaping the rest of the schema.
CREATE TABLE servers (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    name           TEXT    NOT NULL UNIQUE,
    public_address TEXT    NOT NULL DEFAULT '',
    is_local       INTEGER NOT NULL DEFAULT 1
);
`,

	// v1 -> v2: somewhere to keep the generated TLS material, so the panel can
	// serve HTTPS without leaving a key file lying around. This table is never
	// exposed through the API.
	`
CREATE TABLE secrets (
    key   TEXT PRIMARY KEY,
    value BLOB NOT NULL
);
`,

	// v2 -> v3: operator scripts, and the reachability monitors that can run
	// them. Both are admin-only territory.
	`
CREATE TABLE scripts (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    name         TEXT    NOT NULL UNIQUE,
    description  TEXT    NOT NULL DEFAULT '',
    body         TEXT    NOT NULL DEFAULT '',
    timeout_sec  INTEGER NOT NULL DEFAULT 60,
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL
);

-- A bounded history: enough to see what happened last time, not a log store.
CREATE TABLE script_runs (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    script_id  INTEGER NOT NULL REFERENCES scripts(id) ON DELETE CASCADE,
    started_at INTEGER NOT NULL,
    ended_at   INTEGER NOT NULL DEFAULT 0,
    exit_code  INTEGER NOT NULL DEFAULT 0,
    output     TEXT    NOT NULL DEFAULT '',
    error      TEXT    NOT NULL DEFAULT '',
    source     TEXT    NOT NULL DEFAULT 'manual',
    actor      TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX idx_script_runs ON script_runs(script_id, started_at DESC);

CREATE TABLE monitors (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    name            TEXT    NOT NULL UNIQUE,
    target          TEXT    NOT NULL,
    interval_sec    INTEGER NOT NULL DEFAULT 30,
    timeout_sec     INTEGER NOT NULL DEFAULT 5,
    failures_before INTEGER NOT NULL DEFAULT 3,
    script_id       INTEGER REFERENCES scripts(id) ON DELETE SET NULL,
    -- Once it has fired, it stays quiet until the target answers again, so a
    -- long outage does not run the script on every tick.
    rearm_on_recovery INTEGER NOT NULL DEFAULT 1,
    enabled         INTEGER NOT NULL DEFAULT 1,

    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    last_checked_at INTEGER NOT NULL DEFAULT 0,
    last_ok_at      INTEGER NOT NULL DEFAULT 0,
    last_rtt_ms     INTEGER NOT NULL DEFAULT 0,
    last_error      TEXT    NOT NULL DEFAULT '',
    fired_at        INTEGER NOT NULL DEFAULT 0,
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL
);
`,
	// v3 -> v4: usage is counted per server rather than on the peer row, so
	// several wgui servers can carry the same peer and have its allowance
	// measured against everything it has spent across all of them.
	//
	// Each server only ever writes its own row, so the rows never conflict no
	// matter how syncs interleave, and a peer's usage is simply their sum. An
	// empty server_id means "this database's own server, not yet identified";
	// the store stamps it with the real id on the next open.
	`
CREATE TABLE peer_usage (
    peer_id    TEXT    NOT NULL REFERENCES peers(id) ON DELETE CASCADE,
    server_id  TEXT    NOT NULL,
    tx         INTEGER NOT NULL DEFAULT 0,
    rx         INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (peer_id, server_id)
);

INSERT INTO peer_usage (peer_id, server_id, tx, rx, updated_at)
SELECT id, '', total_tx, total_rx, updated_at FROM peers
WHERE total_tx <> 0 OR total_rx <> 0;

-- When a reset happened, so that a server reporting counters from before it
-- cannot quietly restore what was cleared.
ALTER TABLE peers ADD COLUMN usage_reset_at INTEGER NOT NULL DEFAULT 0;

ALTER TABLE peers DROP COLUMN total_tx;
ALTER TABLE peers DROP COLUMN total_rx;
`,
	// v4 -> v5: the nodes a master has heard from. Rows appear on their own the
	// first time a node syncs; there is nothing to register by hand.
	`
CREATE TABLE nodes (
    id           TEXT    PRIMARY KEY,   -- the node's own server id
    name         TEXT    NOT NULL DEFAULT '',
    version      TEXT    NOT NULL DEFAULT '',
    address      TEXT    NOT NULL DEFAULT '',
    peer_count   INTEGER NOT NULL DEFAULT 0,
    first_seen_at INTEGER NOT NULL DEFAULT 0,
    last_seen_at  INTEGER NOT NULL DEFAULT 0
);
`,
	// v5 -> v6: where a peer was last seen, per server. Usage says how much a
	// server carried; this says when it last carried any, and from what remote
	// address, so a peer can be found on whichever server it is actually using.
	`
ALTER TABLE peer_usage ADD COLUMN last_handshake_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE peer_usage ADD COLUMN last_endpoint     TEXT    NOT NULL DEFAULT '';

-- Seed each server's own row from what the peer row already knows, so a
-- database that has been running keeps its history rather than starting blank.
UPDATE peer_usage SET
    last_handshake_at = COALESCE((SELECT p.last_handshake_at FROM peers p WHERE p.id = peer_id), 0),
    last_endpoint     = COALESCE((SELECT p.last_endpoint     FROM peers p WHERE p.id = peer_id), '');

-- The table describes any other wgui this one knows about, in either direction:
-- a master lists the nodes that sync to it, a node lists the master it syncs
-- with. Both want to show the other on a dashboard.
ALTER TABLE nodes ADD COLUMN role TEXT NOT NULL DEFAULT 'node';

-- What the other server said about itself when it last synced. Overwritten
-- each time rather than accumulated: this is a status, not a history, and
-- last_seen_at says how much to trust it.
ALTER TABLE nodes ADD COLUMN online_peers INTEGER NOT NULL DEFAULT 0;
ALTER TABLE nodes ADD COLUMN tx_speed     INTEGER NOT NULL DEFAULT 0;
ALTER TABLE nodes ADD COLUMN rx_speed     INTEGER NOT NULL DEFAULT 0;
`,
	// v6 -> v7: enough history to answer "how much did this server carry in the
	// last day". The usage rows only ever hold a running total, which cannot be
	// windowed, so the total is sampled periodically and a window is the
	// difference between two samples.
	`
CREATE TABLE usage_samples (
    server_id TEXT    NOT NULL,
    at        INTEGER NOT NULL,   -- unix ms
    tx        INTEGER NOT NULL DEFAULT 0,
    rx        INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (server_id, at)
);

-- What each other server reported about its own recent load, so a dashboard can
-- show the fleet without asking each one in turn.
ALTER TABLE nodes ADD COLUMN usage_day   INTEGER NOT NULL DEFAULT 0;
ALTER TABLE nodes ADD COLUMN usage_week  INTEGER NOT NULL DEFAULT 0;
ALTER TABLE nodes ADD COLUMN usage_month INTEGER NOT NULL DEFAULT 0;
`,
	// v7 -> v8: record traffic as it happens instead of sampling the peers'
	// running totals. Those totals shrink whenever a peer or group is reset or
	// deleted and include usage imported from the old panel, so no difference
	// between two readings of them measured anything real. The samples go with
	// the method, and so do the figures other servers computed from them.
	`
DROP TABLE usage_samples;

CREATE TABLE traffic (
    minute INTEGER PRIMARY KEY,   -- unix ms at the start of the minute
    tx     INTEGER NOT NULL DEFAULT 0,
    rx     INTEGER NOT NULL DEFAULT 0
);

ALTER TABLE nodes ADD COLUMN usage_hour INTEGER NOT NULL DEFAULT 0;
UPDATE nodes SET usage_day = 0, usage_week = 0, usage_month = 0;

-- A master's request that a node reset its traffic counters, delivered with
-- the node's next sync. 0 means none has been asked for.
ALTER TABLE nodes ADD COLUMN traffic_reset_at INTEGER NOT NULL DEFAULT 0;
`,
}

// migrate brings the database up to schemaVersion, applying each pending
// migration in its own transaction.
func migrate(db *sql.DB) error {
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}
	if version > schemaVersion {
		return fmt.Errorf("database schema version %d is newer than this build supports (%d)", version, schemaVersion)
	}
	if version == schemaVersion {
		return nil
	}

	// Foreign keys are off while the schema changes, which is what SQLite's own
	// procedure for altering a table calls for. Rewriting a table — which is how
	// SQLite implements dropping a column — moves rows that other tables point
	// at, and enforcing keys through that can fail on a database whose rows are
	// all perfectly consistent before and after. Migrations are code, not user
	// input: what they need is to run to completion and then be checked.
	//
	// The pragma is ignored inside a transaction, so it has to be set out here.
	// The pool is capped at a single connection, so this is the connection the
	// migrations below run on.
	if _, err := db.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		return fmt.Errorf("disable foreign keys for migration: %w", err)
	}
	defer func() {
		if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
			// Nothing useful to do here, and the caller already has whatever
			// error the migration itself produced.
			_ = err
		}
	}()

	for v := version; v < schemaVersion; v++ {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[v]); err != nil {
			tx.Rollback()
			// "FOREIGN KEY constraint failed" names neither the table nor the
			// row, which leaves an operator with nothing to act on. The check
			// runs after the rollback, so it describes the database as it
			// actually is rather than as the half-applied migration left it.
			if detail := describeBrokenKeys(db); detail != "" {
				return fmt.Errorf("migration %d -> %d: %w (%s)", v, v+1, err, detail)
			}
			return fmt.Errorf("migration %d -> %d: %w", v, v+1, err)
		}
		// PRAGMA does not accept bound parameters.
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, v+1)); err != nil {
			tx.Rollback()
			return fmt.Errorf("set user_version %d: %w", v+1, err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return checkForeignKeys(db)
}

// describeBrokenKeys summarises what foreign_key_check finds, for an error
// message. It returns "" when the database is consistent, which is the case
// worth knowing: it means the failure was in the migration rather than in the
// data it was handed.
func describeBrokenKeys(db *sql.DB) string {
	broken, err := brokenKeys(db)
	if err != nil || len(broken) == 0 {
		if err == nil {
			return "the existing rows are consistent, so this is a fault in the migration itself"
		}
		return ""
	}
	parts := make([]string, 0, len(broken))
	for ref, n := range broken {
		parts = append(parts, fmt.Sprintf("%s: %d row(s)", ref, n))
	}
	sort.Strings(parts)
	return "rows pointing at something missing — " + strings.Join(parts, "; ")
}

func brokenKeys(db *sql.DB) (map[string]int, error) {
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	broken := map[string]int{}
	for rows.Next() {
		var table, parent sql.NullString
		var rowid, fkID sql.NullInt64
		if err := rows.Scan(&table, &rowid, &parent, &fkID); err != nil {
			return nil, err
		}
		broken[table.String+" -> "+parent.String]++
	}
	return broken, rows.Err()
}

// checkForeignKeys reports rows left pointing at something that is not there.
//
// A violation here is not a reason to refuse to start: the rows are readable,
// and an operator with a panel they can open can fix the data. It is a reason
// to say so loudly, because the next write that touches such a row is where it
// would otherwise turn into an unexplained failure.
func checkForeignKeys(db *sql.DB) error {
	broken, err := brokenKeys(db)
	if err != nil {
		return fmt.Errorf("check foreign keys: %w", err)
	}
	if len(broken) > 0 {
		slog.Warn("the database has rows referring to something that is no longer there; "+
			"they are readable but editing them will fail", "references", broken)
	}
	return nil
}
