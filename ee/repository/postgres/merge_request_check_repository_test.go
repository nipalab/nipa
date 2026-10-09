package postgres

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/suite"

	sqlcPostgres "github.com/nipalab/nipa/ee/repository/postgres/sqlc"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type MergeRequestCheckRepositorySuite struct {
	baseSuite
}

func TestMergeRequestCheckRepositorySuite(t *testing.T) {
	suite.Run(t, new(MergeRequestCheckRepositorySuite))
}

func (s *MergeRequestCheckRepositorySuite) TestUpsertAndList() {
	ctx := context.Background()
	repo := NewMergeRequestCheckRepository(s.db)

	projectID := seedProject(s.T(), s.q, 1, "game")
	sourceID := seedBranch(s.T(), s.db, projectID, "feature", sql.NullInt64{})
	targetID := seedBranch(s.T(), s.db, projectID, "main", sql.NullInt64{})
	treeID := seedTreeNode(s.T(), s.db, "root", sql.NullInt64{})
	head := seedCommit(s.T(), s.db, s.q, projectID, treeID)
	reporter := seedPBACUser(s.T(), s.db, 42)

	mr, err := s.q.MergeRequestCreate(ctx, sqlcPostgres.MergeRequestCreateParams{
		ID: 2001, ProjectID: projectID.Int64(), SourceBranchID: sourceID.Int64(),
		TargetBranchID: targetID.Int64(), SourceBranchName: "feature",
		TargetBranchName: "main", Title: "t", CreatedBy: reporter.Int64(),
	})
	s.Require().NoError(err)

	check, err := repo.Upsert(ctx, domain.MergeRequestCheck{
		ID:             snow.ID(3001),
		MergeRequestID: mr.ID,
		HeadCommitID:   head,
		Name:           "build",
		State:          domain.MergeRequestCheckPending,
		Reporter:       domain.ReviewActor{UserID: reporter},
	})
	s.Require().NoError(err)
	s.Equal(snow.ID(3001), check.ID)
	s.Equal(mr.ID, check.MergeRequestID)
	s.Equal(head, check.HeadCommitID)
	s.Equal("build", check.Name)
	s.Equal(domain.MergeRequestCheckPending, check.State)
	s.Equal(reporter, check.Reporter.UserID)
	s.False(check.CreatedAt.IsZero())

	updated, err := repo.Upsert(ctx, domain.MergeRequestCheck{
		ID:             snow.ID(3002),
		MergeRequestID: mr.ID,
		HeadCommitID:   head,
		Name:           "build",
		State:          domain.MergeRequestCheckSuccess,
		DetailsURL:     "https://ci.example/run/1",
		Reporter:       domain.ReviewActor{UserID: reporter},
	})
	s.Require().NoError(err)
	s.Equal(snow.ID(3001), updated.ID, "an upsert keeps the original row")
	s.Equal(domain.MergeRequestCheckSuccess, updated.State)
	s.Equal("https://ci.example/run/1", updated.DetailsURL)

	list, err := repo.List(ctx, mr.ID, head)
	s.Require().NoError(err)
	s.Len(list, 1)
	s.Equal(snow.ID(3001), list[0].ID)
	s.Equal(domain.MergeRequestCheckSuccess, list[0].State)
	s.Equal("user42", list[0].Reporter.Name)
	s.Empty(list[0].Reporter.PhotoURL)

	other, err := repo.List(ctx, mr.ID, snow.ID(9999))
	s.Require().NoError(err)
	s.Empty(other)
}

func (s *MergeRequestCheckRepositorySuite) TestErrors() {
	ctx := context.Background()
	repo := NewMergeRequestCheckRepository(s.db)
	s.Require().NoError(s.db.Close())

	_, err := repo.Upsert(ctx, domain.MergeRequestCheck{ID: snow.ID(1), MergeRequestID: 1, HeadCommitID: snow.ID(2)})
	s.Error(err)

	_, err = repo.List(ctx, 1, snow.ID(2))
	s.Error(err)
}
