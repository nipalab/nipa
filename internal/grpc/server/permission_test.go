package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

func adminClaimCtx(userID snow.ID) context.Context {
	return domain.ContextWithClaim(context.Background(), domain.Claims{UserID: userID, IsAdmin: true})
}

func superAdminClaimCtx(userID snow.ID) context.Context {
	return domain.ContextWithClaim(context.Background(), domain.Claims{UserID: userID, IsSuperAdmin: true})
}

func newTestPermissionHandler(t *testing.T) (*nipaServer, *MockpbacRepository) {
	t.Helper()

	ctrl := gomock.NewController(t)
	repo := NewMockpbacRepository(ctrl)
	users := NewMockuserLookup(ctrl)
	users.EXPECT().GetByID(gomock.Any(), gomock.Any()).Return(&domain.User{}, nil).AnyTimes()
	groups := NewMockgroupLookup(ctrl)
	groups.EXPECT().GetByID(gomock.Any(), gomock.Any()).Return(&domain.Group{OrgID: 1}, nil).AnyTimes()
	uc := &mockUsecaseContainer{
		common:     newTestCommon(),
		permission: usecase.NewPermission(repo, users, groups),
	}
	return New(uc), repo
}

func projectContext() *pb.ProjectContext {
	return &pb.ProjectContext{Org: "org", Project: "proj"}
}

func TestNipaServer_GetMyPermissions_SuperAdmin(t *testing.T) {
	srv, repo := newTestPermissionHandler(t)
	defaults := []*domain.ProjectPathPermission{{ProjectID: 42, PathPrefix: "", Permission: domain.PermissionRead}}
	repo.EXPECT().ListPathPermissions(gomock.Any(), snow.ID(42)).Return(defaults, nil)

	resp, err := srv.GetMyPermissions(superAdminClaimCtx(1), &pb.GetMyPermissionsRequest{Context: projectContext()})
	require.NoError(t, err)
	require.Equal(t, uint64(domain.PermissionAll), resp.GetProjectPermission())
	require.Empty(t, resp.GetRules())
	require.Equal(t, []*pb.PermissionEntry{{PathPrefix: "", Permission: uint64(domain.PermissionRead)}}, resp.GetDefaults())
}

func TestNipaServer_GetMyPermissions_RegularUser(t *testing.T) {
	srv, repo := newTestPermissionHandler(t)
	rules := []*domain.PBACRule{{
		ID: 1, UserID: ptrSnow(7), OrgID: 1, ProjectID: ptrSnow(42),
		PathPrefix: "assets", Permission: domain.PermissionRead,
	}}
	defaults := []*domain.ProjectPathPermission{{ProjectID: 42, PathPrefix: "", Permission: domain.PermissionWrite}}
	repo.EXPECT().ListEffectiveRules(gomock.Any(), snow.ID(42), snow.ID(7)).Return(rules, nil)
	repo.EXPECT().ListPathPermissions(gomock.Any(), snow.ID(42)).Return(defaults, nil)

	ctx := domain.ContextWithClaim(context.Background(), domain.Claims{UserID: 7})
	resp, err := srv.GetMyPermissions(ctx, &pb.GetMyPermissionsRequest{Context: projectContext()})
	require.NoError(t, err)
	require.Equal(t, uint64(domain.PermissionRead|domain.PermissionWrite), resp.GetProjectPermission())
	require.Len(t, resp.GetRules(), 1)
	require.Equal(t, "assets", resp.GetRules()[0].GetPathPrefix())
	require.Len(t, resp.GetDefaults(), 1)
}

func TestNipaServer_GetMyPermissions_RepoError(t *testing.T) {
	srv, repo := newTestPermissionHandler(t)
	repo.EXPECT().ListPathPermissions(gomock.Any(), snow.ID(42)).Return(nil, errors.New("boom"))

	_, err := srv.GetMyPermissions(adminClaimCtx(1), &pb.GetMyPermissionsRequest{Context: projectContext()})
	require.Equal(t, codes.Internal, status.Code(err))
}

func TestNipaServer_CreatePBACRule_User(t *testing.T) {
	srv, repo := newTestPermissionHandler(t)
	now := time.Now().UTC()
	repo.EXPECT().CreateRule(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, rule domain.PBACRule) (*domain.PBACRule, error) {
			require.Equal(t, snow.ID(1), rule.OrgID)
			require.Equal(t, snow.ID(42), *rule.ProjectID)
			require.Equal(t, snow.ID(7), *rule.UserID)
			require.Nil(t, rule.GroupID)
			require.Equal(t, "assets", rule.PathPrefix)
			require.Equal(t, domain.PermissionRead|domain.PermissionWrite, rule.Permission)
			rule.ID = 5
			rule.CreatedAt = now
			return &rule, nil
		},
	)

	ctx := adminClaimCtx(1)
	resp, err := srv.CreatePBACRule(ctx, &pb.CreatePBACRuleRequest{
		Context:    projectContext(),
		UserId:     strPtr("7"),
		PathPrefix: " assets/ ",
		Permission: uint64(domain.PermissionRead | domain.PermissionWrite),
	})
	require.NoError(t, err)
	require.Equal(t, int64(5), resp.GetRule().GetId())
	require.Equal(t, "7", resp.GetRule().GetUserId())
	require.Nil(t, resp.GetRule().GroupId)
	require.Equal(t, snow.ID(42).Base36(), resp.GetRule().GetProjectId())
	require.Equal(t, timestamppb.New(now), resp.GetRule().GetCreatedAt())
}

func TestNipaServer_CreatePBACRule_Group(t *testing.T) {
	srv, repo := newTestPermissionHandler(t)
	repo.EXPECT().CreateRule(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, rule domain.PBACRule) (*domain.PBACRule, error) {
			require.Nil(t, rule.UserID)
			require.Equal(t, snow.ID(9), *rule.GroupID)
			return &rule, nil
		},
	)

	ctx := adminClaimCtx(1)
	resp, err := srv.CreatePBACRule(ctx, &pb.CreatePBACRuleRequest{
		Context:    projectContext(),
		GroupId:    strPtr("9"),
		PathPrefix: "",
		Permission: uint64(domain.PermissionRead),
	})
	require.NoError(t, err)
	require.Equal(t, "9", resp.GetRule().GetGroupId())
	require.Nil(t, resp.GetRule().UserId)
}

func TestNipaServer_CreatePBACRule_InvalidUserID(t *testing.T) {
	srv, _ := newTestPermissionHandler(t)

	_, err := srv.CreatePBACRule(adminClaimCtx(1), &pb.CreatePBACRuleRequest{
		Context: projectContext(),
		UserId:  strPtr("!!!"),
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Equal(t, "invalid user id", status.Convert(err).Message())
}

func TestNipaServer_CreatePBACRule_InvalidGroupID(t *testing.T) {
	srv, _ := newTestPermissionHandler(t)

	_, err := srv.CreatePBACRule(adminClaimCtx(1), &pb.CreatePBACRuleRequest{
		Context: projectContext(),
		GroupId: strPtr("!!!"),
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Equal(t, "invalid group id", status.Convert(err).Message())
}

func TestNipaServer_CreatePBACRule_NoPermission(t *testing.T) {
	srv, repo := newTestPermissionHandler(t)
	repo.EXPECT().ListEffectiveRules(gomock.Any(), snow.ID(42), snow.ID(7)).Return(nil, nil)
	repo.EXPECT().ListPathPermissions(gomock.Any(), snow.ID(42)).Return(nil, nil)

	ctx := domain.ContextWithClaim(context.Background(), domain.Claims{UserID: 7})
	_, err := srv.CreatePBACRule(ctx, &pb.CreatePBACRuleRequest{
		Context:    projectContext(),
		UserId:     strPtr("7"),
		Permission: uint64(domain.PermissionRead),
	})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestNipaServer_CreatePBACRule_UseCaseError(t *testing.T) {
	srv, _ := newTestPermissionHandler(t)

	_, err := srv.CreatePBACRule(adminClaimCtx(1), &pb.CreatePBACRuleRequest{
		Context:    projectContext(),
		PathPrefix: "",
		Permission: uint64(domain.PermissionRead),
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Equal(t, "rule must target exactly one user or group", status.Convert(err).Message())
}

func TestNipaServer_CreatePBACRule_RepoError(t *testing.T) {
	srv, repo := newTestPermissionHandler(t)
	repo.EXPECT().CreateRule(gomock.Any(), gomock.Any()).Return(nil, domain.NewErrorDatabase("boom"))

	_, err := srv.CreatePBACRule(adminClaimCtx(1), &pb.CreatePBACRuleRequest{
		Context:    projectContext(),
		UserId:     strPtr("7"),
		Permission: uint64(domain.PermissionRead),
	})
	require.Equal(t, codes.Internal, status.Code(err))
}

func TestNipaServer_ListPBACRules(t *testing.T) {
	srv, repo := newTestPermissionHandler(t)
	now := time.Now().UTC()
	rules := []*domain.PBACRule{
		{ID: 1, UserID: ptrSnow(7), OrgID: 1, ProjectID: ptrSnow(42), PathPrefix: "assets", Permission: domain.PermissionRead, CreatedAt: now},
		{ID: 2, OrgID: 1, PathPrefix: "", Permission: domain.PermissionWrite},
	}
	repo.EXPECT().ListRulesByProject(gomock.Any(), snow.ID(42)).Return(rules, nil)

	resp, err := srv.ListPBACRules(adminClaimCtx(1), &pb.ListPBACRulesRequest{Context: projectContext()})
	require.NoError(t, err)
	require.Len(t, resp.GetRules(), 2)
	require.Equal(t, "7", resp.GetRules()[0].GetUserId())
	require.Equal(t, snow.ID(42).Base36(), resp.GetRules()[0].GetProjectId())
	require.Equal(t, timestamppb.New(now), resp.GetRules()[0].GetCreatedAt())
	require.Nil(t, resp.GetRules()[1].UserId)
	require.Nil(t, resp.GetRules()[1].GroupId)
	require.Nil(t, resp.GetRules()[1].ProjectId)
}

func TestNipaServer_ListPBACRules_Error(t *testing.T) {
	srv, repo := newTestPermissionHandler(t)
	repo.EXPECT().ListRulesByProject(gomock.Any(), snow.ID(42)).Return(nil, errors.New("boom"))

	_, err := srv.ListPBACRules(adminClaimCtx(1), &pb.ListPBACRulesRequest{Context: projectContext()})
	require.Equal(t, codes.Internal, status.Code(err))
}

func TestNipaServer_DeletePBACRule(t *testing.T) {
	srv, repo := newTestPermissionHandler(t)
	projectRule := &domain.PBACRule{ID: 5, OrgID: 1, ProjectID: ptrSnow(42)}
	repo.EXPECT().GetRuleForProject(gomock.Any(), snow.ID(42), int64(5)).Return(projectRule, nil)
	repo.EXPECT().DeleteRuleForProject(gomock.Any(), snow.ID(42), int64(5)).Return(nil)

	_, err := srv.DeletePBACRule(adminClaimCtx(1), &pb.DeletePBACRuleRequest{Context: projectContext(), RuleId: 5})
	require.NoError(t, err)

	repo.EXPECT().GetRuleForProject(gomock.Any(), snow.ID(42), int64(5)).Return(nil, domain.NewErrorRecordNotFound())
	_, err = srv.DeletePBACRule(adminClaimCtx(1), &pb.DeletePBACRuleRequest{Context: projectContext(), RuleId: 5})
	require.Equal(t, codes.NotFound, status.Code(err))
}

func TestNipaServer_ListProjectPathPermissions(t *testing.T) {
	srv, repo := newTestPermissionHandler(t)
	perms := []*domain.ProjectPathPermission{{ProjectID: 42, PathPrefix: "assets", Permission: domain.PermissionRead}}
	repo.EXPECT().ListPathPermissions(gomock.Any(), snow.ID(42)).Return(perms, nil)

	resp, err := srv.ListProjectPathPermissions(adminClaimCtx(1), &pb.ListProjectPathPermissionsRequest{Context: projectContext()})
	require.NoError(t, err)
	require.Len(t, resp.GetPermissions(), 1)
	require.Equal(t, "assets", resp.GetPermissions()[0].GetPathPrefix())
	require.Equal(t, uint64(domain.PermissionRead), resp.GetPermissions()[0].GetPermission())

	repo.EXPECT().ListPathPermissions(gomock.Any(), snow.ID(42)).Return(nil, errors.New("boom"))
	_, err = srv.ListProjectPathPermissions(adminClaimCtx(1), &pb.ListProjectPathPermissionsRequest{Context: projectContext()})
	require.Equal(t, codes.Internal, status.Code(err))
}

func TestNipaServer_SetProjectPathPermission(t *testing.T) {
	srv, repo := newTestPermissionHandler(t)
	repo.EXPECT().UpsertPathPermission(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, perm domain.ProjectPathPermission) (*domain.ProjectPathPermission, error) {
			require.Equal(t, snow.ID(42), perm.ProjectID)
			require.Equal(t, "assets", perm.PathPrefix)
			require.Equal(t, domain.PermissionRead, perm.Permission)
			return &perm, nil
		},
	)

	resp, err := srv.SetProjectPathPermission(adminClaimCtx(1), &pb.SetProjectPathPermissionRequest{
		Context: projectContext(), PathPrefix: "/assets/", Permission: uint64(domain.PermissionRead),
	})
	require.NoError(t, err)
	require.Equal(t, "assets", resp.GetPermission().GetPathPrefix())

	repo.EXPECT().UpsertPathPermission(gomock.Any(), gomock.Any()).Return(nil, domain.NewErrorDatabase("boom"))
	_, err = srv.SetProjectPathPermission(adminClaimCtx(1), &pb.SetProjectPathPermissionRequest{
		Context: projectContext(), PathPrefix: "assets", Permission: uint64(domain.PermissionRead),
	})
	require.Equal(t, codes.Internal, status.Code(err))
}

func TestNipaServer_DeleteProjectPathPermission(t *testing.T) {
	srv, repo := newTestPermissionHandler(t)
	repo.EXPECT().DeletePathPermission(gomock.Any(), snow.ID(42), "assets").Return(nil)

	_, err := srv.DeleteProjectPathPermission(adminClaimCtx(1), &pb.DeleteProjectPathPermissionRequest{
		Context: projectContext(), PathPrefix: "assets",
	})
	require.NoError(t, err)

	_, err = srv.DeleteProjectPathPermission(adminClaimCtx(1), &pb.DeleteProjectPathPermissionRequest{
		Context: projectContext(), PathPrefix: "..",
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	repo.EXPECT().DeletePathPermission(gomock.Any(), snow.ID(42), "assets").Return(domain.NewErrorDatabase("boom"))
	_, err = srv.DeleteProjectPathPermission(adminClaimCtx(1), &pb.DeleteProjectPathPermissionRequest{
		Context: projectContext(), PathPrefix: "assets",
	})
	require.Equal(t, codes.Internal, status.Code(err))
}

func TestNipaServer_PermissionHandlers_ResolveError(t *testing.T) {
	srv, _ := newTestPermissionHandler(t)
	ctx := adminClaimCtx(1)
	bad := &pb.ProjectContext{Org: "missing", Project: "proj"}

	_, err := srv.GetMyPermissions(ctx, &pb.GetMyPermissionsRequest{Context: bad})
	require.Error(t, err)
	_, err = srv.CreatePBACRule(ctx, &pb.CreatePBACRuleRequest{Context: bad, UserId: strPtr("7")})
	require.Error(t, err)
	_, err = srv.ListPBACRules(ctx, &pb.ListPBACRulesRequest{Context: bad})
	require.Error(t, err)
	_, err = srv.DeletePBACRule(ctx, &pb.DeletePBACRuleRequest{Context: bad})
	require.Error(t, err)
	_, err = srv.ListProjectPathPermissions(ctx, &pb.ListProjectPathPermissionsRequest{Context: bad})
	require.Error(t, err)
	_, err = srv.SetProjectPathPermission(ctx, &pb.SetProjectPathPermissionRequest{Context: bad})
	require.Error(t, err)
	_, err = srv.DeleteProjectPathPermission(ctx, &pb.DeleteProjectPathPermissionRequest{Context: bad})
	require.Error(t, err)
}

func strPtr(value string) *string {
	return &value
}
