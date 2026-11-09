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

func TestMigrateQueryPerformanceRoundTrip(t *testing.T) {
	conn := openMigrateTestDB(t)
	require.NoError(t, MigrateUp(conn, "sqlite3"))
	requireQueryPerformanceUp(t, conn)

	m, err := newMigrator(conn, "sqlite3")
	require.NoError(t, err)
	version, dirty, err := m.Version()
	require.NoError(t, err)
	require.False(t, dirty)
	require.Equal(t, uint(18), version)

	require.NoError(t, m.Steps(-1))
	version, _, err = m.Version()
	require.NoError(t, err)
	require.Equal(t, uint(17), version)

	indexes := sqliteIndexes(t, conn)
	for _, name := range queryPerformanceDropped {
		require.Contains(t, indexes, name)
	}
	for _, name := range queryPerformanceAdded {
		require.NotContains(t, indexes, name)
	}
	require.Contains(t, indexes, "idx_branches_project_updated_id")
	require.NotContains(t, indexes["idx_branches_project_updated_id"], "WHERE")

	require.NoError(t, MigrateUp(conn, "sqlite3"))
	requireQueryPerformanceUp(t, conn)
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
