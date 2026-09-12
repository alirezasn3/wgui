package store

import (
	"database/sql"
	"fmt"
	"strings"
)

// checkIntegrity reports what SQLite thinks is wrong with the database, or
// nothing at all when it is sound.
//
// This runs the full check rather than the quick one on purpose. The quick
// check skips exactly the step that matters here — verifying that the indexes
// agree with the table they index — and that disagreement is what a database
// copied while it was being written produces. It is worth the milliseconds: an
// index missing entries does not announce itself, it just quietly answers
// questions wrongly, and the panel asks it which tunnel addresses are free.
func checkIntegrity(db *sql.DB) ([]string, error) {
	rows, err := db.Query(`PRAGMA integrity_check`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var problems []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return nil, err
		}
		if line != "ok" {
			problems = append(problems, line)
		}
	}
	return problems, rows.Err()
}

// ErrCorrupt is returned by Open when the database's indexes no longer agree
// with its contents.
type ErrCorrupt struct {
	Path     string
	Problems []string
}

func (e *ErrCorrupt) Error() string {
	shown := e.Problems
	if len(shown) > 6 {
		shown = append(append([]string{}, shown[:6]...),
			fmt.Sprintf("...and %d more", len(e.Problems)-6))
	}
	return fmt.Sprintf(
		"%s is damaged — its indexes disagree with its contents:\n  %s\n"+
			"Run `wgui --repair` to rebuild them. This is what copying a database "+
			"file while wgui is running leaves behind; stop wgui first, or let a node "+
			"sync instead of copying.",
		e.Path, strings.Join(shown, "\n  "))
}

// uniqueColumns are the columns whose indexes enforce that no two peers share
// them. A rebuild fails if the table holds duplicates, which is what happens
// once a damaged index has stopped rejecting them.
var uniqueColumns = []string{"name", "public_key", "allowed_ips", "id"}

// Repair rebuilds every index from the table. Nothing is read from the indexes
// to do it, so a rebuild fixes exactly the damage that makes them untrustworthy.
//
// It reports what it found rather than only whether it worked, because the
// interesting failure is the one it cannot fix on its own: duplicates that a
// damaged unique index let through, which are two real peers that have to be
// told apart by hand.
func (s *Store) Repair() (before []string, err error) {
	before, err = checkIntegrity(s.db)
	if err != nil {
		return nil, err
	}

	if _, err := s.db.Exec(`REINDEX`); err != nil {
		if dupes := s.duplicates(); len(dupes) > 0 {
			return before, fmt.Errorf(
				"the indexes cannot be rebuilt because the peers table holds values that "+
					"must be unique but are not:\n  %s\n"+
					"A damaged index stopped rejecting them when they were created. Give one "+
					"peer of each pair a different value, then run --repair again.",
				strings.Join(dupes, "\n  "))
		}
		return before, fmt.Errorf("rebuild indexes: %w", err)
	}

	after, err := checkIntegrity(s.db)
	if err != nil {
		return before, err
	}
	if len(after) > 0 {
		return before, fmt.Errorf(
			"the database is still damaged after rebuilding its indexes, which means the "+
				"damage is in the data rather than the indexes:\n  %s",
			strings.Join(after, "\n  "))
	}
	return before, nil
}

// duplicates finds peers sharing a value that is supposed to be unique. Every
// query scans the table: the indexes are what is in question, so an answer
// taken from them would be the same answer that let the duplicates in.
func (s *Store) duplicates() []string {
	var out []string
	for _, col := range uniqueColumns {
		rows, err := s.db.Query(fmt.Sprintf(
			`SELECT p.%[1]s, group_concat(p.name, ', ') FROM peers p NOT INDEXED
			 WHERE +p.%[1]s IN (SELECT +d.%[1]s FROM peers d NOT INDEXED
			                    GROUP BY +d.%[1]s HAVING count(*) > 1)
			 GROUP BY +p.%[1]s`, col))
		if err != nil {
			continue
		}
		for rows.Next() {
			var value, names string
			if err := rows.Scan(&value, &names); err != nil {
				break
			}
			out = append(out, fmt.Sprintf("%s %q is shared by: %s", col, value, names))
		}
		rows.Close()
	}
	return out
}
