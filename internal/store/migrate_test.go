package store

import (
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"testing"
)

// The v4 migration moves every peer's usage out of the peer row and into a
// per-server table, on databases that are already carrying a year of real
// counters. Getting it wrong loses what every customer has spent, so it is
// exercised against a database built the way an older wgui would have left it.
func TestMigrationToPerServerUsage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v3.db")
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)", url.PathEscape(path))

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	// Build the schema exactly as version 3 left it, then stop.
	for i := 0; i < 3; i++ {
		if _, err := db.Exec(migrations[i]); err != nil {
			t.Fatalf("migration %d: %v", i, err)
		}
	}
	if _, err := db.Exec("PRAGMA user_version = 3"); err != nil {
		t.Fatalf("set version: %v", err)
	}

	// Two peers with counters and one that never connected, plus a group, so
	// the group aggregate is checked as well.
	if _, err := db.Exec(`
	    INSERT INTO groups (id, name, allowed_usage, created_at, updated_at)
	         VALUES (1, 'household', 0, 1, 1);
	    INSERT INTO peers (id, name, public_key, allowed_ips, group_id,
	                       total_tx, total_rx, created_at, updated_at)
	         VALUES ('a', 'a', 'a', '10.0.0.2/32', 1, 5000000000, 2500000000, 1, 1),
	                ('b', 'b', 'b', '10.0.0.3/32', 1,  100000000,   50000000, 1, 1),
	                ('c', 'c', 'c', '10.0.0.4/32', NULL,        0,          0, 1, 1);
	`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Opening it is what migrates it.
	s, err := Open(path)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer s.Close()

	now := int64(2)
	for _, want := range []struct {
		id     string
		tx, rx int64
	}{
		{"a", 5000000000, 2500000000},
		{"b", 100000000, 50000000},
		{"c", 0, 0},
	} {
		p, err := s.GetPeer(want.id, now)
		if err != nil {
			t.Fatalf("get %s: %v", want.id, err)
		}
		if p.TotalTX != want.tx || p.TotalRX != want.rx {
			t.Errorf("peer %s carried %d/%d through the migration, want %d/%d",
				want.id, p.TotalTX, p.TotalRX, want.tx, want.rx)
		}
	}

	// The counters must now belong to this server, not to the empty id the
	// migration parks them under, or the first sync would disown them.
	var rows int
	if err := s.DB().QueryRow(
		`SELECT COUNT(*) FROM peer_usage WHERE server_id = ?`, s.ServerID()).Scan(&rows); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 2 {
		t.Errorf("%d usage rows claimed by this server, want 2", rows)
	}
	if s.ServerID() == "" {
		t.Error("the migrated database has no server identity")
	}

	g, err := s.GetGroup(1, now)
	if err != nil {
		t.Fatalf("get group: %v", err)
	}
	if g.Usage != 7650000000 {
		t.Errorf("group usage = %d, want 7650000000 summed from its members", g.Usage)
	}
}

// A database that has been running carries rows whose parents are gone — the
// keys are only checked as rows are written, so a violation can predate
// enforcement. Migrating must not be where that finally stops the panel from
// starting: the rows are readable, and an operator needs the panel open to fix
// them.
func TestMigrationSurvivesBrokenReferences(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v3.db")
	// Seeded with the keys off, which is how such rows come to exist.
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(0)", url.PathEscape(path))
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := db.Exec(migrations[i]); err != nil {
			t.Fatalf("migration %d: %v", i, err)
		}
	}
	if _, err := db.Exec(`PRAGMA user_version = 3`); err != nil {
		t.Fatalf("set version: %v", err)
	}
	if _, err := db.Exec(`
	    INSERT INTO groups (id, name, owner_id, created_at, updated_at)
	         VALUES (1, 'g', 'vanished', 1, 1);
	    INSERT INTO peers (id, name, public_key, allowed_ips, owner_id, group_id,
	                       total_tx, total_rx, created_at, updated_at)
	         VALUES ('a', 'a', 'a', '10.0.0.2/32', 'vanished', 77, 400, 100, 1, 1);
	    INSERT INTO peer_visibility (viewer_id, target_id) VALUES ('a', 'vanished');
	`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatalf("a database with broken references would not migrate: %v", err)
	}
	defer s.Close()

	if p, err := s.GetPeer("a", 2); err != nil {
		t.Fatalf("get: %v", err)
	} else if p.Usage != 500 {
		t.Errorf("usage = %d, want 500 carried through the migration", p.Usage)
	}
}

// The same migration over a realistic number of rows, since a table rewrite on
// one page is not the same operation as one over many.
func TestMigrationAtScale(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v3.db")
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_pragma=foreign_keys(1)", url.PathEscape(path)))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := db.Exec(migrations[i]); err != nil {
			t.Fatalf("migration %d: %v", i, err)
		}
	}
	if _, err := db.Exec(`PRAGMA user_version = 3`); err != nil {
		t.Fatalf("set version: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO groups (id, name, created_at, updated_at) VALUES (1,'g',1,1)`); err != nil {
		t.Fatalf("seed group: %v", err)
	}
	const peers = 500
	var want int64
	for i := 0; i < peers; i++ {
		group := "NULL"
		if i%3 == 0 {
			group = "1"
		}
		tx, rx := int64(i)*1000, int64(i)*400
		want += tx + rx
		if _, err := db.Exec(fmt.Sprintf(`
		    INSERT INTO peers (id, name, public_key, allowed_ips, group_id,
		                       total_tx, total_rx, created_at, updated_at)
		         VALUES ('k%d','n%d','k%d','10.0.%d.%d/32',%s,%d,%d,1,1)`,
			i, i, i, i/250, i%250+2, group, tx, rx)); err != nil {
			t.Fatalf("seed peer %d: %v", i, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer s.Close()

	var got int64
	if err := s.DB().QueryRow(`SELECT COALESCE(SUM(tx + rx), 0) FROM peer_usage`).Scan(&got); err != nil {
		t.Fatalf("sum: %v", err)
	}
	if got != want {
		t.Errorf("carried %d bytes through the migration, want %d", got, want)
	}
}
