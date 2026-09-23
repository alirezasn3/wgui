// Package store is the only place that talks to the database. Everything above
// it works with the types in types.go, so the storage engine can be swapped (or
// extended with per-server usage accounting) without touching the API layer.
package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"wgui/internal/config"

	_ "modernc.org/sqlite"
)

var (
	ErrNotFound  = errors.New("not found")
	ErrDuplicate = errors.New("already exists")
)

type Store struct {
	db *sql.DB
	// serverID labels the usage rows this server owns; see adoptServerID.
	serverID string
}

func dsnFor(path string) string {
	return fmt.Sprintf(
		"file:%s?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)",
		url.PathEscape(path),
	)
}

// Open opens (creating if needed) the SQLite database at path and brings the
// schema up to date.
//
// Access is deliberately serialised to a single connection: every query here is
// sub-millisecond and the panel's concurrency is tiny, so serialising removes an
// entire class of SQLITE_BUSY races for no practical cost.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", dsnFor(path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	// Checked before migrating, not after: migrating turns on the foreign keys
	// that a damaged index then fails, and "FOREIGN KEY constraint failed" is a
	// poor way to learn that the file was copied out from under a running
	// server.
	if problems, err := checkIntegrity(db); err != nil {
		db.Close()
		return nil, err
	} else if len(problems) > 0 {
		db.Close()
		return nil, &ErrCorrupt{Path: path, Problems: problems}
	}

	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	st := &Store{db: db}
	if err := st.adoptServerID(); err != nil {
		db.Close()
		return nil, err
	}
	return st, nil
}

// serverIDSecret holds this server's identity. It lives among the secrets
// because it must never be replicated: a database copied to another machine has
// to become a different server, or both would claim the same usage rows.
const serverIDSecret = "server_id"

// adoptServerID gives this database a stable identity and claims the usage rows
// left unlabelled by the migration from single-server accounting.
func (s *Store) adoptServerID() error {
	id, ok, err := s.Secret(serverIDSecret)
	if err != nil {
		return err
	}
	if !ok || len(id) == 0 {
		buf := make([]byte, 16)
		if _, err := rand.Read(buf); err != nil {
			return err
		}
		id = []byte(hex.EncodeToString(buf))
		if err := s.PutSecret(serverIDSecret, id); err != nil {
			return err
		}
	}
	s.serverID = string(id)

	_, err = s.db.Exec(`UPDATE peer_usage SET server_id = ? WHERE server_id = ''`, s.serverID)
	return err
}

// ServerID is this server's identity in the usage table, and the id other
// servers know it by.
func (s *Store) ServerID() string { return s.serverID }

func (s *Store) Close() error { return s.db.Close() }

// Backup writes a consistent copy of the database to path, replacing whatever
// is there. It is safe against a database in use: VACUUM INTO reads one
// snapshot, which is what copying the file while wgui runs does not do.
func (s *Store) Backup(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if _, err := s.db.Exec(`VACUUM INTO ?`, path); err != nil {
		return err
	}
	// The copy holds every peer's private key, like the database itself.
	return os.Chmod(path, 0o600)
}

// OpenForRepair opens a database without the integrity gate Open applies and
// without migrating it, since a damaged database is exactly what --repair is
// handed and neither would survive it.
func OpenForRepair(path string) (*Store, error) {
	db, err := sql.Open("sqlite", dsnFor(path))
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return &Store{db: db}, nil
}

// DB exposes the handle for the migration command, which needs to run its own
// bulk transaction.
func (s *Store) DB() *sql.DB { return s.db }

func now() int64 { return time.Now().UnixMilli() }

// isUnique reports whether err is a UNIQUE constraint violation.
func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// IsEmpty reports whether the database holds no peers, used to decide whether to
// bootstrap the first admin and to guard the Mongo import.
func (s *Store) IsEmpty() (bool, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM peers`).Scan(&n); err != nil {
		return false, err
	}
	return n == 0, nil
}

// -- settings ---------------------------------------------------------------

// LoadSettings reads the settings table, filling in defaults for keys that have
// never been written.
func (s *Store) LoadSettings() (config.Settings, error) {
	out := config.DefaultSettings()

	rows, err := s.db.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return out, err
	}
	defer rows.Close()

	fields := settingsFields(&out)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return out, err
		}
		target, ok := fields[key]
		if !ok {
			continue // unknown key left over from an older build
		}
		if err := json.Unmarshal([]byte(value), target); err != nil {
			return out, fmt.Errorf("setting %q: %w", key, err)
		}
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	out.Normalize()
	return out, nil
}

// SaveSettings writes every field as its own row so a partially understood
// settings table still round-trips.
func (s *Store) SaveSettings(v config.Settings) error {
	v.Normalize()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`INSERT INTO settings (key, value) VALUES (?, ?)
	                         ON CONFLICT(key) DO UPDATE SET value = excluded.value`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for key, ptr := range settingsFields(&v) {
		b, err := json.Marshal(ptr)
		if err != nil {
			return fmt.Errorf("setting %q: %w", key, err)
		}
		if _, err := stmt.Exec(key, string(b)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// settingsFields maps each settings-table key to the field it populates.
func settingsFields(s *config.Settings) map[string]any {
	return map[string]any{
		"publicAddress":     &s.PublicAddress,
		"endpoints":         &s.Endpoints,
		"defaultEndpoint":   &s.DefaultEndpoint,
		"peerDefaults":      &s.PeerDefaults,
		"groupDefaults":     &s.GroupDefaults,
		"qr":                &s.QR,
		"ipinfo":            &s.IPInfo,
		"usageFlushSeconds": &s.UsageFlushSeconds,
	}
}

// -- secrets ----------------------------------------------------------------

// Secret reads a stored secret. Nothing outside the process ever sees these:
// they are deliberately not reachable through the API.
func (s *Store) Secret(key string) ([]byte, bool, error) {
	var value []byte
	err := s.db.QueryRow(`SELECT value FROM secrets WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return value, true, nil
}

// DeleteSecret removes a stored secret, used when the panel is told to stop
// keeping one.
func (s *Store) DeleteSecret(key string) error {
	_, err := s.db.Exec(`DELETE FROM secrets WHERE key = ?`, key)
	return err
}

func (s *Store) PutSecret(key string, value []byte) error {
	_, err := s.db.Exec(
		`INSERT INTO secrets (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// -- ip info cache ----------------------------------------------------------

// GetIPInfo returns the cached answer for ip if it was fetched within ttl.
func (s *Store) GetIPInfo(ip string, ttl time.Duration) (*IPInfo, error) {
	var v IPInfo
	err := s.db.QueryRow(
		`SELECT ip, asn, as_name, org, country, country_code, cidr, fetched_at
		 FROM ipinfo_cache WHERE ip = ?`, ip,
	).Scan(&v.IP, &v.ASN, &v.ASName, &v.Org, &v.Country, &v.CountryCode, &v.CIDR, &v.FetchedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if ttl > 0 && now()-v.FetchedAt > ttl.Milliseconds() {
		return nil, ErrNotFound
	}
	v.Cached = true
	return &v, nil
}

func (s *Store) PutIPInfo(v IPInfo) error {
	_, err := s.db.Exec(
		`INSERT INTO ipinfo_cache (ip, asn, as_name, org, country, country_code, cidr, fetched_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(ip) DO UPDATE SET
		   asn = excluded.asn, as_name = excluded.as_name, org = excluded.org,
		   country = excluded.country, country_code = excluded.country_code,
		   cidr = excluded.cidr, fetched_at = excluded.fetched_at`,
		v.IP, v.ASN, v.ASName, v.Org, v.Country, v.CountryCode, v.CIDR, now(),
	)
	return err
}
