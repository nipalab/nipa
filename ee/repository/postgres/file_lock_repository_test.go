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

type FileLockRepositorySuite struct {
	baseSuite
}

func TestFileLockRepositorySuite(t *testing.T) {
	suite.Run(t, new(FileLockRepositorySuite))
}

func (s *FileLockRepositorySuite) TestCRUD() {
	ctx := context.Background()
	repo := NewFileLockRepository(s.db)

	projectID := seedProject(s.T(), s.q, 1, "game")
	userID := seedPBACUser(s.T(), s.db, 42)
	mainID := seedBranch(s.T(), s.db, projectID, "main", sql.NullInt64{})
	devID := seedBranch(s.T(), s.db, projectID, "feature", sql.NullInt64{})

	global, err := repo.Create(ctx, domain.FileLock{
		ID: 1001, ProjectID: projectID, Path: "assets", HeldBy: userID,
	})
	s.Require().NoError(err)
	s.Nil(global.BranchID)

	_, err = repo.Create(ctx, domain.FileLock{
		ID: 1002, ProjectID: projectID, Path: "assets", HeldBy: userID,
	})
	s.Require().Error(err, "a second global lock on the same path must be rejected")

	scoped, err := repo.Create(ctx, domain.FileLock{
		ID: 1003, ProjectID: projectID, BranchID: &devID, Path: "assets", HeldBy: userID,
	})
	s.Require().NoError(err)
	s.Require().NotNil(scoped.BranchID)
	s.Equal(devID, *scoped.BranchID)

	_, err = repo.Create(ctx, domain.FileLock{
		ID: 1004, ProjectID: projectID, BranchID: &devID, Path: "assets", HeldBy: userID,
	})
	s.Require().Error(err, "a second branch lock on the same path must be rejected")

	got, err := repo.Get(ctx, projectID, "assets", nil)
	s.Require().NoError(err)
	s.Equal(snow.ID(1001), got.ID)

	_, err = repo.Get(ctx, projectID, "assets", &devID)
	s.Require().NoError(err)

	_, err = repo.Get(ctx, projectID, "missing", nil)
	requireRecordNotFound(s.T(), err)

	mr, err := s.q.MergeRequestCreate(ctx, sqlcPostgres.MergeRequestCreateParams{
		ID: 2001, ProjectID: projectID.Int64(), SourceBranchID: devID.Int64(),
		TargetBranchID:   mainID.Int64(),
		SourceBranchName: "feature", TargetBranchName: "main", Title: "t", CreatedBy: userID.Int64(),
	})
	s.Require().NoError(err)
	linked, err := repo.Create(ctx, domain.FileLock{
		ID: 1006, ProjectID: projectID, Path: "assets/orc.png", HeldBy: userID,
		MergeRequestID: testSnowIDPtr(mr.ID),
	})
	s.Require().NoError(err)
	s.Require().NotNil(linked.MergeRequestID)

	list, err := repo.ListProject(ctx, projectID)
	s.Require().NoError(err)
	s.Len(list, 3)
	var globalRow, featureRow, linkedRow *domain.FileLock
	for _, lock := range list {
		s.Equal("user42", lock.HeldByName)
		switch {
		case lock.Path == "assets" && lock.BranchID == nil:
			globalRow = lock
		case lock.Path == "assets" && lock.BranchID != nil && *lock.BranchID == devID:
			featureRow = lock
		case lock.Path == "assets/orc.png":
			linkedRow = lock
		}
	}
	s.Require().NotNil(globalRow)
	s.Empty(globalRow.Branch)
	s.Require().NotNil(featureRow)
	s.Equal("feature", featureRow.Branch)
	s.Require().NotNil(linkedRow)
	s.Require().NotNil(linkedRow.MergeRequestNumber)
	s.Equal(int64(1), *linkedRow.MergeRequestNumber)

	s.Require().NoError(repo.Delete(ctx, got.ID))
	_, err = repo.Get(ctx, projectID, "assets", nil)
	requireRecordNotFound(s.T(), err)
	requireRecordNotFound(s.T(), repo.Delete(ctx, snow.ID(9999)))

	s.Require().NoError(repo.DeleteByBranch(ctx, projectID, devID))
	_, err = repo.Get(ctx, projectID, "assets", &devID)
	requireRecordNotFound(s.T(), err)

	s.Require().NoError(repo.DeleteByMergeRequest(ctx, projectID, snow.ID(mr.ID)))
	_, err = repo.Get(ctx, projectID, "assets/orc.png", nil)
	requireRecordNotFound(s.T(), err)
}

func (s *FileLockRepositorySuite) TestListEmpty() {
	ctx := context.Background()
	repo := NewFileLockRepository(s.db)
	projectID := seedProject(s.T(), s.q, 1, "game")

	list, err := repo.ListProject(ctx, projectID)
	s.Require().NoError(err)
	s.Empty(list)
}
