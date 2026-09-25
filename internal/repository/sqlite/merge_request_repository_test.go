package sqlite

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
)

func TestMergeRequestRepositorySQLite_CRUD(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewMergeRequestRepository(db)

	projectID := seedProject(t, q, 1, "game")
	userID := seedPBACUser(t, db, 42)
	sourceID := seedBranch(t, db, projectID, "feature", sql.NullInt64{})
	targetID := seedBranch(t, db, projectID, "main", sql.NullInt64{})

	created, err := repo.Create(ctx, domain.MergeRequest{
		ID:             5001,
		ProjectID:      projectID,
		SourceBranchID: sourceID,
		TargetBranchID: targetID,
		SourceBranch:   "feature",
		TargetBranch:   "main",
		Title:          "Add feature",
		Description:    "body",
		CreatedBy:      userID,
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), created.Number)
	require.Equal(t, domain.MergeRequestOpen, created.Status)
	require.Equal(t, "feature", created.SourceBranch)
	require.Equal(t, "main", created.TargetBranch)
	require.Equal(t, userID, created.CreatedBy)

	second, err := repo.Create(ctx, domain.MergeRequest{
		ID:             5002,
		ProjectID:      projectID,
		SourceBranchID: sourceID,
		TargetBranchID: targetID,
		SourceBranch:   "feature",
		TargetBranch:   "main",
		Title:          "Second",
		CreatedBy:      userID,
	})
	require.NoError(t, err)
	require.Equal(t, int64(2), second.Number)

	got, err := repo.Get(ctx, projectID, 1)
	require.NoError(t, err)
	require.Equal(t, "Add feature", got.Title)
	require.Nil(t, got.MergeBaseCommitID)

	list, err := repo.List(ctx, projectID, "", 10)
	require.NoError(t, err)
	require.Len(t, list, 2)
	require.Equal(t, int64(2), list[0].Number)

	list, err = repo.List(ctx, projectID, domain.MergeRequestClosed, 10)
	require.NoError(t, err)
	require.Empty(t, list)

	updated, err := repo.Update(ctx, projectID, 1, "New title", "new body")
	require.NoError(t, err)
	require.Equal(t, "New title", updated.Title)
	require.Equal(t, "new body", updated.Description)

	_, err = repo.Update(ctx, projectID, 9999, "t", "d")
	requireRecordNotFound(t, err)

	require.NoError(t, repo.UpdateStatus(ctx, projectID, 1, domain.MergeRequestMerged, nil))
	got, err = repo.Get(ctx, projectID, 1)
	require.NoError(t, err)
	require.Equal(t, domain.MergeRequestMerged, got.Status)

	_, err = repo.Get(ctx, projectID, 9999)
	requireRecordNotFound(t, err)
}
