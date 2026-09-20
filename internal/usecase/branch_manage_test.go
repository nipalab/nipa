package usecase

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

func newManageBranchFixture(t *testing.T) (*Branch, *MockpermissionUsecase, *MockbranchRepository) {
	t.Helper()

	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)
	return NewBranch(perm, repo, newTestBranchNode(t)), perm, repo
}

func TestBranch_Rename_Success(t *testing.T) {
	uc, perm, repo := newManageBranchFixture(t)
	ctx := permissionCtx(42)

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
		Return(&domain.Branch{ID: 7, ProjectID: 1, Name: "feature"}, nil)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "renamed").Return(nil, domain.NewErrorRecordNotFound())
	repo.EXPECT().RenameBranch(gomock.Any(), snow.ID(1), snow.ID(7), "renamed", "renamed").Return(nil)
	repo.EXPECT().GetByProjectIDAndID(gomock.Any(), snow.ID(1), snow.ID(7)).
		Return(&domain.Branch{ID: 7, ProjectID: 1, Name: "renamed"}, nil)

	got, err := uc.Rename(ctx, snow.ID(1), "feature", " renamed ")
	require.NoError(t, err)
	require.Equal(t, "renamed", got.Name)
}

func TestBranch_Rename_ProtectedNeedsAdmin(t *testing.T) {
	uc, perm, repo := newManageBranchFixture(t)
	ctx := permissionCtx(42)

	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{ID: 1, ProjectID: 1, Name: "main", IsProtected: true, IsDefault: true}, nil)
	perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(false)

	_, err := uc.Rename(ctx, snow.ID(1), "main", "trunk")
	require.True(t, domain.IsErrorNoPermission(err))
}

func TestBranch_Rename_ProtectedByAdmin(t *testing.T) {
	uc, perm, repo := newManageBranchFixture(t)
	ctx := permissionCtx(42)

	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{ID: 1, ProjectID: 1, Name: "main", IsProtected: true}, nil)
	perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "trunk").Return(nil, domain.NewErrorRecordNotFound())
	repo.EXPECT().RenameBranch(gomock.Any(), snow.ID(1), snow.ID(1), "trunk", "trunk").Return(nil)
	repo.EXPECT().GetByProjectIDAndID(gomock.Any(), snow.ID(1), snow.ID(1)).
		Return(&domain.Branch{ID: 1, ProjectID: 1, Name: "trunk", IsProtected: true}, nil)

	got, err := uc.Rename(ctx, snow.ID(1), "main", "trunk")
	require.NoError(t, err)
	require.Equal(t, "trunk", got.Name)
}

func TestBranch_Rename_NoWritePermission(t *testing.T) {
	uc, perm, repo := newManageBranchFixture(t)

	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
		Return(&domain.Branch{ID: 7, ProjectID: 1, Name: "feature"}, nil)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(false)

	_, err := uc.Rename(permissionCtx(42), snow.ID(1), "feature", "renamed")
	require.True(t, domain.IsErrorNoPermission(err))
}

func TestBranch_Rename_ConflictsAndValidation(t *testing.T) {
	uc, perm, repo := newManageBranchFixture(t)
	ctx := permissionCtx(42)

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true).AnyTimes()
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
		Return(&domain.Branch{ID: 7, ProjectID: 1, Name: "feature"}, nil).AnyTimes()

	_, err := uc.Rename(ctx, snow.ID(1), "feature", "feature")
	require.NoError(t, err)

	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "taken").
		Return(&domain.Branch{ID: 9, ProjectID: 1, Name: "taken"}, nil)
	_, err = uc.Rename(ctx, snow.ID(1), "feature", "taken")
	require.True(t, domain.IsErrorConflict(err))

	_, err = uc.Rename(ctx, snow.ID(1), "feature", "bad/name")
	requireUserError(t, err)
}

func TestBranch_Rename_NotFound(t *testing.T) {
	uc, _, repo := newManageBranchFixture(t)

	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "ghost").Return(nil, domain.NewErrorRecordNotFound())

	_, err := uc.Rename(permissionCtx(42), snow.ID(1), "ghost", "renamed")
	require.True(t, domain.IsErrorNotFound(err))
}

func TestBranch_Delete_Success(t *testing.T) {
	uc, perm, repo := newManageBranchFixture(t)

	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
		Return(&domain.Branch{ID: 7, ProjectID: 1, Name: "feature"}, nil)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	repo.EXPECT().DeleteBranch(gomock.Any(), snow.ID(1), snow.ID(7)).Return(nil)

	require.NoError(t, uc.Delete(permissionCtx(42), snow.ID(1), "feature"))
}

func TestBranch_Delete_DefaultBranchRefused(t *testing.T) {
	uc, perm, repo := newManageBranchFixture(t)

	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{ID: 1, ProjectID: 1, Name: "main", IsDefault: true}, nil)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)

	err := uc.Delete(permissionCtx(42), snow.ID(1), "main")
	require.True(t, domain.IsErrorConflict(err))
}

func TestBranch_Delete_ProtectedNeedsAdmin(t *testing.T) {
	uc, perm, repo := newManageBranchFixture(t)

	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "release").
		Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "release", IsProtected: true}, nil)
	perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(false)

	err := uc.Delete(permissionCtx(42), snow.ID(1), "release")
	require.True(t, domain.IsErrorNoPermission(err))
}

func TestBranch_Delete_ProtectedByAdmin(t *testing.T) {
	uc, perm, repo := newManageBranchFixture(t)

	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "release").
		Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "release", IsProtected: true}, nil)
	perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true)
	repo.EXPECT().DeleteBranch(gomock.Any(), snow.ID(1), snow.ID(3)).Return(nil)

	require.NoError(t, uc.Delete(permissionCtx(42), snow.ID(1), "release"))
}

func TestBranch_Delete_NotFound(t *testing.T) {
	uc, _, repo := newManageBranchFixture(t)

	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "ghost").Return(nil, domain.NewErrorRecordNotFound())

	err := uc.Delete(permissionCtx(42), snow.ID(1), "ghost")
	require.True(t, domain.IsErrorNotFound(err))
}

func TestBranch_SetDefault(t *testing.T) {
	uc, perm, repo := newManageBranchFixture(t)

	perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "release").
		Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "release"}, nil)
	repo.EXPECT().SetDefaultBranch(gomock.Any(), snow.ID(1), snow.ID(3)).Return(nil)
	repo.EXPECT().GetByProjectIDAndID(gomock.Any(), snow.ID(1), snow.ID(3)).
		Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "release", IsDefault: true}, nil)

	got, err := uc.SetDefault(permissionCtx(42), snow.ID(1), "release")
	require.NoError(t, err)
	require.True(t, got.IsDefault)
}

func TestBranch_SetDefault_NoPermission(t *testing.T) {
	uc, perm, _ := newManageBranchFixture(t)
	perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(false)

	_, err := uc.SetDefault(permissionCtx(42), snow.ID(1), "release")
	require.True(t, domain.IsErrorNoPermission(err))
}

func TestBranch_SetProtection(t *testing.T) {
	uc, perm, repo := newManageBranchFixture(t)

	perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "release").
		Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "release"}, nil)
	repo.EXPECT().SetBranchProtection(gomock.Any(), snow.ID(1), snow.ID(3), true).Return(nil)
	repo.EXPECT().GetByProjectIDAndID(gomock.Any(), snow.ID(1), snow.ID(3)).
		Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "release", IsProtected: true}, nil)

	got, err := uc.SetProtection(permissionCtx(42), snow.ID(1), "release", true)
	require.NoError(t, err)
	require.True(t, got.IsProtected)
}

func TestBranch_SetProtection_NoPermission(t *testing.T) {
	uc, perm, _ := newManageBranchFixture(t)
	perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(false)

	_, err := uc.SetProtection(permissionCtx(42), snow.ID(1), "release", true)
	require.True(t, domain.IsErrorNoPermission(err))
}

func TestPush_ProtectedBranch_DeniedEvenForAdmins(t *testing.T) {
	uc, perm, repo, _, ctx := newPushFixture(t)

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{ID: 5, ProjectID: 1, Name: "main", IsProtected: true}, nil)

	_, err := uc.Push(ctx, snow.ID(1), "main", "", "msg", nil, nil, "", "")
	require.True(t, domain.IsErrorNoPermission(err))
	require.Contains(t, err.Error(), "merge request")
}

func TestBranch_FastForward_ProtectedAlwaysDenied(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMockbranchRepository(ctrl)

	targetHead := snow.ID(11)
	sourceHead := snow.ID(12)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{ID: 2, ProjectID: 1, Name: "main", CommitID: &targetHead, IsProtected: true}, nil)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
		Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "feature", CommitID: &sourceHead}, nil)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	_, err := uc.FastForward(context.Background(), snow.ID(1), "main", "feature")
	require.True(t, domain.IsErrorNoPermission(err))
	require.Contains(t, err.Error(), "merge request")
}

func TestBranch_FastForwardForMergeRequest_Protected(t *testing.T) {
	t.Run("project admin lands", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		perm := newAllowAllPerm(ctrl)
		repo := NewMockbranchRepository(ctrl)

		targetHead := snow.ID(11)
		sourceHead := snow.ID(12)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true)
		repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
			Return(&domain.Branch{ID: 2, ProjectID: 1, Name: "main", CommitID: &targetHead, IsProtected: true}, nil)
		repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
			Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "feature", CommitID: &sourceHead}, nil)
		repo.EXPECT().GetCommit(gomock.Any(), targetHead).
			Return(&domain.Commit{ID: targetHead, ProjectID: 1, TreeID: 101, Parent1ID: &targetHead}, nil).Times(2)
		repo.EXPECT().GetCommit(gomock.Any(), sourceHead).
			Return(&domain.Commit{ID: sourceHead, ProjectID: 1, TreeID: 102, Parent1ID: &targetHead}, nil).Times(2)
		repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).Return(&domain.TreeNode{ID: 101, Name: "root"}, nil)
		repo.EXPECT().ListFilesByTree(gomock.Any(), int64(101)).Return(nil, nil)
		repo.EXPECT().ListTreeChildren(gomock.Any(), int64(101)).Return(nil, nil)
		repo.EXPECT().GetTreeNode(gomock.Any(), int64(102)).Return(&domain.TreeNode{ID: 102, Name: "root"}, nil)
		repo.EXPECT().ListFilesByTree(gomock.Any(), int64(102)).Return(nil, nil)
		repo.EXPECT().ListTreeChildren(gomock.Any(), int64(102)).Return(nil, nil)
		repo.EXPECT().UpdateCommitIf(gomock.Any(), snow.ID(2), &targetHead, &sourceHead).Return(nil)
		repo.EXPECT().GetByProjectIDAndID(gomock.Any(), snow.ID(1), snow.ID(2)).
			Return(&domain.Branch{ID: 2, ProjectID: 1, Name: "main", CommitID: &sourceHead, IsProtected: true}, nil)

		uc := NewBranch(perm, repo, newTestBranchNode(t))
		updated, err := uc.FastForwardForMergeRequest(context.Background(), snow.ID(1), "main", "feature")
		require.NoError(t, err)
		require.Equal(t, &sourceHead, updated.CommitID)
	})

	t.Run("without project admin is denied", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		perm := newAllowAllPerm(ctrl)
		repo := NewMockbranchRepository(ctrl)

		targetHead := snow.ID(11)
		sourceHead := snow.ID(12)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(false)
		repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
			Return(&domain.Branch{ID: 2, ProjectID: 1, Name: "main", CommitID: &targetHead, IsProtected: true}, nil)
		repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
			Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "feature", CommitID: &sourceHead}, nil)

		uc := NewBranch(perm, repo, newTestBranchNode(t))
		_, err := uc.FastForwardForMergeRequest(context.Background(), snow.ID(1), "main", "feature")
		require.True(t, domain.IsErrorNoPermission(err))
	})
}
