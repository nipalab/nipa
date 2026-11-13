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

func (s *MigrateSuite) TestQueryPerformanceIndexes() {
	ctx := context.Background()
	s.Require().NoError(MigrateUp(s.conn))

	rows, err := s.conn.QueryContext(ctx, "SELECT indexname, indexdef FROM pg_indexes WHERE schemaname = current_schema()")
	s.Require().NoError(err)
	defer func() { _ = rows.Close() }()
	indexes := map[string]string{}
	for rows.Next() {
		var name, def string
		s.Require().NoError(rows.Scan(&name, &def))
		indexes[name] = def
	}
	s.Require().NoError(rows.Err())

	for _, name := range []string{
		"idx_tree_nodes_hash_id",
		"idx_organizations_created_by",
		"idx_pbac_rules_org_wide",
		"idx_pbac_rules_org_users",
		"idx_refresh_tokens_user",
		"idx_merge_requests_source_status",
		"idx_merge_requests_target_status",
		"idx_email_deliveries_thread",
		"idx_email_deliveries_sweep",
		"idx_audit_log_action",
		"idx_branches_project_updated_id",
	} {
		s.Contains(indexes, name)
	}
	for _, name := range []string{
		"idx_tree_nodes_hash",
		"idx_tree_nodes_parent",
		"idx_files_tree_id",
		"idx_merge_requests_project",
		"idx_merge_request_review_requests_mr",
		"idx_merge_request_checks_mr",
		"idx_audit_log_id",
	} {
		s.NotContains(indexes, name)
	}
	s.Contains(indexes["idx_audit_log_action"], `COLLATE "C"`)
	s.Contains(indexes["idx_tree_nodes_hash_id"], "(hash, id)")
	s.Contains(indexes["idx_organizations_created_by"], "WHERE (deleted = false)")
	s.Contains(indexes["idx_pbac_rules_org_wide"], "WHERE (project_id IS NULL)")
	s.Contains(indexes["idx_pbac_rules_org_users"], "WHERE (user_id IS NOT NULL)")
	s.Contains(indexes["idx_branches_project_updated_id"], "WHERE (deleted = false)")
}

const refreshDropPlaintextVersion uint = 6

func (s *MigrateSuite) TestRefreshTokenDropPlaintext() {
	ctx := context.Background()
	s.Require().NoError(MigrateDown(s.conn))
	m, err := newMigrator(s.conn)
	s.Require().NoError(err)
	s.Require().NoError(m.Migrate(refreshDropPlaintextVersion - 1))

	var userID int64
	s.Require().NoError(s.conn.QueryRowContext(ctx, "SELECT id FROM users LIMIT 1").Scan(&userID))
	_, err = s.conn.ExecContext(ctx,
		"INSERT INTO refresh_tokens (user_id, token, expires_at) VALUES ($1, 'plaintext-legacy', now())", userID)
	s.Require().NoError(err)
	var sessionID int64
	s.Require().NoError(s.conn.QueryRowContext(ctx,
		"INSERT INTO refresh_tokens (user_id, token, token_hash, expires_at) VALUES ($1, 'hash-1', 'hash-1', now()) RETURNING id",
		userID).Scan(&sessionID))

	s.Require().NoError(m.Migrate(refreshDropPlaintextVersion))

	var count int
	s.Require().NoError(s.conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM refresh_tokens").Scan(&count))
	s.Equal(1, count, "legacy plaintext rows are dropped")
	var hash string
	s.Require().NoError(s.conn.QueryRowContext(ctx, "SELECT token_hash FROM refresh_tokens WHERE id = $1", sessionID).Scan(&hash))
	s.Equal("hash-1", hash)
	_, err = s.conn.ExecContext(ctx, "SELECT token FROM refresh_tokens")
	s.Error(err)
	_, err = s.conn.ExecContext(ctx,
		"INSERT INTO refresh_tokens (user_id, token_hash, expires_at) VALUES ($1, 'hash-1', now())", userID)
	s.Error(err, "token_hash is unique")
	_, err = s.conn.ExecContext(ctx, "INSERT INTO refresh_tokens (user_id, expires_at) VALUES ($1, now())", userID)
	s.Error(err, "token_hash is required")

	s.Require().NoError(m.Migrate(refreshDropPlaintextVersion - 1))
	var token string
	s.Require().NoError(s.conn.QueryRowContext(ctx, "SELECT token FROM refresh_tokens WHERE id = $1", sessionID).Scan(&token))
	s.Equal("hash-1", token)

	s.Require().NoError(MigrateUp(s.conn))
}
