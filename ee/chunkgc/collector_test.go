package chunkgc

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
	"sync"
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
	"github.com/nipalab/nipa/ee/storage/s3"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/obs"
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
	schema := fmt.Sprintf("gc_test_%d_%s", time.Now().UnixNano(), hex.EncodeToString(suffix[:]))
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
	db.SetMaxOpenConns(6)
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

// memStore is an in-memory bucket. DeleteChunk can be held on a channel to
// keep a sweep inside its transaction.
type memStore struct {
	mu       sync.Mutex
	objects  map[domain.Hash]s3.StoredChunk
	deleted  []domain.Hash
	hold     chan struct{}
	entered  chan struct{}
	failDel  error
	failList error
}

func newMemStore() *memStore {
	return &memStore{objects: map[domain.Hash]s3.StoredChunk{}}
}

func (m *memStore) put(hash domain.Hash, size int64, modified time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objects[hash] = s3.StoredChunk{Hash: hash, SizeBytes: size, LastModified: modified}
}

func (m *memStore) has(hash domain.Hash) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.objects[hash]
	return ok
}

func (m *memStore) DeleteChunk(_ context.Context, hash domain.Hash) error {
	if m.entered != nil {
		m.entered <- struct{}{}
	}
	if m.hold != nil {
		<-m.hold
	}
	if m.failDel != nil {
		return m.failDel
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objects, hash)
	m.deleted = append(m.deleted, hash)
	return nil
}

func (m *memStore) ListChunks(_ context.Context, fn func(s3.StoredChunk) error) error {
	if m.failList != nil {
		return m.failList
	}
	m.mu.Lock()
	objects := make([]s3.StoredChunk, 0, len(m.objects))
	for _, object := range m.objects {
		objects = append(objects, object)
	}
	m.mu.Unlock()
	for _, object := range objects {
		if err := fn(object); err != nil {
			return err
		}
	}
	return nil
}

type gcSuite struct {
	suite.Suite
	db    *sql.DB
	store *memStore
	now   time.Time
}

func TestGCSuite(t *testing.T) {
	suite.Run(t, new(gcSuite))
}

func (s *gcSuite) SetupTest() {
	s.db = newTestDB(s.T())
	s.store = newMemStore()
	s.now = time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
}

func (s *gcSuite) exec(query string, args ...any) {
	s.T().Helper()
	_, err := s.db.ExecContext(context.Background(), query, args...)
	s.Require().NoError(err)
}

func (s *gcSuite) count(query string, args ...any) int {
	s.T().Helper()
	var n int
	s.Require().NoError(s.db.QueryRowContext(context.Background(), query, args...).Scan(&n))
	return n
}

func newHash() domain.Hash {
	var hash domain.Hash
	binary.LittleEndian.PutUint64(hash[24:], uint64(testCounter.Add(1)))
	return hash
}

// chunk records a chunk row and its object; it returns the hash and row id.
func (s *gcSuite) chunk(size int64) (domain.Hash, int64) {
	s.T().Helper()
	hash := newHash()
	var id int64
	s.Require().NoError(s.db.QueryRowContext(context.Background(),
		`INSERT INTO chunks (hash, size_bytes) VALUES ($1, $2) RETURNING id`, hash[:], size).Scan(&id))
	s.store.put(hash, size, s.now.Add(-48*time.Hour))
	return hash, id
}

func (s *gcSuite) hasRow(hash domain.Hash) bool {
	return s.count(`SELECT count(*) FROM chunks WHERE hash = $1`, hash[:]) == 1
}

func (s *gcSuite) collector(mode Mode, at time.Time) *Collector {
	s.T().Helper()
	collector, err := New(s.db, s.store, Config{Mode: mode, Grace: 24 * time.Hour, OrphanScan: true})
	s.Require().NoError(err)
	collector.now = func() time.Time { return at }
	return collector
}

func (s *gcSuite) TestMarksOnlyUnreferencedChunksAndSweepsAfterTheGrace() {
	ctx := context.Background()
	inFile, inFileID := s.chunk(10)
	root := int64(0)
	s.Require().NoError(s.db.QueryRowContext(ctx, `INSERT INTO tree_nodes (hash, name) VALUES ($1, '') RETURNING id`, newHash().Bytes()).Scan(&root))
	var fileID int64
	s.Require().NoError(s.db.QueryRowContext(ctx, `INSERT INTO files (name, tree_id, hash, size_bytes) VALUES ('f', $1, $2, 10) RETURNING id`, root, newHash().Bytes()).Scan(&fileID))
	s.exec(`INSERT INTO file_chunks (file_id, chunk_id, chunk_index) VALUES ($1, $2, 0)`, fileID, inFileID)
	uploaded, _ := s.chunk(20)
	s.exec(`INSERT INTO project_chunk_uploads (project_id, hash) VALUES (1, $1)`, uploaded[:])
	billed, billedID := s.chunk(30)
	s.exec(`INSERT INTO project_chunks (project_id, chunk_id, size_bytes) VALUES (1, $1, 30)`, billedID)
	garbage, _ := s.chunk(40)
	metrics := obs.NewMetrics()

	report, err := s.collector(ModeOn, s.now).WithMetrics(metrics).Run(ctx)
	s.Require().NoError(err)
	s.Equal(int64(1), report.Marked)
	s.Equal(int64(40), report.MarkedBytes)
	s.Zero(report.Deleted, "freshly marked chunks wait out the grace")
	s.True(s.hasRow(garbage))

	report, err = s.collector(ModeOn, s.now.Add(time.Hour)).Run(ctx)
	s.Require().NoError(err)
	s.Zero(report.Marked, "a marked chunk is not marked again")
	s.Zero(report.Deleted)

	report, err = s.collector(ModeOn, s.now.Add(25*time.Hour)).Run(ctx)
	s.Require().NoError(err)
	s.Equal(int64(1), report.Deleted)
	s.Equal(int64(40), report.DeletedBytes)
	s.False(s.hasRow(garbage))
	s.False(s.store.has(garbage))
	for _, kept := range []domain.Hash{inFile, uploaded, billed} {
		s.True(s.hasRow(kept))
		s.True(s.store.has(kept))
	}
}

func (s *gcSuite) TestReferencedAgainIsNotSwept() {
	ctx := context.Background()
	hash, _ := s.chunk(10)
	_, err := s.collector(ModeOn, s.now).Run(ctx)
	s.Require().NoError(err)
	s.Equal(1, s.count(`SELECT count(*) FROM chunks WHERE gc_marked_at IS NOT NULL`))

	// A project links the chunk without the guard (the mark stays stale).
	s.exec(`INSERT INTO project_chunk_uploads (project_id, hash) VALUES (1, $1)`, hash[:])
	report, err := s.collector(ModeOn, s.now.Add(48*time.Hour)).Run(ctx)
	s.Require().NoError(err)
	s.Zero(report.Deleted, "the sweep re-checks references")
	s.True(s.store.has(hash))
}

func (s *gcSuite) TestDryRunDeletesNothing() {
	ctx := context.Background()
	hash, _ := s.chunk(10)
	orphan := newHash()
	s.store.put(orphan, 5, s.now.Add(-48*time.Hour))

	_, err := s.collector(ModeDryRun, s.now).Run(ctx)
	s.Require().NoError(err)
	report, err := s.collector(ModeDryRun, s.now.Add(25*time.Hour)).Run(ctx)
	s.Require().NoError(err)
	s.Equal(int64(1), report.Candidates)
	s.Equal(int64(10), report.CandidateBytes)
	s.Equal(int64(1), report.Adopted, "orphans are counted")
	s.Zero(report.Deleted)
	s.True(s.hasRow(hash))
	s.True(s.store.has(hash))
	s.False(s.hasRow(orphan), "dry run adopts nothing")
}

func (s *gcSuite) TestAdoptsOldOrphanObjectsAndSweepsThem() {
	ctx := context.Background()
	old := newHash()
	s.store.put(old, 7, s.now.Add(-48*time.Hour))
	recent := newHash()
	s.store.put(recent, 9, s.now.Add(-time.Hour))
	recorded, _ := s.chunk(11)
	s.exec(`INSERT INTO project_chunk_uploads (project_id, hash) VALUES (1, $1)`, recorded[:])

	report, err := s.collector(ModeOn, s.now).Run(ctx)
	s.Require().NoError(err)
	s.Equal(int64(1), report.Adopted)
	s.Equal(int64(7), report.AdoptedBytes)
	s.True(s.hasRow(old))
	s.False(s.hasRow(recent), "an upload younger than the grace may still be confirmed")

	report, err = s.collector(ModeOn, s.now.Add(25*time.Hour)).Run(ctx)
	s.Require().NoError(err)
	s.Equal(int64(1), report.Deleted)
	s.False(s.store.has(old))
	s.True(s.store.has(recorded))
}

func (s *gcSuite) TestGuardClearsTheMark() {
	ctx := context.Background()
	hash, _ := s.chunk(10)
	_, err := s.collector(ModeOn, s.now).Run(ctx)
	s.Require().NoError(err)

	s.Require().NoError(NewGuard(s.db).Protect(ctx, []domain.Hash{hash, newHash()}))
	s.Require().NoError(NewGuard(s.db).Protect(ctx, nil))
	s.Zero(s.count(`SELECT count(*) FROM chunks WHERE gc_marked_at IS NOT NULL`))

	report, err := s.collector(ModeOn, s.now.Add(25*time.Hour)).Run(ctx)
	s.Require().NoError(err)
	s.Zero(report.Deleted, "re-marked by this run, so the grace starts again")
	s.True(s.store.has(hash))
}

// TestGuardWaitsForAnInFlightSweep is the race the guard exists for: a sweep
// has locked the row and is deleting the object when a confirm protects the
// hash. Protect must not return before the sweep committed, and afterwards the
// chunk is gone, so the confirm reads it as missing instead of linking it.
func (s *gcSuite) TestGuardWaitsForAnInFlightSweep() {
	ctx := context.Background()
	hash, _ := s.chunk(10)
	_, err := s.collector(ModeOn, s.now).Run(ctx)
	s.Require().NoError(err)

	s.store.hold = make(chan struct{})
	s.store.entered = make(chan struct{}, 1)
	sweepDone := make(chan error, 1)
	go func() {
		_, err := s.collector(ModeOn, s.now.Add(25*time.Hour)).Run(ctx)
		sweepDone <- err
	}()
	<-s.store.entered

	protected := make(chan error, 1)
	go func() { protected <- NewGuard(s.db).Protect(ctx, []domain.Hash{hash}) }()
	select {
	case err := <-protected:
		s.FailNow("Protect returned while the sweep held the row", "err: %v", err)
	case <-time.After(300 * time.Millisecond):
	}

	close(s.store.hold)
	s.Require().NoError(<-sweepDone)
	s.Require().NoError(<-protected)
	s.False(s.hasRow(hash))
	s.False(s.store.has(hash), "the content is gone: a confirm reports it missing and the client uploads it again")
}

func (s *gcSuite) TestFailedObjectDeleteKeepsTheRows() {
	ctx := context.Background()
	hash, _ := s.chunk(10)
	_, err := s.collector(ModeOn, s.now).Run(ctx)
	s.Require().NoError(err)

	s.store.failDel = errors.New("access denied")
	_, err = s.collector(ModeOn, s.now.Add(25*time.Hour)).Run(ctx)
	s.Require().Error(err)
	s.True(s.hasRow(hash), "the batch rolls back; the next run retries")

	s.store.failDel = nil
	report, err := s.collector(ModeOn, s.now.Add(26*time.Hour)).Run(ctx)
	s.Require().NoError(err)
	s.Equal(int64(1), report.Deleted)
}

func (s *gcSuite) TestOnlyOneInstanceRuns() {
	ctx := context.Background()
	conn, err := s.db.Conn(ctx)
	s.Require().NoError(err)
	defer func() { _ = conn.Close() }()
	_, err = conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, advisoryLockID)
	s.Require().NoError(err)

	report, err := s.collector(ModeOn, s.now).Run(ctx)
	s.Require().NoError(err)
	s.True(report.Skipped)

	_, err = conn.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, advisoryLockID)
	s.Require().NoError(err)
	report, err = s.collector(ModeOn, s.now).Run(ctx)
	s.Require().NoError(err)
	s.False(report.Skipped)
}

func (s *gcSuite) TestOrphanScanErrorAndCancel() {
	s.store.failList = errors.New("list denied")
	_, err := s.collector(ModeOn, s.now).Run(context.Background())
	s.Require().ErrorContains(err, "orphan scan")

	s.store.failList = nil
	s.chunk(10)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = s.collector(ModeOn, s.now).Run(ctx)
	s.Require().Error(err)
}

func (s *gcSuite) TestManyChunksAcrossBatches() {
	ctx := context.Background()
	const n = sweepBatch*2 + 7
	for i := 0; i < n; i++ {
		s.chunk(1)
	}
	_, err := s.collector(ModeOn, s.now).Run(ctx)
	s.Require().NoError(err)
	report, err := s.collector(ModeOn, s.now.Add(25*time.Hour)).Run(ctx)
	s.Require().NoError(err)
	s.Equal(int64(n), report.Deleted)
	s.Zero(s.count(`SELECT count(*) FROM chunks`))
}

func TestParseMode(t *testing.T) {
	for raw, want := range map[string]Mode{"": ModeDryRun, "dry-run": ModeDryRun, " ON ": ModeOn, "off": ModeOff} {
		got, err := ParseMode(raw)
		require.NoError(t, err)
		require.Equal(t, want, got)
	}
	_, err := ParseMode("yes")
	require.Error(t, err)
}

func TestNew_Validates(t *testing.T) {
	_, err := New(nil, newMemStore(), Config{Mode: ModeOff})
	require.Error(t, err)
	_, err = New(nil, nil, Config{Mode: ModeOn})
	require.Error(t, err)
	collector, err := New(nil, newMemStore(), Config{Mode: ModeOn})
	require.NoError(t, err)
	require.Equal(t, DefaultGrace, collector.cfg.Grace)
}

func TestRun_DatabaseErrors(t *testing.T) {
	db := newTestDB(t)
	collector, err := New(db, newMemStore(), Config{Mode: ModeOn})
	require.NoError(t, err)
	_, err = db.Exec(`DROP TABLE file_chunks CASCADE`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO chunks (hash, size_bytes) VALUES ('\x01', 1)`)
	require.NoError(t, err)
	_, err = collector.Run(context.Background())
	require.ErrorContains(t, err, "mark")

	_ = db.Close()
	_, err = collector.Run(context.Background())
	require.Error(t, err)
	require.Error(t, NewGuard(db).Protect(context.Background(), []domain.Hash{{1}}), "guard on a closed database")
}
