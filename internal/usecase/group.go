package usecase

import (
	"context"
	"strings"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

//go:generate go run go.uber.org/mock/mockgen -source=$GOFILE -destination=group_mock_test.go -package=usecase
type groupRepository interface {
	Create(ctx context.Context, group domain.Group) (*domain.Group, error)
	GetByID(ctx context.Context, id snow.ID) (*domain.Group, error)
	ListByOrg(ctx context.Context, orgID snow.ID) ([]*domain.Group, error)
	AddMember(ctx context.Context, groupID, userID snow.ID) error
	RemoveMember(ctx context.Context, groupID, userID snow.ID) error
	ListMemberIDs(ctx context.Context, groupID snow.ID) ([]snow.ID, error)
}

type permissionCacheInvalidator interface {
	InvalidateAll()
}

type Group struct {
	repo     groupRepository
	snowNode snow.Node
	permUc   permissionCacheInvalidator
	orgs     orgAuthorizer
}

func NewGroup(repo groupRepository, snowNode snow.Node, permUc permissionCacheInvalidator, orgs orgAuthorizer) *Group {
	return &Group{
		repo:     repo,
		snowNode: snowNode,
		permUc:   permUc,
		orgs:     orgs,
	}
}

func (g *Group) Create(ctx context.Context, orgID snow.ID, name, description string) (*domain.Group, error) {
	if err := g.requireGroupAdmin(ctx, orgID); err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, domain.NewErrorUser("group name is required")
	}
	return g.repo.Create(ctx, domain.Group{
		ID:          g.snowNode.Generate(),
		OrgID:       orgID,
		Name:        name,
		Description: strings.TrimSpace(description),
	})
}

func (g *Group) Get(ctx context.Context, orgID, groupID snow.ID) (*domain.Group, error) {
	if err := g.requireGroupAdmin(ctx, orgID); err != nil {
		return nil, err
	}
	return g.groupInOrg(ctx, orgID, groupID)
}

func (g *Group) List(ctx context.Context, orgID snow.ID) ([]*domain.Group, error) {
	if err := g.requireGroupAdmin(ctx, orgID); err != nil {
		return nil, err
	}
	return g.repo.ListByOrg(ctx, orgID)
}

func (g *Group) AddMember(ctx context.Context, orgID, groupID, userID snow.ID) error {
	if err := g.requireGroupAdmin(ctx, orgID); err != nil {
		return err
	}
	if _, err := g.groupInOrg(ctx, orgID, groupID); err != nil {
		return err
	}
	if err := g.repo.AddMember(ctx, groupID, userID); err != nil {
		return err
	}
	g.permUc.InvalidateAll()
	return nil
}

func (g *Group) RemoveMember(ctx context.Context, orgID, groupID, userID snow.ID) error {
	if err := g.requireGroupAdmin(ctx, orgID); err != nil {
		return err
	}
	if _, err := g.groupInOrg(ctx, orgID, groupID); err != nil {
		return err
	}
	if err := g.repo.RemoveMember(ctx, groupID, userID); err != nil {
		return err
	}
	g.permUc.InvalidateAll()
	return nil
}

func (g *Group) groupInOrg(ctx context.Context, orgID, groupID snow.ID) (*domain.Group, error) {
	group, err := g.repo.GetByID(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if group.OrgID != orgID {
		return nil, domain.NewErrorNotFound("group not found")
	}
	return group, nil
}

func (g *Group) Members(ctx context.Context, orgID, groupID snow.ID) ([]snow.ID, error) {
	if err := g.requireGroupAdmin(ctx, orgID); err != nil {
		return nil, err
	}
	if _, err := g.groupInOrg(ctx, orgID, groupID); err != nil {
		return nil, err
	}
	return g.repo.ListMemberIDs(ctx, groupID)
}

func (g *Group) requireGroupAdmin(ctx context.Context, orgID snow.ID) error {
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok {
		return domain.NewErrorNoPermission()
	}
	if claim.IsSuperAdmin || claim.IsAdmin {
		return nil
	}
	owner, err := g.orgs.IsOrgOwner(ctx, orgID)
	if err != nil {
		return err
	}
	if !owner {
		return domain.NewErrorNoPermission()
	}
	return nil
}
