package usecase

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

func TestBranch_FastForward_FileLocks(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMockbranchRepository(ctrl)
	gate := NewMockfileLockGate(ctrl)

	targetHead := snow.ID(11)
	sourceHead := snow.ID(12)
	target := &domain.Branch{ID: 2, ProjectID: 1, Name: "main", CommitID: &targetHead}
	updated := &domain.Branch{ID: 2, ProjectID: 1, Name: "main", CommitID: &sourceHead}

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(target, nil).AnyTimes()
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
		Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "feature", CommitID: &sourceHead}, nil).AnyTimes()
	repo.EXPECT().GetCommit(gomock.Any(), targetHead).
		Return(&domain.Commit{ID: targetHead, ProjectID: 1, TreeID: 101, Parent1ID: &targetHead}, nil).AnyTimes()
	repo.EXPECT().GetCommit(gomock.Any(), sourceHead).
		Return(&domain.Commit{ID: sourceHead, ProjectID: 1, TreeID: 102, Parent1ID: &targetHead}, nil).AnyTimes()
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).
		Return(&domain.TreeNode{ID: 101, Name: "root"}, nil).AnyTimes()
	repo.EXPECT().ListFilesByTree(gomock.Any(), int64(101)).
		Return([]*domain.File{{ID: 1, Name: "a.txt", TreeID: 101}}, nil).AnyTimes()
	repo.EXPECT().ListTreeChildren(gomock.Any(), int64(101)).Return(nil, nil).AnyTimes()
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(102)).
		Return(&domain.TreeNode{ID: 102, Name: "root"}, nil).AnyTimes()
	repo.EXPECT().ListFilesByTree(gomock.Any(), int64(102)).
		Return([]*domain.File{{ID: 2, Name: "tex.png", TreeID: 102, IsBinary: true}}, nil).AnyTimes()
	repo.EXPECT().ListTreeChildren(gomock.Any(), int64(102)).Return(nil, nil).AnyTimes()

	gomock.InOrder(
		gate.EXPECT().EnsureLocks(gomock.Any(), snow.ID(1), target, []string{"tex.png"}, snow.ID(7)).Return(nil),
		repo.EXPECT().UpdateCommitIf(gomock.Any(), snow.ID(2), &targetHead, &sourceHead).Return(nil),
		gate.EXPECT().ReleaseLanded(gomock.Any(), snow.ID(1), target, []string{"tex.png"}, snow.ID(7)).Return(nil),
		repo.EXPECT().GetByProjectIDAndID(gomock.Any(), snow.ID(1), snow.ID(2)).Return(updated, nil),
	)

	uc := NewBranch(perm, repo, newTestBranchNode(t)).WithFileLocks(gate)
	got, err := uc.FastForward(permissionCtx(7), snow.ID(1), "main", "feature")
	require.NoError(t, err)
	require.Equal(t, updated, got)
}

func TestBranch_FastForward_FileLocksReject(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMockbranchRepository(ctrl)
	gate := NewMockfileLockGate(ctrl)

	targetHead := snow.ID(11)
	sourceHead := snow.ID(12)
	target := &domain.Branch{ID: 2, ProjectID: 1, Name: "main", CommitID: &targetHead}

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(target, nil).AnyTimes()
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
		Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "feature", CommitID: &sourceHead}, nil).AnyTimes()
	repo.EXPECT().GetCommit(gomock.Any(), targetHead).
		Return(&domain.Commit{ID: targetHead, ProjectID: 1, TreeID: 101, Parent1ID: &targetHead}, nil).AnyTimes()
	repo.EXPECT().GetCommit(gomock.Any(), sourceHead).
		Return(&domain.Commit{ID: sourceHead, ProjectID: 1, TreeID: 102, Parent1ID: &targetHead}, nil).AnyTimes()
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).
		Return(&domain.TreeNode{ID: 101, Name: "root"}, nil).AnyTimes()
	repo.EXPECT().ListFilesByTree(gomock.Any(), int64(101)).Return(nil, nil).AnyTimes()
	repo.EXPECT().ListTreeChildren(gomock.Any(), int64(101)).Return(nil, nil).AnyTimes()
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(102)).
		Return(&domain.TreeNode{ID: 102, Name: "root"}, nil).AnyTimes()
	repo.EXPECT().ListFilesByTree(gomock.Any(), int64(102)).
		Return([]*domain.File{{ID: 2, Name: "tex.png", TreeID: 102, IsBinary: true}}, nil).AnyTimes()
	repo.EXPECT().ListTreeChildren(gomock.Any(), int64(102)).Return(nil, nil).AnyTimes()

	wantErr := domain.NewErrorConflict("binary file \"tex.png\" requires a lock")
	gate.EXPECT().EnsureLocks(gomock.Any(), snow.ID(1), target, []string{"tex.png"}, snow.ID(7)).Return(wantErr)

	uc := NewBranch(perm, repo, newTestBranchNode(t)).WithFileLocks(gate)
	_, err := uc.FastForward(permissionCtx(7), snow.ID(1), "main", "feature")
	require.ErrorIs(t, err, wantErr)
}

func TestBranch_Delete_ReleasesLocks(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)
	gate := NewMockfileLockGate(ctrl)

	branch := &domain.Branch{ID: 3, ProjectID: 1, Name: "feature"}
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(branch, nil)
	repo.EXPECT().HasOpenMergeRequests(gomock.Any(), snow.ID(1), snow.ID(3)).Return(false, nil)
	gomock.InOrder(
		repo.EXPECT().DeleteBranch(gomock.Any(), snow.ID(1), snow.ID(3)).Return(nil),
		gate.EXPECT().ReleaseBranch(gomock.Any(), snow.ID(1), snow.ID(3)).Return(nil),
	)

	uc := NewBranch(perm, repo, newTestBranchNode(t)).WithFileLocks(gate)
	require.NoError(t, uc.Delete(permissionCtx(7), snow.ID(1), "feature"))
}

func TestMergeRequest_Create_FileLocks(t *testing.T) {
	mr, repo, branchRepo, perm, merger := newTestMergeRequest(t)
	gate := NewMockfileLockGate(gomock.NewController(t))
	ctx := permissionCtx(7)
	sourceHead, targetHead := snow.ID(11), snow.ID(12)

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
		Return(&domain.Branch{ID: 3, ProjectID: 1, CommitID: &sourceHead}, nil)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{ID: 2, ProjectID: 1, CommitID: &targetHead}, nil)
	merger.EXPECT().GetMergeBase(gomock.Any(), snow.ID(1), MergeRef{CommitID: &targetHead}, MergeRef{CommitID: &sourceHead}).
		Return(&MergeBaseInfo{MergeBaseCommitID: &targetHead}, nil)
	merger.EXPECT().BinaryChangesBetween(gomock.Any(), snow.ID(1), &targetHead, &sourceHead).
		Return([]string{"tex.png"}, nil)
	gate.EXPECT().EnsureMergeRequestLocks(gomock.Any(), snow.ID(1), gomock.Any(), gomock.Any(), []string{"tex.png"}, snow.ID(7)).
		Return(nil)
	repo.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, created domain.MergeRequest) (*domain.MergeRequest, error) {
			require.NotZero(t, created.ID)
			return &created, nil
		},
	)

	created, err := mr.WithFileLocks(gate).Create(ctx, snow.ID(1), "Hero art", "", "feature", "main")
	require.NoError(t, err)
	require.Equal(t, "Hero art", created.Title)
}

func TestMergeRequest_Create_FileLockConflictReleasesPartial(t *testing.T) {
	mr, _, branchRepo, perm, merger := newTestMergeRequest(t)
	gate := NewMockfileLockGate(gomock.NewController(t))
	ctx := permissionCtx(7)
	sourceHead, targetHead := snow.ID(11), snow.ID(12)

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
		Return(&domain.Branch{ID: 3, ProjectID: 1, CommitID: &sourceHead}, nil)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{ID: 2, ProjectID: 1, CommitID: &targetHead}, nil)
	merger.EXPECT().GetMergeBase(gomock.Any(), snow.ID(1), MergeRef{CommitID: &targetHead}, MergeRef{CommitID: &sourceHead}).
		Return(&MergeBaseInfo{MergeBaseCommitID: &targetHead}, nil)
	merger.EXPECT().BinaryChangesBetween(gomock.Any(), snow.ID(1), &targetHead, &sourceHead).
		Return([]string{"tex.png"}, nil)
	gate.EXPECT().EnsureMergeRequestLocks(gomock.Any(), snow.ID(1), gomock.Any(), gomock.Any(), []string{"tex.png"}, snow.ID(7)).
		Return(domain.NewErrorConflict("locked by bob"))
	gate.EXPECT().ReleaseForMergeRequest(gomock.Any(), snow.ID(1), gomock.Any()).Return(nil)

	_, err := mr.WithFileLocks(gate).Create(ctx, snow.ID(1), "Hero art", "", "feature", "main")
	require.True(t, domain.IsErrorConflict(err))
}

func TestMergeRequest_Merge_FileLocks(t *testing.T) {
	mr, repo, branchRepo, perm, merger := newTestMergeRequest(t)
	gate := NewMockfileLockGate(gomock.NewController(t))
	sourceHead, targetHead := snow.ID(11), snow.ID(12)

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true).AnyTimes()
	repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil).Times(2)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(branchWithHead(3, sourceHead), nil).AnyTimes()
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(branchWithHead(2, targetHead), nil).AnyTimes()
	merger.EXPECT().GetMergeBase(gomock.Any(), snow.ID(1), MergeRef{CommitID: &targetHead}, MergeRef{CommitID: &sourceHead}).
		Return(&MergeBaseInfo{MergeBaseCommitID: &targetHead}, nil)
	merger.EXPECT().BinaryChangesBetween(gomock.Any(), snow.ID(1), &targetHead, &sourceHead).
		Return([]string{"tex.png"}, nil)
	gate.EXPECT().EnsureMergeRequestLocks(gomock.Any(), snow.ID(1), snow.ID(5), gomock.Any(), []string{"tex.png"}, snow.ID(7)).
		Return(nil)
	merger.EXPECT().FastForwardForMergeRequest(gomock.Any(), snow.ID(1), "main", "feature").
		Return(&domain.Branch{ID: 2, ProjectID: 1, Name: "main", CommitID: &sourceHead}, nil)
	gomock.InOrder(
		repo.EXPECT().UpdateStatus(gomock.Any(), snow.ID(1), int64(5), domain.MergeRequestMerged, &sourceHead).Return(nil),
		gate.EXPECT().ReleaseForMergeRequest(gomock.Any(), snow.ID(1), snow.ID(5)).Return(nil),
	)

	merged, _, err := mr.WithFileLocks(gate).Merge(permissionCtx(7), snow.ID(1), 5)
	require.NoError(t, err)
	require.Equal(t, openMergeRequest().ID, merged.ID)
}

func TestMergeRequest_Close_ReleasesLocks(t *testing.T) {
	mr, repo, _, perm, _ := newTestMergeRequest(t)
	gate := NewMockfileLockGate(gomock.NewController(t))

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true).AnyTimes()
	repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
	repo.EXPECT().UpdateStatus(gomock.Any(), snow.ID(1), int64(5), domain.MergeRequestClosed, nil).Return(nil)
	closed := openMergeRequest()
	closed.Status = domain.MergeRequestClosed
	repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(closed, nil)
	gate.EXPECT().ReleaseForMergeRequest(gomock.Any(), snow.ID(1), snow.ID(5)).Return(nil)

	got, err := mr.WithFileLocks(gate).Close(permissionCtx(7), snow.ID(1), 5)
	require.NoError(t, err)
	require.Equal(t, domain.MergeRequestClosed, got.Status)
}
