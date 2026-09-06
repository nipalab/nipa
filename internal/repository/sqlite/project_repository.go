package sqlite

import (
	"context"
	"database/sql"
	"regexp"
	"strings"

	"github.com/nipalab/nipa/internal/domain"
	sqlcSqlite "github.com/nipalab/nipa/internal/repository/sqlc/sqlite"
	"github.com/nipalab/nipa/internal/snow"
)

var nonAlphaNum = regexp.MustCompile(`[^a-z0-9]+`)

type ProjectRepository struct {
	queries *sqlcSqlite.Queries
}

func NewProjectRepository(db *sql.DB) *ProjectRepository {
	return &ProjectRepository{queries: sqlcSqlite.New(db)}
}

func (r *ProjectRepository) Create(ctx context.Context, project domain.Project) (*domain.Project, error) {
	slug := project.Slug
	if slug == "" {
		slug = slugify(project.Name)
	}
	row, err := r.queries.CreateProject(ctx, sqlcSqlite.CreateProjectParams{
		ID:          project.ID.Int64(),
		OrgID:       project.OrgID.Int64(),
		Slug:        slug,
		Name:        project.Name,
		Description: project.Description,
	})
	if err != nil {
		return nil, handleError(err)
	}
	return toDomainProject(row), nil
}

func (r *ProjectRepository) Get(ctx context.Context, id snow.ID) (*domain.Project, error) {
	row, err := r.queries.GetProject(ctx, id.Int64())
	if err != nil {
		return nil, handleError(err)
	}
	return toDomainProject(row), nil
}

func (r *ProjectRepository) GetByOrgIDAndSlug(ctx context.Context, orgID snow.ID, slug string) (*domain.Project, error) {
	row, err := r.queries.GetProjectByOrgIDAndSlug(ctx, sqlcSqlite.GetProjectByOrgIDAndSlugParams{
		OrgID: orgID.Int64(),
		Slug:  slug,
	})
	if err != nil {
		return nil, handleError(err)
	}
	return toDomainProject(row), nil
}

func (r *ProjectRepository) ListByOrgID(ctx context.Context, orgID snow.ID) ([]domain.Project, error) {
	rows, err := r.queries.ListProjectsByOrgId(ctx, orgID.Int64())
	if err != nil {
		return nil, handleError(err)
	}
	projects := make([]domain.Project, 0, len(rows))
	for _, row := range rows {
		projects = append(projects, *toDomainProject(row))
	}
	return projects, nil
}

func (r *ProjectRepository) Delete(ctx context.Context, id snow.ID) error {
	return r.queries.DeleteProject(ctx, id.Int64())
}

func toDomainProject(row sqlcSqlite.Project) *domain.Project {
	return &domain.Project{
		ID:          snow.ID(row.ID),
		OrgID:       snow.ID(row.OrgID),
		Slug:        row.Slug,
		Name:        row.Name,
		Description: row.Description,
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
		DeletedAt:   nullTimePtr(row.DeletedAt),
	}
}

func slugify(s string) string {
	s = strings.ToLower(s)
	s = nonAlphaNum.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}
