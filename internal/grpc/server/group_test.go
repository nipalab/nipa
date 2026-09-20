package server

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

func newTestGroupHandler(t *testing.T) (*nipaServer, *MockgroupRepository) {
	t.Helper()

	ctrl := gomock.NewController(t)
	repo := NewMockgroupRepository(ctrl)
	node, err := snow.NewNode(1)
	require.NoError(t, err)
	invalidator := NewMockpermissionCacheInvalidator(ctrl)
	invalidator.EXPECT().InvalidateAll().AnyTimes()
	uc := &mockUsecaseContainer{
		common: newTestCommon(),
		group:  usecase.NewGroup(repo, node, invalidator),
	}
	return New(uc), repo
}

func TestNipaServer_CreateGroup_Success(t *testing.T) {
	srv, repo := newTestGroupHandler(t)
	repo.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, group domain.Group) (*domain.Group, error) {
			require.NotZero(t, group.ID)
			require.Equal(t, snow.ID(1), group.OrgID)
			require.Equal(t, "artists", group.Name)
			require.Equal(t, "2d team", group.Description)
			return &group, nil
		},
	)

	resp, err := srv.CreateGroup(adminClaimCtx(1), &pb.CreateGroupRequest{
		Org: "org", Name: " artists ", Description: " 2d team ",
	})
	require.NoError(t, err)
	require.Equal(t, "artists", resp.GetGroup().GetName())
	require.Equal(t, "2d team", resp.GetGroup().GetDescription())
	require.Equal(t, "1", resp.GetGroup().GetOrgId())
}

func TestNipaServer_CreateGroup_EmptyName(t *testing.T) {
	srv, _ := newTestGroupHandler(t)

	_, err := srv.CreateGroup(adminClaimCtx(1), &pb.CreateGroupRequest{Org: "org", Name: "  "})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestNipaServer_CreateGroup_NoPermission(t *testing.T) {
	srv, _ := newTestGroupHandler(t)

	ctx := domain.ContextWithClaim(context.Background(), domain.Claims{UserID: 7})
	_, err := srv.CreateGroup(ctx, &pb.CreateGroupRequest{Org: "org", Name: "artists"})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestNipaServer_CreateGroup_RepoError(t *testing.T) {
	srv, repo := newTestGroupHandler(t)
	repo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil, domain.NewErrorDatabase("boom"))

	_, err := srv.CreateGroup(adminClaimCtx(1), &pb.CreateGroupRequest{Org: "org", Name: "artists"})
	require.Equal(t, codes.Internal, status.Code(err))
}

func TestNipaServer_ListGroups_Success(t *testing.T) {
	srv, repo := newTestGroupHandler(t)
	groups := []*domain.Group{
		{ID: 1, OrgID: 1, Name: "artists", Description: "2d team"},
		{ID: 2, OrgID: 1, Name: "engineers"},
	}
	repo.EXPECT().ListByOrg(gomock.Any(), snow.ID(1)).Return(groups, nil)

	resp, err := srv.ListGroups(adminClaimCtx(1), &pb.ListGroupsRequest{Org: "org"})
	require.NoError(t, err)
	require.Len(t, resp.GetGroups(), 2)
	require.Equal(t, "artists", resp.GetGroups()[0].GetName())
	require.Equal(t, "2d team", resp.GetGroups()[0].GetDescription())
	require.Equal(t, "engineers", resp.GetGroups()[1].GetName())
}

func TestNipaServer_ListGroups_RepoError(t *testing.T) {
	srv, repo := newTestGroupHandler(t)
	repo.EXPECT().ListByOrg(gomock.Any(), snow.ID(1)).Return(nil, errors.New("boom"))

	_, err := srv.ListGroups(adminClaimCtx(1), &pb.ListGroupsRequest{Org: "org"})
	require.Equal(t, codes.Internal, status.Code(err))
}

func TestNipaServer_AddGroupMember_Success(t *testing.T) {
	srv, repo := newTestGroupHandler(t)
	groupID := snow.ID(9)
	userID := snow.ID(7)
	repo.EXPECT().GetByID(gomock.Any(), groupID).Return(&domain.Group{ID: groupID, OrgID: 1}, nil)
	repo.EXPECT().AddMember(gomock.Any(), groupID, userID).Return(nil)

	_, err := srv.AddGroupMember(adminClaimCtx(1), &pb.AddGroupMemberRequest{
		Org: "org", GroupId: groupID.Base36(), UserId: userID.Base36(),
	})
	require.NoError(t, err)
}

func TestNipaServer_AddGroupMember_InvalidGroupID(t *testing.T) {
	srv, _ := newTestGroupHandler(t)

	_, err := srv.AddGroupMember(adminClaimCtx(1), &pb.AddGroupMemberRequest{
		Org: "org", GroupId: "!!!", UserId: snow.ID(7).Base36(),
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Equal(t, "invalid group id", status.Convert(err).Message())
}

func TestNipaServer_AddGroupMember_InvalidUserID(t *testing.T) {
	srv, _ := newTestGroupHandler(t)

	_, err := srv.AddGroupMember(adminClaimCtx(1), &pb.AddGroupMemberRequest{
		Org: "org", GroupId: snow.ID(9).Base36(), UserId: "!!!",
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Equal(t, "invalid user id", status.Convert(err).Message())
}

func TestNipaServer_AddGroupMember_GroupNotFound(t *testing.T) {
	srv, repo := newTestGroupHandler(t)
	repo.EXPECT().GetByID(gomock.Any(), snow.ID(9)).Return(nil, domain.NewErrorRecordNotFound())

	_, err := srv.AddGroupMember(adminClaimCtx(1), &pb.AddGroupMemberRequest{
		Org: "org", GroupId: snow.ID(9).Base36(), UserId: snow.ID(7).Base36(),
	})
	require.Equal(t, codes.NotFound, status.Code(err))
}

func TestNipaServer_AddGroupMember_GroupInOtherOrg(t *testing.T) {
	srv, repo := newTestGroupHandler(t)
	repo.EXPECT().GetByID(gomock.Any(), snow.ID(9)).Return(&domain.Group{ID: 9, OrgID: 2}, nil)

	_, err := srv.AddGroupMember(adminClaimCtx(1), &pb.AddGroupMemberRequest{
		Org: "org", GroupId: snow.ID(9).Base36(), UserId: snow.ID(7).Base36(),
	})
	require.Equal(t, codes.NotFound, status.Code(err))
}

func TestNipaServer_AddGroupMember_RepoError(t *testing.T) {
	srv, repo := newTestGroupHandler(t)
	groupID := snow.ID(9)
	repo.EXPECT().GetByID(gomock.Any(), groupID).Return(&domain.Group{ID: groupID, OrgID: 1}, nil)
	repo.EXPECT().AddMember(gomock.Any(), groupID, snow.ID(7)).Return(domain.NewErrorDatabase("boom"))

	_, err := srv.AddGroupMember(adminClaimCtx(1), &pb.AddGroupMemberRequest{
		Org: "org", GroupId: groupID.Base36(), UserId: snow.ID(7).Base36(),
	})
	require.Equal(t, codes.Internal, status.Code(err))
}

func TestNipaServer_AddGroupMember_NoPermission(t *testing.T) {
	srv, _ := newTestGroupHandler(t)

	ctx := domain.ContextWithClaim(context.Background(), domain.Claims{UserID: 7})
	_, err := srv.AddGroupMember(ctx, &pb.AddGroupMemberRequest{
		Org: "org", GroupId: snow.ID(9).Base36(), UserId: snow.ID(7).Base36(),
	})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestNipaServer_RemoveGroupMember_Success(t *testing.T) {
	srv, repo := newTestGroupHandler(t)
	groupID := snow.ID(9)
	userID := snow.ID(7)
	repo.EXPECT().GetByID(gomock.Any(), groupID).Return(&domain.Group{ID: groupID, OrgID: 1}, nil)
	repo.EXPECT().RemoveMember(gomock.Any(), groupID, userID).Return(nil)

	_, err := srv.RemoveGroupMember(adminClaimCtx(1), &pb.RemoveGroupMemberRequest{
		Org: "org", GroupId: groupID.Base36(), UserId: userID.Base36(),
	})
	require.NoError(t, err)
}

func TestNipaServer_RemoveGroupMember_RepoError(t *testing.T) {
	srv, repo := newTestGroupHandler(t)
	groupID := snow.ID(9)
	repo.EXPECT().GetByID(gomock.Any(), groupID).Return(&domain.Group{ID: groupID, OrgID: 1}, nil)
	repo.EXPECT().RemoveMember(gomock.Any(), groupID, snow.ID(7)).Return(domain.NewErrorDatabase("boom"))

	_, err := srv.RemoveGroupMember(adminClaimCtx(1), &pb.RemoveGroupMemberRequest{
		Org: "org", GroupId: groupID.Base36(), UserId: snow.ID(7).Base36(),
	})
	require.Equal(t, codes.Internal, status.Code(err))
}

func TestNipaServer_GroupHandlers_ResolveError(t *testing.T) {
	srv, _ := newTestGroupHandler(t)
	ctx := adminClaimCtx(1)

	_, err := srv.CreateGroup(ctx, &pb.CreateGroupRequest{Org: "missing", Name: "artists"})
	require.Error(t, err)
	_, err = srv.ListGroups(ctx, &pb.ListGroupsRequest{Org: "missing"})
	require.Error(t, err)
	_, err = srv.AddGroupMember(ctx, &pb.AddGroupMemberRequest{Org: "missing", GroupId: "9", UserId: "7"})
	require.Error(t, err)
	_, err = srv.RemoveGroupMember(ctx, &pb.RemoveGroupMemberRequest{Org: "missing", GroupId: "9", UserId: "7"})
	require.Error(t, err)
}
