package usecase

import (
	"context"
	"strings"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

//go:generate go run go.uber.org/mock/mockgen -source=$GOFILE -destination=org_mock_test.go -package=usecase
type orgMemberRepository interface {
	GetBySlug(ctx context.Context, slug string) (*domain.Organization, error)
	CreateWithOwner(ctx context.Context, org domain.Organization, ownerID snow.ID) (*domain.Organization, error)
	ListForUser(ctx context.Context, userID snow.ID) ([]*domain.OrgMembership, error)
	ListMembers(ctx context.Context, orgID snow.ID) ([]*domain.OrgMember, error)
	MemberRole(ctx context.Context, orgID, userID snow.ID) (string, error)
	UpsertMember(ctx context.Context, orgID, userID snow.ID, role string) error
	RemoveMember(ctx context.Context, orgID, userID snow.ID) error
	CountMembersByRole(ctx context.Context, orgID snow.ID, role string) (int, error)
}

// orgAuthorizer answers org-scoped admin questions for other usecases.
type orgAuthorizer interface {
	IsOrgOwner(ctx context.Context, orgID snow.ID) (bool, error)
}

type Org struct {
	repo     orgMemberRepository
	snowNode snow.Node
}

func NewOrg(repo orgMemberRepository, snowNode snow.Node) *Org {
	return &Org{repo: repo, snowNode: snowNode}
}

func (o *Org) Create(ctx context.Context, name, slug string) (*domain.Organization, error) {
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok {
		return nil, domain.NewErrorUnauthorized("authentication required")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, domain.NewErrorUser("organization name is required")
	}
	slug = strings.TrimSpace(slug)
	if slug == "" {
		slug = slugify(name)
	}
	if !slugPattern.MatchString(slug) {
		return nil, domain.NewErrorUser("invalid organization slug")
	}
	if _, err := o.repo.GetBySlug(ctx, slug); err == nil {
		return nil, domain.NewErrorConflict("organization slug already in use")
	} else if !domain.IsErrorNotFound(err) {
		return nil, err
	}
	org, err := o.repo.CreateWithOwner(ctx, domain.Organization{
		ID:   o.snowNode.Generate(),
		Name: name,
		Slug: slug,
	}, claim.UserID)
	if err != nil {
		return nil, err
	}
	return org, nil
}

func (o *Org) ListForUser(ctx context.Context, userID snow.ID) ([]*domain.OrgMembership, error) {
	return o.repo.ListForUser(ctx, userID)
}

func (o *Org) ListMembers(ctx context.Context, orgID snow.ID) ([]*domain.OrgMember, error) {
	if err := o.requireOrgOwner(ctx, orgID); err != nil {
		return nil, err
	}
	return o.repo.ListMembers(ctx, orgID)
}

func (o *Org) AddMember(ctx context.Context, orgID, userID snow.ID, role string) error {
	if err := o.requireOrgOwner(ctx, orgID); err != nil {
		return err
	}
	if !domain.IsValidOrgRole(role) {
		return domain.NewErrorUser("invalid org role")
	}
	if role == domain.OrgRoleMember {
		if err := o.ensureNotLastOwner(ctx, orgID, userID); err != nil {
			return err
		}
	}
	return o.repo.UpsertMember(ctx, orgID, userID, role)
}

func (o *Org) UpdateMemberRole(ctx context.Context, orgID, userID snow.ID, role string) error {
	if err := o.requireOrgOwner(ctx, orgID); err != nil {
		return err
	}
	if !domain.IsValidOrgRole(role) {
		return domain.NewErrorUser("invalid org role")
	}
	if role == domain.OrgRoleMember {
		if err := o.ensureNotLastOwner(ctx, orgID, userID); err != nil {
			return err
		}
	}
	return o.repo.UpsertMember(ctx, orgID, userID, role)
}

func (o *Org) RemoveMember(ctx context.Context, orgID, userID snow.ID) error {
	if err := o.requireOrgOwner(ctx, orgID); err != nil {
		return err
	}
	if err := o.ensureNotLastOwner(ctx, orgID, userID); err != nil {
		return err
	}
	return o.repo.RemoveMember(ctx, orgID, userID)
}

func (o *Org) IsOrgOwner(ctx context.Context, orgID snow.ID) (bool, error) {
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok {
		return false, nil
	}
	if claim.IsSuperAdmin || claim.IsAdmin {
		return true, nil
	}
	role, err := o.repo.MemberRole(ctx, orgID, claim.UserID)
	if domain.IsErrorNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return role == domain.OrgRoleOwner, nil
}

func (o *Org) requireOrgOwner(ctx context.Context, orgID snow.ID) error {
	owner, err := o.IsOrgOwner(ctx, orgID)
	if err != nil {
		return err
	}
	if !owner {
		return domain.NewErrorNoPermission()
	}
	return nil
}

func (o *Org) ensureNotLastOwner(ctx context.Context, orgID, userID snow.ID) error {
	role, err := o.repo.MemberRole(ctx, orgID, userID)
	if domain.IsErrorNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if role != domain.OrgRoleOwner {
		return nil
	}
	owners, err := o.repo.CountMembersByRole(ctx, orgID, domain.OrgRoleOwner)
	if err != nil {
		return err
	}
	if owners <= 1 {
		return domain.NewErrorConflict("organization must keep at least one owner")
	}
	return nil
}
