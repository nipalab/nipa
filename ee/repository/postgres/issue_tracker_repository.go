package postgres

import (
	"context"
	"database/sql"
	"strings"

	sqlcPostgres "github.com/nipalab/nipa/ee/repository/postgres/sqlc"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	"github.com/nipalab/nipa/internal/snow"
)

// IssueTrackerRepository stores org issue tracker connections and the
// project settings that use them. Rows of deleted orgs and projects are
// removed by triggers and cascades.
type IssueTrackerRepository struct {
	queries *sqlcPostgres.Queries
}

func NewIssueTrackerRepository(db *sql.DB) *IssueTrackerRepository {
	return &IssueTrackerRepository{queries: sqlcPostgres.New(dbtx.New(db))}
}

const issueTrackerAdvisoryNamespace int64 = 0x4e4950495353

// LockOrg takes the org's transaction-scoped issue tracker lock. It must run
// inside a transaction carried by ctx.
func (r *IssueTrackerRepository) LockOrg(ctx context.Context, orgID snow.ID) error {
	return handleError(r.queries.IssueTrackerLockOrg(ctx, orgID.Int64()^issueTrackerAdvisoryNamespace))
}

func (r *IssueTrackerRepository) GetByOrg(ctx context.Context, orgID snow.ID) (*domain.IssueTracker, error) {
	row, err := r.queries.IssueTrackerGetByOrg(ctx, orgID.Int64())
	if err != nil {
		return nil, handleError(err)
	}
	return issueTrackerToDomain(row), nil
}

func (r *IssueTrackerRepository) Create(ctx context.Context, t domain.IssueTracker) (*domain.IssueTracker, error) {
	row, err := r.queries.IssueTrackerCreate(ctx, sqlcPostgres.IssueTrackerCreateParams{
		ID:           t.ID.Int64(),
		OrgID:        t.OrgID.Int64(),
		Kind:         t.Kind,
		Name:         t.Name,
		BaseUrl:      t.BaseURL,
		AuthKind:     t.AuthKind,
		Account:      t.Account,
		SecretSealed: t.SecretSealed,
		CreatedBy:    t.CreatedBy.Int64(),
		CreatedAt:    t.CreatedAt,
		UpdatedAt:    t.UpdatedAt,
	})
	if err != nil {
		return nil, handleError(err)
	}
	return issueTrackerToDomain(row), nil
}

func (r *IssueTrackerRepository) Update(ctx context.Context, t domain.IssueTracker) (*domain.IssueTracker, error) {
	row, err := r.queries.IssueTrackerUpdate(ctx, sqlcPostgres.IssueTrackerUpdateParams{
		Kind:         t.Kind,
		Name:         t.Name,
		BaseUrl:      t.BaseURL,
		AuthKind:     t.AuthKind,
		Account:      t.Account,
		SecretSealed: t.SecretSealed,
		UpdatedAt:    t.UpdatedAt,
		OrgID:        t.OrgID.Int64(),
		ID:           t.ID.Int64(),
	})
	if err != nil {
		return nil, handleError(err)
	}
	return issueTrackerToDomain(row), nil
}

func (r *IssueTrackerRepository) Delete(ctx context.Context, orgID, id snow.ID) (bool, error) {
	n, err := r.queries.IssueTrackerDelete(ctx, sqlcPostgres.IssueTrackerDeleteParams{OrgID: orgID.Int64(), ID: id.Int64()})
	return n > 0, handleError(err)
}

func (r *IssueTrackerRepository) CountProjects(ctx context.Context, trackerID snow.ID) (int64, error) {
	n, err := r.queries.IssueTrackerCountProjects(ctx, trackerID.Int64())
	return n, handleError(err)
}

func (r *IssueTrackerRepository) GetProject(ctx context.Context, projectID snow.ID) (*domain.ProjectIssueTracker, error) {
	row, err := r.queries.ProjectIssueTrackerGet(ctx, projectID.Int64())
	if err != nil {
		return nil, handleError(err)
	}
	return projectIssueTrackerToDomain(row), nil
}

func (r *IssueTrackerRepository) UpsertProject(ctx context.Context, s domain.ProjectIssueTracker) (*domain.ProjectIssueTracker, error) {
	row, err := r.queries.ProjectIssueTrackerUpsert(ctx, sqlcPostgres.ProjectIssueTrackerUpsertParams{
		ProjectID:         s.ProjectID.Int64(),
		TrackerID:         s.TrackerID.Int64(),
		ProjectKeys:       strings.Join(s.Keys, ","),
		LinkMergeRequests: s.LinkMergeRequests,
		LinkCommits:       s.LinkCommits,
		CommentOnMerge:    s.CommentOnMerge,
		MergedStatus:      s.MergedStatus,
		OpenedStatus:      s.OpenedStatus,
		IsActive:          s.IsActive,
		SetBy:             s.SetBy.Int64(),
		UpdatedAt:         s.UpdatedAt,
	})
	if err != nil {
		return nil, handleError(err)
	}
	return projectIssueTrackerToDomain(row), nil
}

func (r *IssueTrackerRepository) DeleteProject(ctx context.Context, projectID snow.ID) (bool, error) {
	n, err := r.queries.ProjectIssueTrackerDelete(ctx, projectID.Int64())
	return n > 0, handleError(err)
}

func issueTrackerToDomain(row sqlcPostgres.IssueTracker) *domain.IssueTracker {
	return &domain.IssueTracker{
		ID:           snow.ID(row.ID),
		OrgID:        snow.ID(row.OrgID),
		Kind:         row.Kind,
		Name:         row.Name,
		BaseURL:      row.BaseUrl,
		AuthKind:     row.AuthKind,
		Account:      row.Account,
		SecretSealed: row.SecretSealed,
		CreatedBy:    snow.ID(row.CreatedBy),
		CreatedAt:    row.CreatedAt,
		UpdatedAt:    row.UpdatedAt,
	}
}

func projectIssueTrackerToDomain(row sqlcPostgres.ProjectIssueTracker) *domain.ProjectIssueTracker {
	var keys []string
	if row.ProjectKeys != "" {
		keys = strings.Split(row.ProjectKeys, ",")
	}
	return &domain.ProjectIssueTracker{
		ProjectID:         snow.ID(row.ProjectID),
		TrackerID:         snow.ID(row.TrackerID),
		Keys:              keys,
		LinkMergeRequests: row.LinkMergeRequests,
		LinkCommits:       row.LinkCommits,
		CommentOnMerge:    row.CommentOnMerge,
		MergedStatus:      row.MergedStatus,
		OpenedStatus:      row.OpenedStatus,
		IsActive:          row.IsActive,
		SetBy:             snow.ID(row.SetBy),
		UpdatedAt:         row.UpdatedAt,
	}
}
