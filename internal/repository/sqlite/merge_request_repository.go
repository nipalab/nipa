package sqlite

import (
	"context"
	"database/sql"

	"github.com/nipalab/nipa/internal/domain"
	sqlcSqlite "github.com/nipalab/nipa/internal/repository/sqlc/sqlite"
	"github.com/nipalab/nipa/internal/snow"
)

type MergeRequestRepository struct {
	queries *sqlcSqlite.Queries
}

func NewMergeRequestRepository(db *sql.DB) *MergeRequestRepository {
	return &MergeRequestRepository{queries: sqlcSqlite.New(db)}
}

func (r *MergeRequestRepository) Create(ctx context.Context, mr domain.MergeRequest) (*domain.MergeRequest, error) {
	row, err := r.queries.MergeRequestCreate(ctx, sqlcSqlite.MergeRequestCreateParams{
		ID:                mr.ID,
		ProjectID:         mr.ProjectID.Int64(),
		SourceBranchID:    mr.SourceBranchID.Int64(),
		TargetBranchID:    mr.TargetBranchID.Int64(),
		SourceBranchName:  mr.SourceBranch,
		TargetBranchName:  mr.TargetBranch,
		Title:             mr.Title,
		Description:       mr.Description,
		MergeBaseCommitID: nullSnowID(mr.MergeBaseCommitID),
		CreatedBy:         mr.CreatedBy.Int64(),
	})
	if err != nil {
		return nil, handleError(err)
	}
	return mergeRequestToDomain(row), nil
}

func (r *MergeRequestRepository) Get(ctx context.Context, projectID snow.ID, number int64) (*domain.MergeRequest, error) {
	row, err := r.queries.MergeRequestGet(ctx, sqlcSqlite.MergeRequestGetParams{
		ProjectID: projectID.Int64(),
		Number:    number,
	})
	if err != nil {
		return nil, handleError(err)
	}
	return mergeRequestToDomain(row), nil
}

func (r *MergeRequestRepository) List(ctx context.Context, projectID snow.ID, status string, limit int) ([]*domain.MergeRequest, error) {
	var (
		rows []sqlcSqlite.MergeRequest
		err  error
	)
	if status == "" {
		rows, err = r.queries.MergeRequestList(ctx, sqlcSqlite.MergeRequestListParams{
			ProjectID: projectID.Int64(),
			Limit:     int64(limit),
		})
	} else {
		rows, err = r.queries.MergeRequestListByStatus(ctx, sqlcSqlite.MergeRequestListByStatusParams{
			ProjectID: projectID.Int64(),
			Status:    status,
			Limit:     int64(limit),
		})
	}
	if err != nil {
		return nil, handleError(err)
	}
	requests := make([]*domain.MergeRequest, 0, len(rows))
	for _, row := range rows {
		requests = append(requests, mergeRequestToDomain(row))
	}
	return requests, nil
}

func (r *MergeRequestRepository) Update(ctx context.Context, projectID snow.ID, number int64, title, description string) (*domain.MergeRequest, error) {
	row, err := r.queries.MergeRequestUpdate(ctx, sqlcSqlite.MergeRequestUpdateParams{
		Title:       title,
		Description: description,
		ProjectID:   projectID.Int64(),
		Number:      number,
	})
	if err != nil {
		return nil, handleError(err)
	}
	return mergeRequestToDomain(row), nil
}

func (r *MergeRequestRepository) UpdateStatus(ctx context.Context, projectID snow.ID, number int64, status string, mergeCommitID *snow.ID) error {
	err := r.queries.MergeRequestUpdateStatus(ctx, sqlcSqlite.MergeRequestUpdateStatusParams{
		Status:        status,
		MergeCommitID: nullSnowID(mergeCommitID),
		ProjectID:     projectID.Int64(),
		Number:        number,
	})
	return handleError(err)
}

func mergeRequestToDomain(row sqlcSqlite.MergeRequest) *domain.MergeRequest {
	return &domain.MergeRequest{
		ID:                row.ID,
		Number:            row.Number,
		ProjectID:         snow.ID(row.ProjectID),
		SourceBranchID:    snow.ID(row.SourceBranchID),
		TargetBranchID:    snow.ID(row.TargetBranchID),
		SourceBranch:      row.SourceBranchName,
		TargetBranch:      row.TargetBranchName,
		Title:             row.Title,
		Description:       row.Description,
		Status:            row.Status,
		MergeCommitID:     nullInt64SnowIDPtr(row.MergeCommitID),
		MergeBaseCommitID: nullInt64SnowIDPtr(row.MergeBaseCommitID),
		CreatedBy:         snow.ID(row.CreatedBy),
		CreatedAt:         row.CreatedAt,
		UpdatedAt:         row.UpdatedAt,
	}
}
