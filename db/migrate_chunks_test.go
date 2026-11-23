package db

import (
	"database/sql"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/stretchr/testify/require"
)

const perProjectChunksVersion uint = 29

// seedChunksBeforePerProject migrates to the version before per-project
// chunks and inserts one chunk row per name, linking it to the given
// projects through upload markers. It returns the chunk ids by name.
func seedChunksBeforePerProject(t *testing.T, conn *sql.DB, links map[string][]int64) (*migrate.Migrate, map[string]int64) {
	t.Helper()
	m, err := newMigrator(conn, "sqlite3")
	require.NoError(t, err)
	require.NoError(t, m.Migrate(perProjectChunksVersion-1))
	ids := map[string]int64{}
	for name, projects := range links {
		var id int64
		require.NoError(t, conn.QueryRow("INSERT INTO chunks (hash, size_bytes) VALUES (?, ?) RETURNING id", []byte(name), len(name)).Scan(&id))
		ids[name] = id
		for _, project := range projects {
			_, err := conn.Exec("INSERT INTO project_chunk_uploads (project_id, hash) VALUES (?, ?)", project, []byte(name))
			require.NoError(t, err)
		}
	}
	return m, ids
}

func TestMigratePerProjectChunks_Backfill(t *testing.T) {
	conn := openMigrateTestDB(t)
	m, ids := seedChunksBeforePerProject(t, conn, map[string][]int64{
		"linked-a": {1},
		"linked-b": {2},
		"orphan":   nil,
	})
	_, err := conn.Exec("INSERT INTO file_chunks (file_id, chunk_id, chunk_index) VALUES (1, ?, 0)", ids["linked-a"])
	require.NoError(t, err)

	require.NoError(t, m.Migrate(perProjectChunksVersion))

	projectOf := func(name string) (int64, bool) {
		var project int64
		err := conn.QueryRow("SELECT project_id FROM chunks WHERE hash = ?", []byte(name)).Scan(&project)
		if err == sql.ErrNoRows {
			return 0, false
		}
		require.NoError(t, err)
		return project, true
	}
	project, ok := projectOf("linked-a")
	require.True(t, ok)
	require.Equal(t, int64(1), project)
	project, ok = projectOf("linked-b")
	require.True(t, ok)
	require.Equal(t, int64(2), project)
	_, ok = projectOf("orphan")
	require.False(t, ok, "rows no project links are dropped")

	var id int64
	require.NoError(t, conn.QueryRow("SELECT id FROM chunks WHERE hash = ?", []byte("linked-a")).Scan(&id))
	require.Equal(t, ids["linked-a"], id, "ids are kept so file_chunks still resolve")

	// UNIQUE (project_id, hash): the same hash fits another project once.
	_, err = conn.Exec("INSERT INTO chunks (project_id, hash, size_bytes) VALUES (2, ?, 8)", []byte("linked-a"))
	require.NoError(t, err)
	_, err = conn.Exec("INSERT INTO chunks (project_id, hash, size_bytes) VALUES (2, ?, 8)", []byte("linked-a"))
	require.Error(t, err)
	_, err = conn.Exec("INSERT INTO chunks (hash, size_bytes) VALUES (?, 8)", []byte("no project"))
	require.Error(t, err, "project_id is required")

	var after int64
	require.NoError(t, conn.QueryRow("INSERT INTO chunks (project_id, hash, size_bytes) VALUES (3, ?, 1) RETURNING id", []byte("new")).Scan(&after))
	require.Greater(t, after, ids["orphan"], "ids keep growing past the old rows")

	// Down works while no hash is in two projects.
	_, err = conn.Exec("DELETE FROM chunks WHERE project_id = 2 AND hash = ?", []byte("linked-a"))
	require.NoError(t, err)
	require.NoError(t, m.Migrate(perProjectChunksVersion-1))
	require.False(t, sqliteColumns(t, conn, "chunks")["project_id"])
	var markers int
	require.NoError(t, conn.QueryRow("SELECT COUNT(*) FROM project_chunk_uploads WHERE project_id = 3").Scan(&markers))
	require.Equal(t, 1, markers, "down keeps the project links as upload markers")
	require.NoError(t, MigrateUp(conn, "sqlite3"))
}

func TestMigratePerProjectChunks_RefusesSharedChunks(t *testing.T) {
	conn := openMigrateTestDB(t)
	m, _ := seedChunksBeforePerProject(t, conn, map[string][]int64{"shared": {1, 2}})

	err := m.Migrate(perProjectChunksVersion)
	require.ErrorContains(t, err, "chunk_linked_to_several_projects")
	require.False(t, sqliteColumns(t, conn, "chunks")["project_id"], "nothing changed")
}

func TestMigratePerProjectChunks_RefusesReferencedOrphans(t *testing.T) {
	conn := openMigrateTestDB(t)
	m, ids := seedChunksBeforePerProject(t, conn, map[string][]int64{"orphan": nil})
	_, err := conn.Exec("INSERT INTO file_chunks (file_id, chunk_id, chunk_index) VALUES (1, ?, 0)", ids["orphan"])
	require.NoError(t, err)

	err = m.Migrate(perProjectChunksVersion)
	require.ErrorContains(t, err, "referenced_chunk_without_project")
}

func TestMigratePerProjectChunks_EmptyDatabase(t *testing.T) {
	conn := openMigrateTestDB(t)
	require.NoError(t, MigrateUp(conn, "sqlite3"))
	cols := sqliteColumns(t, conn, "chunks")
	require.True(t, cols["project_id"])
	require.NoError(t, MigrateDown(conn, "sqlite3"))
	require.NoError(t, MigrateUp(conn, "sqlite3"))
}
