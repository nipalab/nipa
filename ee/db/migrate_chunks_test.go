package db

import (
	"context"
	"database/sql"
	"errors"

	"github.com/golang-migrate/migrate/v4"
)

const perProjectChunksVersion uint = 20

// seedPerProjectChunks migrates to the version before per-project chunks and
// creates a second project; it returns the migrator.
func (s *MigrateSuite) seedPerProjectChunks() *migrate.Migrate {
	s.Require().NoError(MigrateDown(s.conn))
	m, err := newMigrator(s.conn)
	s.Require().NoError(err)
	s.Require().NoError(m.Migrate(perProjectChunksVersion - 1))
	s.exec("INSERT INTO projects (id, org_id, slug, name) VALUES (2, 1, 'second', 'Second')")
	return m
}

func (s *MigrateSuite) exec(query string, args ...any) {
	s.T().Helper()
	_, err := s.conn.ExecContext(context.Background(), query, args...)
	s.Require().NoError(err)
}

func (s *MigrateSuite) chunkID(name string) int64 {
	s.T().Helper()
	var id int64
	s.Require().NoError(s.conn.QueryRowContext(context.Background(),
		"INSERT INTO chunks (hash, size_bytes) VALUES ($1, $2) RETURNING id", []byte(name), len(name)).Scan(&id))
	return id
}

// chunkProject returns the project_id of the row with hash name; ok is false
// when there is no such row.
func (s *MigrateSuite) chunkProject(name string) (int64, bool) {
	s.T().Helper()
	var project int64
	err := s.conn.QueryRowContext(context.Background(), "SELECT project_id FROM chunks WHERE hash = $1", []byte(name)).Scan(&project)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false
	}
	s.Require().NoError(err)
	return project, true
}

func (s *MigrateSuite) tableExists(name string) bool {
	var exists bool
	s.Require().NoError(s.conn.QueryRowContext(context.Background(),
		"SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = $1)", name).Scan(&exists))
	return exists
}

func (s *MigrateSuite) TestPerProjectChunksBackfill() {
	m := s.seedPerProjectChunks()
	ledger := s.chunkID("ledger-linked")
	s.exec("INSERT INTO project_chunks (project_id, chunk_id, size_bytes) VALUES (2, $1, 13)", ledger)
	s.chunkID("marker-only")
	s.exec("INSERT INTO project_chunk_uploads (project_id, hash) VALUES (1, $1)", []byte("marker-only"))
	both := s.chunkID("both")
	s.exec("INSERT INTO project_chunks (project_id, chunk_id, size_bytes) VALUES (1, $1, 4)", both)
	s.exec("INSERT INTO project_chunk_uploads (project_id, hash) VALUES (1, $1)", []byte("both"))
	s.chunkID("orphan")

	s.Require().NoError(m.Migrate(perProjectChunksVersion))

	project, ok := s.chunkProject("ledger-linked")
	s.True(ok)
	s.Equal(int64(2), project, "backfilled from the ledger link")
	project, ok = s.chunkProject("marker-only")
	s.True(ok)
	s.Equal(int64(1), project, "the upload marker is the fallback")
	project, ok = s.chunkProject("both")
	s.True(ok)
	s.Equal(int64(1), project)
	_, ok = s.chunkProject("orphan")
	s.False(ok, "rows no project links are deleted")
	s.False(s.tableExists("project_chunks"))
	s.True(s.tableExists("project_chunk_uploads"), "the upload marker stays")

	// UNIQUE (project_id, hash) and NOT NULL project_id.
	_, err := s.conn.Exec("INSERT INTO chunks (project_id, hash, size_bytes) VALUES (1, $1, 13)", []byte("ledger-linked"))
	s.Require().NoError(err, "the same hash fits another project")
	_, err = s.conn.Exec("INSERT INTO chunks (project_id, hash, size_bytes) VALUES (1, $1, 13)", []byte("ledger-linked"))
	s.Error(err)
	_, err = s.conn.Exec("INSERT INTO chunks (hash, size_bytes) VALUES ($1, 1)", []byte("no project"))
	s.Error(err)

	var gcIndex bool
	s.Require().NoError(s.conn.QueryRow(
		"SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname = current_schema() AND indexname = 'idx_chunks_gc_marked')").Scan(&gcIndex))
	s.True(gcIndex, "chunks keeps its gc index")

	// Down works again once no hash is in two projects.
	s.exec("DELETE FROM chunks WHERE project_id = 1 AND hash = $1", []byte("ledger-linked"))
	s.Require().NoError(m.Migrate(perProjectChunksVersion - 1))
	s.True(s.tableExists("project_chunks"))
	var links int
	s.Require().NoError(s.conn.QueryRow("SELECT COUNT(*) FROM project_chunks").Scan(&links))
	s.Equal(3, links, "down rebuilds the ledger links from the rows")
	s.Require().NoError(MigrateUp(s.conn))
}

func (s *MigrateSuite) TestPerProjectChunksRefusesSharedChunks() {
	m := s.seedPerProjectChunks()
	shared := s.chunkID("shared")
	s.exec("INSERT INTO project_chunks (project_id, chunk_id, size_bytes) VALUES (1, $1, 6)", shared)
	s.exec("INSERT INTO project_chunk_uploads (project_id, hash) VALUES (2, $1)", []byte("shared"))

	err := m.Migrate(perProjectChunksVersion)
	s.Require().Error(err)
	s.Contains(err.Error(), "more than one project")
	s.recoverDirty(m)
}

func (s *MigrateSuite) TestPerProjectChunksRefusesReferencedOrphans() {
	m := s.seedPerProjectChunks()
	orphan := s.chunkID("orphan")
	var root, file int64
	s.Require().NoError(s.conn.QueryRow("INSERT INTO tree_nodes (hash, name) VALUES ('\\x01', '') RETURNING id").Scan(&root))
	s.Require().NoError(s.conn.QueryRow("INSERT INTO files (name, tree_id, hash, size_bytes) VALUES ('f', $1, '\\x02', 6) RETURNING id", root).Scan(&file))
	s.exec("INSERT INTO file_chunks (file_id, chunk_id, chunk_index) VALUES ($1, $2, 0)", file, orphan)

	err := m.Migrate(perProjectChunksVersion)
	s.Require().Error(err)
	s.Contains(err.Error(), "linked to no project")
	s.recoverDirty(m)
}

// recoverDirty forces a failed migration back to the version before it (the
// statements ran in one transaction, so nothing changed) and resets the
// schema for the other tests.
func (s *MigrateSuite) recoverDirty(m *migrate.Migrate) {
	s.T().Helper()
	_, dirty, err := m.Version()
	s.Require().NoError(err)
	s.True(dirty)
	s.Require().NoError(m.Force(int(perProjectChunksVersion - 1)))
	s.True(s.tableExists("project_chunks"), "the failed migration rolled back")
	s.Require().NoError(MigrateDown(s.conn))
	s.Require().NoError(MigrateUp(s.conn))
}
