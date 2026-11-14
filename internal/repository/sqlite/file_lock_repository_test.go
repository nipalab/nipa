package sqlite

import (
	"context"
	"database/sql"
	"testing"
	"time"

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

	got, err := repo.Get(ctx, projectID, "assets", nil, time.Now())
	require.NoError(t, err)
	require.Equal(t, snow.ID(1001), got.ID)

	_, err = repo.Get(ctx, projectID, "assets", &devID, time.Now())
	require.NoError(t, err)

	_, err = repo.Get(ctx, projectID, "missing", nil, time.Now())
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

	list, err := repo.ListProject(ctx, projectID, time.Now())
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
	_, err = repo.Get(ctx, projectID, "assets", nil, time.Now())
	requireRecordNotFound(t, err)
	requireRecordNotFound(t, repo.Delete(ctx, snow.ID(9999)))

	require.NoError(t, repo.DeleteByBranch(ctx, projectID, devID))
	_, err = repo.Get(ctx, projectID, "assets", &devID, time.Now())
	requireRecordNotFound(t, err)

	require.NoError(t, repo.DeleteByMergeRequest(ctx, projectID, snow.ID(mr.ID)))
	_, err = repo.Get(ctx, projectID, "assets/orc.png", nil, time.Now())
	requireRecordNotFound(t, err)
}

func TestFileLockRepositorySQLite_ListEmpty(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewFileLockRepository(db)
	projectID := seedProject(t, q, 1, "game")

	list, err := repo.ListProject(ctx, projectID, time.Now())
	require.NoError(t, err)
	require.Empty(t, list)
}

func testSnowIDPtr(id int64) *snow.ID {
	value := snow.ID(id)
	return &value
}

func TestFileLockRepositorySQLite_Expiry(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewFileLockRepository(db)
	projectID := seedProject(t, q, 1, "game")
	userID := seedPBACUser(t, db, 42)

	now := time.Now().In(time.FixedZone("UTC+9", 9*3600))
	expires := now.Add(10 * time.Minute)
	created, err := repo.Create(ctx, domain.FileLock{ID: 1, ProjectID: projectID, Path: "a.png", HeldBy: userID, ExpiresAt: &expires})
	require.NoError(t, err)
	require.NotNil(t, created.ExpiresAt)
	require.True(t, created.ExpiresAt.Equal(expires))
	_, err = repo.Create(ctx, domain.FileLock{ID: 2, ProjectID: projectID, Path: "b.png", HeldBy: userID})
	require.NoError(t, err)

	list, err := repo.ListProject(ctx, projectID, now)
	require.NoError(t, err)
	require.Len(t, list, 2)
	require.True(t, list[0].ExpiresAt.Equal(expires))
	require.Nil(t, list[1].ExpiresAt)

	later := expires.Add(time.Nanosecond * 1500)
	list, err = repo.ListProject(ctx, projectID, later)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, "b.png", list[0].Path)
	_, err = repo.Get(ctx, projectID, "a.png", nil, later)
	requireRecordNotFound(t, err)
	_, err = repo.Get(ctx, projectID, "a.png", nil, expires.Add(-time.Millisecond))
	require.NoError(t, err)

	requireRecordNotFound(t, repo.SetExpiry(ctx, 1, 999, later.Add(time.Hour)))
	require.NoError(t, repo.SetExpiry(ctx, 1, userID, later.Add(time.Hour)))
	got, err := repo.Get(ctx, projectID, "a.png", nil, later)
	require.NoError(t, err)
	require.True(t, got.ExpiresAt.Equal(later.Add(time.Hour)))

	require.NoError(t, repo.DeleteExpired(ctx, projectID, later))
	list, err = repo.ListProject(ctx, projectID, now)
	require.NoError(t, err)
	require.Len(t, list, 2)

	require.NoError(t, repo.DeleteExpired(ctx, projectID, later.Add(2*time.Hour)))
	list, err = repo.ListProject(ctx, projectID, now)
	require.NoError(t, err)
	require.Len(t, list, 1)
	_, err = repo.Create(ctx, domain.FileLock{ID: 3, ProjectID: projectID, Path: "a.png", HeldBy: userID})
	require.NoError(t, err)
}

func TestFileLockRepositorySQLite_Requests(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewFileLockRepository(db)

	projectID := seedProject(t, q, 1, "game")
	holder := seedPBACUser(t, db, 42)
	asker := seedPBACUser(t, db, 43)
	lock, err := repo.Create(ctx, domain.FileLock{ID: 1001, ProjectID: projectID, Path: "assets", HeldBy: holder})
	require.NoError(t, err)

	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	_, err = repo.GetRequest(ctx, lock.ID, asker)
	requireRecordNotFound(t, err)

	created, err := repo.CreateRequest(ctx, domain.FileLockRequest{ID: 7, LockID: lock.ID, RequesterID: asker, Message: "please", CreatedAt: at})
	require.NoError(t, err)
	require.Equal(t, "please", created.Message)
	_, err = repo.CreateRequest(ctx, domain.FileLockRequest{ID: 8, LockID: lock.ID, RequesterID: asker, CreatedAt: at})
	require.Error(t, err, "one request per lock and requester")

	got, err := repo.GetRequest(ctx, lock.ID, asker)
	require.NoError(t, err)
	require.Equal(t, snow.ID(7), got.ID)

	later := at.Add(time.Hour)
	require.NoError(t, repo.UpdateRequest(ctx, 7, "now", later))
	requireRecordNotFound(t, repo.UpdateRequest(ctx, 99, "x", later))

	list, err := repo.ListRequests(ctx, projectID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, "now", list[0].Message)
	require.Equal(t, lock.ID, list[0].LockID)
	require.NotEmpty(t, list[0].RequesterName)
	other, err := repo.ListRequests(ctx, projectID+1)
	require.NoError(t, err)
	require.Empty(t, other)

	require.NoError(t, repo.Delete(ctx, lock.ID))
	list, err = repo.ListRequests(ctx, projectID)
	require.NoError(t, err)
	require.Empty(t, list, "releasing the lock drops its requests even without foreign keys")
}
