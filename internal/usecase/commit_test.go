package usecase

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

func testCommit(id, projectID snow.ID, parents ...snow.ID) *domain.Commit {
	c := &domain.Commit{ID: id, ProjectID: projectID, TreeID: int64(id)}
	if len(parents) > 0 {
		p := parents[0]
		c.Parent1ID = &p
	}
	if len(parents) > 1 {
		p := parents[1]
		c.Parent2ID = &p
	}
	return c
}

func walkIDs(commits []*domain.Commit) []snow.ID {
	ids := make([]snow.ID, len(commits))
	for i, c := range commits {
		ids[i] = c.ID
	}
	return ids
}

func TestBranch_GetCommit_NoPermission(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(false)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	_, _, err := uc.GetCommit(context.Background(), snow.ID(1), snow.ID(2))
	require.True(t, domain.IsErrorNoPermission(err))
}

func TestBranch_GetCommit_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)
	repo.EXPECT().
		GetCommit(gomock.Any(), snow.ID(2)).
		Return(nil, domain.NewErrorRecordNotFound())

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	_, _, err := uc.GetCommit(context.Background(), snow.ID(1), snow.ID(2))
	require.True(t, domain.IsErrorNotFound(err))
	require.Equal(t, "commit 2 not found", err.Error())
}

func TestBranch_GetCommit_WrongProject(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)
	repo.EXPECT().
		GetCommit(gomock.Any(), snow.ID(2)).
		Return(testCommit(2, 9), nil)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	_, _, err := uc.GetCommit(context.Background(), snow.ID(1), snow.ID(2))
	require.True(t, domain.IsErrorNotFound(err))
	require.Equal(t, "commit 2 not found", err.Error())
}

func TestBranch_GetCommit_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	rootHash := domain.Hash{0xaa}
	dirHash := domain.Hash{0xbb}
	fileHash := domain.Hash{0xcc}
	chunkHash := domain.Hash{0xdd}
	commit := testCommit(2, 1)
	commit.TreeID = 5

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)
	repo.EXPECT().
		GetCommit(gomock.Any(), snow.ID(2)).
		Return(commit, nil)
	gomock.InOrder(
		repo.EXPECT().
			GetTreeNode(gomock.Any(), int64(5)).
			Return(&domain.TreeNode{ID: 5, Hash: rootHash}, nil),
		repo.EXPECT().
			ListFilesByTree(gomock.Any(), int64(5)).
			Return([]*domain.File{{ID: 1, Name: "readme.md", TreeID: 5, Hash: fileHash}}, nil),
		repo.EXPECT().
			ListTreeChildren(gomock.Any(), int64(5)).
			Return([]*domain.TreeNode{{ID: 6, Name: "src", Hash: dirHash}}, nil),
		repo.EXPECT().
			ListFilesByTree(gomock.Any(), int64(6)).
			Return([]*domain.File{{
				ID:     2,
				Name:   "main.go",
				Mode:   0o644,
				TreeID: 6,
				Hash:   fileHash,
				Chunks: []domain.Chunk{{ID: 3, Hash: chunkHash, SizeBytes: 10}},
			}}, nil),
		repo.EXPECT().
			ListTreeChildren(gomock.Any(), int64(6)).
			Return(nil, nil),
	)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	gotCommit, gotRoot, err := uc.GetCommit(context.Background(), snow.ID(1), snow.ID(2))
	require.NoError(t, err)
	require.Equal(t, commit, gotCommit)
	require.Equal(t, rootHash, gotRoot.Hash)
	require.Len(t, gotRoot.FileChildren, 1)
	require.Len(t, gotRoot.TreeChildren, 1)
	require.Equal(t, "src", gotRoot.TreeChildren[0].Name)
	require.Len(t, gotRoot.TreeChildren[0].FileChildren, 1)
	require.Equal(t, "main.go", gotRoot.TreeChildren[0].FileChildren[0].Name)
}

func TestBranch_WalkCommits_NoPermission(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(false)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	_, err := uc.WalkCommits(context.Background(), snow.ID(1), snow.ID(3), nil, 10)
	require.True(t, domain.IsErrorNoPermission(err))
}

func TestBranch_WalkCommits_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)
	repo.EXPECT().
		GetCommit(gomock.Any(), snow.ID(2)).
		Return(nil, domain.NewErrorRecordNotFound())

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	_, err := uc.WalkCommits(context.Background(), snow.ID(1), snow.ID(2), nil, 10)
	require.True(t, domain.IsErrorNotFound(err))
}

func TestBranch_WalkCommits_WrongProject(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)
	repo.EXPECT().
		GetCommit(gomock.Any(), snow.ID(2)).
		Return(testCommit(2, 9), nil)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	_, err := uc.WalkCommits(context.Background(), snow.ID(1), snow.ID(2), nil, 10)
	require.True(t, domain.IsErrorNotFound(err))
}

func TestBranch_WalkCommits_LinearNewestFirst(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)
	repo.EXPECT().GetCommit(gomock.Any(), snow.ID(1)).Return(testCommit(1, 1), nil)
	repo.EXPECT().GetCommit(gomock.Any(), snow.ID(2)).Return(testCommit(2, 1, 1), nil)
	repo.EXPECT().GetCommit(gomock.Any(), snow.ID(3)).Return(testCommit(3, 1, 2), nil)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	commits, err := uc.WalkCommits(context.Background(), snow.ID(1), snow.ID(3), nil, 10)
	require.NoError(t, err)
	require.Equal(t, []snow.ID{3, 2, 1}, walkIDs(commits))
}

func TestBranch_WalkCommits_StopExclusive(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	stop := snow.ID(2)
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)
	repo.EXPECT().GetCommit(gomock.Any(), snow.ID(3)).Return(testCommit(3, 1, 2), nil)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	commits, err := uc.WalkCommits(context.Background(), snow.ID(1), snow.ID(3), &stop, 10)
	require.NoError(t, err)
	require.Equal(t, []snow.ID{3}, walkIDs(commits))
}

func TestBranch_WalkCommits_StopEqualsStart(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	start := snow.ID(3)
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	commits, err := uc.WalkCommits(context.Background(), snow.ID(1), start, &start, 10)
	require.NoError(t, err)
	require.Empty(t, commits)
}

func TestBranch_WalkCommits_MergeBothParents(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)
	repo.EXPECT().GetCommit(gomock.Any(), snow.ID(1)).Return(testCommit(1, 1), nil)
	repo.EXPECT().GetCommit(gomock.Any(), snow.ID(2)).Return(testCommit(2, 1, 1), nil)
	repo.EXPECT().GetCommit(gomock.Any(), snow.ID(3)).Return(testCommit(3, 1, 1), nil)
	repo.EXPECT().GetCommit(gomock.Any(), snow.ID(4)).Return(testCommit(4, 1, 2, 3), nil)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	commits, err := uc.WalkCommits(context.Background(), snow.ID(1), snow.ID(4), nil, 10)
	require.NoError(t, err)
	require.Equal(t, []snow.ID{4, 3, 2, 1}, walkIDs(commits))
}

func TestBranch_WalkCommits_Limit(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)
	repo.EXPECT().GetCommit(gomock.Any(), snow.ID(1)).Return(testCommit(1, 1), nil)
	repo.EXPECT().GetCommit(gomock.Any(), snow.ID(2)).Return(testCommit(2, 1, 1), nil)
	repo.EXPECT().GetCommit(gomock.Any(), snow.ID(3)).Return(testCommit(3, 1, 2), nil)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	commits, err := uc.WalkCommits(context.Background(), snow.ID(1), snow.ID(3), nil, 2)
	require.NoError(t, err)
	require.Equal(t, []snow.ID{3, 2}, walkIDs(commits))
}
