package db

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

var (
	queryPerformanceAdded = []string{
		"idx_pbac_rules_org_wide",
		"idx_refresh_tokens_user",
		"idx_merge_requests_source_status",
		"idx_merge_requests_target_status",
		"idx_email_deliveries_thread",
		"idx_email_deliveries_sweep",
	}
	queryPerformanceDropped = []string{
		"idx_tree_nodes_parent",
		"idx_files_tree_id",
		"idx_merge_requests_project",
		"idx_merge_request_review_requests_mr",
		"idx_merge_request_checks_mr",
		"idx_audit_log_id",
	}
)

func openMigrateTestDB(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := OpenSQLite(filepath.Join(t.TempDir(), "nipa.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func sqliteIndexes(t *testing.T, conn *sql.DB) map[string]string {
	t.Helper()
	rows, err := conn.Query("SELECT name, COALESCE(sql, '') FROM sqlite_master WHERE type = 'index'")
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	out := map[string]string{}
	for rows.Next() {
		var name, def string
		require.NoError(t, rows.Scan(&name, &def))
		out[name] = def
	}
	require.NoError(t, rows.Err())
	return out
}

func requireQueryPerformanceUp(t *testing.T, conn *sql.DB) {
	t.Helper()
	indexes := sqliteIndexes(t, conn)
	for _, name := range queryPerformanceAdded {
		require.Contains(t, indexes, name)
	}
	for _, name := range queryPerformanceDropped {
		require.NotContains(t, indexes, name)
	}
	require.Contains(t, indexes["idx_branches_project_updated_id"], "WHERE deleted = FALSE")
}

const queryPerformanceVersion uint = 18

func TestMigrateQueryPerformanceRoundTrip(t *testing.T) {
	conn := openMigrateTestDB(t)
	m, err := newMigrator(conn, "sqlite3")
	require.NoError(t, err)

	require.NoError(t, m.Migrate(queryPerformanceVersion))
	version, dirty, err := m.Version()
	require.NoError(t, err)
	require.False(t, dirty)
	require.Equal(t, queryPerformanceVersion, version)
	requireQueryPerformanceUp(t, conn)

	require.NoError(t, m.Migrate(queryPerformanceVersion-1))
	version, _, err = m.Version()
	require.NoError(t, err)
	require.Equal(t, queryPerformanceVersion-1, version)

	indexes := sqliteIndexes(t, conn)
	for _, name := range queryPerformanceDropped {
		require.Contains(t, indexes, name)
	}
	for _, name := range queryPerformanceAdded {
		require.NotContains(t, indexes, name)
	}
	require.Contains(t, indexes, "idx_branches_project_updated_id")
	require.NotContains(t, indexes["idx_branches_project_updated_id"], "WHERE")

	require.NoError(t, m.Migrate(queryPerformanceVersion))
	requireQueryPerformanceUp(t, conn)
	require.NoError(t, MigrateUp(conn, "sqlite3"))
}

func TestMigrateDownRemovesSchema(t *testing.T) {
	conn := openMigrateTestDB(t)
	require.NoError(t, MigrateUp(conn, "sqlite3"))
	require.NoError(t, MigrateDown(conn, "sqlite3"))

	var tables int
	require.NoError(t, conn.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name NOT IN ('schema_migrations') AND name NOT LIKE 'sqlite_%'",
	).Scan(&tables))
	require.Zero(t, tables)

	require.NoError(t, MigrateUp(conn, "sqlite3"))
	requireQueryPerformanceUp(t, conn)
}

func TestMigrateUnsupportedDialect(t *testing.T) {
	conn := openMigrateTestDB(t)
	require.Error(t, MigrateUp(conn, "postgres"))
	require.Error(t, MigrateDown(conn, "postgres"))
}

const refreshDropPlaintextVersion uint = 21

func TestMigrateRefreshTokenDropPlaintext(t *testing.T) {
	conn := openMigrateTestDB(t)
	m, err := newMigrator(conn, "sqlite3")
	require.NoError(t, err)
	require.NoError(t, m.Migrate(refreshDropPlaintextVersion-1))

	var userID int64
	require.NoError(t, conn.QueryRow("SELECT id FROM users LIMIT 1").Scan(&userID))
	insertOld := func(token string, hash any) int64 {
		var id int64
		require.NoError(t, conn.QueryRow(
			"INSERT INTO refresh_tokens (user_id, token, token_hash, expires_at) VALUES (?, ?, ?, CURRENT_TIMESTAMP) RETURNING id",
			userID, token, hash).Scan(&id))
		return id
	}
	insertOld("plaintext-legacy", nil)
	sessionID := insertOld("hash-1", "hash-1")
	goneID := insertOld("hash-2", "hash-2")
	_, err = conn.Exec("DELETE FROM refresh_tokens WHERE id = ?", goneID)
	require.NoError(t, err)

	require.NoError(t, m.Migrate(refreshDropPlaintextVersion))

	var count int
	require.NoError(t, conn.QueryRow("SELECT COUNT(*) FROM refresh_tokens").Scan(&count))
	require.Equal(t, 1, count, "legacy plaintext rows are dropped")
	var hash string
	require.NoError(t, conn.QueryRow("SELECT token_hash FROM refresh_tokens WHERE id = ?", sessionID).Scan(&hash))
	require.Equal(t, "hash-1", hash)
	_, err = conn.Exec("SELECT token FROM refresh_tokens")
	require.Error(t, err)
	require.Contains(t, sqliteIndexes(t, conn), "idx_refresh_tokens_user")

	var nextID int64
	require.NoError(t, conn.QueryRow(
		"INSERT INTO refresh_tokens (user_id, token_hash, expires_at) VALUES (?, 'hash-3', CURRENT_TIMESTAMP) RETURNING id",
		userID).Scan(&nextID))
	require.Greater(t, nextID, goneID, "session ids are never reused")
	_, err = conn.Exec("INSERT INTO refresh_tokens (user_id, token_hash, expires_at) VALUES (?, 'hash-3', CURRENT_TIMESTAMP)", userID)
	require.Error(t, err, "token_hash is unique")

	require.NoError(t, m.Migrate(refreshDropPlaintextVersion-1))
	var token string
	require.NoError(t, conn.QueryRow("SELECT token FROM refresh_tokens WHERE id = ?", sessionID).Scan(&token))
	require.Equal(t, "hash-1", token)
	require.Greater(t, insertOld("hash-4", "hash-4"), nextID)
	require.Contains(t, sqliteIndexes(t, conn), "idx_refresh_tokens_user")

	require.NoError(t, m.Migrate(refreshDropPlaintextVersion))
}
