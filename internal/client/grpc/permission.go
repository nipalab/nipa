package grpc

import (
	"context"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
)

func (c *Client) GetMyPermissions(ctx context.Context, org, project string) (*domain.PermissionInfo, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	res, err := client.GetMyPermissions(ctx, &pb.GetMyPermissionsRequest{
		Context: &pb.ProjectContext{Org: org, Project: project},
	})
	if err != nil {
		return nil, toDomainError(err)
	}
	info := &domain.PermissionInfo{
		ProjectPermission: res.GetProjectPermission(),
		Rules:             toPermissionEntries(res.GetRules()),
		Defaults:          toPermissionEntries(res.GetDefaults()),
	}
	return info, nil
}

func (c *Client) CreatePBACRule(ctx context.Context, org, project, userID, groupID, pathPrefix string, permission uint64) (*domain.PBACRuleInfo, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	req := &pb.CreatePBACRuleRequest{
		Context:    &pb.ProjectContext{Org: org, Project: project},
		PathPrefix: pathPrefix,
		Permission: permission,
	}
	if userID != "" {
		req.UserId = &userID
	}
	if groupID != "" {
		req.GroupId = &groupID
	}
	res, err := client.CreatePBACRule(ctx, req)
	if err != nil {
		return nil, toDomainError(err)
	}
	return toPBACRuleInfo(res.GetRule()), nil
}

func (c *Client) ListPBACRules(ctx context.Context, org, project string) ([]*domain.PBACRuleInfo, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	res, err := client.ListPBACRules(ctx, &pb.ListPBACRulesRequest{
		Context: &pb.ProjectContext{Org: org, Project: project},
	})
	if err != nil {
		return nil, toDomainError(err)
	}
	rules := make([]*domain.PBACRuleInfo, 0, len(res.GetRules()))
	for _, rule := range res.GetRules() {
		rules = append(rules, toPBACRuleInfo(rule))
	}
	return rules, nil
}

func (c *Client) DeletePBACRule(ctx context.Context, org, project string, ruleID int64) error {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return err
	}
	_, err = client.DeletePBACRule(ctx, &pb.DeletePBACRuleRequest{
		Context: &pb.ProjectContext{Org: org, Project: project},
		RuleId:  ruleID,
	})
	return toDomainError(err)
}

func (c *Client) ListProjectPathPermissions(ctx context.Context, org, project string) ([]domain.PermissionEntry, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	res, err := client.ListProjectPathPermissions(ctx, &pb.ListProjectPathPermissionsRequest{
		Context: &pb.ProjectContext{Org: org, Project: project},
	})
	if err != nil {
		return nil, toDomainError(err)
	}
	return toPermissionEntries(res.GetPermissions()), nil
}

func (c *Client) SetProjectPathPermission(ctx context.Context, org, project, pathPrefix string, permission uint64) (*domain.PermissionEntry, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	res, err := client.SetProjectPathPermission(ctx, &pb.SetProjectPathPermissionRequest{
		Context:    &pb.ProjectContext{Org: org, Project: project},
		PathPrefix: pathPrefix,
		Permission: permission,
	})
	if err != nil {
		return nil, toDomainError(err)
	}
	return &domain.PermissionEntry{
		PathPrefix: res.GetPermission().GetPathPrefix(),
		Permission: res.GetPermission().GetPermission(),
	}, nil
}

func (c *Client) DeleteProjectPathPermission(ctx context.Context, org, project, pathPrefix string) error {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return err
	}
	_, err = client.DeleteProjectPathPermission(ctx, &pb.DeleteProjectPathPermissionRequest{
		Context:    &pb.ProjectContext{Org: org, Project: project},
		PathPrefix: pathPrefix,
	})
	return toDomainError(err)
}

func (c *Client) CreateGroup(ctx context.Context, org, name, description string) (*domain.GroupInfo, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	res, err := client.CreateGroup(ctx, &pb.CreateGroupRequest{
		Org:         org,
		Name:        name,
		Description: description,
	})
	if err != nil {
		return nil, toDomainError(err)
	}
	return toGroupInfo(res.GetGroup()), nil
}

func (c *Client) ListGroups(ctx context.Context, org string) ([]*domain.GroupInfo, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	res, err := client.ListGroups(ctx, &pb.ListGroupsRequest{Org: org})
	if err != nil {
		return nil, toDomainError(err)
	}
	groups := make([]*domain.GroupInfo, 0, len(res.GetGroups()))
	for _, group := range res.GetGroups() {
		groups = append(groups, toGroupInfo(group))
	}
	return groups, nil
}

func (c *Client) AddGroupMember(ctx context.Context, org, groupID, userID string) error {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return err
	}
	_, err = client.AddGroupMember(ctx, &pb.AddGroupMemberRequest{
		Org:     org,
		GroupId: groupID,
		UserId:  userID,
	})
	return toDomainError(err)
}

func (c *Client) RemoveGroupMember(ctx context.Context, org, groupID, userID string) error {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return err
	}
	_, err = client.RemoveGroupMember(ctx, &pb.RemoveGroupMemberRequest{
		Org:     org,
		GroupId: groupID,
		UserId:  userID,
	})
	return toDomainError(err)
}

func toPermissionEntries(entries []*pb.PermissionEntry) []domain.PermissionEntry {
	out := make([]domain.PermissionEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, domain.PermissionEntry{
			PathPrefix: entry.GetPathPrefix(),
			Permission: entry.GetPermission(),
		})
	}
	return out
}

func toPBACRuleInfo(rule *pb.PBACRuleDetail) *domain.PBACRuleInfo {
	if rule == nil {
		return nil
	}
	return &domain.PBACRuleInfo{
		ID:         rule.GetId(),
		UserID:     rule.GetUserId(),
		GroupID:    rule.GetGroupId(),
		PathPrefix: rule.GetPathPrefix(),
		Permission: rule.GetPermission(),
	}
}

func toGroupInfo(group *pb.GroupDetail) *domain.GroupInfo {
	if group == nil {
		return nil
	}
	return &domain.GroupInfo{
		ID:          group.GetId(),
		OrgID:       group.GetOrgId(),
		Name:        group.GetName(),
		Description: group.GetDescription(),
	}
}
