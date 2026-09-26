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

func TestFileLockRepositorySQLite_CRUD(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewFileLockRepository(db)

	projectID := seedProject(t, q, 1, "game")
	userID := seedPBACUser(t, db, 42)
	mainID := seedBranch(t, db, projectID, "main", sql.NullInt64{})
	devID := seedBranch(t, db, projectID, "feature", sql.NullInt64{})

	global, err := repo.Create(ctx, domain.FileLock{
		ID: 1001, ProjectID: projectID, Path: "assets", HeldBy: userID,
	})
	require.NoError(t, err)
	require.Nil(t, global.BranchID)

	_, err = repo.Create(ctx, domain.FileLock{
		ID: 1002, ProjectID: projectID, Path: "assets", HeldBy: userID,
	})
	require.Error(t, err, "a second global lock on the same path must be rejected")

	scoped, err := repo.Create(ctx, domain.FileLock{
		ID: 1003, ProjectID: projectID, BranchID: &devID, Path: "assets", HeldBy: userID,
	})
	require.NoError(t, err)
	require.NotNil(t, scoped.BranchID)
	require.Equal(t, devID, *scoped.BranchID)

	_, err = repo.Create(ctx, domain.FileLock{
		ID: 1004, ProjectID: projectID, BranchID: &devID, Path: "assets", HeldBy: userID,
	})
	require.Error(t, err, "a second branch lock on the same path must be rejected")

	got, err := repo.Get(ctx, projectID, "assets", nil)
	require.NoError(t, err)
	require.Equal(t, snow.ID(1001), got.ID)

	_, err = repo.Get(ctx, projectID, "assets", &devID)
	require.NoError(t, err)

	_, err = repo.Get(ctx, projectID, "missing", nil)
	requireRecordNotFound(t, err)

	mr, err := q.MergeRequestCreate(ctx, sqlcSqlite.MergeRequestCreateParams{
		ID: 2001, ProjectID: projectID.Int64(), SourceBranchID: devID.Int64(),
		TargetBranchID:   mainID.Int64(),
		SourceBranchName: "feature", TargetBranchName: "main", Title: "t", CreatedBy: userID.Int64(),
	})
	require.NoError(t, err)
	linked, err := repo.Create(ctx, domain.FileLock{
		ID: 1006, ProjectID: projectID, Path: "assets/orc.png", HeldBy: userID,
		MergeRequestID: testSnowIDPtr(mr.ID),
	})
	require.NoError(t, err)
	require.NotNil(t, linked.MergeRequestID)

	list, err := repo.ListProject(ctx, projectID)
	require.NoError(t, err)
	require.Len(t, list, 3)
	var globalRow, featureRow, linkedRow *domain.FileLock
	for _, lock := range list {
		require.Equal(t, "user42", lock.HeldByName)
		switch {
		case lock.Path == "assets" && lock.BranchID == nil:
			globalRow = lock
		case lock.Path == "assets" && lock.BranchID != nil && *lock.BranchID == devID:
			featureRow = lock
		case lock.Path == "assets/orc.png":
			linkedRow = lock
		}
	}
	require.NotNil(t, globalRow)
	require.Empty(t, globalRow.Branch)
	require.NotNil(t, featureRow)
	require.Equal(t, "feature", featureRow.Branch)
	require.NotNil(t, linkedRow)
	require.NotNil(t, linkedRow.MergeRequestNumber)
	require.Equal(t, int64(1), *linkedRow.MergeRequestNumber)

	require.NoError(t, repo.Delete(ctx, got.ID))
	_, err = repo.Get(ctx, projectID, "assets", nil)
	requireRecordNotFound(t, err)
	requireRecordNotFound(t, repo.Delete(ctx, snow.ID(9999)))

	require.NoError(t, repo.DeleteByBranch(ctx, projectID, devID))
	_, err = repo.Get(ctx, projectID, "assets", &devID)
	requireRecordNotFound(t, err)

	require.NoError(t, repo.DeleteByMergeRequest(ctx, projectID, snow.ID(mr.ID)))
	_, err = repo.Get(ctx, projectID, "assets/orc.png", nil)
	requireRecordNotFound(t, err)
}

func TestFileLockRepositorySQLite_ListEmpty(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewFileLockRepository(db)
	projectID := seedProject(t, q, 1, "game")

	list, err := repo.ListProject(ctx, projectID)
	require.NoError(t, err)
	require.Empty(t, list)
}

func testSnowIDPtr(id int64) *snow.ID {
	value := snow.ID(id)
	return &value
}
