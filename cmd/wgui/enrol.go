package main

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"wgui/internal/store"
)

// archiveStandaloneDB sets aside a database that was a server's own before it
// was made a node.
//
// A node holds its master's peers and nothing else: the first sync deletes
// whatever the master does not list, which for a server that had been running
// on its own is everything it had. Rather than let a sync quietly empty a real
// database, the file is moved out of the way and the node starts from nothing,
// leaving the old data where an operator can still reach it.
//
// A database that has already synced with a master carries its id and is left
// alone, so this happens once rather than on every restart.
func archiveStandaloneDB(path string, log *slog.Logger) error {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil // nothing there yet: a node starting clean
	} else if err != nil {
		return err
	}

	standalone, err := isStandalone(path)
	if err != nil || !standalone {
		return err
	}

	stamp := time.Now().Format("20060102-150405")
	ext := filepath.Ext(path)
	archived := strings.TrimSuffix(path, ext) + "-standalone-" + stamp + ext

	// The write-ahead log and shared-memory files belong to the database; a
	// rename that leaves them behind would graft them onto the new one.
	for _, suffix := range []string{"", "-wal", "-shm"} {
		from, to := path+suffix, archived+suffix
		if err := os.Rename(from, to); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("set aside %s: %w", from, err)
		}
	}

	log.Warn("this server had a database of its own and is now a node; it has been set aside "+
		"and everything will be loaded from the master",
		"archived", archived)
	return nil
}

// isStandalone reports whether the database at path holds data that did not
// come from a master.
func isStandalone(path string) (bool, error) {
	st, err := store.Open(path)
	if err != nil {
		return false, err
	}
	defer st.Close()

	empty, err := st.IsEmpty()
	if err != nil || empty {
		return false, err
	}
	master, err := st.MasterID()
	if err != nil {
		return false, err
	}
	return master == "", nil
}

// repairDatabase rebuilds the indexes of a database whose indexes no longer
// agree with its contents, which is what copying the file while wgui is running
// leaves behind.
func repairDatabase(path string, log *slog.Logger) error {
	st, err := store.OpenForRepair(path)
	if err != nil {
		return err
	}
	defer st.Close()

	problems, err := st.Repair()
	for _, p := range problems {
		log.Warn("found", "problem", p)
	}
	if err != nil {
		return err
	}
	if len(problems) == 0 {
		log.Info("the database was already sound; nothing needed rebuilding", "path", path)
		return nil
	}
	log.Info("indexes rebuilt; the database now checks out", "path", path, "problems", len(problems))
	return nil
}
