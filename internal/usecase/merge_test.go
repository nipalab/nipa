package usecase

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

func TestBranch_FastForward_NoPermission(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMockbranchRepository(ctrl)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).
		Return(false)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	_, err := uc.FastForward(context.Background(), snow.ID(1), "main", "feature")
	require.True(t, domain.IsErrorNoPermission(err))
}

func TestBranch_FastForward_PathWriteDenied(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	targetHead := snow.ID(11)
	sourceHead := snow.ID(12)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	perm.EXPECT().HasPathAccess(gomock.Any(), snow.ID(1), "a.txt", domain.PermissionWrite).Return(true)
	perm.EXPECT().HasPathAccess(gomock.Any(), snow.ID(1), "b.txt", domain.PermissionWrite).Return(false)

	repo.EXPECT().
		GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{ID: 2, ProjectID: 1, Name: "main", CommitID: &targetHead}, nil)
	repo.EXPECT().
		GetBranchByName(gomock.Any(), snow.ID(1), "feature").
		Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "feature", CommitID: &sourceHead}, nil)
	repo.EXPECT().GetCommit(gomock.Any(), targetHead).
		Return(&domain.Commit{ID: targetHead, ProjectID: 1, TreeID: 101, Parent1ID: &targetHead}, nil).
		Times(2)
	repo.EXPECT().GetCommit(gomock.Any(), sourceHead).
		Return(&domain.Commit{ID: sourceHead, ProjectID: 1, TreeID: 102, Parent1ID: &targetHead}, nil).
		Times(2)
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).
		Return(&domain.TreeNode{ID: 101, Name: "root"}, nil)
	repo.EXPECT().ListFilesByTree(gomock.Any(), int64(101)).
		Return([]*domain.File{{ID: 1, Name: "a.txt", TreeID: 101}}, nil)
	repo.EXPECT().ListTreeChildren(gomock.Any(), int64(101)).Return(nil, nil)
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(102)).
		Return(&domain.TreeNode{ID: 102, Name: "root"}, nil)
	repo.EXPECT().ListFilesByTree(gomock.Any(), int64(102)).
		Return([]*domain.File{{ID: 2, Name: "b.txt", TreeID: 102}}, nil)
	repo.EXPECT().ListTreeChildren(gomock.Any(), int64(102)).Return(nil, nil)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	_, err := uc.FastForward(context.Background(), snow.ID(1), "main", "feature")
	require.True(t, domain.IsErrorNoPermission(err))
}

func TestBranch_FastForward_TargetNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMockbranchRepository(ctrl)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).
		Return(true)
	repo.EXPECT().
		GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(nil, domain.NewErrorRecordNotFound())

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	_, err := uc.FastForward(context.Background(), snow.ID(1), "main", "feature")
	require.Equal(t, `branch "main" not found`, err.Error())
}

func TestBranch_FastForward_SourceNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMockbranchRepository(ctrl)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).
		Return(true)
	gomock.InOrder(
		repo.EXPECT().
			GetBranchByName(gomock.Any(), snow.ID(1), "main").
			Return(&domain.Branch{ID: 2, ProjectID: 1, Name: "main"}, nil),
		repo.EXPECT().
			GetBranchByName(gomock.Any(), snow.ID(1), "feature").
			Return(nil, domain.NewErrorRecordNotFound()),
	)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	_, err := uc.FastForward(context.Background(), snow.ID(1), "main", "feature")
	require.Equal(t, `branch "feature" not found`, err.Error())
}

func TestBranch_FastForward_SourceEmpty(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMockbranchRepository(ctrl)

	head := snow.ID(11)
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).
		Return(true)
	gomock.InOrder(
		repo.EXPECT().
			GetBranchByName(gomock.Any(), snow.ID(1), "main").
			Return(&domain.Branch{ID: 2, ProjectID: 1, Name: "main", CommitID: &head}, nil),
		repo.EXPECT().
			GetBranchByName(gomock.Any(), snow.ID(1), "feature").
			Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "feature"}, nil),
	)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	_, err := uc.FastForward(context.Background(), snow.ID(1), "main", "feature")
	require.True(t, domain.IsErrorConflict(err))
	require.Contains(t, err.Error(), "no commits")
}

func TestBranch_FastForward_TargetEmpty(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMockbranchRepository(ctrl)

	head := snow.ID(11)
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).
		Return(true)
	gomock.InOrder(
		repo.EXPECT().
			GetBranchByName(gomock.Any(), snow.ID(1), "main").
			Return(&domain.Branch{ID: 2, ProjectID: 1, Name: "main"}, nil),
		repo.EXPECT().
			GetBranchByName(gomock.Any(), snow.ID(1), "feature").
			Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "feature", CommitID: &head}, nil),
	)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	_, err := uc.FastForward(context.Background(), snow.ID(1), "main", "feature")
	require.True(t, domain.IsErrorConflict(err))
	require.Contains(t, err.Error(), "empty branch")
}

func TestBranch_FastForward_Diverged(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMockbranchRepository(ctrl)

	base := snow.ID(10)
	targetHead := snow.ID(11)
	sourceHead := snow.ID(12)
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).
		Return(true)
	gomock.InOrder(
		repo.EXPECT().
			GetBranchByName(gomock.Any(), snow.ID(1), "main").
			Return(&domain.Branch{ID: 2, ProjectID: 1, Name: "main", CommitID: &targetHead}, nil),
		repo.EXPECT().
			GetBranchByName(gomock.Any(), snow.ID(1), "feature").
			Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "feature", CommitID: &sourceHead}, nil),
		repo.EXPECT().GetCommit(gomock.Any(), targetHead).
			Return(&domain.Commit{ID: targetHead, TreeID: 101, Parent1ID: &base}, nil),
		repo.EXPECT().GetCommit(gomock.Any(), sourceHead).
			Return(&domain.Commit{ID: sourceHead, TreeID: 102, Parent1ID: &base}, nil),
	)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	_, err := uc.FastForward(context.Background(), snow.ID(1), "main", "feature")
	require.True(t, domain.IsErrorConflict(err))
	require.Contains(t, err.Error(), "diverged")
}

func TestBranch_FastForward_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMockbranchRepository(ctrl)

	targetHead := snow.ID(11)
	sourceHead := snow.ID(12)
	target := &domain.Branch{ID: 2, ProjectID: 1, Name: "main", CommitID: &targetHead}
	updated := &domain.Branch{ID: 2, ProjectID: 1, Name: "main", CommitID: &sourceHead}
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).
		Return(true)
	repo.EXPECT().
		GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(target, nil)
	repo.EXPECT().
		GetBranchByName(gomock.Any(), snow.ID(1), "feature").
		Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "feature", CommitID: &sourceHead}, nil)
	repo.EXPECT().GetCommit(gomock.Any(), targetHead).
		Return(&domain.Commit{ID: targetHead, ProjectID: 1, TreeID: 101, Parent1ID: &targetHead}, nil).
		Times(2)
	repo.EXPECT().GetCommit(gomock.Any(), sourceHead).
		Return(&domain.Commit{ID: sourceHead, ProjectID: 1, TreeID: 102, Parent1ID: &targetHead}, nil).
		Times(2)
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).
		Return(&domain.TreeNode{ID: 101, Name: "root"}, nil)
	repo.EXPECT().ListFilesByTree(gomock.Any(), int64(101)).
		Return([]*domain.File{{ID: 1, Name: "a.txt", TreeID: 101}}, nil)
	repo.EXPECT().ListTreeChildren(gomock.Any(), int64(101)).Return(nil, nil)
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(102)).
		Return(&domain.TreeNode{ID: 102, Name: "root"}, nil)
	repo.EXPECT().ListFilesByTree(gomock.Any(), int64(102)).
		Return([]*domain.File{{ID: 2, Name: "b.txt", TreeID: 102}}, nil)
	repo.EXPECT().ListTreeChildren(gomock.Any(), int64(102)).Return(nil, nil)
	repo.EXPECT().UpdateCommitIf(gomock.Any(), snow.ID(2), &targetHead, &sourceHead).Return(nil)
	repo.EXPECT().GetByProjectIDAndID(gomock.Any(), snow.ID(1), snow.ID(2)).Return(updated, nil)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	got, err := uc.FastForward(context.Background(), snow.ID(1), "main", "feature")
	require.NoError(t, err)
	require.Equal(t, updated, got)
}

func TestBranch_FastForward_UpdateConflict(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMockbranchRepository(ctrl)

	targetHead := snow.ID(11)
	sourceHead := snow.ID(12)
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).
		Return(true)
	repo.EXPECT().
		GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{ID: 2, ProjectID: 1, Name: "main", CommitID: &targetHead}, nil)
	repo.EXPECT().
		GetBranchByName(gomock.Any(), snow.ID(1), "feature").
		Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "feature", CommitID: &sourceHead}, nil)
	repo.EXPECT().GetCommit(gomock.Any(), targetHead).
		Return(&domain.Commit{ID: targetHead, ProjectID: 1, TreeID: 101, Parent1ID: &targetHead}, nil).
		Times(2)
	repo.EXPECT().GetCommit(gomock.Any(), sourceHead).
		Return(&domain.Commit{ID: sourceHead, ProjectID: 1, TreeID: 102, Parent1ID: &targetHead}, nil).
		Times(2)
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).
		Return(&domain.TreeNode{ID: 101, Name: "root"}, nil)
	repo.EXPECT().ListFilesByTree(gomock.Any(), int64(101)).
		Return([]*domain.File{{ID: 1, Name: "a.txt", TreeID: 101}}, nil)
	repo.EXPECT().ListTreeChildren(gomock.Any(), int64(101)).Return(nil, nil)
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(102)).
		Return(&domain.TreeNode{ID: 102, Name: "root"}, nil)
	repo.EXPECT().ListFilesByTree(gomock.Any(), int64(102)).
		Return([]*domain.File{{ID: 2, Name: "b.txt", TreeID: 102}}, nil)
	repo.EXPECT().ListTreeChildren(gomock.Any(), int64(102)).Return(nil, nil)
	repo.EXPECT().UpdateCommitIf(gomock.Any(), snow.ID(2), &targetHead, &sourceHead).
		Return(domain.NewErrorConflict("branch has moved; refresh and try again"))

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	_, err := uc.FastForward(context.Background(), snow.ID(1), "main", "feature")
	require.True(t, domain.IsErrorConflict(err))
}

func TestBranch_GetMergeBase_NoPermission(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMockbranchRepository(ctrl)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(false)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	_, err := uc.GetMergeBase(context.Background(), snow.ID(1), MergeRef{BranchName: "main"}, MergeRef{BranchName: "feature"})
	require.True(t, domain.IsErrorNoPermission(err))
}

func TestBranch_GetMergeBase_TargetBranchNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMockbranchRepository(ctrl)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)

	repo.EXPECT().
		GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(nil, domain.NewErrorRecordNotFound())

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	_, err := uc.GetMergeBase(context.Background(), snow.ID(1), MergeRef{BranchName: "main"}, MergeRef{BranchName: "feature"})
	require.Error(t, err)
	require.True(t, domain.IsErrorNotFound(err))
	require.Equal(t, `branch "main" not found`, err.Error())
}

func TestBranch_GetMergeBase_SourceBranchNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMockbranchRepository(ctrl)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)

	gomock.InOrder(
		repo.EXPECT().
			GetBranchByName(gomock.Any(), snow.ID(1), "main").
			Return(&domain.Branch{ID: 2, ProjectID: 1, Name: "main"}, nil),
		repo.EXPECT().
			GetBranchByName(gomock.Any(), snow.ID(1), "feature").
			Return(nil, domain.NewErrorRecordNotFound()),
	)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	_, err := uc.GetMergeBase(context.Background(), snow.ID(1), MergeRef{BranchName: "main"}, MergeRef{BranchName: "feature"})
	require.Error(t, err)
	require.True(t, domain.IsErrorNotFound(err))
	require.Equal(t, `branch "feature" not found`, err.Error())
}

func TestBranch_GetMergeBase_BothHeadsEmpty(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMockbranchRepository(ctrl)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)

	gomock.InOrder(
		repo.EXPECT().
			GetBranchByName(gomock.Any(), snow.ID(1), "main").
			Return(&domain.Branch{ID: 2, ProjectID: 1, Name: "main"}, nil),
		repo.EXPECT().
			GetBranchByName(gomock.Any(), snow.ID(1), "feature").
			Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "feature"}, nil),
	)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	info, err := uc.GetMergeBase(context.Background(), snow.ID(1), MergeRef{BranchName: "main"}, MergeRef{BranchName: "feature"})
	require.NoError(t, err)
	require.Equal(t, "main", info.TargetBranch)
	require.Equal(t, "feature", info.SourceBranch)
	require.Nil(t, info.TargetCommitID)
	require.Nil(t, info.SourceCommitID)
	require.Nil(t, info.MergeBaseCommitID)
	require.Nil(t, info.MergeBaseTree)
}

func TestBranch_GetMergeBase_EmptySource(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMockbranchRepository(ctrl)

	head := snow.ID(11)
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)

	gomock.InOrder(
		repo.EXPECT().
			GetBranchByName(gomock.Any(), snow.ID(1), "main").
			Return(&domain.Branch{ID: 2, ProjectID: 1, Name: "main", CommitID: &head}, nil),
		repo.EXPECT().
			GetBranchByName(gomock.Any(), snow.ID(1), "feature").
			Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "feature"}, nil),
		repo.EXPECT().GetCommit(gomock.Any(), head).Return(&domain.Commit{ID: head, TreeID: 101, Hash: domain.Hash{7}}, nil),
	)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	info, err := uc.GetMergeBase(context.Background(), snow.ID(1), MergeRef{BranchName: "main"}, MergeRef{BranchName: "feature"})
	require.NoError(t, err)
	require.Equal(t, &head, info.TargetCommitID)
	require.Nil(t, info.SourceCommitID)
	require.Nil(t, info.MergeBaseCommitID)
	require.Nil(t, info.MergeBaseTree)
	require.NotNil(t, info.TargetCommitHash)
	require.Equal(t, domain.Hash{7}, *info.TargetCommitHash)
	require.Nil(t, info.SourceCommitHash)
}

func TestBranch_GetMergeBase_SameHead(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMockbranchRepository(ctrl)

	head := snow.ID(11)
	root := &domain.TreeNode{ID: 100, Name: "root"}
	rootFiles := []*domain.File{{ID: 1, Name: "a.txt"}}
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)

	gomock.InOrder(
		repo.EXPECT().
			GetBranchByName(gomock.Any(), snow.ID(1), "main").
			Return(&domain.Branch{ID: 2, ProjectID: 1, Name: "main", CommitID: &head}, nil),
		repo.EXPECT().
			GetBranchByName(gomock.Any(), snow.ID(1), "feature").
			Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "feature", CommitID: &head}, nil),
		// target head hash, then source head hash.
		repo.EXPECT().GetCommit(gomock.Any(), head).Return(&domain.Commit{ID: head, TreeID: 100, Hash: domain.Hash{1}}, nil),
		repo.EXPECT().GetCommit(gomock.Any(), head).Return(&domain.Commit{ID: head, TreeID: 100, Hash: domain.Hash{1}}, nil),
		// commitTreeManifest on the (equal) base.
		repo.EXPECT().GetCommit(gomock.Any(), head).Return(&domain.Commit{ID: head, TreeID: 100, Hash: domain.Hash{1}}, nil),
		repo.EXPECT().GetTreeNode(gomock.Any(), int64(100)).Return(root, nil),
		repo.EXPECT().ListFilesByTree(gomock.Any(), int64(100)).Return(rootFiles, nil),
		repo.EXPECT().ListTreeChildren(gomock.Any(), int64(100)).Return(nil, nil),
	)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	info, err := uc.GetMergeBase(context.Background(), snow.ID(1), MergeRef{BranchName: "main"}, MergeRef{BranchName: "feature"})
	require.NoError(t, err)
	require.Equal(t, &head, info.TargetCommitID)
	require.Equal(t, &head, info.SourceCommitID)
	require.Equal(t, &head, info.MergeBaseCommitID)
	require.NotNil(t, info.TargetCommitHash)
	require.Equal(t, domain.Hash{1}, *info.TargetCommitHash)
	require.Equal(t, domain.Hash{1}, *info.SourceCommitHash)
	require.Equal(t, root, info.MergeBaseTree)
	require.Equal(t, rootFiles, info.MergeBaseTree.FileChildren)
}

func TestBranch_GetMergeBase_ForkedBranches(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMockbranchRepository(ctrl)

	base := snow.ID(10)
	targetHead := snow.ID(11)
	sourceHead := snow.ID(12)
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)

	gomock.InOrder(
		repo.EXPECT().
			GetBranchByName(gomock.Any(), snow.ID(1), "main").
			Return(&domain.Branch{ID: 2, ProjectID: 1, Name: "main", CommitID: &targetHead}, nil),
		repo.EXPECT().
			GetBranchByName(gomock.Any(), snow.ID(1), "feature").
			Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "feature", CommitID: &sourceHead}, nil),
		// head hashes.
		repo.EXPECT().GetCommit(gomock.Any(), targetHead).
			Return(&domain.Commit{ID: targetHead, TreeID: 101, Hash: domain.Hash{8}, Parent1ID: &base}, nil),
		repo.EXPECT().GetCommit(gomock.Any(), sourceHead).
			Return(&domain.Commit{ID: sourceHead, TreeID: 102, Hash: domain.Hash{9}, Parent1ID: &base}, nil),
		// findMergeBase: walk target side first, then source side finds base.
		repo.EXPECT().GetCommit(gomock.Any(), targetHead).
			Return(&domain.Commit{ID: targetHead, TreeID: 101, Hash: domain.Hash{8}, Parent1ID: &base}, nil),
		repo.EXPECT().GetCommit(gomock.Any(), sourceHead).
			Return(&domain.Commit{ID: sourceHead, TreeID: 102, Hash: domain.Hash{9}, Parent1ID: &base}, nil),
		// merge base tree manifest (recursive).
		repo.EXPECT().GetCommit(gomock.Any(), base).Return(&domain.Commit{ID: base, TreeID: 100}, nil),
		repo.EXPECT().GetTreeNode(gomock.Any(), int64(100)).Return(&domain.TreeNode{ID: 100, Name: "root"}, nil),
		repo.EXPECT().ListFilesByTree(gomock.Any(), int64(100)).Return(nil, nil),
		repo.EXPECT().ListTreeChildren(gomock.Any(), int64(100)).Return(nil, nil),
	)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	info, err := uc.GetMergeBase(context.Background(), snow.ID(1), MergeRef{BranchName: "main"}, MergeRef{BranchName: "feature"})
	require.NoError(t, err)
	require.Equal(t, &targetHead, info.TargetCommitID)
	require.Equal(t, &sourceHead, info.SourceCommitID)
	require.Equal(t, &base, info.MergeBaseCommitID)
	require.Equal(t, domain.Hash{8}, *info.TargetCommitHash)
	require.Equal(t, domain.Hash{9}, *info.SourceCommitHash)
	require.NotNil(t, info.MergeBaseTree)
	require.Equal(t, "root", info.MergeBaseTree.Name)
}

func TestBranch_GetMergeBase_WalkError(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMockbranchRepository(ctrl)

	targetHead := snow.ID(11)
	sourceHead := snow.ID(12)
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)

	gomock.InOrder(
		repo.EXPECT().
			GetBranchByName(gomock.Any(), snow.ID(1), "main").
			Return(&domain.Branch{ID: 2, ProjectID: 1, Name: "main", CommitID: &targetHead}, nil),
		repo.EXPECT().
			GetBranchByName(gomock.Any(), snow.ID(1), "feature").
			Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "feature", CommitID: &sourceHead}, nil),
		repo.EXPECT().GetCommit(gomock.Any(), targetHead).
			Return(nil, domain.NewErrorDatabase("boom")),
	)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	_, err := uc.GetMergeBase(context.Background(), snow.ID(1), MergeRef{BranchName: "main"}, MergeRef{BranchName: "feature"})
	require.Error(t, err)
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 500, domErr.Code)
}

func TestBranch_GetMergeBase_TreeLoadError(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMockbranchRepository(ctrl)

	base := snow.ID(10)
	targetHead := snow.ID(11)
	sourceHead := snow.ID(12)
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)

	gomock.InOrder(
		repo.EXPECT().
			GetBranchByName(gomock.Any(), snow.ID(1), "main").
			Return(&domain.Branch{ID: 2, ProjectID: 1, Name: "main", CommitID: &targetHead}, nil),
		repo.EXPECT().
			GetBranchByName(gomock.Any(), snow.ID(1), "feature").
			Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "feature", CommitID: &sourceHead}, nil),
		// head hashes, then the merge base tree lookup fails.
		repo.EXPECT().GetCommit(gomock.Any(), targetHead).
			Return(&domain.Commit{ID: targetHead, TreeID: 101, Parent1ID: &base}, nil),
		repo.EXPECT().GetCommit(gomock.Any(), sourceHead).
			Return(&domain.Commit{ID: sourceHead, TreeID: 102, Parent1ID: &base}, nil),
		repo.EXPECT().GetCommit(gomock.Any(), targetHead).
			Return(&domain.Commit{ID: targetHead, TreeID: 101, Parent1ID: &base}, nil),
		repo.EXPECT().GetCommit(gomock.Any(), sourceHead).
			Return(&domain.Commit{ID: sourceHead, TreeID: 102, Parent1ID: &base}, nil),
		repo.EXPECT().GetCommit(gomock.Any(), base).Return(&domain.Commit{ID: base, TreeID: 100}, nil),
		repo.EXPECT().GetTreeNode(gomock.Any(), int64(100)).
			Return(nil, domain.NewErrorDatabase("boom")),
	)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	_, err := uc.GetMergeBase(context.Background(), snow.ID(1), MergeRef{BranchName: "main"}, MergeRef{BranchName: "feature"})
	require.Error(t, err)
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 500, domErr.Code)
}

func TestBranch_GetMergeBase_CommitRefs(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMockbranchRepository(ctrl)

	base := snow.ID(10)
	targetID := snow.ID(11)
	sourceID := snow.ID(12)
	targetCommit := &domain.Commit{ID: targetID, ProjectID: 1, TreeID: 101, Hash: domain.Hash{8}, Parent1ID: &base}
	sourceCommit := &domain.Commit{ID: sourceID, ProjectID: 1, TreeID: 102, Hash: domain.Hash{9}, Parent1ID: &base}

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)

	gomock.InOrder(
		repo.EXPECT().GetCommit(gomock.Any(), targetID).Return(targetCommit, nil),
		repo.EXPECT().GetCommit(gomock.Any(), sourceID).Return(sourceCommit, nil),
		repo.EXPECT().GetCommit(gomock.Any(), targetID).Return(targetCommit, nil),
		repo.EXPECT().GetCommit(gomock.Any(), sourceID).Return(sourceCommit, nil),
		repo.EXPECT().GetCommit(gomock.Any(), base).Return(&domain.Commit{ID: base, TreeID: 100}, nil),
		repo.EXPECT().GetTreeNode(gomock.Any(), int64(100)).Return(&domain.TreeNode{ID: 100, Name: "root"}, nil),
		repo.EXPECT().ListFilesByTree(gomock.Any(), int64(100)).Return(nil, nil),
		repo.EXPECT().ListTreeChildren(gomock.Any(), int64(100)).Return(nil, nil),
	)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	info, err := uc.GetMergeBase(context.Background(), snow.ID(1),
		MergeRef{CommitID: &targetID}, MergeRef{CommitID: &sourceID})
	require.NoError(t, err)
	require.Empty(t, info.TargetBranch)
	require.Empty(t, info.SourceBranch)
	require.Equal(t, &targetID, info.TargetCommitID)
	require.Equal(t, &sourceID, info.SourceCommitID)
	require.Equal(t, &base, info.MergeBaseCommitID)
	require.Equal(t, domain.Hash{8}, *info.TargetCommitHash)
	require.Equal(t, domain.Hash{9}, *info.SourceCommitHash)
	require.NotNil(t, info.MergeBaseTree)
}

func TestBranch_GetMergeBase_CommitRefNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMockbranchRepository(ctrl)

	missing := snow.ID(99)
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)
	repo.EXPECT().
		GetCommit(gomock.Any(), missing).
		Return(nil, domain.NewErrorRecordNotFound())

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	_, err := uc.GetMergeBase(context.Background(), snow.ID(1), MergeRef{CommitID: &missing}, MergeRef{BranchName: "feature"})
	require.True(t, domain.IsErrorNotFound(err))
	require.Equal(t, "commit "+missing.Base36()+" not found", err.Error())
}

func TestBranch_GetMergeBase_CommitRefWrongProject(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMockbranchRepository(ctrl)

	other := snow.ID(99)
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)
	repo.EXPECT().
		GetCommit(gomock.Any(), other).
		Return(&domain.Commit{ID: other, ProjectID: 2, TreeID: 100}, nil)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	_, err := uc.GetMergeBase(context.Background(), snow.ID(1), MergeRef{CommitID: &other}, MergeRef{BranchName: "feature"})
	require.True(t, domain.IsErrorNotFound(err))
}
