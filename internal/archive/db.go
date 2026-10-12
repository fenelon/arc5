// Package archive is the only writer to the arc5 database and media folder.
package archive

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

const dbFile = "arc5.db"

// Store owns the database and the media folder under one data directory.
type Store struct {
	w       *sql.DB // single connection; every write goes through it
	r       *sql.DB // read-only pool
	dataDir string
	now     func() time.Time
}

// Open creates the data folder and database if needed and applies migrations.
func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data folder: %w", err)
	}
	if err := os.Chmod(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("restrict data folder: %w", err)
	}
	path := filepath.Join(dataDir, dbFile)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create database: %w", err)
	}
	f.Close()
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, fmt.Errorf("restrict database: %w", err)
	}

	writeDSN, err := dsn(path, "busy_timeout(5000)", "foreign_keys(1)", "journal_mode(WAL)", "synchronous(NORMAL)")
	if err != nil {
		return nil, err
	}
	readDSN, err := dsn(path, "busy_timeout(5000)", "foreign_keys(1)", "query_only(1)")
	if err != nil {
		return nil, err
	}
	w, err := sql.Open("sqlite", writeDSN)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	// One writer avoids SQLITE_BUSY between connectors; database/sql queues callers.
	w.SetMaxOpenConns(1)
	r, err := sql.Open("sqlite", readDSN)
	if err != nil {
		w.Close()
		return nil, fmt.Errorf("open database for reading: %w", err)
	}

	s := &Store{w: w, r: r, dataDir: dataDir, now: time.Now}
	if err := s.migrate(); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// dsn builds a SQLite file: URI. Appending "?params" to a plain path would be
// cut at a "?" in the path itself and open a different file, so the path is
// escaped instead.
func dsn(path string, pragmas ...string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve database path: %w", err)
	}
	params := make([]string, len(pragmas))
	for i, p := range pragmas {
		params[i] = "_pragma=" + p
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(abs), RawQuery: strings.Join(params, "&")}
	return u.String(), nil
}

func (s *Store) Close() error {
	return errors.Join(s.r.Close(), s.w.Close())
}

func (s *Store) migrate() error {
	if _, err := s.w.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("prepare migrations: %w", err)
	}
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)

	for _, name := range names {
		version, err := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
		if err != nil {
			return fmt.Errorf("migration %s: name must start with a number", name)
		}
		var applied int
		if err := s.w.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, version).Scan(&applied); err != nil {
			return err
		}
		if applied > 0 {
			continue
		}
		body, err := migrationFiles.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		tx, err := s.w.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`, version, s.now().Unix()); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migration %s: %w", name, err)
		}
	}
	return nil
}
