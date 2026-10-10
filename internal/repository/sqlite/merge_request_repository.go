package sqlite

import (
	"context"
	"database/sql"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	sqlcSqlite "github.com/nipalab/nipa/internal/repository/sqlc/sqlite"
	"github.com/nipalab/nipa/internal/snow"
)

type MergeRequestRepository struct {
	queries *sqlcSqlite.Queries
}

func NewMergeRequestRepository(db *sql.DB) *MergeRequestRepository {
	return &MergeRequestRepository{queries: sqlcSqlite.New(dbtx.New(db))}
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
	row, err := r.queries.MergeRequestGet(ctx, sqlcSqlite.MergeRequestGetParams{
		ProjectID: projectID.Int64(),
		Number:    number,
	})
	if err != nil {
		return nil, handleError(err)
	}
	return mergeRequestToDomain(row), nil
}

func (r *MergeRequestRepository) List(ctx context.Context, projectID snow.ID, opts domain.MergeRequestListOptions) ([]*domain.MergeRequest, error) {
	params := sqlcSqlite.MergeRequestListParams{
		ProjectID: projectID.Int64(),
		Limit:     int64(opts.Limit),
	}
	if opts.Status != "" {
		params.Status = opts.Status
	}
	if opts.Author != nil {
		params.Author = opts.Author.Int64()
	}
	if opts.SourceBranch != "" {
		params.SourceBranch = opts.SourceBranch
	}
	if opts.TargetBranch != "" {
		params.TargetBranch = opts.TargetBranch
	}
	if opts.Draft != nil {
		params.Draft = *opts.Draft
	}
	if opts.Search != "" {
		params.Search = opts.Search
	}
	if opts.Assignee != nil {
		params.Assignee = opts.Assignee.Int64()
	}
	if opts.After > 0 {
		params.AfterNumber = opts.After
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

func (r *MergeRequestRepository) SetDraft(ctx context.Context, projectID snow.ID, number int64, draft bool) (*domain.MergeRequest, error) {
	row, err := r.queries.MergeRequestSetDraft(ctx, sqlcSqlite.MergeRequestSetDraftParams{
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
	err := r.queries.MergeRequestUpdateStatus(ctx, sqlcSqlite.MergeRequestUpdateStatusParams{
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

func (r *MergeRequestRepository) ListAssigneesByMergeRequest(ctx context.Context, mergeRequestID int64) ([]domain.ReviewActor, error) {
	rows, err := r.queries.MergeRequestAssigneeListByMergeRequest(ctx, mergeRequestID)
	if err != nil {
		return nil, handleError(err)
	}
	assignees := make([]domain.ReviewActor, 0, len(rows))
	for _, row := range rows {
		assignees = append(assignees, domain.ReviewActor{
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
	return handleError(r.queries.MergeRequestAssigneeAdd(ctx, sqlcSqlite.MergeRequestAssigneeAddParams{
		MergeRequestID: mergeRequestID,
		UserID:         userID.Int64(),
	}))
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
		Draft:             row.IsDraft,
		MergeCommitID:     nullInt64SnowIDPtr(row.MergeCommitID),
		MergeBaseCommitID: nullInt64SnowIDPtr(row.MergeBaseCommitID),
		CreatedBy:         snow.ID(row.CreatedBy),
		CreatedAt:         row.CreatedAt,
		UpdatedAt:         row.UpdatedAt,
	}
}
