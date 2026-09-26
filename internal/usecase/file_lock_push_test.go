package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

// trackedBinaryTree fixtures a head tree with root/dir containing a tracked
// binary file. The returned mocks answer every tree walk with AnyTimes so the
// tests can focus on which paths reach EnsureLocks.
func trackedBinaryTree(repo *MockbranchRepository, rootID, dirID int64) {
	repo.EXPECT().GetTreeNode(gomock.Any(), rootID).
		Return(&domain.TreeNode{ID: rootID, Name: "root"}, nil).AnyTimes()
	repo.EXPECT().ListFilesByTree(gomock.Any(), rootID).Return(nil, nil).AnyTimes()
	repo.EXPECT().ListTreeChildren(gomock.Any(), rootID).
		Return([]*domain.TreeNode{{ID: dirID, Name: "assets", Hash: domain.Hash{4}}}, nil).AnyTimes()
	repo.EXPECT().GetTreeChildByName(gomock.Any(), rootID, "assets").
		Return(&domain.TreeNode{ID: dirID, Name: "assets"}, nil).AnyTimes()
	repo.EXPECT().ListFilesByTree(gomock.Any(), dirID).Return([]*domain.File{{
		ID: 7, Name: "tex.png", TreeID: dirID, Mode: 0o644, IsBinary: true,
		Hash: domain.Hash{7}, Chunks: []domain.Chunk{{Hash: domain.Hash{8}}},
	}}, nil).AnyTimes()
	repo.EXPECT().ListTreeChildren(gomock.Any(), dirID).Return(nil, nil).AnyTimes()
}

func TestPush_FileLocks_TextOverwriteOfTrackedBinary(t *testing.T) {
	uc, perm, repo, pushRepo, ctx := newPushFixture(t)
	gate := NewMockfileLockGate(gomock.NewController(t))
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)

	head := snow.ID(9)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{ID: 5, ProjectID: 1, Name: "main", CommitID: &head}, nil)
	repo.EXPECT().GetCommit(gomock.Any(), head).
		Return(&domain.Commit{ID: 9, ProjectID: 1, TreeID: 100, Hash: domain.Hash{5}}, nil)
	trackedBinaryTree(repo, 100, 101)

	var locked []string
	gate.EXPECT().EnsureLocks(gomock.Any(), snow.ID(1), gomock.Any(), gomock.Any(), snow.ID(7)).
		DoAndReturn(func(_ context.Context, _ snow.ID, _ *domain.Branch, paths []string, _ snow.ID) error {
			locked = paths
			return nil
		})
	gate.EXPECT().ReleaseLanded(gomock.Any(), snow.ID(1), gomock.Any(), gomock.Any(), snow.ID(7)).Return(nil)
	pushRepo.EXPECT().ApplyPush(gomock.Any(), gomock.Any()).Return(nil)

	ch, fh := chunkAndFileHash(t, "text now")
	file := &domain.PushFile{Path: "assets/tex.png", Mode: 0o644, SizeBytes: 8, FileHash: fh, ChunkHashes: []domain.Hash{ch}}
	_, err := uc.WithFileLocks(gate).Push(ctx, snow.ID(1), "main", "", "msg", []*domain.PushFile{file}, nil, "", head.Base36())
	require.NoError(t, err)
	require.Equal(t, []string{"assets/tex.png"}, locked, "overwriting a tracked binary with text still requires a lock")
}

func TestPush_FileLocks_RemovedUnknownBinary(t *testing.T) {
	uc, perm, repo, pushRepo, ctx := newPushFixture(t)
	gate := NewMockfileLockGate(gomock.NewController(t))
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)

	head := snow.ID(9)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{ID: 5, ProjectID: 1, Name: "main", CommitID: &head}, nil)
	repo.EXPECT().GetCommit(gomock.Any(), head).
		Return(&domain.Commit{ID: 9, ProjectID: 1, TreeID: 100, Hash: domain.Hash{5}}, nil)
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(100)).
		Return(&domain.TreeNode{ID: 100, Name: "root"}, nil).AnyTimes()
	repo.EXPECT().ListFilesByTree(gomock.Any(), int64(100)).Return([]*domain.File{{
		ID: 3, Name: "scene.bin", TreeID: 100, Mode: 0o644, IsBinary: true,
		Hash: domain.Hash{3}, Chunks: []domain.Chunk{{Hash: domain.Hash{2}}},
	}}, nil).AnyTimes()
	repo.EXPECT().ListTreeChildren(gomock.Any(), int64(100)).Return(nil, nil).AnyTimes()

	var locked []string
	gate.EXPECT().EnsureLocks(gomock.Any(), snow.ID(1), gomock.Any(), gomock.Any(), snow.ID(7)).
		DoAndReturn(func(_ context.Context, _ snow.ID, _ *domain.Branch, paths []string, _ snow.ID) error {
			locked = paths
			return nil
		})
	gate.EXPECT().ReleaseLanded(gomock.Any(), snow.ID(1), gomock.Any(), gomock.Any(), snow.ID(7)).Return(nil)
	pushRepo.EXPECT().ApplyPush(gomock.Any(), gomock.Any()).Return(nil)

	ch, fh := chunkAndFileHash(t, "notes")
	file := &domain.PushFile{Path: "notes.txt", Mode: 0o644, SizeBytes: 5, FileHash: fh, ChunkHashes: []domain.Hash{ch}}
	_, err := uc.WithFileLocks(gate).Push(ctx, snow.ID(1), "main", "", "msg",
		[]*domain.PushFile{file}, []string{"scene.bin"}, "", head.Base36())
	require.NoError(t, err)
	require.Equal(t, []string{"scene.bin"}, locked, "removing a tracked binary requires a lock even without a known extension")
}

func TestPush_FileLocks_EnsureLocksError(t *testing.T) {
	uc, perm, repo, _, ctx := newPushFixture(t)
	gate := NewMockfileLockGate(gomock.NewController(t))
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{ID: 5, ProjectID: 1, Name: "main"}, nil)

	wantErr := domain.NewErrorConflict("locked by bob")
	gate.EXPECT().EnsureLocks(gomock.Any(), snow.ID(1), gomock.Any(), []string{"a.png"}, snow.ID(7)).Return(wantErr)

	ch, fh := chunkAndFileHash(t, "x")
	file := &domain.PushFile{Path: "a.png", Mode: 0o644, IsBinary: true, FileHash: fh, ChunkHashes: []domain.Hash{ch}}
	_, err := uc.WithFileLocks(gate).Push(ctx, snow.ID(1), "main", "", "msg", []*domain.PushFile{file}, nil, "", "")
	require.ErrorIs(t, err, wantErr)
}

func TestPush_FileLocks_ReleaseLandedError(t *testing.T) {
	uc, perm, repo, pushRepo, ctx := newPushFixture(t)
	gate := NewMockfileLockGate(gomock.NewController(t))
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{ID: 5, ProjectID: 1, Name: "main"}, nil)

	gate.EXPECT().EnsureLocks(gomock.Any(), snow.ID(1), gomock.Any(), []string{"a.png"}, snow.ID(7)).Return(nil)
	pushRepo.EXPECT().ApplyPush(gomock.Any(), gomock.Any()).Return(nil)
	wantErr := errors.New("db down")
	gate.EXPECT().ReleaseLanded(gomock.Any(), snow.ID(1), gomock.Any(), []string{"a.png"}, snow.ID(7)).Return(wantErr)

	ch, fh := chunkAndFileHash(t, "x")
	file := &domain.PushFile{Path: "a.png", Mode: 0o644, IsBinary: true, FileHash: fh, ChunkHashes: []domain.Hash{ch}}
	_, err := uc.WithFileLocks(gate).Push(ctx, snow.ID(1), "main", "", "msg", []*domain.PushFile{file}, nil, "", "")
	require.ErrorIs(t, err, wantErr)
}

func TestPush_FileLocks_HeadTreeError(t *testing.T) {
	uc, perm, repo, _, ctx := newPushFixture(t)
	gate := NewMockfileLockGate(gomock.NewController(t))
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)

	head := snow.ID(9)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{ID: 5, ProjectID: 1, Name: "main", CommitID: &head}, nil)
	repo.EXPECT().GetCommit(gomock.Any(), head).
		Return(&domain.Commit{ID: 9, ProjectID: 1, TreeID: 100, Hash: domain.Hash{5}}, nil)
	wantErr := errors.New("db down")
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(100)).Return(nil, wantErr)

	ch, fh := chunkAndFileHash(t, "x")
	file := &domain.PushFile{Path: "a.txt", Mode: 0o644, FileHash: fh, ChunkHashes: []domain.Hash{ch}}
	_, err := uc.WithFileLocks(gate).Push(ctx, snow.ID(1), "main", "", "msg", []*domain.PushFile{file}, nil, "", head.Base36())
	require.ErrorIs(t, err, wantErr)
}
