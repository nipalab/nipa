package usecase

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

var (
	slugPattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	slugSeparator = regexp.MustCompile(`[^a-z0-9]+`)
)

const defaultBranchName = "main"

//go:generate go run go.uber.org/mock/mockgen -source=$GOFILE -destination=project_mock_test.go -package=usecase
type projectRepository interface {
	Create(ctx context.Context, project domain.Project) (*domain.Project, error)
	CreateWithDefaultBranch(ctx context.Context, project domain.Project, defaultBranch domain.Branch) (*domain.Project, error)
	Get(ctx context.Context, id snow.ID) (*domain.Project, error)
	GetByOrgIDAndSlug(ctx context.Context, orgID snow.ID, slug string) (*domain.Project, error)
	ListByOrgID(ctx context.Context, orgID snow.ID) ([]domain.Project, error)
	Update(ctx context.Context, project domain.Project) (*domain.Project, error)
	Delete(ctx context.Context, id snow.ID) error
	GetDeleted(ctx context.Context, orgID, id snow.ID) (*domain.Project, error)
	ListDeletedByOrgID(ctx context.Context, orgID snow.ID) ([]domain.Project, error)
	Restore(ctx context.Context, id snow.ID, deletedSince *time.Time) (bool, error)
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
	audit    AuditRecorder
	storage  ProjectStorageAssigner
	ledger   projectStorageLedger
	window   time.Duration
	now      func() time.Time
}

// projectStorageLedger releases a deleted project's bytes and charges them
// back on restore (the StorageLedger slice Project needs).
type projectStorageLedger interface {
	ReleaseProject(ctx context.Context, projectID snow.ID) (int64, error)
	RestoreProject(ctx context.Context, projectID snow.ID) (int64, error)
}

// WithStorageLedger releases a project's attributed storage when it is
// deleted and charges it back on restore. Without one (the free default)
// neither touches usage.
func (p *Project) WithStorageLedger(ledger projectStorageLedger) *Project {
	p.ledger = ledger
	return p
}

// WithRestoreWindow limits restoring to projects deleted within window, the
// retention after which the purge removes them for good. Zero (the default)
// means a deleted project stays restorable.
func (p *Project) WithRestoreWindow(window time.Duration) *Project {
	p.window = window
	return p
}

// RestoreWindow is the configured retention of deleted projects; zero means
// unlimited.
func (p *Project) RestoreWindow() time.Duration {
	return p.window
}

// ProjectStorageAssigner picks where a new project's chunk content lives. It
// runs inside the project's creation transaction; requested is the storage
// server the caller asked for, nil when none.
type ProjectStorageAssigner interface {
	Assign(ctx context.Context, orgID, projectID snow.ID, requested *snow.ID) error
}

// WithStorageAssigner attaches the storage assignment step of Create.
func (p *Project) WithStorageAssigner(assigner ProjectStorageAssigner) *Project {
	p.storage = assigner
	return p
}

// WithAudit records project creation, edits and deletion.
func (p *Project) WithAudit(rec AuditRecorder) *Project {
	p.audit = rec
	return p
}

func NewProject(repo projectRepository, snowNode snow.Node, perm projectAccess, orgs orgAuthorizer) *Project {
	return &Project{
		repo:     repo,
		snowNode: snowNode,
		perm:     perm,
		orgs:     orgs,
		now:      time.Now,
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

func (p *Project) Create(ctx context.Context, orgID snow.ID, name, description, slug string, storageServerID *snow.ID) (*domain.Project, error) {
	if err := p.requireOrgAdmin(ctx, orgID); err != nil {
		return nil, err
	}
	if storageServerID != nil && p.storage == nil {
		return nil, domain.NewErrorUser("storage servers are not available")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, domain.NewErrorUser("project name is required")
	}
	slug = strings.TrimSpace(slug)
	if slug == "" {
		slug = slugify(name)
	}
	if !slugPattern.MatchString(slug) {
		return nil, domain.NewErrorUser("invalid project slug")
	}
	if isReservedProjectSlug(slug) {
		return nil, domain.NewErrorUser("project slug is reserved")
	}
	if _, err := p.repo.GetByOrgIDAndSlug(ctx, orgID, slug); err == nil {
		return nil, domain.NewErrorConflict("project slug already in use")
	} else if !domain.IsErrorNotFound(err) {
		return nil, err
	}
	projectID := p.snowNode.Generate()
	var created *domain.Project
	err := auditedWrite(ctx, p.audit, func(ctx context.Context) error {
		var err error
		created, err = p.repo.CreateWithDefaultBranch(ctx, domain.Project{
			ID:          projectID,
			OrgID:       orgID,
			Name:        name,
			Description: strings.TrimSpace(description),
			Slug:        slug,
		}, domain.Branch{
			ID:        p.snowNode.Generate(),
			ProjectID: projectID,
			Name:      defaultBranchName,
			IsDefault: true,
		})
		if err != nil || p.storage == nil {
			return err
		}
		return p.storage.Assign(ctx, orgID, projectID, storageServerID)
	}, func() *domain.AuditEntry {
		return projectEntityAudit(domain.AuditProjectCreated, created, map[string]any{
			"name":        created.Name,
			"slug":        created.Slug,
			"description": created.Description,
		})
	})
	if err != nil {
		return nil, err
	}
	return created, nil
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
	var updated *domain.Project
	err = auditedWrite(ctx, p.audit, func(ctx context.Context) error {
		var err error
		updated, err = p.repo.Update(ctx, domain.Project{
			ID:          projectID,
			Name:        name,
			Description: strings.TrimSpace(description),
		})
		return err
	}, func() *domain.AuditEntry {
		if project.Name == updated.Name && project.Description == updated.Description {
			return nil
		}
		return projectEntityAudit(domain.AuditProjectUpdated, project, map[string]any{
			"old": map[string]any{"name": project.Name, "description": project.Description},
			"new": map[string]any{"name": updated.Name, "description": updated.Description},
		})
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

func (p *Project) Delete(ctx context.Context, projectID snow.ID) error {
	project, err := p.repo.Get(ctx, projectID)
	if err != nil {
		return err
	}
	if err := p.requireOrgAdmin(ctx, project.OrgID); err != nil {
		return err
	}
	var released int64
	return auditedWrite(ctx, p.audit, func(ctx context.Context) error {
		if err := p.repo.Delete(ctx, projectID); err != nil {
			return err
		}
		if p.ledger == nil {
			return nil
		}
		var err error
		released, err = p.ledger.ReleaseProject(ctx, projectID)
		return err
	}, func() *domain.AuditEntry {
		metadata := map[string]any{
			"name": project.Name,
			"slug": project.Slug,
		}
		if p.ledger != nil {
			metadata["released_bytes"] = released
		}
		return projectEntityAudit(domain.AuditProjectDeleted, project, metadata)
	})
}

// ListDeleted lists the organization's deleted projects that can still be
// restored (org owner or global admin).
func (p *Project) ListDeleted(ctx context.Context, orgID snow.ID) ([]*domain.Project, error) {
	if err := p.requireOrgAdmin(ctx, orgID); err != nil {
		return nil, err
	}
	projects, err := p.repo.ListDeletedByOrgID(ctx, orgID)
	if err != nil {
		return nil, err
	}
	since := p.deletedSince()
	restorable := make([]*domain.Project, 0, len(projects))
	for i := range projects {
		if since != nil && (projects[i].DeletedAt == nil || projects[i].DeletedAt.Before(*since)) {
			continue
		}
		restorable = append(restorable, &projects[i])
	}
	return restorable, nil
}

// Restore undeletes a project of the organization deleted within the restore
// window (org owner or global admin) and charges its history back to the
// owner's storage quota; a restore that does not fit fails with the quota
// error and changes nothing.
func (p *Project) Restore(ctx context.Context, orgID, projectID snow.ID) (*domain.Project, error) {
	if err := p.requireOrgAdmin(ctx, orgID); err != nil {
		return nil, err
	}
	project, err := p.repo.GetDeleted(ctx, orgID, projectID)
	if err != nil {
		return nil, err
	}
	var charged int64
	err = auditedWrite(ctx, p.audit, func(ctx context.Context) error {
		restored, err := p.repo.Restore(ctx, projectID, p.deletedSince())
		if err != nil {
			return err
		}
		if !restored {
			return domain.NewErrorNotFound("project is past its restore window")
		}
		if p.ledger == nil {
			return nil
		}
		charged, err = p.ledger.RestoreProject(ctx, projectID)
		return err
	}, func() *domain.AuditEntry {
		metadata := map[string]any{
			"name": project.Name,
			"slug": project.Slug,
		}
		if p.ledger != nil {
			metadata["charged_bytes"] = charged
		}
		return projectEntityAudit(domain.AuditProjectRestored, project, metadata)
	})
	if err != nil {
		return nil, err
	}
	return p.repo.Get(ctx, projectID)
}

// deletedSince is the oldest deletion time still inside the restore window;
// nil when the window is unlimited.
func (p *Project) deletedSince() *time.Time {
	if p.window <= 0 {
		return nil
	}
	since := p.now().Add(-p.window)
	return &since
}

func projectEntityAudit(action string, project *domain.Project, metadata map[string]any) *domain.AuditEntry {
	entry := projectAudit(action, project.ID, domain.AuditTargetProject, project.ID.Base36(), metadata)
	entry.OrgID = domain.IDPtr(project.OrgID)
	return entry
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

func slugify(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = slugSeparator.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}
