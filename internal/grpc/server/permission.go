package server

import (
	"context"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/snow"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (n *nipaServer) GetMyPermissions(ctx context.Context, req *pb.GetMyPermissionsRequest) (*pb.GetMyPermissionsResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.GetContext().GetOrg(), req.GetContext().GetProject())
	if err != nil {
		return nil, handleError(err)
	}

	mask, rules, defaults, err := n.uc.Permission().MyPermissions(ctx, project.ID)
	if err != nil {
		return nil, handleError(err)
	}

	resp := &pb.GetMyPermissionsResponse{ProjectPermission: uint64(mask)}
	for _, rule := range rules {
		resp.Rules = append(resp.Rules, &pb.PermissionEntry{
			PathPrefix: rule.PathPrefix,
			Permission: uint64(rule.Permission),
		})
	}
	for _, def := range defaults {
		resp.Defaults = append(resp.Defaults, &pb.PermissionEntry{
			PathPrefix: def.PathPrefix,
			Permission: uint64(def.Permission),
		})
	}
	return resp, nil
}

func (n *nipaServer) CreatePBACRule(ctx context.Context, req *pb.CreatePBACRuleRequest) (*pb.CreatePBACRuleResponse, error) {
	org, project, err := n.uc.Common().ResolveBySlug(ctx, req.GetContext().GetOrg(), req.GetContext().GetProject())
	if err != nil {
		return nil, handleError(err)
	}

	rule := domain.PBACRule{
		OrgID:      org.ID,
		ProjectID:  &project.ID,
		PathPrefix: req.GetPathPrefix(),
		Permission: domain.Permission(req.GetPermission()),
	}
	if raw := req.GetUserId(); raw != "" {
		id, err := snow.ParseBase36(raw)
		if err != nil {
			return nil, handleError(domain.NewErrorUser("invalid user id"))
		}
		rule.UserID = &id
	}
	if raw := req.GetGroupId(); raw != "" {
		id, err := snow.ParseBase36(raw)
		if err != nil {
			return nil, handleError(domain.NewErrorUser("invalid group id"))
		}
		rule.GroupID = &id
	}

	created, err := n.uc.Permission().CreateRule(ctx, rule)
	if err != nil {
		return nil, handleError(err)
	}
	return &pb.CreatePBACRuleResponse{Rule: domainPBACRuleToPB(created)}, nil
}

func (n *nipaServer) ListPBACRules(ctx context.Context, req *pb.ListPBACRulesRequest) (*pb.ListPBACRulesResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.GetContext().GetOrg(), req.GetContext().GetProject())
	if err != nil {
		return nil, handleError(err)
	}

	rules, err := n.uc.Permission().ListRules(ctx, project.ID)
	if err != nil {
		return nil, handleError(err)
	}
	resp := &pb.ListPBACRulesResponse{Rules: make([]*pb.PBACRuleDetail, 0, len(rules))}
	for _, rule := range rules {
		resp.Rules = append(resp.Rules, domainPBACRuleToPB(rule))
	}
	return resp, nil
}

func (n *nipaServer) DeletePBACRule(ctx context.Context, req *pb.DeletePBACRuleRequest) (*pb.DeletePBACRuleResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.GetContext().GetOrg(), req.GetContext().GetProject())
	if err != nil {
		return nil, handleError(err)
	}
	if err := n.uc.Permission().DeleteRule(ctx, project.ID, req.GetRuleId()); err != nil {
		return nil, handleError(err)
	}
	return &pb.DeletePBACRuleResponse{}, nil
}

func (n *nipaServer) ListProjectPathPermissions(ctx context.Context, req *pb.ListProjectPathPermissionsRequest) (*pb.ListProjectPathPermissionsResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.GetContext().GetOrg(), req.GetContext().GetProject())
	if err != nil {
		return nil, handleError(err)
	}

	perms, err := n.uc.Permission().ListPathPermissions(ctx, project.ID)
	if err != nil {
		return nil, handleError(err)
	}
	resp := &pb.ListProjectPathPermissionsResponse{Permissions: make([]*pb.PermissionEntry, 0, len(perms))}
	for _, perm := range perms {
		resp.Permissions = append(resp.Permissions, &pb.PermissionEntry{
			PathPrefix: perm.PathPrefix,
			Permission: uint64(perm.Permission),
		})
	}
	return resp, nil
}

func (n *nipaServer) SetProjectPathPermission(ctx context.Context, req *pb.SetProjectPathPermissionRequest) (*pb.SetProjectPathPermissionResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.GetContext().GetOrg(), req.GetContext().GetProject())
	if err != nil {
		return nil, handleError(err)
	}

	perm, err := n.uc.Permission().SetPathPermission(ctx, project.ID, req.GetPathPrefix(), domain.Permission(req.GetPermission()))
	if err != nil {
		return nil, handleError(err)
	}
	return &pb.SetProjectPathPermissionResponse{Permission: &pb.PermissionEntry{
		PathPrefix: perm.PathPrefix,
		Permission: uint64(perm.Permission),
	}}, nil
}

func (n *nipaServer) DeleteProjectPathPermission(ctx context.Context, req *pb.DeleteProjectPathPermissionRequest) (*pb.DeleteProjectPathPermissionResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.GetContext().GetOrg(), req.GetContext().GetProject())
	if err != nil {
		return nil, handleError(err)
	}
	if err := n.uc.Permission().DeletePathPermission(ctx, project.ID, req.GetPathPrefix()); err != nil {
		return nil, handleError(err)
	}
	return &pb.DeleteProjectPathPermissionResponse{}, nil
}

func domainPBACRuleToPB(rule *domain.PBACRule) *pb.PBACRuleDetail {
	detail := &pb.PBACRuleDetail{
		Id:         rule.ID,
		OrgId:      rule.OrgID.Base36(),
		PathPrefix: rule.PathPrefix,
		Permission: uint64(rule.Permission),
		CreatedAt:  timestamppb.New(rule.CreatedAt),
	}
	if rule.UserID != nil {
		value := rule.UserID.Base36()
		detail.UserId = &value
	}
	if rule.GroupID != nil {
		value := rule.GroupID.Base36()
		detail.GroupId = &value
	}
	if rule.ProjectID != nil {
		value := rule.ProjectID.Base36()
		detail.ProjectId = &value
	}
	return detail
}
