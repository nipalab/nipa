package postgres

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/nipalab/nipa/internal/domain"
)

type MergeRequestRepositorySuite struct {
	baseSuite
}

func TestMergeRequestRepositorySuite(t *testing.T) {
	suite.Run(t, new(MergeRequestRepositorySuite))
}

func (s *MergeRequestRepositorySuite) TestCRUD() {
	ctx := context.Background()
	repo := NewMergeRequestRepository(s.db)

	projectID := seedProject(s.T(), s.q, 1, "game")
	userID := seedPBACUser(s.T(), s.db, 42)
	sourceID := seedBranch(s.T(), s.db, projectID, "feature", sql.NullInt64{})
	targetID := seedBranch(s.T(), s.db, projectID, "main", sql.NullInt64{})

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
	s.Require().NoError(err)
	s.Equal(int64(1), created.Number)
	s.Equal(domain.MergeRequestOpen, created.Status)
	s.Equal("feature", created.SourceBranch)
	s.Equal("main", created.TargetBranch)
	s.Equal(userID, created.CreatedBy)

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
	s.Require().NoError(err)
	s.Equal(int64(2), second.Number)

	got, err := repo.Get(ctx, projectID, 1)
	s.Require().NoError(err)
	s.Equal("Add feature", got.Title)
	s.Nil(got.MergeBaseCommitID)

	list, err := repo.List(ctx, projectID, domain.MergeRequestListOptions{Limit: 10})
	s.Require().NoError(err)
	s.Len(list, 2)
	s.Equal(int64(2), list[0].Number)

	list, err = repo.List(ctx, projectID, domain.MergeRequestListOptions{Status: domain.MergeRequestClosed, Limit: 10})
	s.Require().NoError(err)
	s.Empty(list)

	page, err := repo.List(ctx, projectID, domain.MergeRequestListOptions{Limit: 1})
	s.Require().NoError(err)
	s.Len(page, 1)
	s.Equal(int64(2), page[0].Number)
	next, err := repo.List(ctx, projectID, domain.MergeRequestListOptions{After: page[0].Number, Limit: 1})
	s.Require().NoError(err)
	s.Len(next, 1)
	s.Equal(int64(1), next[0].Number)

	byAuthor, err := repo.List(ctx, projectID, domain.MergeRequestListOptions{Author: &userID, Limit: 10})
	s.Require().NoError(err)
	s.Len(byAuthor, 2)
	bySource, err := repo.List(ctx, projectID, domain.MergeRequestListOptions{SourceBranch: "feature", Limit: 10})
	s.Require().NoError(err)
	s.Len(bySource, 2)
	byTarget, err := repo.List(ctx, projectID, domain.MergeRequestListOptions{TargetBranch: "ghost", Limit: 10})
	s.Require().NoError(err)
	s.Empty(byTarget)

	draftOnly := true
	drafts, err := repo.List(ctx, projectID, domain.MergeRequestListOptions{Draft: &draftOnly, Limit: 10})
	s.Require().NoError(err)
	s.Empty(drafts)

	draft, err := repo.SetDraft(ctx, projectID, 1, true)
	s.Require().NoError(err)
	s.True(draft.Draft)
	drafts, err = repo.List(ctx, projectID, domain.MergeRequestListOptions{Draft: &draftOnly, Limit: 10})
	s.Require().NoError(err)
	s.Len(drafts, 1)
	s.Equal(int64(1), drafts[0].Number)

	_, err = repo.SetDraft(ctx, projectID, 9999, false)
	requireRecordNotFound(s.T(), err)

	updated, err := repo.Update(ctx, projectID, 1, "New title", "new body")
	s.Require().NoError(err)
	s.Equal("New title", updated.Title)
	s.Equal("new body", updated.Description)

	_, err = repo.Update(ctx, projectID, 9999, "t", "d")
	requireRecordNotFound(s.T(), err)

	s.Require().NoError(repo.UpdateStatus(ctx, projectID, 1, domain.MergeRequestMerged, nil))
	got, err = repo.Get(ctx, projectID, 1)
	s.Require().NoError(err)
	s.Equal(domain.MergeRequestMerged, got.Status)

	_, err = repo.Get(ctx, projectID, 9999)
	requireRecordNotFound(s.T(), err)
}

func (s *MergeRequestRepositorySuite) TestAssigneesAndSearch() {
	ctx := context.Background()
	repo := NewMergeRequestRepository(s.db)

	projectID := seedProject(s.T(), s.q, 1, "game")
	userID := seedPBACUser(s.T(), s.db, 42)
	otherID := seedPBACUser(s.T(), s.db, 43)
	sourceID := seedBranch(s.T(), s.db, projectID, "feature", sql.NullInt64{})
	targetID := seedBranch(s.T(), s.db, projectID, "main", sql.NullInt64{})

	first, err := repo.Create(ctx, domain.MergeRequest{
		ID: 5001, ProjectID: projectID, SourceBranchID: sourceID, TargetBranchID: targetID,
		SourceBranch: "feature", TargetBranch: "main", Title: "Add feature", CreatedBy: userID,
	})
	s.Require().NoError(err)
	second, err := repo.Create(ctx, domain.MergeRequest{
		ID: 5002, ProjectID: projectID, SourceBranchID: sourceID, TargetBranchID: targetID,
		SourceBranch: "feature", TargetBranch: "main", Title: "Fix bug", CreatedBy: userID,
	})
	s.Require().NoError(err)

	s.Require().NoError(repo.AddAssignee(ctx, first.ID, userID))
	s.Require().NoError(repo.AddAssignee(ctx, first.ID, otherID))

	assignees, err := repo.ListAssignees(ctx, projectID)
	s.Require().NoError(err)
	s.Len(assignees[first.Number], 2)
	names := []string{assignees[first.Number][0].Name, assignees[first.Number][1].Name}
	s.Contains(names, "user42")
	s.Contains(names, "user43")

	assigned, err := repo.List(ctx, projectID, domain.MergeRequestListOptions{Assignee: &userID, Limit: 10})
	s.Require().NoError(err)
	s.Len(assigned, 1)
	s.Equal(first.Number, assigned[0].Number)

	searched, err := repo.List(ctx, projectID, domain.MergeRequestListOptions{Search: "bug", Limit: 10})
	s.Require().NoError(err)
	s.Len(searched, 1)
	s.Equal(second.Number, searched[0].Number)

	s.Require().NoError(repo.ClearAssignees(ctx, first.ID))
	assignees, err = repo.ListAssignees(ctx, projectID)
	s.Require().NoError(err)
	s.Empty(assignees[first.Number])
}

func (s *MergeRequestRepositorySuite) TestErrors() {
	ctx := context.Background()
	repo := NewMergeRequestRepository(s.db)
	s.Require().NoError(s.db.Close())

	_, err := repo.Create(ctx, domain.MergeRequest{ID: 1, ProjectID: 1, SourceBranchID: 1, TargetBranchID: 2, CreatedBy: 1})
	s.Error(err)

	_, err = repo.List(ctx, 1, domain.MergeRequestListOptions{Limit: 1})
	s.Error(err)

	_, err = repo.ListAssignees(ctx, 1)
	s.Error(err)

	s.Error(repo.ClearAssignees(ctx, 1))
	s.Error(repo.AddAssignee(ctx, 1, 42))
}
