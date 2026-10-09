package postgres

import (
	"context"
	"database/sql"

	sqlcPostgres "github.com/nipalab/nipa/ee/repository/postgres/sqlc"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	"github.com/nipalab/nipa/internal/snow"
)

type MergeRequestRepository struct {
	queries *sqlcPostgres.Queries
}

func NewMergeRequestRepository(db *sql.DB) *MergeRequestRepository {
	return &MergeRequestRepository{queries: sqlcPostgres.New(dbtx.New(db))}
}

func (r *MergeRequestRepository) Create(ctx context.Context, mr domain.MergeRequest) (*domain.MergeRequest, error) {
	row, err := r.queries.MergeRequestCreate(ctx, sqlcPostgres.MergeRequestCreateParams{
		ID:                mr.ID,
		ProjectID:         mr.ProjectID.Int64(),
		SourceBranchID:    mr.SourceBranchID.Int64(),
		TargetBranchID:    mr.TargetBranchID.Int64(),
		SourceBranchName:  mr.SourceBranch,
		TargetBranchName:  mr.TargetBranch,
		Title:             mr.Title,
		Description:       mr.Description,
		IsDraft:           mr.Draft,
		MergeBaseCommitID: nullSnowID(mr.MergeBaseCommitID),
		CreatedBy:         mr.CreatedBy.Int64(),
	})
	if err != nil {
		return nil, handleError(err)
	}
	return mergeRequestToDomain(row), nil
}

func (r *MergeRequestRepository) Get(ctx context.Context, projectID snow.ID, number int64) (*domain.MergeRequest, error) {
	row, err := r.queries.MergeRequestGet(ctx, sqlcPostgres.MergeRequestGetParams{
		ProjectID: projectID.Int64(),
		Number:    number,
	})
	if err != nil {
		return nil, handleError(err)
	}
	return mergeRequestToDomain(row), nil
}

func (r *MergeRequestRepository) List(ctx context.Context, projectID snow.ID, opts domain.MergeRequestListOptions) ([]*domain.MergeRequest, error) {
	params := sqlcPostgres.MergeRequestListParams{
		ProjectID: projectID.Int64(),
		Limit:     int64(opts.Limit),
	}
	if opts.Status != "" {
		params.Status = sql.NullString{String: opts.Status, Valid: true}
	}
	if opts.Author != nil {
		params.Author = sql.NullInt64{Int64: opts.Author.Int64(), Valid: true}
	}
	if opts.SourceBranch != "" {
		params.SourceBranch = sql.NullString{String: opts.SourceBranch, Valid: true}
	}
	if opts.TargetBranch != "" {
		params.TargetBranch = sql.NullString{String: opts.TargetBranch, Valid: true}
	}
	if opts.Draft != nil {
		params.Draft = sql.NullBool{Bool: *opts.Draft, Valid: true}
	}
	if opts.Search != "" {
		params.Search = sql.NullString{String: opts.Search, Valid: true}
	}
	if opts.Assignee != nil {
		params.Assignee = sql.NullInt64{Int64: opts.Assignee.Int64(), Valid: true}
	}
	if opts.After > 0 {
		params.AfterNumber = sql.NullInt64{Int64: opts.After, Valid: true}
	}
	rows, err := r.queries.MergeRequestList(ctx, params)
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
	row, err := r.queries.MergeRequestUpdate(ctx, sqlcPostgres.MergeRequestUpdateParams{
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

func (r *MergeRequestRepository) SetDraft(ctx context.Context, projectID snow.ID, number int64, draft bool) (*domain.MergeRequest, error) {
	row, err := r.queries.MergeRequestSetDraft(ctx, sqlcPostgres.MergeRequestSetDraftParams{
		IsDraft:   draft,
		ProjectID: projectID.Int64(),
		Number:    number,
	})
	if err != nil {
		return nil, handleError(err)
	}
	return mergeRequestToDomain(row), nil
}

func (r *MergeRequestRepository) UpdateStatus(ctx context.Context, projectID snow.ID, number int64, status string, mergeCommitID *snow.ID) error {
	err := r.queries.MergeRequestUpdateStatus(ctx, sqlcPostgres.MergeRequestUpdateStatusParams{
		Status:        status,
		MergeCommitID: nullSnowID(mergeCommitID),
		ProjectID:     projectID.Int64(),
		Number:        number,
	})
	return handleError(err)
}

func (r *MergeRequestRepository) ListAssignees(ctx context.Context, projectID snow.ID) (map[int64][]domain.ReviewActor, error) {
	rows, err := r.queries.MergeRequestAssigneeList(ctx, projectID.Int64())
	if err != nil {
		return nil, handleError(err)
	}
	assignees := make(map[int64][]domain.ReviewActor)
	for _, row := range rows {
		assignees[row.MergeRequestNumber] = append(assignees[row.MergeRequestNumber], domain.ReviewActor{
			UserID:   snow.ID(row.UserID),
			Name:     row.UserName,
			PhotoURL: row.UserPhotoUrl.String,
		})
	}
	return assignees, nil
}

func (r *MergeRequestRepository) ClearAssignees(ctx context.Context, mergeRequestID int64) error {
	return handleError(r.queries.MergeRequestAssigneeClear(ctx, mergeRequestID))
}

func (r *MergeRequestRepository) AddAssignee(ctx context.Context, mergeRequestID int64, userID snow.ID) error {
	return handleError(r.queries.MergeRequestAssigneeAdd(ctx, sqlcPostgres.MergeRequestAssigneeAddParams{
		MergeRequestID: mergeRequestID,
		UserID:         userID.Int64(),
	}))
}

func mergeRequestToDomain(row sqlcPostgres.MergeRequest) *domain.MergeRequest {
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
		Draft:             row.IsDraft,
		MergeCommitID:     nullInt64SnowIDPtr(row.MergeCommitID),
		MergeBaseCommitID: nullInt64SnowIDPtr(row.MergeBaseCommitID),
		CreatedBy:         snow.ID(row.CreatedBy),
		CreatedAt:         row.CreatedAt,
		UpdatedAt:         row.UpdatedAt,
	}
}
