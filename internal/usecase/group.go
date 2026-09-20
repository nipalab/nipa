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
}

type permissionCacheInvalidator interface {
	InvalidateAll()
}

type Group struct {
	repo     groupRepository
	snowNode snow.Node
	permUc   permissionCacheInvalidator
}

func NewGroup(repo groupRepository, snowNode snow.Node, permUc permissionCacheInvalidator) *Group {
	return &Group{
		repo:     repo,
		snowNode: snowNode,
		permUc:   permUc,
	}
}

func (g *Group) Create(ctx context.Context, orgID snow.ID, name, description string) (*domain.Group, error) {
	if err := requireGroupAdmin(ctx); err != nil {
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

func (g *Group) List(ctx context.Context, orgID snow.ID) ([]*domain.Group, error) {
	if err := requireGroupAdmin(ctx); err != nil {
		return nil, err
	}
	return g.repo.ListByOrg(ctx, orgID)
}

func (g *Group) AddMember(ctx context.Context, orgID, groupID, userID snow.ID) error {
	if err := requireGroupAdmin(ctx); err != nil {
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
	if err := requireGroupAdmin(ctx); err != nil {
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

func requireGroupAdmin(ctx context.Context) error {
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok {
		return domain.NewErrorNoPermission()
	}
	if claim.IsSuperAdmin || claim.IsAdmin {
		return nil
	}
	return domain.NewErrorNoPermission()
}
