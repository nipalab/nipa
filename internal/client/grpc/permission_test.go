package grpc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
)

type permissionFakeServer struct {
	pb.UnimplementedNipaServiceServer

	myResp    *pb.GetMyPermissionsResponse
	myErr     error
	lastMyReq *pb.GetMyPermissionsRequest

	createRuleResp    *pb.CreatePBACRuleResponse
	createRuleErr     error
	lastCreateRuleReq *pb.CreatePBACRuleRequest

	listRulesResp    *pb.ListPBACRulesResponse
	listRulesErr     error
	lastListRulesReq *pb.ListPBACRulesRequest

	deleteRuleErr     error
	lastDeleteRuleReq *pb.DeletePBACRuleRequest

	listPathsResp    *pb.ListProjectPathPermissionsResponse
	listPathsErr     error
	lastListPathsReq *pb.ListProjectPathPermissionsRequest

	setPathResp    *pb.SetProjectPathPermissionResponse
	setPathErr     error
	lastSetPathReq *pb.SetProjectPathPermissionRequest

	deletePathErr     error
	lastDeletePathReq *pb.DeleteProjectPathPermissionRequest

	createGroupResp    *pb.CreateGroupResponse
	createGroupErr     error
	lastCreateGroupReq *pb.CreateGroupRequest

	listGroupsResp    *pb.ListGroupsResponse
	listGroupsErr     error
	lastListGroupsReq *pb.ListGroupsRequest

	addMemberErr     error
	lastAddMemberReq *pb.AddGroupMemberRequest

	removeMemberErr     error
	lastRemoveMemberReq *pb.RemoveGroupMemberRequest
}

func (f *permissionFakeServer) GetMyPermissions(_ context.Context, req *pb.GetMyPermissionsRequest) (*pb.GetMyPermissionsResponse, error) {
	f.lastMyReq = req
	return f.myResp, f.myErr
}

func (f *permissionFakeServer) CreatePBACRule(_ context.Context, req *pb.CreatePBACRuleRequest) (*pb.CreatePBACRuleResponse, error) {
	f.lastCreateRuleReq = req
	return f.createRuleResp, f.createRuleErr
}

func (f *permissionFakeServer) ListPBACRules(_ context.Context, req *pb.ListPBACRulesRequest) (*pb.ListPBACRulesResponse, error) {
	f.lastListRulesReq = req
	return f.listRulesResp, f.listRulesErr
}

func (f *permissionFakeServer) DeletePBACRule(_ context.Context, req *pb.DeletePBACRuleRequest) (*pb.DeletePBACRuleResponse, error) {
	f.lastDeleteRuleReq = req
	return &pb.DeletePBACRuleResponse{}, f.deleteRuleErr
}

func (f *permissionFakeServer) ListProjectPathPermissions(_ context.Context, req *pb.ListProjectPathPermissionsRequest) (*pb.ListProjectPathPermissionsResponse, error) {
	f.lastListPathsReq = req
	return f.listPathsResp, f.listPathsErr
}

func (f *permissionFakeServer) SetProjectPathPermission(_ context.Context, req *pb.SetProjectPathPermissionRequest) (*pb.SetProjectPathPermissionResponse, error) {
	f.lastSetPathReq = req
	return f.setPathResp, f.setPathErr
}

func (f *permissionFakeServer) DeleteProjectPathPermission(_ context.Context, req *pb.DeleteProjectPathPermissionRequest) (*pb.DeleteProjectPathPermissionResponse, error) {
	f.lastDeletePathReq = req
	return &pb.DeleteProjectPathPermissionResponse{}, f.deletePathErr
}

func (f *permissionFakeServer) CreateGroup(_ context.Context, req *pb.CreateGroupRequest) (*pb.CreateGroupResponse, error) {
	f.lastCreateGroupReq = req
	return f.createGroupResp, f.createGroupErr
}

func (f *permissionFakeServer) ListGroups(_ context.Context, req *pb.ListGroupsRequest) (*pb.ListGroupsResponse, error) {
	f.lastListGroupsReq = req
	return f.listGroupsResp, f.listGroupsErr
}

func (f *permissionFakeServer) AddGroupMember(_ context.Context, req *pb.AddGroupMemberRequest) (*pb.AddGroupMemberResponse, error) {
	f.lastAddMemberReq = req
	return &pb.AddGroupMemberResponse{}, f.addMemberErr
}

func (f *permissionFakeServer) RemoveGroupMember(_ context.Context, req *pb.RemoveGroupMemberRequest) (*pb.RemoveGroupMemberResponse, error) {
	f.lastRemoveMemberReq = req
	return &pb.RemoveGroupMemberResponse{}, f.removeMemberErr
}

func newPermissionTestClient(t *testing.T, srv *permissionFakeServer) *Client {
	t.Helper()

	addr := startTestServer(t, srv)
	c := NewClient(NewTransport(), &stubSession{accessToken: "access"})
	require.NoError(t, c.Connect(context.Background(), addr))
	t.Cleanup(func() { require.NoError(t, c.Close()) })
	return c
}

func TestClient_GetMyPermissions_Success(t *testing.T) {
	srv := &permissionFakeServer{myResp: &pb.GetMyPermissionsResponse{
		ProjectPermission: 7,
		Rules:             []*pb.PermissionEntry{{PathPrefix: "assets", Permission: 3}},
		Defaults:          []*pb.PermissionEntry{{PathPrefix: "", Permission: 1}},
	}}
	c := newPermissionTestClient(t, srv)

	info, err := c.GetMyPermissions(context.Background(), "org", "proj")
	require.NoError(t, err)
	require.Equal(t, uint64(7), info.ProjectPermission)
	require.Equal(t, []domain.PermissionEntry{{PathPrefix: "assets", Permission: 3}}, info.Rules)
	require.Equal(t, []domain.PermissionEntry{{PathPrefix: "", Permission: 1}}, info.Defaults)
	require.Equal(t, "org", srv.lastMyReq.GetContext().GetOrg())
	require.Equal(t, "proj", srv.lastMyReq.GetContext().GetProject())
}

func TestClient_GetMyPermissions_Error(t *testing.T) {
	srv := &permissionFakeServer{myErr: status.Error(codes.PermissionDenied, "no access")}
	c := newPermissionTestClient(t, srv)

	_, err := c.GetMyPermissions(context.Background(), "org", "proj")
	require.Error(t, err)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 403, domErr.Code)
	require.Equal(t, "no access", domErr.Message)
}

func TestClient_CreatePBACRule_User(t *testing.T) {
	srv := &permissionFakeServer{createRuleResp: &pb.CreatePBACRuleResponse{
		Rule: &pb.PBACRuleDetail{Id: 5, PathPrefix: "assets", Permission: 3, UserId: strPtr("u1")},
	}}
	c := newPermissionTestClient(t, srv)

	rule, err := c.CreatePBACRule(context.Background(), "org", "proj", "u1", "", "assets", 3)
	require.NoError(t, err)
	require.Equal(t, int64(5), rule.ID)
	require.Equal(t, "assets", rule.PathPrefix)
	require.Equal(t, uint64(3), rule.Permission)
	require.Equal(t, "u1", rule.UserID)
	require.Empty(t, rule.GroupID)
	require.Equal(t, "u1", srv.lastCreateRuleReq.GetUserId())
	require.Nil(t, srv.lastCreateRuleReq.GroupId)
}

func TestClient_CreatePBACRule_Group(t *testing.T) {
	srv := &permissionFakeServer{createRuleResp: &pb.CreatePBACRuleResponse{
		Rule: &pb.PBACRuleDetail{Id: 6, GroupId: strPtr("g1")},
	}}
	c := newPermissionTestClient(t, srv)

	rule, err := c.CreatePBACRule(context.Background(), "org", "proj", "", "g1", "", 1)
	require.NoError(t, err)
	require.Equal(t, "g1", rule.GroupID)
	require.Empty(t, rule.UserID)
	require.Nil(t, srv.lastCreateRuleReq.UserId)
	require.Equal(t, "g1", srv.lastCreateRuleReq.GetGroupId())
}

func TestClient_CreatePBACRule_NilRule(t *testing.T) {
	srv := &permissionFakeServer{createRuleResp: &pb.CreatePBACRuleResponse{}}
	c := newPermissionTestClient(t, srv)

	rule, err := c.CreatePBACRule(context.Background(), "org", "proj", "u1", "", "", 0)
	require.NoError(t, err)
	require.Nil(t, rule)
}

func TestClient_CreatePBACRule_Error(t *testing.T) {
	srv := &permissionFakeServer{createRuleErr: status.Error(codes.InvalidArgument, "bad rule")}
	c := newPermissionTestClient(t, srv)

	_, err := c.CreatePBACRule(context.Background(), "org", "proj", "u1", "", "", 0)
	require.Error(t, err)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 400, domErr.Code)
}

func TestClient_ListPBACRules_Success(t *testing.T) {
	srv := &permissionFakeServer{listRulesResp: &pb.ListPBACRulesResponse{
		Rules: []*pb.PBACRuleDetail{
			{Id: 1, PathPrefix: "assets", Permission: 3, GroupId: strPtr("g1")},
			nil,
		},
	}}
	c := newPermissionTestClient(t, srv)

	rules, err := c.ListPBACRules(context.Background(), "org", "proj")
	require.NoError(t, err)
	require.Len(t, rules, 2)
	require.Equal(t, int64(1), rules[0].ID)
	require.Equal(t, "g1", rules[0].GroupID)
	require.Equal(t, &domain.PBACRuleInfo{}, rules[1])
}

func TestClient_PermissionConverters_Nil(t *testing.T) {
	require.Nil(t, toPBACRuleInfo(nil))
	require.Nil(t, toGroupInfo(nil))
}

func TestClient_ListPBACRules_Error(t *testing.T) {
	srv := &permissionFakeServer{listRulesErr: status.Error(codes.Internal, "boom")}
	c := newPermissionTestClient(t, srv)

	_, err := c.ListPBACRules(context.Background(), "org", "proj")
	require.Error(t, err)
}

func TestClient_DeletePBACRule(t *testing.T) {
	srv := &permissionFakeServer{}
	c := newPermissionTestClient(t, srv)

	require.NoError(t, c.DeletePBACRule(context.Background(), "org", "proj", 12))
	require.Equal(t, int64(12), srv.lastDeleteRuleReq.GetRuleId())

	srv.deleteRuleErr = status.Error(codes.NotFound, "rule not found")
	err := c.DeletePBACRule(context.Background(), "org", "proj", 12)
	require.Error(t, err)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)
}

func TestClient_ListProjectPathPermissions(t *testing.T) {
	srv := &permissionFakeServer{listPathsResp: &pb.ListProjectPathPermissionsResponse{
		Permissions: []*pb.PermissionEntry{{PathPrefix: "", Permission: 1}},
	}}
	c := newPermissionTestClient(t, srv)

	perms, err := c.ListProjectPathPermissions(context.Background(), "org", "proj")
	require.NoError(t, err)
	require.Equal(t, []domain.PermissionEntry{{PathPrefix: "", Permission: 1}}, perms)

	srv.listPathsErr = status.Error(codes.Internal, "boom")
	_, err = c.ListProjectPathPermissions(context.Background(), "org", "proj")
	require.Error(t, err)
}

func TestClient_SetProjectPathPermission(t *testing.T) {
	srv := &permissionFakeServer{setPathResp: &pb.SetProjectPathPermissionResponse{
		Permission: &pb.PermissionEntry{PathPrefix: "assets", Permission: 3},
	}}
	c := newPermissionTestClient(t, srv)

	entry, err := c.SetProjectPathPermission(context.Background(), "org", "proj", "assets", 3)
	require.NoError(t, err)
	require.Equal(t, "assets", entry.PathPrefix)
	require.Equal(t, uint64(3), entry.Permission)
	require.Equal(t, "assets", srv.lastSetPathReq.GetPathPrefix())
	require.Equal(t, uint64(3), srv.lastSetPathReq.GetPermission())

	srv.setPathErr = status.Error(codes.InvalidArgument, "bad prefix")
	_, err = c.SetProjectPathPermission(context.Background(), "org", "proj", "..", 0)
	require.Error(t, err)
}

func TestClient_DeleteProjectPathPermission(t *testing.T) {
	srv := &permissionFakeServer{}
	c := newPermissionTestClient(t, srv)

	require.NoError(t, c.DeleteProjectPathPermission(context.Background(), "org", "proj", "assets"))
	require.Equal(t, "assets", srv.lastDeletePathReq.GetPathPrefix())

	srv.deletePathErr = status.Error(codes.NotFound, "missing")
	err := c.DeleteProjectPathPermission(context.Background(), "org", "proj", "assets")
	require.Error(t, err)
}

func TestClient_CreateGroup(t *testing.T) {
	srv := &permissionFakeServer{createGroupResp: &pb.CreateGroupResponse{
		Group: &pb.GroupDetail{Id: "g1", OrgId: "1", Name: "artists", Description: "2d team"},
	}}
	c := newPermissionTestClient(t, srv)

	group, err := c.CreateGroup(context.Background(), "org", "artists", "2d team")
	require.NoError(t, err)
	require.Equal(t, &domain.GroupInfo{ID: "g1", OrgID: "1", Name: "artists", Description: "2d team"}, group)
	require.Equal(t, "org", srv.lastCreateGroupReq.GetOrg())
	require.Equal(t, "artists", srv.lastCreateGroupReq.GetName())

	srv.createGroupErr = status.Error(codes.AlreadyExists, "duplicate")
	_, err = c.CreateGroup(context.Background(), "org", "artists", "")
	require.Error(t, err)
}

func TestClient_CreateGroup_NilGroup(t *testing.T) {
	srv := &permissionFakeServer{createGroupResp: &pb.CreateGroupResponse{}}
	c := newPermissionTestClient(t, srv)

	group, err := c.CreateGroup(context.Background(), "org", "artists", "")
	require.NoError(t, err)
	require.Nil(t, group)
}

func TestClient_ListGroups(t *testing.T) {
	srv := &permissionFakeServer{listGroupsResp: &pb.ListGroupsResponse{
		Groups: []*pb.GroupDetail{
			{Id: "g1", OrgId: "1", Name: "artists"},
			nil,
		},
	}}
	c := newPermissionTestClient(t, srv)

	groups, err := c.ListGroups(context.Background(), "org")
	require.NoError(t, err)
	require.Len(t, groups, 2)
	require.Equal(t, "artists", groups[0].Name)
	require.Equal(t, &domain.GroupInfo{}, groups[1])
	require.Equal(t, "org", srv.lastListGroupsReq.GetOrg())

	srv.listGroupsErr = status.Error(codes.Internal, "boom")
	_, err = c.ListGroups(context.Background(), "org")
	require.Error(t, err)
}

func TestClient_GroupMembers(t *testing.T) {
	srv := &permissionFakeServer{}
	c := newPermissionTestClient(t, srv)

	require.NoError(t, c.AddGroupMember(context.Background(), "org", "g1", "u1"))
	require.Equal(t, "g1", srv.lastAddMemberReq.GetGroupId())
	require.Equal(t, "u1", srv.lastAddMemberReq.GetUserId())

	srv.addMemberErr = status.Error(codes.NotFound, "group not found")
	require.Error(t, c.AddGroupMember(context.Background(), "org", "g1", "u1"))

	require.NoError(t, c.RemoveGroupMember(context.Background(), "org", "g1", "u1"))
	require.Equal(t, "g1", srv.lastRemoveMemberReq.GetGroupId())
	require.Equal(t, "u1", srv.lastRemoveMemberReq.GetUserId())

	srv.removeMemberErr = status.Error(codes.NotFound, "group not found")
	require.Error(t, c.RemoveGroupMember(context.Background(), "org", "g1", "u1"))
}

func TestClient_PermissionMethods_NotConnected(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{})
	ctx := context.Background()

	_, err := c.GetMyPermissions(ctx, "org", "proj")
	require.Error(t, err)
	_, err = c.CreatePBACRule(ctx, "org", "proj", "u1", "", "", 1)
	require.Error(t, err)
	_, err = c.ListPBACRules(ctx, "org", "proj")
	require.Error(t, err)
	require.Error(t, c.DeletePBACRule(ctx, "org", "proj", 1))
	_, err = c.ListProjectPathPermissions(ctx, "org", "proj")
	require.Error(t, err)
	_, err = c.SetProjectPathPermission(ctx, "org", "proj", "", 1)
	require.Error(t, err)
	require.Error(t, c.DeleteProjectPathPermission(ctx, "org", "proj", ""))
	_, err = c.CreateGroup(ctx, "org", "g", "")
	require.Error(t, err)
	_, err = c.ListGroups(ctx, "org")
	require.Error(t, err)
	require.Error(t, c.AddGroupMember(ctx, "org", "g1", "u1"))
	require.Error(t, c.RemoveGroupMember(ctx, "org", "g1", "u1"))
}

func strPtr(value string) *string {
	return &value
}
