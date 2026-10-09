package sqlite

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	sqlcSqlite "github.com/nipalab/nipa/internal/repository/sqlc/sqlite"
	"github.com/nipalab/nipa/internal/snow"
)

func TestMergeRequestCheckRepositorySQLite_UpsertAndList(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewMergeRequestCheckRepository(db)

	projectID := seedProject(t, q, 1, "game")
	sourceID := seedBranch(t, db, projectID, "feature", sql.NullInt64{})
	targetID := seedBranch(t, db, projectID, "main", sql.NullInt64{})
	treeID := seedTreeNode(t, db, "root", sql.NullInt64{})
	head := seedCommit(t, db, q, projectID, treeID)
	reporter := seedPBACUser(t, db, 42)

	mr, err := q.MergeRequestCreate(ctx, sqlcSqlite.MergeRequestCreateParams{
		ID: 2001, ProjectID: projectID.Int64(), SourceBranchID: sourceID.Int64(),
		TargetBranchID: targetID.Int64(), SourceBranchName: "feature",
		TargetBranchName: "main", Title: "t", CreatedBy: reporter.Int64(),
	})
	require.NoError(t, err)

	check, err := repo.Upsert(ctx, domain.MergeRequestCheck{
		ID:             snow.ID(3001),
		MergeRequestID: mr.ID,
		HeadCommitID:   head,
		Name:           "build",
		State:          domain.MergeRequestCheckPending,
		Reporter:       domain.ReviewActor{UserID: reporter},
	})
	require.NoError(t, err)
	require.Equal(t, snow.ID(3001), check.ID)
	require.Equal(t, mr.ID, check.MergeRequestID)
	require.Equal(t, head, check.HeadCommitID)
	require.Equal(t, "build", check.Name)
	require.Equal(t, domain.MergeRequestCheckPending, check.State)
	require.Equal(t, reporter, check.Reporter.UserID)
	require.False(t, check.CreatedAt.IsZero())

	// upserting the same (request, head, name) updates the row in place
	updated, err := repo.Upsert(ctx, domain.MergeRequestCheck{
		ID:             snow.ID(3002),
		MergeRequestID: mr.ID,
		HeadCommitID:   head,
		Name:           "build",
		State:          domain.MergeRequestCheckSuccess,
		DetailsURL:     "https://ci.example/run/1",
		Reporter:       domain.ReviewActor{UserID: reporter},
	})
	require.NoError(t, err)
	require.Equal(t, snow.ID(3001), updated.ID, "an upsert keeps the original row")
	require.Equal(t, domain.MergeRequestCheckSuccess, updated.State)
	require.Equal(t, "https://ci.example/run/1", updated.DetailsURL)

	list, err := repo.List(ctx, mr.ID, head)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, snow.ID(3001), list[0].ID)
	require.Equal(t, domain.MergeRequestCheckSuccess, list[0].State)
	require.Equal(t, "user42", list[0].Reporter.Name)
	require.Empty(t, list[0].Reporter.PhotoURL)

	other, err := repo.List(ctx, mr.ID, snow.ID(9999))
	require.NoError(t, err)
	require.Empty(t, other)
}

func TestMergeRequestCheckRepositorySQLite_Errors(t *testing.T) {
	ctx := context.Background()
	db, _ := newSQLiteTestDB(t)
	repo := NewMergeRequestCheckRepository(db)
	require.NoError(t, db.Close())

	_, err := repo.Upsert(ctx, domain.MergeRequestCheck{ID: snow.ID(1), MergeRequestID: 1, HeadCommitID: snow.ID(2)})
	require.Error(t, err)

	_, err = repo.List(ctx, 1, snow.ID(2))
	require.Error(t, err)
}
