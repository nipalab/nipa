package sqlite

import (
	"context"
	"database/sql"

	"github.com/nipalab/nipa/internal/domain"
	sqlcSqlite "github.com/nipalab/nipa/internal/repository/sqlc/sqlite"
	"github.com/nipalab/nipa/internal/snow"
)

type FileLockRepository struct {
	queries *sqlcSqlite.Queries
}

func NewFileLockRepository(db *sql.DB) *FileLockRepository {
	return &FileLockRepository{queries: sqlcSqlite.New(db)}
}

func (r *FileLockRepository) Create(ctx context.Context, lock domain.FileLock) (*domain.FileLock, error) {
	row, err := r.queries.FileLockCreate(ctx, sqlcSqlite.FileLockCreateParams{
		ID:             lock.ID.Int64(),
		ProjectID:      lock.ProjectID.Int64(),
		BranchID:       nullSnowID(lock.BranchID),
		Path:           lock.Path,
		HeldBy:         lock.HeldBy.Int64(),
		MergeRequestID: nullSnowID(lock.MergeRequestID),
	})
	if err != nil {
		return nil, handleError(err)
	}
	return fileLockToDomain(row), nil
}

func (r *FileLockRepository) Get(ctx context.Context, projectID snow.ID, path string, branchID *snow.ID) (*domain.FileLock, error) {
	row, err := r.queries.FileLockGet(ctx, sqlcSqlite.FileLockGetParams{
		ProjectID: projectID.Int64(),
		Path:      path,
		BranchID:  nullSnowID(branchID),
	})
	if err != nil {
		return nil, handleError(err)
	}
	return fileLockToDomain(row), nil
}

func (r *FileLockRepository) ListProject(ctx context.Context, projectID snow.ID) ([]*domain.FileLock, error) {
	rows, err := r.queries.FileLockListProject(ctx, projectID.Int64())
	if err != nil {
		return nil, handleError(err)
	}
	locks := make([]*domain.FileLock, 0, len(rows))
	for _, row := range rows {
		locks = append(locks, fileLockRowToDomain(row))
	}
	return locks, nil
}

func (r *FileLockRepository) Delete(ctx context.Context, id snow.ID) error {
	rows, err := r.queries.FileLockDelete(ctx, id.Int64())
	if err != nil {
		return handleError(err)
	}
	if rows == 0 {
		return domain.NewErrorRecordNotFound()
	}
	return nil
}

func (r *FileLockRepository) DeleteByMergeRequest(ctx context.Context, projectID, mergeRequestID snow.ID) error {
	_, err := r.queries.FileLockDeleteByMergeRequest(ctx, sqlcSqlite.FileLockDeleteByMergeRequestParams{
		ProjectID:      projectID.Int64(),
		MergeRequestID: sql.NullInt64{Int64: mergeRequestID.Int64(), Valid: true},
	})
	return handleError(err)
}

func (r *FileLockRepository) DeleteByBranch(ctx context.Context, projectID, branchID snow.ID) error {
	_, err := r.queries.FileLockDeleteByBranch(ctx, sqlcSqlite.FileLockDeleteByBranchParams{
		ProjectID: projectID.Int64(),
		BranchID:  sql.NullInt64{Int64: branchID.Int64(), Valid: true},
	})
	return handleError(err)
}

func fileLockToDomain(row sqlcSqlite.FileLock) *domain.FileLock {
	return &domain.FileLock{
		ID:             snow.ID(row.ID),
		ProjectID:      snow.ID(row.ProjectID),
		BranchID:       nullInt64SnowIDPtr(row.BranchID),
		Path:           row.Path,
		HeldBy:         snow.ID(row.HeldBy),
		MergeRequestID: nullInt64SnowIDPtr(row.MergeRequestID),
		AcquiredAt:     row.AcquiredAt,
	}
}

func fileLockRowToDomain(row sqlcSqlite.FileLockListProjectRow) *domain.FileLock {
	return &domain.FileLock{
		ID:                 snow.ID(row.ID),
		ProjectID:          snow.ID(row.ProjectID),
		BranchID:           nullInt64SnowIDPtr(row.BranchID),
		Branch:             row.BranchName.String,
		Path:               row.Path,
		HeldBy:             snow.ID(row.HeldBy),
		HeldByName:         row.HeldByName,
		MergeRequestID:     nullInt64SnowIDPtr(row.MergeRequestID),
		MergeRequestNumber: nullInt64Ptr(row.MergeRequestNumber),
		AcquiredAt:         row.AcquiredAt,
	}
}
