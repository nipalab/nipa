package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/treehash"
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
	expectTreeRows(repo, map[int64][]*domain.File{101: []*domain.File{{ID: 1, Name: "a.txt", TreeID: 101}}, 102: []*domain.File{{ID: 2, Name: "tex.png", TreeID: 102, IsBinary: true}}}, map[int64][]*domain.TreeNode{})
	repo.EXPECT().ListFileMetaByTree(gomock.Any(), int64(101)).
		Return([]*domain.File{{ID: 1, Name: "a.txt", TreeID: 101}}, nil).AnyTimes()
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(102)).
		Return(&domain.TreeNode{ID: 102, Name: "root"}, nil).AnyTimes()
	repo.EXPECT().ListFileMetaByTree(gomock.Any(), int64(102)).
		Return([]*domain.File{{ID: 2, Name: "tex.png", TreeID: 102, IsBinary: true}}, nil).AnyTimes()

	gomock.InOrder(
		gate.EXPECT().EnsureLocks(gomock.Any(), snow.ID(1), target, nil, []string{"tex.png"}, snow.ID(7)).Return(nil),
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
	expectTreeRows(repo, map[int64][]*domain.File{102: []*domain.File{{ID: 2, Name: "tex.png", TreeID: 102, IsBinary: true}}}, map[int64][]*domain.TreeNode{})
	repo.EXPECT().ListFileMetaByTree(gomock.Any(), int64(101)).Return(nil, nil).AnyTimes()
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(102)).
		Return(&domain.TreeNode{ID: 102, Name: "root"}, nil).AnyTimes()
	repo.EXPECT().ListFileMetaByTree(gomock.Any(), int64(102)).
		Return([]*domain.File{{ID: 2, Name: "tex.png", TreeID: 102, IsBinary: true}}, nil).AnyTimes()

	wantErr := domain.NewErrorConflict("binary file \"tex.png\" requires a lock")
	gate.EXPECT().EnsureLocks(gomock.Any(), snow.ID(1), target, nil, []string{"tex.png"}, snow.ID(7)).Return(wantErr)

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
	repo.EXPECT().ListBranches(gomock.Any(), snow.ID(1), 2, nil, snow.ID(0)).
		Return([]*domain.Branch{branch, {ID: 1, ProjectID: 1, Name: "main", IsDefault: true}}, nil)
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
	merger.EXPECT().GetMergeBaseCommits(gomock.Any(), snow.ID(1), MergeRef{CommitID: &targetHead}, MergeRef{CommitID: &sourceHead}).
		Return(&MergeBaseInfo{MergeBaseCommitID: &targetHead}, nil)
	merger.EXPECT().BinaryChangesBetween(gomock.Any(), snow.ID(1), &targetHead, &sourceHead).
		Return([]string{"tex.png"}, nil)
	gate.EXPECT().EnsureMergeRequestLocks(gomock.Any(), snow.ID(1), gomock.Any(), gomock.Any(), []string{"tex.png"}, snow.ID(7), snow.ID(7)).
		Return(nil)
	repo.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, created domain.MergeRequest) (*domain.MergeRequest, error) {
			require.NotZero(t, created.ID)
			return &created, nil
		},
	)

	created, err := mr.WithFileLocks(gate).Create(ctx, snow.ID(1), CreateMergeRequestInput{Title: "Hero art", SourceBranch: "feature", TargetBranch: "main"})
	require.NoError(t, err)
	require.Equal(t, "Hero art", created.Title)
}

func TestMergeRequest_Create_FileLockConflict(t *testing.T) {
	mr, repo, branchRepo, perm, merger := newTestMergeRequest(t)
	gate := NewMockfileLockGate(gomock.NewController(t))
	ctx := permissionCtx(7)
	sourceHead, targetHead := snow.ID(11), snow.ID(12)

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
		Return(&domain.Branch{ID: 3, ProjectID: 1, CommitID: &sourceHead}, nil)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{ID: 2, ProjectID: 1, CommitID: &targetHead}, nil)
	merger.EXPECT().GetMergeBaseCommits(gomock.Any(), snow.ID(1), MergeRef{CommitID: &targetHead}, MergeRef{CommitID: &sourceHead}).
		Return(&MergeBaseInfo{MergeBaseCommitID: &targetHead}, nil)
	repo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(openMergeRequest(), nil)
	merger.EXPECT().BinaryChangesBetween(gomock.Any(), snow.ID(1), &targetHead, &sourceHead).
		Return([]string{"tex.png"}, nil)
	gate.EXPECT().EnsureMergeRequestLocks(gomock.Any(), snow.ID(1), gomock.Any(), gomock.Any(), []string{"tex.png"}, snow.ID(7), snow.ID(7)).
		Return(domain.NewErrorConflict("locked by bob"))

	_, err := mr.WithFileLocks(gate).Create(ctx, snow.ID(1), CreateMergeRequestInput{Title: "Hero art", SourceBranch: "feature", TargetBranch: "main"})
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
	merger.EXPECT().GetMergeBaseCommits(gomock.Any(), snow.ID(1), MergeRef{CommitID: &targetHead}, MergeRef{CommitID: &sourceHead}).
		Return(&MergeBaseInfo{MergeBaseCommitID: &targetHead}, nil)
	merger.EXPECT().BinaryChangesBetween(gomock.Any(), snow.ID(1), &targetHead, &sourceHead).
		Return([]string{"tex.png"}, nil)
	gate.EXPECT().EnsureMergeRequestLocks(gomock.Any(), snow.ID(1), snow.ID(5), gomock.Any(), []string{"tex.png"}, snow.ID(7), snow.ID(7)).
		Return(nil)
	merger.EXPECT().MergeForMergeRequest(gomock.Any(), snow.ID(1), "main", "feature", gomock.Any()).
		Return(&domain.Branch{ID: 2, ProjectID: 1, Name: "main", CommitID: &sourceHead}, nil)
	gomock.InOrder(
		repo.EXPECT().UpdateStatus(gomock.Any(), snow.ID(1), int64(5), domain.MergeRequestMerged, &sourceHead).Return(nil),
		repo.EXPECT().RecordMerge(gomock.Any(), snow.ID(1), int64(5), gomock.Any(), gomock.Any()).Return(nil),
		gate.EXPECT().ReleaseForMergeRequest(gomock.Any(), snow.ID(1), snow.ID(5)).Return(nil),
	)

	merged, _, err := mr.WithFileLocks(gate).Merge(permissionCtx(7), snow.ID(1), 5, "", false)
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

func TestMergeRequest_Create_BinaryChangesError(t *testing.T) {
	mr, _, branchRepo, perm, merger := newTestMergeRequest(t)
	gate := NewMockfileLockGate(gomock.NewController(t))
	sourceHead, targetHead := snow.ID(11), snow.ID(12)

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
		Return(&domain.Branch{ID: 3, ProjectID: 1, CommitID: &sourceHead}, nil)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{ID: 2, ProjectID: 1, CommitID: &targetHead}, nil)
	merger.EXPECT().GetMergeBaseCommits(gomock.Any(), snow.ID(1), MergeRef{CommitID: &targetHead}, MergeRef{CommitID: &sourceHead}).
		Return(&MergeBaseInfo{MergeBaseCommitID: &targetHead}, nil)
	wantErr := errors.New("db down")
	merger.EXPECT().BinaryChangesBetween(gomock.Any(), snow.ID(1), &targetHead, &sourceHead).Return(nil, wantErr)

	_, err := mr.WithFileLocks(gate).Create(permissionCtx(7), snow.ID(1), CreateMergeRequestInput{Title: "t", SourceBranch: "feature", TargetBranch: "main"})
	require.ErrorIs(t, err, wantErr)
}

func TestMergeRequest_Create_RepoError(t *testing.T) {
	mr, repo, branchRepo, perm, merger := newTestMergeRequest(t)
	gate := NewMockfileLockGate(gomock.NewController(t))
	sourceHead, targetHead := snow.ID(11), snow.ID(12)

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
		Return(&domain.Branch{ID: 3, ProjectID: 1, CommitID: &sourceHead}, nil)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{ID: 2, ProjectID: 1, CommitID: &targetHead}, nil)
	merger.EXPECT().GetMergeBaseCommits(gomock.Any(), snow.ID(1), MergeRef{CommitID: &targetHead}, MergeRef{CommitID: &sourceHead}).
		Return(&MergeBaseInfo{MergeBaseCommitID: &targetHead}, nil)
	merger.EXPECT().BinaryChangesBetween(gomock.Any(), snow.ID(1), &targetHead, &sourceHead).
		Return([]string{"tex.png"}, nil)
	wantErr := errors.New("db down")
	repo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil, wantErr)

	_, err := mr.WithFileLocks(gate).Create(permissionCtx(7), snow.ID(1), CreateMergeRequestInput{Title: "t", SourceBranch: "feature", TargetBranch: "main"})
	require.ErrorIs(t, err, wantErr)
}

func TestMergeRequest_Merge_FileLocksBranchError(t *testing.T) {
	mr, repo, branchRepo, perm, merger := newTestMergeRequest(t)
	gate := NewMockfileLockGate(gomock.NewController(t))
	sourceHead, targetHead := snow.ID(11), snow.ID(12)

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true).AnyTimes()
	repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
	gomock.InOrder(
		branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(branchWithHead(3, sourceHead), nil),
		branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(branchWithHead(2, targetHead), nil),
		branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(nil, errors.New("db down")),
	)
	merger.EXPECT().GetMergeBaseCommits(gomock.Any(), snow.ID(1), MergeRef{CommitID: &targetHead}, MergeRef{CommitID: &sourceHead}).
		Return(&MergeBaseInfo{MergeBaseCommitID: &targetHead}, nil)

	_, _, err := mr.WithFileLocks(gate).Merge(permissionCtx(7), snow.ID(1), 5, "", false)
	require.Error(t, err)
}

func TestMergeRequest_Merge_FileLocksReject(t *testing.T) {
	mr, repo, branchRepo, perm, merger := newTestMergeRequest(t)
	gate := NewMockfileLockGate(gomock.NewController(t))
	sourceHead, targetHead := snow.ID(11), snow.ID(12)

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true).AnyTimes()
	repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(branchWithHead(3, sourceHead), nil).AnyTimes()
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(branchWithHead(2, targetHead), nil).AnyTimes()
	merger.EXPECT().GetMergeBaseCommits(gomock.Any(), snow.ID(1), MergeRef{CommitID: &targetHead}, MergeRef{CommitID: &sourceHead}).
		Return(&MergeBaseInfo{MergeBaseCommitID: &targetHead}, nil)
	merger.EXPECT().BinaryChangesBetween(gomock.Any(), snow.ID(1), &targetHead, &sourceHead).
		Return([]string{"tex.png"}, nil)
	wantErr := domain.NewErrorConflict("locked by bob")
	gate.EXPECT().EnsureMergeRequestLocks(gomock.Any(), snow.ID(1), snow.ID(5), gomock.Any(), []string{"tex.png"}, snow.ID(7), snow.ID(7)).
		Return(wantErr)

	_, _, err := mr.WithFileLocks(gate).Merge(permissionCtx(7), snow.ID(1), 5, "", false)
	require.ErrorIs(t, err, wantErr)
}

func TestMergeRequest_Merge_FileLocksReleaseError(t *testing.T) {
	mr, repo, branchRepo, perm, merger := newTestMergeRequest(t)
	gate := NewMockfileLockGate(gomock.NewController(t))
	sourceHead, targetHead := snow.ID(11), snow.ID(12)

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true).AnyTimes()
	repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(branchWithHead(3, sourceHead), nil).AnyTimes()
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(branchWithHead(2, targetHead), nil).AnyTimes()
	merger.EXPECT().GetMergeBaseCommits(gomock.Any(), snow.ID(1), MergeRef{CommitID: &targetHead}, MergeRef{CommitID: &sourceHead}).
		Return(&MergeBaseInfo{MergeBaseCommitID: &targetHead}, nil)
	merger.EXPECT().BinaryChangesBetween(gomock.Any(), snow.ID(1), &targetHead, &sourceHead).
		Return([]string{"tex.png"}, nil)
	gate.EXPECT().EnsureMergeRequestLocks(gomock.Any(), snow.ID(1), snow.ID(5), gomock.Any(), []string{"tex.png"}, snow.ID(7), snow.ID(7)).
		Return(nil)
	merger.EXPECT().MergeForMergeRequest(gomock.Any(), snow.ID(1), "main", "feature", gomock.Any()).
		Return(&domain.Branch{ID: 2, ProjectID: 1, Name: "main", CommitID: &sourceHead}, nil)
	repo.EXPECT().UpdateStatus(gomock.Any(), snow.ID(1), int64(5), domain.MergeRequestMerged, &sourceHead).Return(nil)
	repo.EXPECT().RecordMerge(gomock.Any(), snow.ID(1), int64(5), gomock.Any(), gomock.Any()).Return(nil)
	wantErr := errors.New("db down")
	gate.EXPECT().ReleaseForMergeRequest(gomock.Any(), snow.ID(1), snow.ID(5)).Return(wantErr)

	_, _, err := mr.WithFileLocks(gate).Merge(permissionCtx(7), snow.ID(1), 5, "", false)
	require.ErrorIs(t, err, wantErr)
}

func TestMergeRequest_Close_ReleaseError(t *testing.T) {
	mr, repo, _, perm, _ := newTestMergeRequest(t)
	gate := NewMockfileLockGate(gomock.NewController(t))

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true).AnyTimes()
	repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
	repo.EXPECT().UpdateStatus(gomock.Any(), snow.ID(1), int64(5), domain.MergeRequestClosed, nil).Return(nil)
	closed := openMergeRequest()
	closed.Status = domain.MergeRequestClosed
	repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(closed, nil)
	wantErr := errors.New("db down")
	gate.EXPECT().ReleaseForMergeRequest(gomock.Any(), snow.ID(1), snow.ID(5)).Return(wantErr)

	_, err := mr.WithFileLocks(gate).Close(permissionCtx(7), snow.ID(1), 5)
	require.ErrorIs(t, err, wantErr)
}

func TestMergeRequest_Reopen_ReacquiresLocks(t *testing.T) {
	mr, repo, branchRepo, perm, merger := newTestMergeRequest(t)
	gate := NewMockfileLockGate(gomock.NewController(t))
	sourceHead, targetHead := snow.ID(11), snow.ID(12)

	closed := openMergeRequest()
	closed.Status = domain.MergeRequestClosed

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true).AnyTimes()
	repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(closed, nil)
	repo.EXPECT().UpdateStatus(gomock.Any(), snow.ID(1), int64(5), domain.MergeRequestOpen, nil).Return(nil)
	repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(branchWithHead(2, targetHead), nil)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(branchWithHead(3, sourceHead), nil)
	merger.EXPECT().GetMergeBaseCommits(gomock.Any(), snow.ID(1), MergeRef{CommitID: &targetHead}, MergeRef{CommitID: &sourceHead}).
		Return(&MergeBaseInfo{MergeBaseCommitID: &targetHead}, nil)
	merger.EXPECT().BinaryChangesBetween(gomock.Any(), snow.ID(1), &targetHead, &sourceHead).
		Return([]string{"tex.png"}, nil)
	gate.EXPECT().EnsureMergeRequestLocks(gomock.Any(), snow.ID(1), snow.ID(5), gomock.Any(), []string{"tex.png"}, snow.ID(7), snow.ID(7)).
		Return(nil)

	got, err := mr.WithFileLocks(gate).Reopen(permissionCtx(7), snow.ID(1), 5)
	require.NoError(t, err)
	require.Equal(t, domain.MergeRequestOpen, got.Status)
}

func TestMergeRequest_Reopen_LockConflict(t *testing.T) {
	mr, repo, branchRepo, perm, merger := newTestMergeRequest(t)
	gate := NewMockfileLockGate(gomock.NewController(t))
	sourceHead, targetHead := snow.ID(11), snow.ID(12)

	closed := openMergeRequest()
	closed.Status = domain.MergeRequestClosed

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true).AnyTimes()
	repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(closed, nil)
	repo.EXPECT().UpdateStatus(gomock.Any(), snow.ID(1), int64(5), domain.MergeRequestOpen, nil).Return(nil)
	repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(branchWithHead(2, targetHead), nil)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(branchWithHead(3, sourceHead), nil)
	merger.EXPECT().GetMergeBaseCommits(gomock.Any(), snow.ID(1), MergeRef{CommitID: &targetHead}, MergeRef{CommitID: &sourceHead}).
		Return(&MergeBaseInfo{MergeBaseCommitID: &targetHead}, nil)
	merger.EXPECT().BinaryChangesBetween(gomock.Any(), snow.ID(1), &targetHead, &sourceHead).
		Return([]string{"tex.png"}, nil)
	wantErr := domain.NewErrorConflict("locked by bob")
	gate.EXPECT().EnsureMergeRequestLocks(gomock.Any(), snow.ID(1), snow.ID(5), gomock.Any(), []string{"tex.png"}, snow.ID(7), snow.ID(7)).
		Return(wantErr)

	_, err := mr.WithFileLocks(gate).Reopen(permissionCtx(7), snow.ID(1), 5)
	require.ErrorIs(t, err, wantErr)
}

func TestMergeRequest_Reopen_EmptySourceBranch(t *testing.T) {
	mr, repo, branchRepo, perm, merger := newTestMergeRequest(t)
	gate := NewMockfileLockGate(gomock.NewController(t))

	closed := openMergeRequest()
	closed.Status = domain.MergeRequestClosed

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true).AnyTimes()
	repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(closed, nil)
	repo.EXPECT().UpdateStatus(gomock.Any(), snow.ID(1), int64(5), domain.MergeRequestOpen, nil).Return(nil)
	repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(&domain.Branch{ID: 2, ProjectID: 1, Name: "main"}, nil)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "feature"}, nil)
	merger.EXPECT().BinaryChangesBetween(gomock.Any(), snow.ID(1), nil, nil).Return(nil, nil)
	gate.EXPECT().EnsureMergeRequestLocks(gomock.Any(), snow.ID(1), snow.ID(5), gomock.Any(), nil, snow.ID(7), snow.ID(7)).Return(nil)

	got, err := mr.WithFileLocks(gate).Reopen(permissionCtx(7), snow.ID(1), 5)
	require.NoError(t, err)
	require.Equal(t, domain.MergeRequestOpen, got.Status)
}

func TestBranch_Delete_DeleteError(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)
	gate := NewMockfileLockGate(ctrl)

	branch := &domain.Branch{ID: 3, ProjectID: 1, Name: "feature"}
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(branch, nil)
	repo.EXPECT().ListBranches(gomock.Any(), snow.ID(1), 2, nil, snow.ID(0)).
		Return([]*domain.Branch{branch, {ID: 1, ProjectID: 1, Name: "main", IsDefault: true}}, nil)
	repo.EXPECT().HasOpenMergeRequests(gomock.Any(), snow.ID(1), snow.ID(3)).Return(false, nil)
	wantErr := errors.New("db down")
	repo.EXPECT().DeleteBranch(gomock.Any(), snow.ID(1), snow.ID(3)).Return(wantErr)

	uc := NewBranch(perm, repo, newTestBranchNode(t)).WithFileLocks(gate)
	require.ErrorIs(t, uc.Delete(permissionCtx(7), snow.ID(1), "feature"), wantErr)
}

func TestBranch_Delete_ReleaseError(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)
	gate := NewMockfileLockGate(ctrl)

	branch := &domain.Branch{ID: 3, ProjectID: 1, Name: "feature"}
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(branch, nil)
	repo.EXPECT().ListBranches(gomock.Any(), snow.ID(1), 2, nil, snow.ID(0)).
		Return([]*domain.Branch{branch, {ID: 1, ProjectID: 1, Name: "main", IsDefault: true}}, nil)
	repo.EXPECT().HasOpenMergeRequests(gomock.Any(), snow.ID(1), snow.ID(3)).Return(false, nil)
	repo.EXPECT().DeleteBranch(gomock.Any(), snow.ID(1), snow.ID(3)).Return(nil)
	wantErr := errors.New("db down")
	gate.EXPECT().ReleaseBranch(gomock.Any(), snow.ID(1), snow.ID(3)).Return(wantErr)

	uc := NewBranch(perm, repo, newTestBranchNode(t)).WithFileLocks(gate)
	require.ErrorIs(t, uc.Delete(permissionCtx(7), snow.ID(1), "feature"), wantErr)
}

func TestBranch_FastForward_FileLocksNoClaim(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMockbranchRepository(ctrl)
	gate := NewMockfileLockGate(ctrl)

	headID := snow.ID(11)
	target := &domain.Branch{ID: 2, ProjectID: 1, Name: "main", CommitID: &headID}

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(target, nil).AnyTimes()
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
		Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "feature", CommitID: &headID}, nil).AnyTimes()
	repo.EXPECT().GetCommit(gomock.Any(), headID).
		Return(&domain.Commit{ID: headID, ProjectID: 1, TreeID: 101, Parent1ID: &headID}, nil).AnyTimes()
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).
		Return(&domain.TreeNode{ID: 101, Name: "root"}, nil).AnyTimes()
	expectTreeRows(repo, map[int64][]*domain.File{}, map[int64][]*domain.TreeNode{})
	repo.EXPECT().ListFileMetaByTree(gomock.Any(), int64(101)).Return(nil, nil).AnyTimes()

	uc := NewBranch(perm, repo, newTestBranchNode(t)).WithFileLocks(gate)
	_, err := uc.FastForward(context.Background(), snow.ID(1), "main", "feature")
	require.True(t, domain.IsErrorNoPermission(err))
}

func TestBranch_FastForward_FileLocksReleaseError(t *testing.T) {
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
	expectTreeRows(repo, map[int64][]*domain.File{102: []*domain.File{{ID: 2, Name: "tex.png", TreeID: 102, IsBinary: true}}}, map[int64][]*domain.TreeNode{})
	repo.EXPECT().ListFileMetaByTree(gomock.Any(), int64(101)).Return(nil, nil).AnyTimes()
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(102)).
		Return(&domain.TreeNode{ID: 102, Name: "root"}, nil).AnyTimes()
	repo.EXPECT().ListFileMetaByTree(gomock.Any(), int64(102)).
		Return([]*domain.File{{ID: 2, Name: "tex.png", TreeID: 102, IsBinary: true}}, nil).AnyTimes()

	gate.EXPECT().EnsureLocks(gomock.Any(), snow.ID(1), target, nil, []string{"tex.png"}, snow.ID(7)).Return(nil)
	repo.EXPECT().UpdateCommitIf(gomock.Any(), snow.ID(2), &targetHead, &sourceHead).Return(nil)
	wantErr := errors.New("db down")
	gate.EXPECT().ReleaseLanded(gomock.Any(), snow.ID(1), target, []string{"tex.png"}, snow.ID(7)).Return(wantErr)

	uc := NewBranch(perm, repo, newTestBranchNode(t)).WithFileLocks(gate)
	_, err := uc.FastForward(permissionCtx(7), snow.ID(1), "main", "feature")
	require.ErrorIs(t, err, wantErr)
}

func TestMergeRequest_Merge_AdminMergesAuthorRequest(t *testing.T) {
	mr, repo, branchRepo, perm, merger := newTestMergeRequest(t)
	gate := NewMockfileLockGate(gomock.NewController(t))
	sourceHead, targetHead := snow.ID(11), snow.ID(12)

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true).AnyTimes()
	repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil).Times(2)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(branchWithHead(3, sourceHead), nil).AnyTimes()
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(branchWithHead(2, targetHead), nil).AnyTimes()
	merger.EXPECT().GetMergeBaseCommits(gomock.Any(), snow.ID(1), MergeRef{CommitID: &targetHead}, MergeRef{CommitID: &sourceHead}).
		Return(&MergeBaseInfo{MergeBaseCommitID: &targetHead}, nil)
	merger.EXPECT().BinaryChangesBetween(gomock.Any(), snow.ID(1), &targetHead, &sourceHead).
		Return([]string{"tex.png"}, nil)
	merger.EXPECT().MergeForMergeRequest(gomock.Any(), snow.ID(1), "main", "feature", gomock.Any()).
		Return(&domain.Branch{ID: 2, ProjectID: 1, Name: "main", CommitID: &sourceHead}, nil)
	gomock.InOrder(
		gate.EXPECT().EnsureMergeRequestLocks(gomock.Any(), snow.ID(1), snow.ID(5), gomock.Any(), []string{"tex.png"}, snow.ID(99), snow.ID(7)).Return(nil),
		repo.EXPECT().UpdateStatus(gomock.Any(), snow.ID(1), int64(5), domain.MergeRequestMerged, &sourceHead).Return(nil),
		repo.EXPECT().RecordMerge(gomock.Any(), snow.ID(1), int64(5), gomock.Any(), gomock.Any()).Return(nil),
		gate.EXPECT().ReleaseForMergeRequest(gomock.Any(), snow.ID(1), snow.ID(5)).Return(nil),
	)

	merged, _, err := mr.WithFileLocks(gate).Merge(permissionCtx(99), snow.ID(1), 5, "", false)
	require.NoError(t, err)
	require.Equal(t, openMergeRequest().ID, merged.ID)
}

func binaryTreeFixture(t *testing.T, repo *MockbranchRepository, fromHead, toHead snow.ID) {
	t.Helper()
	repo.EXPECT().GetCommit(gomock.Any(), fromHead).
		Return(&domain.Commit{ID: fromHead, ProjectID: 1, TreeID: 101}, nil).AnyTimes()
	repo.EXPECT().GetCommit(gomock.Any(), toHead).
		Return(&domain.Commit{ID: toHead, ProjectID: 1, TreeID: 102}, nil).AnyTimes()
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).
		Return(&domain.TreeNode{ID: 101, Name: "root"}, nil).AnyTimes()
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(102)).
		Return(&domain.TreeNode{ID: 102, Name: "root"}, nil).AnyTimes()
	// Stored file hashes cover the chunk list, as Push guarantees.
	file := func(id int64, name string, tree int64, binary bool, chunk byte) *domain.File {
		hashes := []domain.Hash{{chunk}}
		return &domain.File{ID: id, Name: name, TreeID: tree, Mode: 0o644, IsBinary: binary,
			Hash: treehash.FileHash(hashes), Chunks: []domain.Chunk{{Hash: hashes[0]}}}
	}
	expectTreeRows(repo, map[int64][]*domain.File{
		101: {file(1, "a.txt", 101, false, 1), file(2, "conv.txt", 101, false, 4), file(3, "gone.bin", 101, true, 3), file(4, "old.png", 101, true, 2)},
		102: {file(5, "a.txt", 102, false, 1), file(6, "conv.txt", 102, true, 7), file(7, "new.png", 102, true, 6), file(8, "old.png", 102, true, 5)},
	}, nil)
}

func TestBranch_BinaryChangesBetween(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMockbranchRepository(ctrl)
	fromHead, toHead := snow.ID(11), snow.ID(12)
	binaryTreeFixture(t, repo, fromHead, toHead)

	uc := NewBranch(perm, repo, newTestBranchNode(t))

	paths, err := uc.BinaryChangesBetween(permissionCtx(7), snow.ID(1), &fromHead, &toHead)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"conv.txt", "gone.bin", "new.png", "old.png"}, paths)

	added, err := uc.BinaryChangesBetween(permissionCtx(7), snow.ID(1), nil, &toHead)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"conv.txt", "new.png", "old.png"}, added)
}

func TestBranch_BinaryLockPlan(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMockbranchRepository(ctrl)
	fromHead, toHead := snow.ID(11), snow.ID(12)
	binaryTreeFixture(t, repo, fromHead, toHead)

	uc := NewBranch(perm, repo, newTestBranchNode(t))

	required, checked, err := uc.BinaryLockPlan(permissionCtx(7), snow.ID(1), &fromHead, &toHead)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"conv.txt", "gone.bin", "old.png"}, required)
	require.ElementsMatch(t, []string{"new.png"}, checked)

	required, checked, err = uc.BinaryLockPlan(permissionCtx(7), snow.ID(1), nil, &toHead)
	require.NoError(t, err)
	require.Empty(t, required)
	require.ElementsMatch(t, []string{"conv.txt", "new.png", "old.png"}, checked)
}

func TestBranch_BinaryChanges_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMockbranchRepository(ctrl)
	fromHead, toHead := snow.ID(11), snow.ID(12)

	repo.EXPECT().GetCommit(gomock.Any(), fromHead).
		Return(&domain.Commit{ID: fromHead, ProjectID: 1, TreeID: 101}, nil).AnyTimes()
	wantErr := errors.New("db down")
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).Return(nil, wantErr).AnyTimes()

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	_, err := uc.BinaryChangesBetween(permissionCtx(7), snow.ID(1), &fromHead, &toHead)
	require.ErrorIs(t, err, wantErr)

	_, _, err = uc.BinaryLockPlan(permissionCtx(7), snow.ID(1), &fromHead, &toHead)
	require.ErrorIs(t, err, wantErr)
}
