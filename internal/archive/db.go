// Package archive is the only writer to the arc5 database and media folder.
package archive

import (
	"context"
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
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

const dbFile = "arc5.db"

// Store owns the database and the media folder under one data directory.
type Store struct {
	w        *sql.DB // single connection; every write goes through it
	r        *sql.DB // read-only pool
	dataDir  string
	now      func() time.Time
	analyzer atomic.Pointer[Analyzer] // nil until SetAnalyzer; see index.go
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

	// synchronous=FULL syncs the log on every commit, so a message that was
	// acknowledged survives a power cut. NORMAL would be faster but can lose
	// the last commits, which an archive of auto-deleting messages cannot afford.
	writeDSN, err := dsn(path, "busy_timeout(5000)", "foreign_keys(1)", "journal_mode(WAL)", "synchronous(FULL)")
	if err != nil {
		return nil, err
	}
	readDSN, err := dsn(path, "busy_timeout(5000)", "foreign_keys(1)", "query_only(1)")
	if err != nil {
		return nil, err
	}
	// BEGIN IMMEDIATE takes the write lock up front, so a second process on
	// this folder waits for busy_timeout instead of failing when a
	// transaction that began by reading tries to write.
	w, err := sql.Open("sqlite", writeDSN+"&_txlock=immediate")
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

// migrationLimit, when set by a test, stops migrating after that version, so
// a test can build an archive as an older arc5 left it.
var migrationLimit int

// foreignKeysOff marks a migration that must run with foreign keys off, such
// as one that rebuilds a table other tables refer to.
const foreignKeysOff = "-- arc5: foreign keys off"

// applyMigration runs one migration in a transaction on one connection. A
// migration marked foreignKeysOff runs with them off and is checked with
// foreign_key_check before it commits; they are switched on again after.
func (s *Store) applyMigration(version int, body string) (err error) {
	ctx := context.Background()
	conn, err := s.w.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if strings.HasPrefix(body, foreignKeysOff) {
		if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
			return err
		}
		defer func() {
			if _, onErr := conn.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err == nil {
				err = onErr
			}
		}()
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(body); err != nil {
		return err
	}
	rows, err := tx.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	broken := rows.Next()
	rows.Close()
	if broken {
		return errors.New("it would break foreign keys")
	}
	if _, err := tx.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`, version, s.now().Unix()); err != nil {
		return err
	}
	return tx.Commit()
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
		if migrationLimit > 0 && version > migrationLimit {
			break
		}
		body, err := migrationFiles.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		if err := s.applyMigration(version, string(body)); err != nil {
			return fmt.Errorf("migration %s: %w", name, err)
		}
	}
	return nil
}
