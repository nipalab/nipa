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

	list, err := repo.List(ctx, projectID, domain.MergeRequestListOptions{Limit: 10})
	require.NoError(t, err)
	require.Len(t, list, 2)
	require.Equal(t, int64(2), list[0].Number)

	list, err = repo.List(ctx, projectID, domain.MergeRequestListOptions{Status: domain.MergeRequestClosed, Limit: 10})
	require.NoError(t, err)
	require.Empty(t, list)

	// the number is the keyset cursor, and the filters narrow the page
	page, err := repo.List(ctx, projectID, domain.MergeRequestListOptions{Limit: 1})
	require.NoError(t, err)
	require.Len(t, page, 1)
	require.Equal(t, int64(2), page[0].Number)
	next, err := repo.List(ctx, projectID, domain.MergeRequestListOptions{After: page[0].Number, Limit: 1})
	require.NoError(t, err)
	require.Len(t, next, 1)
	require.Equal(t, int64(1), next[0].Number)

	byAuthor, err := repo.List(ctx, projectID, domain.MergeRequestListOptions{Author: &userID, Limit: 10})
	require.NoError(t, err)
	require.Len(t, byAuthor, 2)
	bySource, err := repo.List(ctx, projectID, domain.MergeRequestListOptions{SourceBranch: "feature", Limit: 10})
	require.NoError(t, err)
	require.Len(t, bySource, 2)
	byTarget, err := repo.List(ctx, projectID, domain.MergeRequestListOptions{TargetBranch: "ghost", Limit: 10})
	require.NoError(t, err)
	require.Empty(t, byTarget)

	draftOnly := true
	drafts, err := repo.List(ctx, projectID, domain.MergeRequestListOptions{Draft: &draftOnly, Limit: 10})
	require.NoError(t, err)
	require.Empty(t, drafts)

	draft, err := repo.SetDraft(ctx, projectID, 1, true)
	require.NoError(t, err)
	require.True(t, draft.Draft)
	drafts, err = repo.List(ctx, projectID, domain.MergeRequestListOptions{Draft: &draftOnly, Limit: 10})
	require.NoError(t, err)
	require.Len(t, drafts, 1)
	require.Equal(t, int64(1), drafts[0].Number)

	_, err = repo.SetDraft(ctx, projectID, 9999, false)
	requireRecordNotFound(t, err)

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

func TestMergeRequestRepositorySQLite_AssigneesAndSearch(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewMergeRequestRepository(db)

	projectID := seedProject(t, q, 1, "game")
	userID := seedPBACUser(t, db, 42)
	otherID := seedPBACUser(t, db, 43)
	sourceID := seedBranch(t, db, projectID, "feature", sql.NullInt64{})
	targetID := seedBranch(t, db, projectID, "main", sql.NullInt64{})

	first, err := repo.Create(ctx, domain.MergeRequest{
		ID: 5001, ProjectID: projectID, SourceBranchID: sourceID, TargetBranchID: targetID,
		SourceBranch: "feature", TargetBranch: "main", Title: "Add feature", CreatedBy: userID,
	})
	require.NoError(t, err)
	second, err := repo.Create(ctx, domain.MergeRequest{
		ID: 5002, ProjectID: projectID, SourceBranchID: sourceID, TargetBranchID: targetID,
		SourceBranch: "feature", TargetBranch: "main", Title: "Fix bug", CreatedBy: userID,
	})
	require.NoError(t, err)

	require.NoError(t, repo.AddAssignee(ctx, first.ID, userID))
	require.NoError(t, repo.AddAssignee(ctx, first.ID, otherID))

	assignees, err := repo.ListAssignees(ctx, projectID)
	require.NoError(t, err)
	require.Len(t, assignees[first.Number], 2)
	names := []string{assignees[first.Number][0].Name, assignees[first.Number][1].Name}
	require.Contains(t, names, "user42")
	require.Contains(t, names, "user43")

	assigned, err := repo.List(ctx, projectID, domain.MergeRequestListOptions{Assignee: &userID, Limit: 10})
	require.NoError(t, err)
	require.Len(t, assigned, 1)
	require.Equal(t, first.Number, assigned[0].Number)

	searched, err := repo.List(ctx, projectID, domain.MergeRequestListOptions{Search: "bug", Limit: 10})
	require.NoError(t, err)
	require.Len(t, searched, 1)
	require.Equal(t, second.Number, searched[0].Number)

	require.NoError(t, repo.ClearAssignees(ctx, first.ID))
	assignees, err = repo.ListAssignees(ctx, projectID)
	require.NoError(t, err)
	require.Empty(t, assignees[first.Number])
}

func TestMergeRequestRepositorySQLite_Errors(t *testing.T) {
	ctx := context.Background()
	db, _ := newSQLiteTestDB(t)
	repo := NewMergeRequestRepository(db)
	require.NoError(t, db.Close())

	_, err := repo.Create(ctx, domain.MergeRequest{ID: 1, ProjectID: 1, SourceBranchID: 1, TargetBranchID: 2, CreatedBy: 1})
	require.Error(t, err)

	_, err = repo.List(ctx, 1, domain.MergeRequestListOptions{Limit: 1})
	require.Error(t, err)

	_, err = repo.ListAssignees(ctx, 1)
	require.Error(t, err)

	require.Error(t, repo.ClearAssignees(ctx, 1))
	require.Error(t, repo.AddAssignee(ctx, 1, 42))
}
