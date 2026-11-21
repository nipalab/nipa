// Package dbtest hands tests a freshly migrated SQLite database without
// replaying every migration per test. The schema is migrated once per test
// binary into a template file whose bytes are then copied into each test's
// own temp directory, which is an order of magnitude cheaper than MigrateUp
// (especially under -race).
package dbtest

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	database "github.com/nipalab/nipa/db"
)

var (
	templateOnce  sync.Once
	templateBytes []byte
	templateErr   error

	// buildTemplateFn is swapped by tests to exercise the failure path.
	buildTemplateFn = buildTemplate
)

// SQLite returns a migrated SQLite database opened through
// database.OpenSQLite in t.TempDir(), closed on test cleanup.
func SQLite(t testing.TB) *sql.DB {
	t.Helper()
	conn, _ := SQLiteWithPath(t)
	return conn
}

// SQLiteWithPath is SQLite but also returns the database file path, for tests
// that reopen the file or pass its DSN to code under test.
func SQLiteWithPath(t testing.TB) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "nipa.db")
	if err := WriteMigrated(path); err != nil {
		t.Fatalf("dbtest: %v", err)
	}
	conn, err := database.OpenSQLite(path)
	if err != nil {
		t.Fatalf("dbtest: open %s: %v", path, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn, path
}

// WriteMigrated writes a fully migrated SQLite database file to path.
func WriteMigrated(path string) error {
	templateOnce.Do(func() { templateBytes, templateErr = buildTemplateFn() })
	if templateErr != nil {
		return fmt.Errorf("build template: %w", templateErr)
	}
	return os.WriteFile(path, templateBytes, 0o600)
}

func buildTemplate() ([]byte, error) {
	dir, err := os.MkdirTemp("", "nipa-dbtest-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	return migrateInto(filepath.Join(dir, "template.db"))
}

func migrateInto(path string) ([]byte, error) {
	conn, err := database.OpenSQLite(path)
	if err != nil {
		return nil, err
	}
	if err := database.MigrateUp(conn, "sqlite3"); err != nil {
		_ = conn.Close()
		return nil, err
	}
	// Fold the WAL into the main file so the bytes alone hold the schema.
	if _, err := conn.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := conn.Close(); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}
