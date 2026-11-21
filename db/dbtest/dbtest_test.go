package dbtest

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func resetTemplate(t *testing.T, build func() ([]byte, error)) {
	t.Helper()
	reset := func(fn func() ([]byte, error)) {
		templateOnce = sync.Once{}
		templateBytes, templateErr = nil, nil
		buildTemplateFn = fn
	}
	reset(build)
	t.Cleanup(func() { reset(buildTemplate) })
}

func TestSQLiteIsMigrated(t *testing.T) {
	conn := SQLite(t)

	var version int
	var dirty bool
	require.NoError(t, conn.QueryRow("SELECT version, dirty FROM schema_migrations").Scan(&version, &dirty))
	require.Positive(t, version)
	require.False(t, dirty)

	var users int
	require.NoError(t, conn.QueryRow("SELECT COUNT(*) FROM users").Scan(&users))
}

func TestSQLiteDatabasesAreIndependent(t *testing.T) {
	first, firstPath := SQLiteWithPath(t)
	second, secondPath := SQLiteWithPath(t)
	require.NotEqual(t, firstPath, secondPath)
	require.FileExists(t, firstPath)

	_, err := first.Exec(`INSERT INTO users (id, name, email, password) VALUES (987654321, 'a', 'dbtest@example.com', '')`)
	require.NoError(t, err)

	var n int
	require.NoError(t, first.QueryRow("SELECT COUNT(*) FROM users WHERE id = 987654321").Scan(&n))
	require.Equal(t, 1, n)
	require.NoError(t, second.QueryRow("SELECT COUNT(*) FROM users WHERE id = 987654321").Scan(&n))
	require.Zero(t, n)
}

func TestWriteMigratedBuildError(t *testing.T) {
	resetTemplate(t, func() ([]byte, error) { return nil, errors.New("boom") })
	err := WriteMigrated(filepath.Join(t.TempDir(), "x.db"))
	require.ErrorContains(t, err, "boom")
}

func TestMigrateIntoOpenError(t *testing.T) {
	_, err := migrateInto(filepath.Join(t.TempDir(), "missing", "dir", "x.db"))
	require.Error(t, err)
}

func TestWriteMigratedWriteError(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	err := WriteMigrated(filepath.Join(dir, "x.db"))
	require.Error(t, err)
	_, statErr := os.Stat(dir)
	require.True(t, os.IsNotExist(statErr))
}
