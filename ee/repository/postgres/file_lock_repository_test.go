package postgres

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	sqlcPostgres "github.com/nipalab/nipa/ee/repository/postgres/sqlc"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/dbtx"
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

	got, err := repo.Get(ctx, projectID, "assets", nil, time.Now())
	s.Require().NoError(err)
	s.Equal(snow.ID(1001), got.ID)

	_, err = repo.Get(ctx, projectID, "assets", &devID, time.Now())
	s.Require().NoError(err)

	_, err = repo.Get(ctx, projectID, "missing", nil, time.Now())
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

	list, err := repo.ListProject(ctx, projectID, time.Now())
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
	_, err = repo.Get(ctx, projectID, "assets", nil, time.Now())
	requireRecordNotFound(s.T(), err)
	requireRecordNotFound(s.T(), repo.Delete(ctx, snow.ID(9999)))

	s.Require().NoError(repo.DeleteByBranch(ctx, projectID, devID))
	_, err = repo.Get(ctx, projectID, "assets", &devID, time.Now())
	requireRecordNotFound(s.T(), err)

	s.Require().NoError(repo.DeleteByMergeRequest(ctx, projectID, snow.ID(mr.ID)))
	_, err = repo.Get(ctx, projectID, "assets/orc.png", nil, time.Now())
	requireRecordNotFound(s.T(), err)
}

func (s *FileLockRepositorySuite) TestListEmpty() {
	ctx := context.Background()
	repo := NewFileLockRepository(s.db)
	projectID := seedProject(s.T(), s.q, 1, "game")

	list, err := repo.ListProject(ctx, projectID, time.Now())
	s.Require().NoError(err)
	s.Empty(list)
}

func (s *FileLockRepositorySuite) TestLockProjectSerializesTransactions() {
	ctx := context.Background()
	repo := NewFileLockRepository(s.db)
	transactor := dbtx.NewTransactor(s.db)

	acquired := make(chan struct{})
	release := make(chan struct{})
	holderDone := make(chan error, 1)
	go func() {
		holderDone <- transactor.WithinTx(ctx, func(txCtx context.Context) error {
			if err := repo.LockProject(txCtx, 77); err != nil {
				return err
			}
			close(acquired)
			<-release
			return nil
		})
	}()
	<-acquired

	waiterDone := make(chan error, 1)
	waiterEntered := make(chan struct{})
	go func() {
		waiterDone <- transactor.WithinTx(ctx, func(txCtx context.Context) error {
			close(waiterEntered)
			return repo.LockProject(txCtx, 77)
		})
	}()
	<-waiterEntered

	select {
	case err := <-waiterDone:
		s.Failf("second transaction should wait for the project lock", "returned early: %v", err)
	case <-time.After(300 * time.Millisecond):
	}

	other := make(chan error, 1)
	go func() {
		other <- transactor.WithinTx(ctx, func(txCtx context.Context) error {
			return repo.LockProject(txCtx, 78)
		})
	}()
	select {
	case err := <-other:
		s.Require().NoError(err, "other projects are not blocked")
	case <-time.After(5 * time.Second):
		s.Fail("a different project must not wait")
	}

	close(release)
	s.Require().NoError(<-holderDone)
	s.Require().NoError(<-waiterDone)
}

func (s *FileLockRepositorySuite) TestLockProjectJoinsAnOuterTransaction() {
	ctx := context.Background()
	repo := NewFileLockRepository(s.db)
	transactor := dbtx.NewTransactor(s.db)

	acquired := make(chan struct{})
	release := make(chan struct{})
	outerDone := make(chan error, 1)
	go func() {
		outerDone <- transactor.WithinTx(ctx, func(outerCtx context.Context) error {
			return transactor.WithinTx(outerCtx, func(innerCtx context.Context) error {
				if err := repo.LockProject(innerCtx, 91); err != nil {
					return err
				}
				close(acquired)
				<-release
				return nil
			})
		})
	}()
	<-acquired

	waiter := make(chan error, 1)
	go func() {
		waiter <- transactor.WithinTx(ctx, func(txCtx context.Context) error {
			return repo.LockProject(txCtx, 91)
		})
	}()

	select {
	case err := <-waiter:
		s.Failf("the lock must be held until the outer transaction ends", "returned early: %v", err)
	case <-time.After(300 * time.Millisecond):
	}

	close(release)
	s.Require().NoError(<-outerDone)
	s.Require().NoError(<-waiter)
}

func (s *FileLockRepositorySuite) TestExpiry() {
	ctx := context.Background()
	repo := NewFileLockRepository(s.db)
	projectID := seedProject(s.T(), s.q, 1, "game")
	userID := seedPBACUser(s.T(), s.db, 42)

	now := time.Now().Truncate(time.Microsecond)
	expires := now.Add(10 * time.Minute)
	created, err := repo.Create(ctx, domain.FileLock{ID: 1, ProjectID: projectID, Path: "a.png", HeldBy: userID, ExpiresAt: &expires})
	s.Require().NoError(err)
	s.Require().NotNil(created.ExpiresAt)
	s.True(created.ExpiresAt.Equal(expires))
	_, err = repo.Create(ctx, domain.FileLock{ID: 2, ProjectID: projectID, Path: "b.png", HeldBy: userID})
	s.Require().NoError(err)

	list, err := repo.ListProject(ctx, projectID, now)
	s.Require().NoError(err)
	s.Require().Len(list, 2)

	later := expires.Add(time.Second)
	list, err = repo.ListProject(ctx, projectID, later)
	s.Require().NoError(err)
	s.Require().Len(list, 1)
	s.Equal("b.png", list[0].Path)
	_, err = repo.Get(ctx, projectID, "a.png", nil, later)
	requireRecordNotFound(s.T(), err)

	requireRecordNotFound(s.T(), repo.SetExpiry(ctx, 1, 999, later.Add(time.Hour)))
	s.Require().NoError(repo.SetExpiry(ctx, 1, userID, later.Add(time.Hour)))
	got, err := repo.Get(ctx, projectID, "a.png", nil, later)
	s.Require().NoError(err)
	s.True(got.ExpiresAt.Equal(later.Add(time.Hour)))

	s.Require().NoError(repo.DeleteExpired(ctx, projectID, later.Add(2*time.Hour)))
	list, err = repo.ListProject(ctx, projectID, now)
	s.Require().NoError(err)
	s.Require().Len(list, 1)
	_, err = repo.Create(ctx, domain.FileLock{ID: 3, ProjectID: projectID, Path: "a.png", HeldBy: userID})
	s.Require().NoError(err)
}

func (s *FileLockRepositorySuite) TestRequests() {
	ctx := context.Background()
	repo := NewFileLockRepository(s.db)

	projectID := seedProject(s.T(), s.q, 1, "game")
	holder := seedPBACUser(s.T(), s.db, 42)
	asker := seedPBACUser(s.T(), s.db, 43)
	lock, err := repo.Create(ctx, domain.FileLock{ID: 1001, ProjectID: projectID, Path: "assets", HeldBy: holder})
	s.Require().NoError(err)

	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	_, err = repo.GetRequest(ctx, lock.ID, asker)
	s.Require().Error(err)
	s.True(domain.IsErrorNotFound(err))

	created, err := repo.CreateRequest(ctx, domain.FileLockRequest{ID: 7, LockID: lock.ID, RequesterID: asker, Message: "please", CreatedAt: at})
	s.Require().NoError(err)
	s.Equal("please", created.Message)
	_, err = repo.CreateRequest(ctx, domain.FileLockRequest{ID: 8, LockID: lock.ID, RequesterID: asker, CreatedAt: at})
	s.Require().Error(err, "one request per lock and requester")

	got, err := repo.GetRequest(ctx, lock.ID, asker)
	s.Require().NoError(err)
	s.Equal(snow.ID(7), got.ID)

	s.Require().NoError(repo.UpdateRequest(ctx, 7, "now", at.Add(time.Hour)))
	s.True(domain.IsErrorNotFound(repo.UpdateRequest(ctx, 99, "x", at)))

	list, err := repo.ListRequests(ctx, projectID)
	s.Require().NoError(err)
	s.Require().Len(list, 1)
	s.Equal("now", list[0].Message)
	s.NotEmpty(list[0].RequesterName)

	s.Require().NoError(repo.Delete(ctx, lock.ID))
	list, err = repo.ListRequests(ctx, projectID)
	s.Require().NoError(err)
	s.Empty(list, "requests go with the lock")
}
