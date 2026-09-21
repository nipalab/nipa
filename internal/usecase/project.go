package usecase

import (
	"context"
	"regexp"
	"strings"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

var (
	projectSlugPattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	projectSlugSeparator = regexp.MustCompile(`[^a-z0-9]+`)
)

//go:generate go run go.uber.org/mock/mockgen -source=$GOFILE -destination=project_mock_test.go -package=usecase
type projectRepository interface {
	Create(ctx context.Context, project domain.Project) (*domain.Project, error)
	Get(ctx context.Context, id snow.ID) (*domain.Project, error)
	GetByOrgIDAndSlug(ctx context.Context, orgID snow.ID, slug string) (*domain.Project, error)
	ListByOrgID(ctx context.Context, orgID snow.ID) ([]domain.Project, error)
	Update(ctx context.Context, project domain.Project) (*domain.Project, error)
	Delete(ctx context.Context, id snow.ID) error
}

type projectAccess interface {
	HasProjectAccess(ctx context.Context, projectID snow.ID, permission domain.Permission) bool
	AdminHasProject(ctx context.Context, projectID snow.ID) bool
}

type Project struct {
	repo     projectRepository
	snowNode snow.Node
	perm     projectAccess
	orgs     orgAuthorizer
}

func NewProject(repo projectRepository, snowNode snow.Node, perm projectAccess, orgs orgAuthorizer) *Project {
	return &Project{
		repo:     repo,
		snowNode: snowNode,
		perm:     perm,
		orgs:     orgs,
	}
}

func (p *Project) List(ctx context.Context, orgID snow.ID) ([]*domain.Project, error) {
	projects, err := p.repo.ListByOrgID(ctx, orgID)
	if err != nil {
		return nil, err
	}
	visible := make([]*domain.Project, 0, len(projects))
	for i := range projects {
		if p.perm.HasProjectAccess(ctx, projects[i].ID, domain.PermissionRead) {
			visible = append(visible, &projects[i])
		}
	}
	return visible, nil
}

func (p *Project) Get(ctx context.Context, projectID snow.ID) (*domain.Project, error) {
	if !p.perm.HasProjectAccess(ctx, projectID, domain.PermissionRead) {
		return nil, domain.NewErrorNotFound("project not found")
	}
	return p.repo.Get(ctx, projectID)
}

func (p *Project) Create(ctx context.Context, orgID snow.ID, name, description, slug string) (*domain.Project, error) {
	if err := p.requireOrgAdmin(ctx, orgID); err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, domain.NewErrorUser("project name is required")
	}
	slug = strings.TrimSpace(slug)
	if slug == "" {
		slug = slugifyProject(name)
	}
	if !projectSlugPattern.MatchString(slug) {
		return nil, domain.NewErrorUser("invalid project slug")
	}
	if _, err := p.repo.GetByOrgIDAndSlug(ctx, orgID, slug); err == nil {
		return nil, domain.NewErrorConflict("project slug already in use")
	} else if !domain.IsErrorNotFound(err) {
		return nil, err
	}
	return p.repo.Create(ctx, domain.Project{
		ID:          p.snowNode.Generate(),
		OrgID:       orgID,
		Name:        name,
		Description: strings.TrimSpace(description),
		Slug:        slug,
	})
}

func (p *Project) Update(ctx context.Context, projectID snow.ID, name, description string) (*domain.Project, error) {
	project, err := p.repo.Get(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if err := p.requireProjectAdmin(ctx, project); err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, domain.NewErrorUser("project name is required")
	}
	return p.repo.Update(ctx, domain.Project{
		ID:          projectID,
		Name:        name,
		Description: strings.TrimSpace(description),
	})
}

func (p *Project) Delete(ctx context.Context, projectID snow.ID) error {
	project, err := p.repo.Get(ctx, projectID)
	if err != nil {
		return err
	}
	if err := p.requireOrgAdmin(ctx, project.OrgID); err != nil {
		return err
	}
	return p.repo.Delete(ctx, projectID)
}

func (p *Project) requireOrgAdmin(ctx context.Context, orgID snow.ID) error {
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok {
		return domain.NewErrorNoPermission()
	}
	if claim.IsSuperAdmin || claim.IsAdmin {
		return nil
	}
	owner, err := p.orgs.IsOrgOwner(ctx, orgID)
	if err != nil {
		return err
	}
	if !owner {
		return domain.NewErrorNoPermission()
	}
	return nil
}

func (p *Project) requireProjectAdmin(ctx context.Context, project *domain.Project) error {
	if p.perm.AdminHasProject(ctx, project.ID) {
		return nil
	}
	owner, err := p.orgs.IsOrgOwner(ctx, project.OrgID)
	if err != nil {
		return err
	}
	if owner {
		return nil
	}
	return domain.NewErrorNoPermission()
}

func slugifyProject(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = projectSlugSeparator.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}
