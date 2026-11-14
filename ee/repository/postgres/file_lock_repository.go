package postgres

import (
	"context"
	"database/sql"
	"time"

	sqlcPostgres "github.com/nipalab/nipa/ee/repository/postgres/sqlc"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	"github.com/nipalab/nipa/internal/snow"
)

// fileLockAdvisoryNamespace keeps the lock keys apart from the other advisory
// locks taken with plain row ids (the storage ledger keys by owner id).
const fileLockAdvisoryNamespace int64 = 0x4e49504c4f434b

type FileLockRepository struct {
	queries *sqlcPostgres.Queries
	db      *dbtx.DB
}

func NewFileLockRepository(db *sql.DB) *FileLockRepository {
	wrapped := dbtx.New(db)
	return &FileLockRepository{queries: sqlcPostgres.New(wrapped), db: wrapped}
}

// LockProject takes a transaction-scoped advisory lock for the project, so lock
// grants stay serialized across server processes. It must run inside a
// transaction carried by ctx; the lock is released on commit or rollback.
func (r *FileLockRepository) LockProject(ctx context.Context, projectID snow.ID) error {
	_, err := r.db.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, projectID.Int64()^fileLockAdvisoryNamespace)
	return handleError(err)
}

func (r *FileLockRepository) Create(ctx context.Context, lock domain.FileLock) (*domain.FileLock, error) {
	row, err := r.queries.FileLockCreate(ctx, sqlcPostgres.FileLockCreateParams{
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
	row, err := r.queries.FileLockGet(ctx, sqlcPostgres.FileLockGetParams{
		ProjectID: projectID.Int64(),
		Path:      path,
		BranchID:  nullSnowID(branchID),
		Now:       now.UTC(),
	})
	if err != nil {
		return nil, handleError(err)
	}
	return fileLockToDomain(row), nil
}

func (r *FileLockRepository) ListProject(ctx context.Context, projectID snow.ID, now time.Time) ([]*domain.FileLock, error) {
	rows, err := r.queries.FileLockListProject(ctx, sqlcPostgres.FileLockListProjectParams{ProjectID: projectID.Int64(), Now: now.UTC()})
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
	_, err := r.queries.FileLockDeleteExpired(ctx, sqlcPostgres.FileLockDeleteExpiredParams{
		ProjectID: projectID.Int64(),
		Now:       now.UTC(),
	})
	return handleError(err)
}

func (r *FileLockRepository) SetExpiry(ctx context.Context, id, holder snow.ID, expiresAt time.Time) error {
	rows, err := r.queries.FileLockSetExpiry(ctx, sqlcPostgres.FileLockSetExpiryParams{
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
	_, err := r.queries.FileLockDeleteByMergeRequest(ctx, sqlcPostgres.FileLockDeleteByMergeRequestParams{
		ProjectID:      projectID.Int64(),
		MergeRequestID: sql.NullInt64{Int64: mergeRequestID.Int64(), Valid: true},
	})
	return handleError(err)
}

func (r *FileLockRepository) DeleteByBranch(ctx context.Context, projectID, branchID snow.ID) error {
	_, err := r.queries.FileLockDeleteByBranch(ctx, sqlcPostgres.FileLockDeleteByBranchParams{
		ProjectID: projectID.Int64(),
		BranchID:  sql.NullInt64{Int64: branchID.Int64(), Valid: true},
	})
	return handleError(err)
}

func fileLockToDomain(row sqlcPostgres.FileLock) *domain.FileLock {
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

func fileLockRowToDomain(row sqlcPostgres.FileLockListProjectRow) *domain.FileLock {
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

func (r *FileLockRepository) GetRequest(ctx context.Context, lockID, requesterID snow.ID) (*domain.FileLockRequest, error) {
	row, err := r.queries.FileLockRequestGet(ctx, sqlcPostgres.FileLockRequestGetParams{LockID: lockID.Int64(), RequesterID: requesterID.Int64()})
	if err != nil {
		return nil, handleError(err)
	}
	return &domain.FileLockRequest{
		ID:          snow.ID(row.ID),
		LockID:      snow.ID(row.LockID),
		RequesterID: snow.ID(row.RequesterID),
		Message:     row.Message,
		CreatedAt:   row.CreatedAt,
	}, nil
}

func (r *FileLockRepository) CreateRequest(ctx context.Context, request domain.FileLockRequest) (*domain.FileLockRequest, error) {
	row, err := r.queries.FileLockRequestCreate(ctx, sqlcPostgres.FileLockRequestCreateParams{
		ID:          request.ID.Int64(),
		LockID:      request.LockID.Int64(),
		RequesterID: request.RequesterID.Int64(),
		Message:     request.Message,
		CreatedAt:   request.CreatedAt.UTC(),
	})
	if err != nil {
		return nil, handleError(err)
	}
	return &domain.FileLockRequest{
		ID:          snow.ID(row.ID),
		LockID:      snow.ID(row.LockID),
		RequesterID: snow.ID(row.RequesterID),
		Message:     row.Message,
		CreatedAt:   row.CreatedAt,
	}, nil
}

func (r *FileLockRepository) UpdateRequest(ctx context.Context, id snow.ID, message string, createdAt time.Time) error {
	rows, err := r.queries.FileLockRequestUpdate(ctx, sqlcPostgres.FileLockRequestUpdateParams{ID: id.Int64(), Message: message, CreatedAt: createdAt.UTC()})
	if err != nil {
		return handleError(err)
	}
	if rows == 0 {
		return domain.NewErrorRecordNotFound()
	}
	return nil
}

func (r *FileLockRepository) ListRequests(ctx context.Context, projectID snow.ID) ([]*domain.FileLockRequest, error) {
	rows, err := r.queries.FileLockRequestListProject(ctx, projectID.Int64())
	if err != nil {
		return nil, handleError(err)
	}
	out := make([]*domain.FileLockRequest, 0, len(rows))
	for _, row := range rows {
		out = append(out, &domain.FileLockRequest{
			ID:            snow.ID(row.ID),
			LockID:        snow.ID(row.LockID),
			RequesterID:   snow.ID(row.RequesterID),
			RequesterName: row.RequesterName,
			Message:       row.Message,
			CreatedAt:     row.CreatedAt,
		})
	}
	return out, nil
}
