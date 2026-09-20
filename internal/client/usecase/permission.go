package usecase

import (
	"context"

	"github.com/nipalab/nipa/internal/client/domain"
)

type permissionClient interface {
	Connect(ctx context.Context, host string) error
	GetMyPermissions(ctx context.Context, org, project string) (*domain.PermissionInfo, error)
	CreatePBACRule(ctx context.Context, org, project, userID, groupID, pathPrefix string, permission uint64) (*domain.PBACRuleInfo, error)
	ListPBACRules(ctx context.Context, org, project string) ([]*domain.PBACRuleInfo, error)
	DeletePBACRule(ctx context.Context, org, project string, ruleID int64) error
	ListProjectPathPermissions(ctx context.Context, org, project string) ([]domain.PermissionEntry, error)
	SetProjectPathPermission(ctx context.Context, org, project, pathPrefix string, permission uint64) (*domain.PermissionEntry, error)
	DeleteProjectPathPermission(ctx context.Context, org, project, pathPrefix string) error
	CreateGroup(ctx context.Context, org, name, description string) (*domain.GroupInfo, error)
	ListGroups(ctx context.Context, org string) ([]*domain.GroupInfo, error)
	AddGroupMember(ctx context.Context, org, groupID, userID string) error
	RemoveGroupMember(ctx context.Context, org, groupID, userID string) error
}

type Permission struct {
	auth   *Auth
	client permissionClient
}

func NewPermission(auth *Auth, client permissionClient) *Permission {
	return &Permission{
		auth:   auth,
		client: client,
	}
}

func (p *Permission) ready(ctx context.Context, host string) error {
	if err := p.client.Connect(ctx, host); err != nil {
		return err
	}
	return p.auth.MakeSureLoggedIn(ctx, host)
}

func (p *Permission) My(ctx context.Context, host, org, project string) (*domain.PermissionInfo, error) {
	if err := p.ready(ctx, host); err != nil {
		return nil, err
	}
	return p.client.GetMyPermissions(ctx, org, project)
}

func (p *Permission) Rules(ctx context.Context, host, org, project string) ([]*domain.PBACRuleInfo, error) {
	if err := p.ready(ctx, host); err != nil {
		return nil, err
	}
	return p.client.ListPBACRules(ctx, org, project)
}

func (p *Permission) Grant(ctx context.Context, host, org, project, userID, groupID, pathPrefix string, permission uint64) (*domain.PBACRuleInfo, error) {
	if err := p.ready(ctx, host); err != nil {
		return nil, err
	}
	return p.client.CreatePBACRule(ctx, org, project, userID, groupID, pathPrefix, permission)
}

func (p *Permission) Revoke(ctx context.Context, host, org, project string, ruleID int64) error {
	if err := p.ready(ctx, host); err != nil {
		return err
	}
	return p.client.DeletePBACRule(ctx, org, project, ruleID)
}

func (p *Permission) PathPermissions(ctx context.Context, host, org, project string) ([]domain.PermissionEntry, error) {
	if err := p.ready(ctx, host); err != nil {
		return nil, err
	}
	return p.client.ListProjectPathPermissions(ctx, org, project)
}

func (p *Permission) SetPath(ctx context.Context, host, org, project, pathPrefix string, permission uint64) (*domain.PermissionEntry, error) {
	if err := p.ready(ctx, host); err != nil {
		return nil, err
	}
	return p.client.SetProjectPathPermission(ctx, org, project, pathPrefix, permission)
}

func (p *Permission) RemovePath(ctx context.Context, host, org, project, pathPrefix string) error {
	if err := p.ready(ctx, host); err != nil {
		return err
	}
	return p.client.DeleteProjectPathPermission(ctx, org, project, pathPrefix)
}

func (p *Permission) CreateGroup(ctx context.Context, host, org, name, description string) (*domain.GroupInfo, error) {
	if err := p.ready(ctx, host); err != nil {
		return nil, err
	}
	return p.client.CreateGroup(ctx, org, name, description)
}

func (p *Permission) Groups(ctx context.Context, host, org string) ([]*domain.GroupInfo, error) {
	if err := p.ready(ctx, host); err != nil {
		return nil, err
	}
	return p.client.ListGroups(ctx, org)
}

func (p *Permission) AddGroupMember(ctx context.Context, host, org, groupID, userID string) error {
	if err := p.ready(ctx, host); err != nil {
		return err
	}
	return p.client.AddGroupMember(ctx, org, groupID, userID)
}

func (p *Permission) RemoveGroupMember(ctx context.Context, host, org, groupID, userID string) error {
	if err := p.ready(ctx, host); err != nil {
		return err
	}
	return p.client.RemoveGroupMember(ctx, org, groupID, userID)
}
