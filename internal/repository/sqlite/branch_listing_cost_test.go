package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	sqlcSqlite "github.com/nipalab/nipa/internal/repository/sqlc/sqlite"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

// countingDBTX counts the SQL statements a repository sends to the database.
type countingDBTX struct {
	inner      sqlcSqlite.DBTX
	statements atomic.Int64
}

func (c *countingDBTX) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	c.statements.Add(1)
	return c.inner.ExecContext(ctx, query, args...)
}

func (c *countingDBTX) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	return c.inner.PrepareContext(ctx, query)
}

func (c *countingDBTX) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	c.statements.Add(1)
	return c.inner.QueryContext(ctx, query, args...)
}

func (c *countingDBTX) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	c.statements.Add(1)
	return c.inner.QueryRowContext(ctx, query, args...)
}

// allowAllPermissions lets the branch usecase run against a real repository.
type allowAllPermissions struct{}

func (allowAllPermissions) HasProjectAccess(context.Context, snow.ID, domain.Permission) bool {
	return true
}

func (allowAllPermissions) HasPathAccess(context.Context, snow.ID, string, domain.Permission) bool {
	return true
}

func (allowAllPermissions) CompileFilter(context.Context, snow.ID, domain.Permission) (*usecase.PathFilter, error) {
	return usecase.AllowAllFilter(), nil
}

func (allowAllPermissions) AdminHasProject(context.Context, snow.ID) bool { return true }

// seedReviewers creates n users with ascending ids.
func seedReviewers(t *testing.T, q *sqlcSqlite.Queries, n int) []snow.ID {
	t.Helper()
	ids := make([]snow.ID, 0, n)
	for i := 0; i < n; i++ {
		id, err := q.UserCreate(context.Background(), sqlcSqlite.UserCreateParams{
			ID:       int64(9000 + i),
			Name:     fmt.Sprintf("reviewer-%d", i),
			Email:    fmt.Sprintf("reviewer-%d@example.com", i),
			Password: "hashed-password",
		})
		require.NoError(t, err)
		ids = append(ids, snow.ID(id))
	}
	return ids
}

// seedProtectedBranches creates n branches that each require the two given
// reviewers and two checks.
func seedProtectedBranches(t *testing.T, db *sql.DB, repo *BranchRepository, projectID snow.ID, prefix string, n int, reviewers []snow.ID) []snow.ID {
	t.Helper()
	ids := make([]snow.ID, 0, n)
	for i := 0; i < n; i++ {
		id := seedBranch(t, db, projectID, fmt.Sprintf("%s-%04d", prefix, i), sql.NullInt64{})
		require.NoError(t, repo.SetBranchProtection(context.Background(), projectID, id, domain.BranchProtection{
			Protected:           true,
			RequiredApprovals:   1,
			RequireStatusChecks: true,
			RequiredReviewers:   reviewers,
			RequiredChecks:      []string{"build", "test"},
		}))
		ids = append(ids, id)
	}
	return ids
}

// TestBranchListing_StatementCountIsConstant drives the branch usecase over
// the real repository and asserts that listing branches with their protection
// lists costs the same number of SQL statements for 15 and for 200 branches.
func TestBranchListing_StatementCountIsConstant(t *testing.T) {
	ctx := context.Background()
	counts := map[int]int64{}
	for _, n := range []int{0, 15, 200} {
		db, q := newSQLiteTestDB(t)
		counter := &countingDBTX{inner: dbtx.New(db)}
		repo := &BranchRepository{db: db, tx: dbtx.NewTransactor(db), queries: sqlcSqlite.New(counter)}
		uc := usecase.NewBranch(allowAllPermissions{}, repo, newTestNode(t))

		projectID := seedProject(t, q, 1, "game")
		reviewers := seedReviewers(t, q, 2)
		seedProtectedBranches(t, db, repo, projectID, "release", n, reviewers)

		counter.statements.Store(0)
		branches, err := uc.ListBranches(ctx, projectID, 1000, nil, 0)
		require.NoError(t, err)
		require.Len(t, branches, n)
		for _, branch := range branches {
			require.Len(t, branch.RequiredReviewers, 2)
			require.Equal(t, []string{"build", "test"}, branch.RequiredChecks)
		}
		counts[n] = counter.statements.Load()

		if os.Getenv("NIPA_BRANCH_LISTING_TIMING") != "" {
			const rounds = 300
			start := time.Now()
			for i := 0; i < rounds; i++ {
				_, err := uc.ListBranches(ctx, projectID, 1000, nil, 0)
				require.NoError(t, err)
			}
			t.Logf("branches=%d statements=%d mean=%s", n, counts[n], time.Since(start)/rounds)
		}
	}
	require.Equal(t, int64(1), counts[0], "an empty listing needs no protection lookup")
	require.Equal(t, counts[15], counts[200], "statements must not grow with the number of branches: %v", counts)
	require.Equal(t, int64(3), counts[200])
}

// requireBatchedProtectionMatches asserts the batched loaders return, for
// every given branch, exactly what the per-branch loaders return.
func requireBatchedProtectionMatches(t *testing.T, repo *BranchRepository, branchIDs []snow.ID) {
	t.Helper()
	ctx := context.Background()
	reviewers, err := repo.RequiredReviewersByBranches(ctx, branchIDs)
	require.NoError(t, err)
	checks, err := repo.RequiredChecksByBranches(ctx, branchIDs)
	require.NoError(t, err)

	wantReviewers, wantChecks := 0, 0
	seen := map[snow.ID]bool{}
	for _, id := range branchIDs {
		single, err := repo.RequiredReviewers(ctx, id)
		require.NoError(t, err)
		if len(single) == 0 {
			require.NotContains(t, reviewers, id)
		} else {
			require.Equal(t, single, reviewers[id], "reviewers of branch %d", id)
		}
		names, err := repo.RequiredChecks(ctx, id)
		require.NoError(t, err)
		if len(names) == 0 {
			require.NotContains(t, checks, id)
		} else {
			require.Equal(t, names, checks[id], "checks of branch %d", id)
		}
		if !seen[id] {
			seen[id] = true
			if len(single) > 0 {
				wantReviewers++
			}
			if len(names) > 0 {
				wantChecks++
			}
		}
	}
	require.Len(t, reviewers, wantReviewers)
	require.Len(t, checks, wantChecks)
}

func testBatchedProtectionEmpty(t *testing.T, db *sql.DB, q *sqlcSqlite.Queries) {
	ctx := context.Background()
	counter := &countingDBTX{inner: dbtx.New(db)}
	repo := &BranchRepository{db: db, tx: dbtx.NewTransactor(db), queries: sqlcSqlite.New(counter)}
	projectID := seedProject(t, q, 1, "game")
	seedProtectedBranches(t, db, repo, projectID, "release", 2, seedReviewers(t, q, 1))

	counter.statements.Store(0)
	for _, ids := range [][]snow.ID{nil, {}} {
		reviewers, err := repo.RequiredReviewersByBranches(ctx, ids)
		require.NoError(t, err)
		require.Empty(t, reviewers)
		checks, err := repo.RequiredChecksByBranches(ctx, ids)
		require.NoError(t, err)
		require.Empty(t, checks)
	}
	require.Zero(t, counter.statements.Load(), "no branches, no statement")
}

func testBatchedProtectionMixed(t *testing.T, db *sql.DB, q *sqlcSqlite.Queries) {
	ctx := context.Background()
	repo := NewBranchRepository(db)
	projectID := seedProject(t, q, 1, "game")
	users := seedReviewers(t, q, 3)

	plain := seedBranch(t, db, projectID, "plain", sql.NullInt64{})
	alsoPlain := seedBranch(t, db, projectID, "also-plain", sql.NullInt64{})
	requireBatchedProtectionMatches(t, repo, []snow.ID{plain, alsoPlain})

	both := seedBranch(t, db, projectID, "both", sql.NullInt64{})
	require.NoError(t, repo.SetBranchProtection(ctx, projectID, both, domain.BranchProtection{
		Protected: true,
		// Stored out of order: the lists come back by user id and by name.
		RequiredReviewers: []snow.ID{users[2], users[0], users[1]},
		RequiredChecks:    []string{"test", "Build", "build", "lint"},
	}))
	reviewersOnly := seedBranch(t, db, projectID, "reviewers-only", sql.NullInt64{})
	require.NoError(t, repo.SetBranchProtection(ctx, projectID, reviewersOnly, domain.BranchProtection{
		Protected:         true,
		RequiredReviewers: []snow.ID{users[1]},
	}))
	checksOnly := seedBranch(t, db, projectID, "checks-only", sql.NullInt64{})
	require.NoError(t, repo.SetBranchProtection(ctx, projectID, checksOnly, domain.BranchProtection{
		Protected:      true,
		RequiredChecks: []string{"build"},
	}))
	// Lists survive on a branch whose protection is switched off.
	unprotected := seedBranch(t, db, projectID, "unprotected", sql.NullInt64{})
	require.NoError(t, repo.SetBranchProtection(ctx, projectID, unprotected, domain.BranchProtection{
		RequiredReviewers: []snow.ID{users[2], users[1]},
		RequiredChecks:    []string{"z", "a"},
	}))
	// Not requested, so it must not leak into the result.
	other := seedBranch(t, db, projectID, "other", sql.NullInt64{})
	require.NoError(t, repo.SetBranchProtection(ctx, projectID, other, domain.BranchProtection{
		Protected:         true,
		RequiredReviewers: []snow.ID{users[0]},
		RequiredChecks:    []string{"other"},
	}))

	ids := []snow.ID{unprotected, plain, both, snow.ID(424242), checksOnly, both, reviewersOnly, alsoPlain}
	requireBatchedProtectionMatches(t, repo, ids)

	reviewers, err := repo.RequiredReviewersByBranches(ctx, ids)
	require.NoError(t, err)
	require.Len(t, reviewers, 3)
	require.Equal(t, []domain.ReviewActor{
		{UserID: users[0], Name: "reviewer-0"},
		{UserID: users[1], Name: "reviewer-1"},
		{UserID: users[2], Name: "reviewer-2"},
	}, reviewers[both])
	checks, err := repo.RequiredChecksByBranches(ctx, ids)
	require.NoError(t, err)
	require.Len(t, checks, 3)
	require.Len(t, checks[both], 4)
	require.Equal(t, []string{"a", "z"}, checks[unprotected])
}

// testBatchedProtectionManyBranches lists more branches than one id batch
// carries.
func testBatchedProtectionManyBranches(t *testing.T, db *sql.DB, q *sqlcSqlite.Queries) {
	ctx := context.Background()
	counter := &countingDBTX{inner: dbtx.New(db)}
	repo := &BranchRepository{db: db, tx: dbtx.NewTransactor(db), queries: sqlcSqlite.New(counter)}
	projectID := seedProject(t, q, 1, "game")
	users := seedReviewers(t, q, 3)

	const n = treeBatchSize + 20
	ids := make([]snow.ID, 0, n)
	for i := 0; i < n; i++ {
		id := seedBranch(t, db, projectID, fmt.Sprintf("b-%04d", i), sql.NullInt64{})
		ids = append(ids, id)
		if i%4 == 0 {
			continue // no protection rows at all
		}
		checks := make([]string, 0, 2)
		for c := 0; c < i%3; c++ {
			checks = append(checks, fmt.Sprintf("check-%d", (i+c)%5))
		}
		require.NoError(t, repo.SetBranchProtection(ctx, projectID, id, domain.BranchProtection{
			Protected:         i%2 == 0,
			RequiredReviewers: users[:i%4],
			RequiredChecks:    checks,
		}))
	}

	counter.statements.Store(0)
	_, err := repo.RequiredReviewersByBranches(ctx, ids)
	require.NoError(t, err)
	_, err = repo.RequiredChecksByBranches(ctx, ids)
	require.NoError(t, err)
	require.Equal(t, int64(4), counter.statements.Load(), "two id batches per list")

	requireBatchedProtectionMatches(t, repo, ids)
}

func testBatchedProtectionErrors(t *testing.T, db *sql.DB, q *sqlcSqlite.Queries) {
	ctx := context.Background()
	repo := NewBranchRepository(db)
	projectID := seedProject(t, q, 1, "game")
	branchID := seedBranch(t, db, projectID, "release", sql.NullInt64{})

	_, err := db.ExecContext(ctx, `DROP TABLE branches_required_reviewers`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `DROP TABLE branches_required_checks`)
	require.NoError(t, err)

	_, err = repo.RequiredReviewersByBranches(ctx, []snow.ID{branchID})
	require.Error(t, err)
	_, err = repo.RequiredChecksByBranches(ctx, []snow.ID{branchID})
	require.Error(t, err)
}

func TestBranchRepositorySQLite_BatchedProtection_Empty(t *testing.T) {
	db, q := newSQLiteTestDB(t)
	testBatchedProtectionEmpty(t, db, q)
}

func TestBranchRepositorySQLite_BatchedProtection_Mixed(t *testing.T) {
	db, q := newSQLiteTestDB(t)
	testBatchedProtectionMixed(t, db, q)
}

func TestBranchRepositorySQLite_BatchedProtection_ManyBranches(t *testing.T) {
	db, q := newSQLiteTestDB(t)
	testBatchedProtectionManyBranches(t, db, q)
}

func TestBranchRepositorySQLite_BatchedProtection_Errors(t *testing.T) {
	db, q := newSQLiteTestDB(t)
	testBatchedProtectionErrors(t, db, q)
}
