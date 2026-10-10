package nodelease

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	pgxmigrate "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	postgresmigrations "github.com/nipalab/nipa/ee/db/migrations/postgres"
)

var testDSN string

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
	if err := testcontainers.TerminateContainer(container); err != nil {
		fmt.Fprintln(os.Stderr, "terminate postgres test container:", err)
	}
	os.Exit(code)
}

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	require.NotEmpty(t, testDSN, "postgres test container was not started")

	schema := testSchemaName()
	admin, err := sql.Open("pgx", testDSN)
	require.NoError(t, err)
	_, err = admin.Exec("CREATE SCHEMA " + schema)
	require.NoError(t, err)

	db, err := sql.Open("pgx", withSearchPath(testDSN, schema))
	require.NoError(t, err)
	db.SetMaxOpenConns(4)
	migrateTestSchema(t, db, schema)

	t.Cleanup(func() {
		require.NoError(t, db.Close())
		_, err := admin.Exec("DROP SCHEMA " + schema + " CASCADE")
		require.NoError(t, err)
		require.NoError(t, admin.Close())
	})
	return db
}

func testSchemaName() string {
	var suffix [4]byte
	_, _ = rand.Read(suffix[:])
	return fmt.Sprintf("nipa_nodelease_%d_%s", time.Now().UnixNano(), hex.EncodeToString(suffix[:]))
}

func withSearchPath(dsn, schema string) string {
	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	return dsn + separator + "search_path=" + schema
}

func migrateTestSchema(t *testing.T, db *sql.DB, schema string) {
	t.Helper()

	driver, err := pgxmigrate.WithInstance(db, &pgxmigrate.Config{SchemaName: schema})
	require.NoError(t, err)
	source, err := iofs.New(postgresmigrations.FS, ".")
	require.NoError(t, err)
	m, err := migrate.NewWithInstance("iofs", source, "postgres", driver)
	require.NoError(t, err)
	require.NoError(t, m.Up())
}

func newTestManager(t *testing.T, db *sql.DB, holder string, ttl time.Duration, onLost func(int64)) *Manager {
	t.Helper()

	manager, err := New(Config{DB: db, Holder: holder, TTL: ttl, OnLost: onLost})
	require.NoError(t, err)
	t.Cleanup(func() { manager.Release(context.Background()) })
	return manager
}

func leaseCount(t *testing.T, db *sql.DB) int {
	t.Helper()

	var count int
	require.NoError(t, db.QueryRow("SELECT count(*) FROM snowflake_node_leases").Scan(&count))
	return count
}

func leaseHolder(t *testing.T, db *sql.DB, nodeID int64) (string, bool) {
	t.Helper()

	var holder string
	err := db.QueryRow("SELECT holder FROM snowflake_node_leases WHERE node_id = $1", nodeID).Scan(&holder)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false
	}
	require.NoError(t, err)
	return holder, true
}

func TestNewValidation(t *testing.T) {
	db := newTestDB(t)

	_, err := New(Config{Holder: "h", TTL: time.Minute})
	require.Error(t, err)

	_, err = New(Config{DB: db, TTL: time.Minute})
	require.Error(t, err)

	_, err = New(Config{DB: db, Holder: "h", TTL: time.Millisecond})
	require.Error(t, err)
}

func TestAcquireDistinctNodes(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	mgrA := newTestManager(t, db, "instance-a", time.Minute, nil)
	nodeA, err := mgrA.Acquire(ctx)
	require.NoError(t, err)
	require.NotNil(t, nodeA)

	mgrB := newTestManager(t, db, "instance-b", time.Minute, nil)
	nodeB, err := mgrB.Acquire(ctx)
	require.NoError(t, err)
	require.NotNil(t, nodeB)

	require.NotEqual(t, mgrA.NodeID(), mgrB.NodeID())
	require.Equal(t, mgrA.NodeID(), mgrA.NodeID())
	require.Equal(t, 2, leaseCount(t, db))
}

func TestAcquireIsIdempotent(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	mgr := newTestManager(t, db, "instance-a", time.Minute, nil)
	first, err := mgr.Acquire(ctx)
	require.NoError(t, err)
	second, err := mgr.Acquire(ctx)
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.Equal(t, 1, leaseCount(t, db))
}

func TestAcquireStealsExpiredLease(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	_, err := db.ExecContext(ctx,
		`INSERT INTO snowflake_node_leases (node_id, holder, acquired_at, expires_at)
		 VALUES (0, 'dead-instance', now() - interval '2 hours', now() - interval '1 hour')`)
	require.NoError(t, err)

	mgr := newTestManager(t, db, "instance-a", time.Minute, nil)
	_, err = mgr.Acquire(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(0), mgr.NodeID())

	holder, ok := leaseHolder(t, db, 0)
	require.True(t, ok)
	require.Equal(t, "instance-a", holder)
}

func TestAcquireRejectsLiveLease(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	_, err := db.ExecContext(ctx,
		`INSERT INTO snowflake_node_leases (node_id, holder, acquired_at, expires_at)
		 VALUES (0, 'other-instance', now(), now() + interval '1 hour')`)
	require.NoError(t, err)

	mgr := newTestManager(t, db, "instance-a", time.Minute, nil)
	_, err = mgr.Acquire(ctx)
	require.NoError(t, err)
	require.NotEqual(t, int64(0), mgr.NodeID())
}

func TestAcquireExhausted(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	_, err := db.ExecContext(ctx,
		`INSERT INTO snowflake_node_leases (node_id, holder, acquired_at, expires_at)
		 SELECT g, 'filler', now(), now() + interval '1 hour' FROM generate_series(0, 254) AS g`)
	require.NoError(t, err)

	mgr := newTestManager(t, db, "instance-a", time.Minute, nil)
	_, err = mgr.Acquire(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(255), mgr.NodeID())

	second := newTestManager(t, db, "instance-b", time.Minute, nil)
	_, err = second.Acquire(ctx)
	require.ErrorIs(t, err, ErrNoNodeID)
}

func TestReleaseFreesNode(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	mgrA := newTestManager(t, db, "instance-a", time.Minute, nil)
	_, err := mgrA.Acquire(ctx)
	require.NoError(t, err)
	mgrA.Release(ctx)
	require.Zero(t, leaseCount(t, db))

	mgrB := newTestManager(t, db, "instance-b", time.Minute, nil)
	_, err = mgrB.Acquire(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(0), mgrB.NodeID())
}

func TestHeartbeatRenewsLease(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	mgr := newTestManager(t, db, "instance-a", 3*time.Second, nil)
	_, err := mgr.Acquire(ctx)
	require.NoError(t, err)

	var before time.Time
	require.NoError(t, db.QueryRow("SELECT expires_at FROM snowflake_node_leases WHERE node_id = $1", mgr.NodeID()).Scan(&before))

	require.Eventually(t, func() bool {
		var after time.Time
		err := db.QueryRow("SELECT expires_at FROM snowflake_node_leases WHERE node_id = $1", mgr.NodeID()).Scan(&after)
		return err == nil && after.After(before)
	}, 5*time.Second, 100*time.Millisecond)
}

func TestLeaseLostCallback(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	lost := make(chan int64, 1)
	mgr := newTestManager(t, db, "instance-a", 2*time.Second, func(nodeID int64) { lost <- nodeID })
	_, err := mgr.Acquire(ctx)
	require.NoError(t, err)

	_, err = db.ExecContext(ctx,
		`UPDATE snowflake_node_leases SET holder = 'thief', expires_at = now() + interval '1 hour'
		 WHERE node_id = $1`, mgr.NodeID())
	require.NoError(t, err)

	select {
	case nodeID := <-lost:
		require.Equal(t, mgr.NodeID(), nodeID)
	case <-time.After(10 * time.Second):
		t.Fatal("lease lost callback was not invoked")
	}
}
