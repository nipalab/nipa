package postgres

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
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
	sqlcPostgres "github.com/nipalab/nipa/ee/repository/postgres/sqlc"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

var (
	testNodeCounter atomic.Int64
	testHashCounter atomic.Int64
	testDSN         string
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

type baseSuite struct {
	suite.Suite
	db *sql.DB
	q  *sqlcPostgres.Queries
}

func (s *baseSuite) SetupTest() {
	s.db, s.q = newTestDB(s.T())
}

func newTestDB(t *testing.T) (*sql.DB, *sqlcPostgres.Queries) {
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
	return db, sqlcPostgres.New(db)
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

func newTestNode(t *testing.T) snow.Node {
	t.Helper()
	id := testNodeCounter.Add(1) % 256
	node, err := snow.NewNode(id)
	require.NoError(t, err)
	return node
}

func testHashBytes() []byte {
	n := testHashCounter.Add(1)
	b := make([]byte, 32)
	binary.LittleEndian.PutUint64(b, uint64(n))
	return b
}

func seedUser(t *testing.T, q *sqlcPostgres.Queries, name, email string, photoUrl sql.NullString) snow.ID {
	t.Helper()

	node := newTestNode(t)
	id, err := q.UserCreate(context.Background(), sqlcPostgres.UserCreateParams{
		ID:       node.Generate().Int64(),
		Name:     name,
		Email:    email,
		Password: "hashed-password",
		PhotoUrl: photoUrl,
		IsAdmin:  true,
	})
	require.NoError(t, err)
	return snow.ID(id)
}

func seedPBACUser(t *testing.T, db *sql.DB, id int64) snow.ID {
	t.Helper()

	_, err := db.ExecContext(context.Background(),
		`INSERT INTO users (id, name, email, password) VALUES ($1, $2, $3, 'x')`,
		id, fmt.Sprintf("user%d", id), fmt.Sprintf("user%d@example.com", id),
	)
	require.NoError(t, err)
	return snow.ID(id)
}

func seedPBACGroup(t *testing.T, db *sql.DB, id, orgID int64, name string, members ...int64) snow.ID {
	t.Helper()

	_, err := db.ExecContext(context.Background(),
		`INSERT INTO groups (id, org_id, name) VALUES ($1, $2, $3)`,
		id, orgID, name,
	)
	require.NoError(t, err)
	for _, member := range members {
		_, err := db.ExecContext(context.Background(),
			`INSERT INTO group_members (group_id, user_id) VALUES ($1, $2)`,
			id, member,
		)
		require.NoError(t, err)
	}
	return snow.ID(id)
}

func seedProject(t *testing.T, q *sqlcPostgres.Queries, orgID int64, name string) snow.ID {
	t.Helper()

	node := newTestNode(t)
	id := node.Generate()

	_, err := q.CreateProject(context.Background(), sqlcPostgres.CreateProjectParams{
		ID:          id.Int64(),
		OrgID:       orgID,
		Slug:        name,
		Name:        name,
		Description: name,
	})
	require.NoError(t, err)
	return id
}

func seedBranch(t *testing.T, db *sql.DB, projectID snow.ID, name string, commitID sql.NullInt64) snow.ID {
	t.Helper()

	node := newTestNode(t)
	id := node.Generate()

	_, err := db.ExecContext(context.Background(),
		`INSERT INTO branches (id, project_id, name, key, commit_id) VALUES ($1, $2, $3, $4, $5)`,
		id.Int64(), projectID.Int64(), name, name, commitID,
	)
	require.NoError(t, err)
	return id
}

func seedTreeNode(t *testing.T, db *sql.DB, name string, parentID sql.NullInt64) int64 {
	t.Helper()

	var id int64
	err := db.QueryRowContext(context.Background(),
		`INSERT INTO tree_nodes (hash, name, mode, parent_tree_id) VALUES ($1, $2, $3, $4) RETURNING id`,
		testHashBytes(), name, 0o755, parentID,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

func seedCommit(t *testing.T, db *sql.DB, q *sqlcPostgres.Queries, projectID snow.ID, treeID int64) snow.ID {
	t.Helper()

	userID := seedUser(t, q, "committer", "committer@example.com", sql.NullString{})
	node := newTestNode(t)
	id := node.Generate()

	_, err := db.ExecContext(context.Background(),
		`INSERT INTO commits (id, hash, project_id, tree_id, user_id, message) VALUES ($1, $2, $3, $4, $5, $6)`,
		id.Int64(), testHashBytes(), projectID.Int64(), treeID, userID.Int64(), "initial commit",
	)
	require.NoError(t, err)
	return id
}

func requireRecordNotFound(t *testing.T, err error) {
	t.Helper()

	require.Error(t, err)
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)
	require.Equal(t, "record not found", domErr.Message)
}

func testSnowIDPtr(id int64) *snow.ID {
	value := snow.ID(id)
	return &value
}

func pbacIDPtr(id snow.ID) *snow.ID {
	return &id
}
