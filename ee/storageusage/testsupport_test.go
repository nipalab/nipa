package storageusage

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
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

var (
	testDSN         string
	testHashCounter atomic.Int64
	testIDCounter   atomic.Int64
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

	schema := testSchemaName()
	admin, err := sql.Open("pgx", testDSN)
	require.NoError(t, err)
	_, err = admin.Exec("CREATE SCHEMA " + schema)
	require.NoError(t, err)

	db, err := sql.Open("pgx", withSearchPath(testDSN, schema))
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

func testSchemaName() string {
	var suffix [4]byte
	_, _ = rand.Read(suffix[:])
	return fmt.Sprintf("nipa_test_%d_%s", time.Now().UnixNano(), hex.EncodeToString(suffix[:]))
}

func withSearchPath(dsn, schema string) string {
	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	return dsn + separator + "search_path=" + schema
}

type ledgerSuite struct {
	suite.Suite
	db     *sql.DB
	ledger *Ledger
}

func TestLedgerSuite(t *testing.T) {
	suite.Run(t, new(ledgerSuite))
}

func (s *ledgerSuite) SetupTest() {
	s.db = newTestDB(s.T())
	s.ledger = NewLedger(s.db, Config{})
}

func (s *ledgerSuite) seedUser(name string) snow.ID {
	s.T().Helper()
	id := snow.ID(testIDCounter.Add(1) + 1000)
	_, err := s.db.ExecContext(context.Background(),
		`INSERT INTO users (id, name, email, password) VALUES ($1, $2, $3, 'x')`,
		id.Int64(), name, fmt.Sprintf("%s@example.com", name),
	)
	s.Require().NoError(err)
	return id
}

func (s *ledgerSuite) seedOrg(createdBy snow.ID) snow.ID {
	s.T().Helper()
	id := snow.ID(testIDCounter.Add(1) + 10000)
	slug := fmt.Sprintf("org-%d", id.Int64())
	_, err := s.db.ExecContext(context.Background(),
		`INSERT INTO organizations (id, slug, name, created_by_user_id) VALUES ($1, $2, $3, $4)`,
		id.Int64(), slug, slug, createdBy.Int64(),
	)
	s.Require().NoError(err)
	return id
}

func (s *ledgerSuite) seedProject(orgID snow.ID) snow.ID {
	s.T().Helper()
	id := snow.ID(testIDCounter.Add(1) + 20000)
	slug := fmt.Sprintf("project-%d", id.Int64())
	_, err := s.db.ExecContext(context.Background(),
		`INSERT INTO projects (id, org_id, slug, name) VALUES ($1, $2, $3, $4)`,
		id.Int64(), orgID.Int64(), slug, slug,
	)
	s.Require().NoError(err)
	return id
}

// seedChunk returns a fresh chunk hash; like a confirm, s.confirm records its
// row once the ledger accepted it.
func (s *ledgerSuite) seedChunk(int64) domain.Hash {
	var hash domain.Hash
	copy(hash[:], newTestHash())
	return hash
}

// seedRow records a chunk row of the project directly, without the ledger
// (content the project holds but was never charged, or history for recount).
func (s *ledgerSuite) seedRow(projectID snow.ID, size int64) (domain.Hash, int64) {
	s.T().Helper()
	hash := s.seedChunk(size)
	var id int64
	err := s.db.QueryRowContext(context.Background(),
		`INSERT INTO chunks (project_id, hash, size_bytes) VALUES ($1, $2, $3) RETURNING id`,
		projectID.Int64(), hash[:], size,
	).Scan(&id)
	s.Require().NoError(err)
	return hash, id
}

// confirm records chunks the way usecase.Chunk confirms them: the ledger
// attributes them and the project's rows are inserted after it, in one
// transaction.
func (s *ledgerSuite) confirm(ledger *Ledger, ctx context.Context, org *domain.Organization, project *domain.Project, chunks []usecase.LedgerChunk) error {
	return confirmChunks(ctx, s.db, func(ctx context.Context) error {
		return ledger.Attribute(ctx, org, project, chunks)
	}, project.ID, chunks)
}

// confirmProject is confirm for server-produced chunks (merge output).
func (s *ledgerSuite) confirmProject(ledger *Ledger, ctx context.Context, projectID snow.ID, chunks []usecase.LedgerChunk) error {
	return confirmChunks(ctx, s.db, func(ctx context.Context) error {
		return ledger.AttributeProjectChunks(ctx, projectID, chunks)
	}, projectID, chunks)
}

// confirmChunks runs attribute and then inserts the project's chunk rows in
// one transaction, like usecase.Chunk.recordChunks over the repositories.
func confirmChunks(ctx context.Context, db *sql.DB, attribute func(ctx context.Context) error, projectID snow.ID, chunks []usecase.LedgerChunk) error {
	return dbtx.NewTransactor(db).WithinTx(ctx, func(ctx context.Context) error {
		if err := attribute(ctx); err != nil {
			return err
		}
		conn := dbtx.New(db)
		for _, chunk := range chunks {
			if _, err := conn.ExecContext(ctx,
				`INSERT INTO chunks (project_id, hash, size_bytes) VALUES ($1, $2, $3) ON CONFLICT (project_id, hash) DO NOTHING`,
				projectID.Int64(), chunk.Hash[:], chunk.SizeBytes); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *ledgerSuite) orgUsed(orgID snow.ID) int64 {
	s.T().Helper()
	var used int64
	err := s.db.QueryRowContext(context.Background(),
		`SELECT COALESCE((SELECT used_bytes FROM org_usage WHERE org_id = $1), 0)`,
		orgID.Int64(),
	).Scan(&used)
	s.Require().NoError(err)
	return used
}

func (s *ledgerSuite) projectUsed(projectID snow.ID) int64 {
	s.T().Helper()
	var used int64
	err := s.db.QueryRowContext(context.Background(),
		`SELECT COALESCE((SELECT used_bytes FROM project_usage WHERE project_id = $1), 0)`,
		projectID.Int64(),
	).Scan(&used)
	s.Require().NoError(err)
	return used
}

func (s *ledgerSuite) linkCount(projectID snow.ID) int {
	s.T().Helper()
	var count int
	err := s.db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM chunks WHERE project_id = $1`,
		projectID.Int64(),
	).Scan(&count)
	s.Require().NoError(err)
	return count
}

func (s *ledgerSuite) billingOwner(orgID snow.ID) (snow.ID, bool) {
	s.T().Helper()
	var owner int64
	err := s.db.QueryRowContext(context.Background(),
		`SELECT user_id FROM org_billing_owner WHERE org_id = $1`,
		orgID.Int64(),
	).Scan(&owner)
	if err == sql.ErrNoRows {
		return 0, false
	}
	s.Require().NoError(err)
	return snow.ID(owner), true
}

func isQuotaExceeded(err error) bool {
	return errorCode(err) == 402
}

func errorCode(err error) int {
	var domErr *domain.Error
	if err == nil || !errors.As(err, &domErr) {
		return 0
	}
	return domErr.Code
}

func newTestHash() []byte {
	n := testHashCounter.Add(1)
	b := make([]byte, 32)
	binary.LittleEndian.PutUint64(b, uint64(n))
	return b
}

func (s *ledgerSuite) seedTreeNode(parent *int64) int64 {
	s.T().Helper()
	var id int64
	err := s.db.QueryRowContext(context.Background(),
		`INSERT INTO tree_nodes (hash, name, mode, parent_tree_id) VALUES ($1, $2, $3, $4) RETURNING id`,
		newTestHash(), fmt.Sprintf("node-%d", testIDCounter.Add(1)), 0o755, parent,
	).Scan(&id)
	s.Require().NoError(err)
	return id
}

func (s *ledgerSuite) seedFile(treeID, chunkID int64) {
	s.T().Helper()
	var fileID int64
	err := s.db.QueryRowContext(context.Background(),
		`INSERT INTO files (name, mode, tree_id, hash, size_bytes, is_binary, encoding)
		 VALUES ($1, $2, $3, $4, $5, false, 'raw') RETURNING id`,
		fmt.Sprintf("file-%d", testIDCounter.Add(1)), 0o644, treeID, newTestHash(), 0,
	).Scan(&fileID)
	s.Require().NoError(err)
	_, err = s.db.ExecContext(context.Background(),
		`INSERT INTO file_chunks (file_id, chunk_id, chunk_index) VALUES ($1, $2, 0)`,
		fileID, chunkID,
	)
	s.Require().NoError(err)
}

func (s *ledgerSuite) seedCommit(projectID snow.ID, treeID int64) snow.ID {
	s.T().Helper()
	id := snow.ID(testIDCounter.Add(1) + 30000)
	_, err := s.db.ExecContext(context.Background(),
		`INSERT INTO commits (id, hash, project_id, tree_id, user_id, message) VALUES ($1, $2, $3, $4, 1, 'commit')`,
		id.Int64(), newTestHash(), projectID.Int64(), treeID,
	)
	s.Require().NoError(err)
	return id
}
