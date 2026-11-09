package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	sqlcSqlite "github.com/nipalab/nipa/internal/repository/sqlc/sqlite"
	"github.com/nipalab/nipa/internal/snow"
)

type FileLockRepository struct {
	queries *sqlcSqlite.Queries
}

func NewFileLockRepository(db *sql.DB) *FileLockRepository {
	return &FileLockRepository{queries: sqlcSqlite.New(dbtx.New(db))}
}

func (r *FileLockRepository) Create(ctx context.Context, lock domain.FileLock) (*domain.FileLock, error) {
	row, err := r.queries.FileLockCreate(ctx, sqlcSqlite.FileLockCreateParams{
		ID:             lock.ID.Int64(),
		ProjectID:      lock.ProjectID.Int64(),
		BranchID:       nullSnowID(lock.BranchID),
		Path:           lock.Path,
		HeldBy:         lock.HeldBy.Int64(),
		MergeRequestID: nullSnowID(lock.MergeRequestID),
		ExpiresAt:      utcNullTime(lock.ExpiresAt),
	})
	if err != nil {
		return nil, handleError(err)
	}
	return fileLockToDomain(row), nil
}

func (r *FileLockRepository) Get(ctx context.Context, projectID snow.ID, path string, branchID *snow.ID, now time.Time) (*domain.FileLock, error) {
	row, err := r.queries.FileLockGet(ctx, sqlcSqlite.FileLockGetParams{
		ProjectID: projectID.Int64(),
		Path:      path,
		BranchID:  nullSnowID(branchID),
		Now:       sql.NullTime{Time: now.UTC(), Valid: true},
	})
	if err != nil {
		return nil, handleError(err)
	}
	return fileLockToDomain(row), nil
}

func (r *FileLockRepository) ListProject(ctx context.Context, projectID snow.ID, now time.Time) ([]*domain.FileLock, error) {
	rows, err := r.queries.FileLockListProject(ctx, sqlcSqlite.FileLockListProjectParams{ProjectID: projectID.Int64(), Now: sql.NullTime{Time: now.UTC(), Valid: true}})
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

func (r *FileLockRepository) DeleteExpired(ctx context.Context, projectID snow.ID, now time.Time) error {
	_, err := r.queries.FileLockDeleteExpired(ctx, sqlcSqlite.FileLockDeleteExpiredParams{
		ProjectID: projectID.Int64(),
		Now:       sql.NullTime{Time: now.UTC(), Valid: true},
	})
	return handleError(err)
}

func (r *FileLockRepository) SetExpiry(ctx context.Context, id, holder snow.ID, expiresAt time.Time) error {
	rows, err := r.queries.FileLockSetExpiry(ctx, sqlcSqlite.FileLockSetExpiryParams{
		ExpiresAt: utcNullTime(&expiresAt),
		ID:        id.Int64(),
		HeldBy:    holder.Int64(),
	})
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
		ExpiresAt:      nullTimePtr(row.ExpiresAt),
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
		ExpiresAt:          nullTimePtr(row.ExpiresAt),
	}
}

func utcNullTime(t *time.Time) sql.NullTime {
	if t == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: t.UTC(), Valid: true}
}
