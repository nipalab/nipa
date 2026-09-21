package usecase

import (
	"context"
	"fmt"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type orgSlugLookup interface {
	GetBySlug(ctx context.Context, slug string) (*domain.Organization, error)
}
type projectSlugLookup interface {
	GetByOrgIDAndSlug(ctx context.Context, orgID snow.ID, slug string) (*domain.Project, error)
}

type Common struct {
	orgRepository     orgSlugLookup
	projectRepository projectSlugLookup
}

func NewCommon(orgRepository orgSlugLookup, projectRepository projectSlugLookup) *Common {
	return &Common{
		orgRepository:     orgRepository,
		projectRepository: projectRepository,
	}
}

func (c *Common) ResolveOrg(ctx context.Context, orgSlug string) (*domain.Organization, error) {
	org, err := c.orgRepository.GetBySlug(ctx, orgSlug)
	if err != nil {
		if domain.IsErrorNotFound(err) {
			return nil, domain.NewErrorNotFound(fmt.Sprintf("organization %q not found", orgSlug))
		}
		return nil, err
	}
	return org, nil
}

func (c *Common) ResolveBySlug(ctx context.Context, orgSlug, projectSlug string) (org *domain.Organization, project *domain.Project, err error) {
	org, err = c.orgRepository.GetBySlug(ctx, orgSlug)
	if err != nil {
		if domain.IsErrorNotFound(err) {
			return nil, nil, domain.NewErrorNotFound(fmt.Sprintf("organization %q not found", orgSlug))
		}
		return nil, nil, err
	}
	project, err = c.projectRepository.GetByOrgIDAndSlug(ctx, org.ID, projectSlug)
	if err != nil {
		if domain.IsErrorNotFound(err) {
			return nil, nil, domain.NewErrorNotFound(fmt.Sprintf("project %q not found", projectSlug))
		}
		return nil, nil, err
	}
	return
}
