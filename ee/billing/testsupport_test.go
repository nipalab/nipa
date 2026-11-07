package billing

import (
	"context"
	"crypto/rand"
	"database/sql"
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
	"github.com/nipalab/nipa/ee/storageusage"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	"github.com/nipalab/nipa/internal/snow"
)

var (
	testDSN       string
	testIDCounter atomic.Int64
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
	if err := testcontainers.TerminateContainer(container); err != nil {
		fmt.Fprintln(os.Stderr, "terminate postgres test container:", err)
	}
	os.Exit(code)
}

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	require.NotEmpty(t, testDSN, "postgres test container was not started")

	var suffix [4]byte
	_, _ = rand.Read(suffix[:])
	schema := fmt.Sprintf("nipa_test_%d_%s", time.Now().UnixNano(), hex.EncodeToString(suffix[:]))
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
	db.SetMaxOpenConns(8)

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

type billingSuite struct {
	suite.Suite
	db      *sql.DB
	ledger  *storageusage.Ledger
	service *Service
	repo    *postgres.BillingRepository
}

func TestBillingSuite(t *testing.T) {
	suite.Run(t, new(billingSuite))
}

func (s *billingSuite) SetupTest() {
	s.db = newTestDB(s.T())
	s.ledger = storageusage.NewLedger(s.db, storageusage.Config{DefaultQuotaBytes: 999})
	s.repo = postgres.NewBillingRepository(s.db)
	s.service = New(s.repo, s.ledger, dbtx.NewTransactor(s.db), testConfig)
}

func (s *billingSuite) exec(query string, args ...any) {
	s.T().Helper()
	_, err := s.db.ExecContext(context.Background(), query, args...)
	s.Require().NoError(err)
}

func (s *billingSuite) seedUser(name string) snow.ID {
	s.T().Helper()
	id := snow.ID(testIDCounter.Add(1) + 1000)
	s.exec(`INSERT INTO users (id, name, email, password) VALUES ($1, $2, $3, 'x')`,
		id.Int64(), name, fmt.Sprintf("%s-%d@example.com", name, id.Int64()))
	return id
}

func (s *billingSuite) seedOrg(createdBy snow.ID) snow.ID {
	s.T().Helper()
	id := snow.ID(testIDCounter.Add(1) + 10000)
	slug := fmt.Sprintf("org-%d", id.Int64())
	s.exec(`INSERT INTO organizations (id, slug, name, created_by_user_id) VALUES ($1, $2, $2, $3)`,
		id.Int64(), slug, createdBy.Int64())
	s.exec(`INSERT INTO org_members (org_id, user_id, role) VALUES ($1, $2, 'owner')`, id.Int64(), createdBy.Int64())
	return id
}

func (s *billingSuite) seedGroup(orgID snow.ID) snow.ID {
	s.T().Helper()
	id := snow.ID(testIDCounter.Add(1) + 30000)
	s.exec(`INSERT INTO groups (id, org_id, name) VALUES ($1, $2, $3)`, id.Int64(), orgID.Int64(), fmt.Sprintf("group-%d", id.Int64()))
	return id
}

func (s *billingSuite) seats(owner snow.ID) int64 {
	s.T().Helper()
	used, err := s.repo.CountSeats(context.Background(), owner)
	s.Require().NoError(err)
	return used
}

func (s *billingSuite) quota(owner snow.ID) (sql.NullInt64, sql.NullInt64) {
	s.T().Helper()
	var quota, override sql.NullInt64
	err := s.db.QueryRowContext(context.Background(),
		`SELECT quota_bytes, override_bytes FROM owner_quota WHERE user_id = $1`, owner.Int64()).Scan(&quota, &override)
	s.Require().NoError(err)
	return quota, override
}

func errorCode(err error) int {
	var domErr *domain.Error
	if !errors.As(err, &domErr) {
		return 0
	}
	return domErr.Code
}
