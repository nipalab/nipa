package postgres

import (
	"context"
	"database/sql"
	"math"
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

func (r *IssueTrackerRepository) GetByID(ctx context.Context, id snow.ID) (*domain.IssueTracker, error) {
	row, err := r.queries.IssueTrackerGetByID(ctx, id.Int64())
	if err != nil {
		return nil, handleError(err)
	}
	return issueTrackerToDomain(row), nil
}

func (r *IssueTrackerRepository) UpsertIssueLink(ctx context.Context, l domain.IssueLink) error {
	return handleError(r.queries.IssueLinkUpsert(ctx, sqlcPostgres.IssueLinkUpsertParams{
		ID:          l.ID.Int64(),
		ProjectID:   l.ProjectID.Int64(),
		TrackerID:   l.TrackerID.Int64(),
		IssueKey:    l.IssueKey,
		SubjectKind: l.SubjectKind,
		SubjectID:   l.SubjectID.Int64(),
		Relation:    l.Relation,
		CreatedAt:   l.CreatedAt,
	}))
}

func (r *IssueTrackerRepository) ListIssueLinks(ctx context.Context, projectID snow.ID, kind string, subjectID snow.ID) ([]domain.IssueLink, error) {
	rows, err := r.queries.IssueLinkListBySubject(ctx, sqlcPostgres.IssueLinkListBySubjectParams{
		ProjectID: projectID.Int64(), SubjectKind: kind, SubjectID: subjectID.Int64(),
	})
	if err != nil {
		return nil, handleError(err)
	}
	return issueLinksToDomain(rows), nil
}

func (r *IssueTrackerRepository) ListIssueLinksBySubjects(ctx context.Context, projectID snow.ID, kind string, subjectIDs []snow.ID) ([]domain.IssueLink, error) {
	if len(subjectIDs) == 0 {
		return nil, nil
	}
	ids := make([]int64, len(subjectIDs))
	for i, id := range subjectIDs {
		ids[i] = id.Int64()
	}
	rows, err := r.queries.IssueLinkListBySubjects(ctx, sqlcPostgres.IssueLinkListBySubjectsParams{
		ProjectID: projectID.Int64(), SubjectKind: kind, SubjectIds: ids,
	})
	if err != nil {
		return nil, handleError(err)
	}
	return issueLinksToDomain(rows), nil
}

func (r *IssueTrackerRepository) DeleteIssueLinks(ctx context.Context, projectID snow.ID, kind string, subjectID snow.ID, keys []string) (int64, error) {
	if len(keys) == 0 {
		return 0, nil
	}
	n, err := r.queries.IssueLinkDeleteKeys(ctx, sqlcPostgres.IssueLinkDeleteKeysParams{
		ProjectID: projectID.Int64(), SubjectKind: kind, SubjectID: subjectID.Int64(), IssueKeys: keys,
	})
	return n, handleError(err)
}

// ListIssueSubjects pages the links of one issue, newest first, below the
// before id (zero = from the newest).
func (r *IssueTrackerRepository) ListIssueSubjects(ctx context.Context, projectID snow.ID, key string, before snow.ID, limit int) ([]domain.IssueLinkSubject, error) {
	if before == 0 {
		before = snow.ID(math.MaxInt64)
	}
	rows, err := r.queries.IssueLinkListSubjects(ctx, sqlcPostgres.IssueLinkListSubjectsParams{
		ProjectID: projectID.Int64(), IssueKey: key, BeforeID: before.Int64(), RowLimit: int64(limit),
	})
	if err != nil {
		return nil, handleError(err)
	}
	out := make([]domain.IssueLinkSubject, 0, len(rows))
	for _, row := range rows {
		out = append(out, issueLinkSubject(domain.IssueLink{
			ID: snow.ID(row.ID), ProjectID: projectID, IssueKey: key, SubjectKind: row.SubjectKind,
			SubjectID: snow.ID(row.SubjectID), Relation: row.Relation, CreatedAt: row.CreatedAt,
		}, row.CommitMessage, row.CommitCreatedAt, row.MrNumber, row.MrTitle, row.MrStatus, row.BranchName))
	}
	return out, nil
}

func issueLinkSubject(link domain.IssueLink, message sql.NullString, committed sql.NullTime, number sql.NullInt64, title, status, branch sql.NullString) domain.IssueLinkSubject {
	s := domain.IssueLinkSubject{IssueLink: link}
	switch link.SubjectKind {
	case domain.IssueSubjectCommit:
		s.Missing = !message.Valid
		s.CommitMessage, s.CommitCreatedAt = message.String, committed.Time
	case domain.IssueSubjectMergeRequest:
		s.Missing = !number.Valid
		s.MRNumber, s.MRTitle, s.MRStatus = number.Int64, title.String, status.String
	case domain.IssueSubjectBranch:
		s.Missing = !branch.Valid
		s.BranchName = branch.String
	}
	return s
}

func issueLinksToDomain(rows []sqlcPostgres.IssueLink) []domain.IssueLink {
	out := make([]domain.IssueLink, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.IssueLink{
			ID:          snow.ID(row.ID),
			ProjectID:   snow.ID(row.ProjectID),
			TrackerID:   snow.ID(row.TrackerID),
			IssueKey:    row.IssueKey,
			SubjectKind: row.SubjectKind,
			SubjectID:   snow.ID(row.SubjectID),
			Relation:    row.Relation,
			CreatedAt:   row.CreatedAt,
		})
	}
	return out
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
