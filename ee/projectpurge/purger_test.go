package projectpurge

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	pgxmigrate "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	postgresmigrations "github.com/nipalab/nipa/ee/db/migrations/postgres"
	"github.com/nipalab/nipa/ee/repository/postgres"
	sqlcPostgres "github.com/nipalab/nipa/ee/repository/postgres/sqlc"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

var (
	testDSN     string
	testCounter atomic.Int64
)

func TestMain(m *testing.M) {
	if dsn := os.Getenv("NIPA_TEST_POSTGRES_DSN"); dsn != "" {
		testDSN = dsn
		os.Exit(m.Run())
	}
	ctx := context.Background()
	container, err := tcpostgres.Run(ctx, "postgres:17",
		tcpostgres.WithDatabase("nipa"),
		tcpostgres.WithUsername("nipa"),
		tcpostgres.WithPassword("nipa"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "start postgres test container:", err)
		os.Exit(1)
	}
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintln(os.Stderr, "postgres connection string:", err)
		_ = testcontainers.TerminateContainer(container)
		os.Exit(1)
	}
	testDSN = dsn
	code := m.Run()
	_ = testcontainers.TerminateContainer(container)
	os.Exit(code)
}

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	var suffix [4]byte
	_, _ = rand.Read(suffix[:])
	schema := fmt.Sprintf("purge_test_%d_%s", time.Now().UnixNano(), hex.EncodeToString(suffix[:]))
	admin, err := sql.Open("pgx", testDSN)
	require.NoError(t, err)
	_, err = admin.Exec("CREATE SCHEMA " + schema)
	require.NoError(t, err)
	separator := "?"
	if strings.Contains(testDSN, "?") {
		separator = "&"
	}
	db, err := sql.Open("pgx", testDSN+separator+"search_path="+schema)
	require.NoError(t, err)
	db.SetMaxOpenConns(4)
	driver, err := pgxmigrate.WithInstance(db, &pgxmigrate.Config{SchemaName: schema})
	require.NoError(t, err)
	source, err := iofs.New(postgresmigrations.FS, ".")
	require.NoError(t, err)
	m, err := migrate.NewWithInstance("iofs", source, "postgres", driver)
	require.NoError(t, err)
	require.NoError(t, m.Up())
	t.Cleanup(func() {
		require.NoError(t, db.Close())
		_, err := admin.Exec("DROP SCHEMA " + schema + " CASCADE")
		require.NoError(t, err)
		require.NoError(t, admin.Close())
	})
	return db
}

type fakeReleaser struct{ released []snow.ID }

func (f *fakeReleaser) ReleaseProject(_ context.Context, projectID snow.ID) (int64, error) {
	f.released = append(f.released, projectID)
	return 0, nil
}

type purgeSuite struct {
	suite.Suite
	db  *sql.DB
	now time.Time
}

func TestPurgeSuite(t *testing.T) {
	suite.Run(t, new(purgeSuite))
}

func (s *purgeSuite) SetupTest() {
	s.db = newTestDB(s.T())
	s.now = time.Now()
}

func (s *purgeSuite) exec(query string, args ...any) {
	s.T().Helper()
	_, err := s.db.ExecContext(context.Background(), query, args...)
	s.Require().NoError(err)
}

func (s *purgeSuite) count(query string, args ...any) int {
	s.T().Helper()
	var n int
	s.Require().NoError(s.db.QueryRowContext(context.Background(), query, args...).Scan(&n))
	return n
}

func nextID() int64 { return testCounter.Add(1) + 100000 }

func hashFor(label string) []byte {
	b := make([]byte, 32)
	copy(b, label)
	return b
}

func uniqueHash() []byte {
	b := make([]byte, 32)
	binary.LittleEndian.PutUint64(b[24:], uint64(nextID()))
	return b
}

func (s *purgeSuite) project(slug string) snow.ID {
	s.T().Helper()
	id := nextID()
	s.exec(`INSERT INTO projects (id, org_id, slug, name) VALUES ($1, 1, $2, $2)`, id, slug)
	return snow.ID(id)
}

func (s *purgeSuite) deleteProject(id snow.ID, ago time.Duration) {
	s.T().Helper()
	s.exec(`UPDATE projects SET deleted = true, deleted_at = $2 WHERE id = $1`, id.Int64(), s.now.Add(-ago))
}

func (s *purgeSuite) chunk(projectID snow.ID) int64 {
	s.T().Helper()
	var id int64
	s.Require().NoError(s.db.QueryRowContext(context.Background(),
		`INSERT INTO chunks (project_id, hash, size_bytes) VALUES ($1, $2, 10) RETURNING id`, projectID.Int64(), uniqueHash()).Scan(&id))
	return id
}

// fakeStore records the projects whose objects the purge deleted.
type fakeStore struct {
	deleted []snow.ID
	err     error
}

func (f *fakeStore) DeleteProject(_ context.Context, projectID snow.ID) error {
	if f.err != nil {
		return f.err
	}
	f.deleted = append(f.deleted, projectID)
	return nil
}

func (s *purgeSuite) tree(hash []byte, name string, parent *int64) int64 {
	s.T().Helper()
	var id int64
	s.Require().NoError(s.db.QueryRowContext(context.Background(),
		`INSERT INTO tree_nodes (hash, name, parent_tree_id) VALUES ($1, $2, $3) RETURNING id`, hash, name, parent).Scan(&id))
	return id
}

func (s *purgeSuite) file(treeID, chunkID int64, name string) {
	s.T().Helper()
	var fileID int64
	s.Require().NoError(s.db.QueryRowContext(context.Background(),
		`INSERT INTO files (name, tree_id, hash, size_bytes) VALUES ($1, $2, $3, 10) RETURNING id`,
		name, treeID, uniqueHash()).Scan(&fileID))
	s.exec(`INSERT INTO file_chunks (file_id, chunk_id, chunk_index) VALUES ($1, $2, 0)`, fileID, chunkID)
}

func (s *purgeSuite) commit(projectID snow.ID, treeID int64, parent *int64) int64 {
	s.T().Helper()
	id := nextID()
	s.exec(`INSERT INTO commits (id, hash, project_id, tree_id, parent_1_id, user_id, message) VALUES ($1, $2, $3, $4, $5, 1, 'c')`,
		id, uniqueHash(), projectID.Int64(), treeID, parent)
	return id
}

// seedFullProject gives a project a two-commit history whose first commit
// holds an "assets" directory with the content hash shared, refs, a merge
// request with every commit-referencing child, a file lock, its chunk row,
// upload marker and usage counter. It returns the commit ids.
func (s *purgeSuite) seedFullProject(projectID snow.ID, shared []byte) (int64, int64) {
	s.T().Helper()
	chunkID := s.chunk(projectID)
	root1 := s.tree(uniqueHash(), "", nil)
	assets := s.tree(shared, "assets", &root1)
	s.file(assets, chunkID, "hero.png")
	s.file(root1, chunkID, "README.md")
	c1 := s.commit(projectID, root1, nil)
	root2 := s.tree(uniqueHash(), "", nil)
	s.tree(shared, "assets", &root2) // kept directory: a bare node
	c2 := s.commit(projectID, root2, &c1)

	pid := projectID.Int64()
	main := nextID()
	feature := nextID()
	s.exec(`INSERT INTO branches (id, project_id, name, key, commit_id, is_default) VALUES ($1, $2, 'main', 'main', $3, true)`, main, pid, c2)
	s.exec(`INSERT INTO branches (id, project_id, name, key, commit_id) VALUES ($1, $2, 'feature', 'feature', $3)`, feature, pid, c1)
	s.exec(`INSERT INTO tags (id, project_id, name, key, commit_id, user_id) VALUES ($1, $2, 'v1', 'v1', $3, 1)`, nextID(), pid, c1)
	mr := nextID()
	s.exec(`INSERT INTO merge_requests (id, number, project_id, source_branch_id, target_branch_id, source_branch_name, target_branch_name, title, created_by, merge_base_commit_id)
		VALUES ($1, 1, $2, $3, $4, 'feature', 'main', 'mr', 1, $5)`, mr, pid, feature, main, c1)
	s.exec(`INSERT INTO merge_request_events (id, merge_request_id, actor_id, kind, commit_id) VALUES ($1, $2, 1, 'pushed', $3)`, nextID(), mr, c2)
	s.exec(`INSERT INTO merge_request_checks (id, merge_request_id, head_commit_id, name, state, reporter_id) VALUES ($1, $2, $3, 'ci', 'success', 1)`, nextID(), mr, c2)
	s.exec(`INSERT INTO merge_request_reviews (id, merge_request_id, reviewer_id, state, head_commit_id) VALUES ($1, $2, 1, 'approved', $3)`, nextID(), mr, c2)
	s.exec(`INSERT INTO merge_request_threads (id, merge_request_id, base_commit_id, head_commit_id, created_by) VALUES ($1, $2, $3, $4, 1)`, nextID(), mr, c1, c2)
	s.exec(`INSERT INTO file_locks (id, project_id, branch_id, path, held_by, merge_request_id) VALUES ($1, $2, $3, 'assets/hero.png', 1, $4)`, nextID(), pid, main, mr)
	s.exec(`INSERT INTO project_chunk_uploads (project_id, hash) SELECT $1, hash FROM chunks WHERE id = $2`, pid, chunkID)
	s.exec(`INSERT INTO project_usage (project_id, used_bytes) VALUES ($1, 10)`, pid)
	return c1, c2
}

func (s *purgeSuite) newPurger(batch int) *Purger {
	s.T().Helper()
	purger, err := New(s.db, Config{Retention: 30 * 24 * time.Hour, BatchSize: batch})
	s.Require().NoError(err)
	purger.now = func() time.Time { return s.now }
	return purger
}

func (s *purgeSuite) TestPurgeExpired_RemovesTheProjectForGood() {
	ctx := context.Background()
	shared := hashFor("shared assets dir")
	purged := s.project("old-game")
	s.seedFullProject(purged, shared)

	// A live project created later keeps an "assets" directory with the same
	// content: its bare node resolves by hash to the oldest content node,
	// which belongs to the purged project until the purge.
	live := s.project("live-game")
	liveChunk := s.chunk(live)
	liveRoot1 := s.tree(uniqueHash(), "", nil)
	liveAssets := s.tree(shared, "assets", &liveRoot1)
	s.file(liveAssets, liveChunk, "hero.png")
	liveC1 := s.commit(live, liveRoot1, nil)
	liveRoot2 := s.tree(uniqueHash(), "", nil)
	liveKept := s.tree(shared, "assets", &liveRoot2)
	s.commit(live, liveRoot2, &liveC1)

	recent := s.project("recent")
	s.seedFullProject(recent, uniqueHash())
	s.deleteProject(recent, 24*time.Hour)
	s.deleteProject(purged, 31*24*time.Hour)

	queries := sqlcPostgres.New(s.db)
	files, err := queries.FileListByTree(ctx, liveKept)
	s.Require().NoError(err)
	s.Require().Len(files, 1, "the kept directory reads through the oldest content node")

	node, err := snow.NewNode(1)
	s.Require().NoError(err)
	audit := usecase.NewAudit(postgres.NewAuditRepository(s.db), node, dbtx.NewTransactor(s.db))
	releaser := &fakeReleaser{}
	store := &fakeStore{}
	purger := s.newPurger(1).WithAudit(audit).WithReleaser(releaser).WithStore(store)

	stats, err := purger.PurgeExpired(ctx)
	s.Require().NoError(err)
	s.Require().Len(stats, 1, "only the project past its retention")
	s.Equal(purged, stats[0].ProjectID)
	s.Equal(int64(2), stats[0].Commits)
	s.Equal(int64(2), stats[0].Trees)
	s.Equal([]snow.ID{purged}, releaser.released)
	s.Equal(int64(1), stats[0].Chunks)
	s.Equal([]snow.ID{purged}, store.deleted, "the project's objects go with one prefix delete")

	pid := purged.Int64()
	for table, query := range map[string]string{
		"projects":              `SELECT count(*) FROM projects WHERE id = $1`,
		"commits":               `SELECT count(*) FROM commits WHERE project_id = $1`,
		"branches":              `SELECT count(*) FROM branches WHERE project_id = $1`,
		"tags":                  `SELECT count(*) FROM tags WHERE project_id = $1`,
		"merge_requests":        `SELECT count(*) FROM merge_requests WHERE project_id = $1`,
		"file_locks":            `SELECT count(*) FROM file_locks WHERE project_id = $1`,
		"project_chunk_uploads": `SELECT count(*) FROM project_chunk_uploads WHERE project_id = $1`,
		"chunks":                `SELECT count(*) FROM chunks WHERE project_id = $1`,
		"project_usage":         `SELECT count(*) FROM project_usage WHERE project_id = $1`,
	} {
		s.Zero(s.count(query, pid), "%s rows remain", table)
	}
	s.Equal(3, s.count(`SELECT count(*) FROM files`), "the live (1) and the recent (2) project keep their files")
	s.Zero(s.count(`SELECT count(*) FROM file_chunks fc LEFT JOIN files f ON f.id = fc.file_id WHERE f.id IS NULL`))
	s.Zero(s.count(`SELECT count(*) FROM tree_nodes t WHERE t.parent_tree_id IS NULL AND NOT EXISTS (SELECT 1 FROM commits c WHERE c.tree_id = t.id)`),
		"no orphaned tree root is left behind")
	s.Equal(2, s.count(`SELECT count(*) FROM chunks`), "the live and the recent project keep their chunk rows")

	files, err = queries.FileListByTree(ctx, liveKept)
	s.Require().NoError(err)
	s.Require().Len(files, 1, "the live project still reads its kept directory, now through its own content")
	s.Equal("hero.png", files[0].Name)

	s.Equal(1, s.count(`SELECT count(*) FROM projects WHERE id = $1 AND deleted`, recent.Int64()), "inside the window it stays")
	s.Positive(s.count(`SELECT count(*) FROM commits WHERE project_id = $1`, recent.Int64()))

	s.exec(`INSERT INTO projects (id, org_id, slug, name) VALUES ($1, 1, 'old-game', 'again')`, nextID())

	var action, target string
	var orgID sql.NullInt64
	var commits int64
	s.Require().NoError(s.db.QueryRowContext(ctx,
		`SELECT action, target_id, org_id, (metadata->>'commits')::bigint FROM audit_log WHERE action = $1`,
		domain.AuditProjectPurged).Scan(&action, &target, &orgID, &commits))
	s.Equal(purged.Base36(), target)
	s.Equal(int64(1), orgID.Int64)
	s.Equal(int64(2), commits)
}

func (s *purgeSuite) TestPurgeProject_OwnStorageKeepsItsObjects() {
	ctx := context.Background()
	project := s.project("byo")
	s.seedFullProject(project, uniqueHash())
	s.exec(`INSERT INTO storage_servers (id, owner_user_id, name, endpoint, bucket, access_key_id, secret_sealed)
		VALUES (91, 1, 'byo', 'https://s3.example.com', 'b', 'a', '\x00')`)
	s.exec(`INSERT INTO project_storage (project_id, storage_server_id) VALUES ($1, 91)`, project.Int64())
	s.deleteProject(project, 40*24*time.Hour)

	store := &fakeStore{}
	_, err := s.newPurger(10).WithStore(store).PurgeProject(ctx, project, s.now.Add(-30*24*time.Hour))
	s.Require().NoError(err)
	s.Empty(store.deleted, "the customer's bucket is never touched")
	s.Zero(s.count(`SELECT count(*) FROM chunks WHERE project_id = $1`, project.Int64()), "the rows go anyway")
}

func (s *purgeSuite) TestPurgeProject_StoreErrorStopsBeforeTheProjectRow() {
	ctx := context.Background()
	project := s.project("store-error")
	s.seedFullProject(project, uniqueHash())
	s.deleteProject(project, 40*24*time.Hour)

	store := &fakeStore{err: errors.New("bucket down")}
	_, err := s.newPurger(10).WithStore(store).PurgeProject(ctx, project, s.now.Add(-30*24*time.Hour))
	s.Require().ErrorContains(err, "bucket down")
	s.Equal(1, s.count(`SELECT count(*) FROM projects WHERE id = $1`, project.Int64()), "the next run retries the prefix delete")
	s.Zero(s.count(`SELECT count(*) FROM chunks WHERE project_id = $1`, project.Int64()))

	store.err = nil
	stats, err := s.newPurger(10).WithStore(store).PurgeProject(ctx, project, s.now.Add(-30*24*time.Hour))
	s.Require().NoError(err)
	s.Zero(stats.Chunks)
	s.Equal([]snow.ID{project}, store.deleted)
	s.Zero(s.count(`SELECT count(*) FROM projects WHERE id = $1`, project.Int64()))
}

func (s *purgeSuite) TestPurgeProject_RestoredProjectIsLeftAlone() {
	ctx := context.Background()
	project := s.project("restored")
	s.seedFullProject(project, uniqueHash())
	s.deleteProject(project, 40*24*time.Hour)
	s.exec(`UPDATE projects SET deleted = false, deleted_at = NULL WHERE id = $1`, project.Int64())

	_, err := s.newPurger(10).PurgeProject(ctx, project, s.now.Add(-30*24*time.Hour))
	s.ErrorIs(err, errGone)
	s.Equal(2, s.count(`SELECT count(*) FROM commits WHERE project_id = $1`, project.Int64()))

	stats, err := s.newPurger(10).PurgeExpired(ctx)
	s.Require().NoError(err)
	s.Empty(stats)
}

func (s *purgeSuite) TestPurgeExpired_SkipsAFailingProject() {
	ctx := context.Background()
	broken := s.project("broken")
	s.seedFullProject(broken, uniqueHash())
	s.deleteProject(broken, 40*24*time.Hour)
	// A row outside the project that still points at its commit blocks the
	// commit delete.
	other := s.project("other")
	var foreignCommit int64
	s.Require().NoError(s.db.QueryRowContext(ctx, `SELECT id FROM commits WHERE project_id = $1 LIMIT 1`, broken.Int64()).Scan(&foreignCommit))
	s.exec(`INSERT INTO tags (id, project_id, name, key, commit_id, user_id) VALUES ($1, $2, 'x', 'x', $3, 1)`, nextID(), other.Int64(), foreignCommit)

	ok := s.project("ok")
	s.seedFullProject(ok, uniqueHash())
	s.deleteProject(ok, 35*24*time.Hour)

	stats, err := s.newPurger(10).PurgeExpired(ctx)
	s.Require().NoError(err)
	s.Require().Len(stats, 1)
	s.Equal(ok, stats[0].ProjectID)
	s.Equal(1, s.count(`SELECT count(*) FROM projects WHERE id = $1`, broken.Int64()))
}

func (s *purgeSuite) TestPurgeExpired_StopsOnCancel() {
	project := s.project("cancel")
	s.deleteProject(project, 40*24*time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := s.newPurger(10).PurgeExpired(ctx)
	s.Error(err)
}

func TestNew_Validates(t *testing.T) {
	_, err := New(nil, Config{})
	require.Error(t, err)
	purger, err := New(nil, Config{Retention: time.Hour})
	require.NoError(t, err)
	require.Equal(t, int64(DefaultBatchSize), purger.batch)
}

func TestPurgeExpired_ListError(t *testing.T) {
	db := newTestDB(t)
	purger, err := New(db, Config{Retention: time.Hour})
	require.NoError(t, err)
	require.NoError(t, db.Close())
	_, err = purger.PurgeExpired(context.Background())
	require.Error(t, err)
}
