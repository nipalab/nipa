package server

import (
	"context"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/snow"
)

func (n *nipaServer) CreateGroup(ctx context.Context, req *pb.CreateGroupRequest) (*pb.CreateGroupResponse, error) {
	org, err := n.uc.Common().ResolveOrg(ctx, req.GetOrg())
	if err != nil {
		return nil, handleError(err)
	}

	group, err := n.uc.Group().Create(ctx, org.ID, req.GetName(), req.GetDescription())
	if err != nil {
		return nil, handleError(err)
	}
	return &pb.CreateGroupResponse{Group: domainGroupToPB(group)}, nil
}

func (n *nipaServer) ListGroups(ctx context.Context, req *pb.ListGroupsRequest) (*pb.ListGroupsResponse, error) {
	org, err := n.uc.Common().ResolveOrg(ctx, req.GetOrg())
	if err != nil {
		return nil, handleError(err)
	}

	groups, err := n.uc.Group().List(ctx, org.ID)
	if err != nil {
		return nil, handleError(err)
	}
	resp := &pb.ListGroupsResponse{Groups: make([]*pb.GroupDetail, 0, len(groups))}
	for _, group := range groups {
		resp.Groups = append(resp.Groups, domainGroupToPB(group))
	}
	return resp, nil
}

func (n *nipaServer) AddGroupMember(ctx context.Context, req *pb.AddGroupMemberRequest) (*pb.AddGroupMemberResponse, error) {
	org, groupID, userID, err := n.resolveGroupMember(ctx, req.GetOrg(), req.GetGroupId(), req.GetUserId())
	if err != nil {
		return nil, err
	}
	if err := n.uc.Group().AddMember(ctx, org, groupID, userID); err != nil {
		return nil, handleError(err)
	}
	return &pb.AddGroupMemberResponse{}, nil
}

func (n *nipaServer) RemoveGroupMember(ctx context.Context, req *pb.RemoveGroupMemberRequest) (*pb.RemoveGroupMemberResponse, error) {
	org, groupID, userID, err := n.resolveGroupMember(ctx, req.GetOrg(), req.GetGroupId(), req.GetUserId())
	if err != nil {
		return nil, err
	}
	if err := n.uc.Group().RemoveMember(ctx, org, groupID, userID); err != nil {
		return nil, handleError(err)
	}
	return &pb.RemoveGroupMemberResponse{}, nil
}

func (n *nipaServer) resolveGroupMember(ctx context.Context, orgSlug, groupRaw, userRaw string) (snow.ID, snow.ID, snow.ID, error) {
	org, err := n.uc.Common().ResolveOrg(ctx, orgSlug)
	if err != nil {
		return 0, 0, 0, handleError(err)
	}
	groupID, err := snow.ParseBase36(groupRaw)
	if err != nil {
		return 0, 0, 0, handleError(domain.NewErrorUser("invalid group id"))
	}
	userID, err := snow.ParseBase36(userRaw)
	if err != nil {
		return 0, 0, 0, handleError(domain.NewErrorUser("invalid user id"))
	}
	return org.ID, groupID, userID, nil
}

func domainGroupToPB(group *domain.Group) *pb.GroupDetail {
	return &pb.GroupDetail{
		Id:          group.ID.Base36(),
		OrgId:       group.OrgID.Base36(),
		Name:        group.Name,
		Description: group.Description,
	}
}
