package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/suite"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
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

type MigrateSuite struct {
	suite.Suite
	conn *sql.DB
}

func TestMigrateSuite(t *testing.T) {
	suite.Run(t, new(MigrateSuite))
}

func (s *MigrateSuite) SetupTest() {
	conn, err := Open(testDSN)
	s.Require().NoError(err)
	s.conn = conn
}

func (s *MigrateSuite) TearDownTest() {
	s.Require().NoError(s.conn.Close())
}

func (s *MigrateSuite) TestUpDown() {
	ctx := context.Background()

	s.Require().NoError(MigrateDown(s.conn))
	s.Require().NoError(MigrateUp(s.conn))

	var count int
	s.Require().NoError(s.conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE email = 'nipa'").Scan(&count))
	s.Equal(1, count)

	var exists bool
	s.Require().NoError(s.conn.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'merge_request_reviews')").Scan(&exists))
	s.True(exists)

	s.Require().NoError(MigrateDown(s.conn))
	s.Require().NoError(s.conn.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'users')").Scan(&exists))
	s.False(exists)
}
