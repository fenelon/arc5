package archive

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpenCreatesSchema(t *testing.T) {
	s := openTemp(t)
	rows, err := s.r.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		got = append(got, name)
	}
	want := []string{"accounts", "attachments", "chats", "connector_state", "message_versions", "messages", "schema_migrations", "senders", "settings"}
	if !slices.Equal(got, want) {
		t.Fatalf("tables = %v, want %v", got, want)
	}
}

func TestOpenRestrictsPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for path, want := range map[string]os.FileMode{dir: 0o700, filepath.Join(dir, "arc5.db"): 0o600} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s mode = %o, want %o", path, got, want)
		}
	}
}

func TestReopenKeepsDataAndAppliesMigrationsOnce(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.w.Exec(`INSERT INTO settings (key, value) VALUES ('probe', '1')`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var migrations, probes int
	if err := s.r.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&migrations); err != nil {
		t.Fatal(err)
	}
	if err := s.r.QueryRow(`SELECT COUNT(*) FROM settings WHERE key = 'probe'`).Scan(&probes); err != nil {
		t.Fatal(err)
	}
	if migrations != 1 || probes != 1 {
		t.Fatalf("migrations = %d, probes = %d, want 1 and 1", migrations, probes)
	}
}

func TestReadHandleRejectsWrites(t *testing.T) {
	s := openTemp(t)
	if _, err := s.r.Exec(`INSERT INTO settings (key, value) VALUES ('x', 'y')`); err == nil {
		t.Fatal("write through the read handle succeeded")
	}
}

func TestOpenFailsWhenDataFolderIsAFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-folder")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("Open on a regular file succeeded")
	}
}

// The search milestone depends on FTS5. Fail here, early, if the driver lacks it.
func TestFTS5Available(t *testing.T) {
	s := openTemp(t)
	if _, err := s.w.Exec(`CREATE VIRTUAL TABLE temp.fts_probe USING fts5(body)`); err != nil {
		t.Fatalf("FTS5 is not available in this SQLite build: %v", err)
	}
	if _, err := s.w.Exec(`INSERT INTO temp.fts_probe (body) VALUES ('hello world')`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.w.QueryRow(`SELECT COUNT(*) FROM temp.fts_probe WHERE fts_probe MATCH 'hello'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("FTS5 match count = %d, want 1", n)
	}
}
