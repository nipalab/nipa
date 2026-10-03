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

	list, err := repo.List(ctx, projectID, "", 10)
	s.Require().NoError(err)
	s.Len(list, 2)
	s.Equal(int64(2), list[0].Number)

	list, err = repo.List(ctx, projectID, domain.MergeRequestClosed, 10)
	s.Require().NoError(err)
	s.Empty(list)

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

func (s *MergeRequestRepositorySuite) TestDelete() {
	ctx := context.Background()
	repo := NewMergeRequestRepository(s.db)

	projectID := seedProject(s.T(), s.q, 1, "game")
	userID := seedPBACUser(s.T(), s.db, 42)
	sourceID := seedBranch(s.T(), s.db, projectID, "feature", sql.NullInt64{})
	targetID := seedBranch(s.T(), s.db, projectID, "main", sql.NullInt64{})

	created, err := repo.Create(ctx, domain.MergeRequest{
		ID:             5101,
		ProjectID:      projectID,
		SourceBranchID: sourceID,
		TargetBranchID: targetID,
		SourceBranch:   "feature",
		TargetBranch:   "main",
		Title:          "Rollback me",
		CreatedBy:      userID,
	})
	s.Require().NoError(err)

	s.Require().NoError(repo.Delete(ctx, projectID, created.ID))
	_, err = repo.Get(ctx, projectID, created.Number)
	requireRecordNotFound(s.T(), err)

	s.Require().NoError(repo.Delete(ctx, projectID, created.ID))
}
