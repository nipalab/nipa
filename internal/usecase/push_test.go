package usecase

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/treehash"
)

func newPushFixture(t *testing.T) (*Push, *MockpermissionUsecase, *MockbranchRepository, *MockpushRepository, context.Context) {
	t.Helper()
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)
	pushRepo := NewMockpushRepository(ctrl)

	node, err := snow.NewNode(1)
	require.NoError(t, err)
	uc := NewPush(perm, repo, pushRepo, node)
	ctx := domain.ContextWithClaim(context.Background(), domain.Claims{UserID: snow.ID(7)})
	return uc, perm, repo, pushRepo, ctx
}

func chunkAndFileHash(t *testing.T, data string) (domain.Hash, domain.Hash) {
	t.Helper()
	c := chunker.Sum([]byte(data))
	return c, treehash.FileHash([]domain.Hash{c})
}

func TestPush_NoPermission(t *testing.T) {
	uc, perm, _, _, ctx := newPushFixture(t)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(false)

	_, err := uc.Push(ctx, snow.ID(1), "main", "", "msg", nil, nil)
	require.Error(t, err)
	var dErr *domain.Error
	require.ErrorAs(t, err, &dErr)
	require.Equal(t, 403, dErr.Code)
}

func TestPush_MissingMessage(t *testing.T) {
	uc, perm, _, _, ctx := newPushFixture(t)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)

	_, err := uc.Push(ctx, snow.ID(1), "main", "", "  ", nil, nil)
	require400(t, err, "commit message is required")
}

func TestPush_InvalidPath(t *testing.T) {
	uc, perm, _, _, ctx := newPushFixture(t)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)

	ch, _ := chunkAndFileHash(t, "x")
	file := &domain.PushFile{Path: "../escape", Mode: 0o644, FileHash: ch, ChunkHashes: []domain.Hash{ch}}
	_, err := uc.Push(ctx, snow.ID(1), "main", "", "msg", []*domain.PushFile{file}, nil)
	require400(t, err, "invalid file path")
}

func TestPush_FileHashMismatch(t *testing.T) {
	uc, perm, _, _, ctx := newPushFixture(t)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)

	ch, _ := chunkAndFileHash(t, "x")
	file := &domain.PushFile{Path: "a.txt", Mode: 0o644, FileHash: domain.Hash{9}, ChunkHashes: []domain.Hash{ch}}
	_, err := uc.Push(ctx, snow.ID(1), "main", "", "msg", []*domain.PushFile{file}, nil)
	require400(t, err, "file hash mismatch")
}

func TestPush_SamePathPushedAndRemoved(t *testing.T) {
	uc, perm, _, _, ctx := newPushFixture(t)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)

	ch, fh := chunkAndFileHash(t, "x")
	file := &domain.PushFile{Path: "a.txt", Mode: 0o644, FileHash: fh, ChunkHashes: []domain.Hash{ch}}
	_, err := uc.Push(ctx, snow.ID(1), "main", "", "msg", []*domain.PushFile{file}, []string{"a.txt"})
	require400(t, err, "both pushed and removed")
}

func TestPush_BranchNotFound(t *testing.T) {
	uc, perm, repo, _, ctx := newPushFixture(t)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(nil, domain.NewErrorRecordNotFound())

	_, err := uc.Push(ctx, snow.ID(1), "main", "", "msg", nil, nil)
	require.Error(t, err)
	require.True(t, domain.IsErrorNotFound(err))
}

func TestPush_ProtectedBranch(t *testing.T) {
	uc, perm, repo, _, ctx := newPushFixture(t)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{ID: 5, ProjectID: 1, Name: "main", IsProtected: true}, nil)

	_, err := uc.Push(ctx, snow.ID(1), "main", "", "msg", nil, nil)
	require.Error(t, err)
	require.True(t, domain.IsErrorNoPermission(err))
}

func TestPush_StaleBaseConflict(t *testing.T) {
	uc, perm, repo, _, ctx := newPushFixture(t)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{ID: 5, ProjectID: 1, Name: "main", CommitID: idPtr(snow.ID(9))}, nil)
	headHash := domain.Hash{1}
	repo.EXPECT().GetCommit(gomock.Any(), snow.ID(9)).
		Return(&domain.Commit{ID: 9, TreeID: 100, Hash: headHash}, nil)
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(100)).
		Return(&domain.TreeNode{ID: 100, Hash: domain.Hash{2}, Name: "root"}, nil)

	_, err := uc.Push(ctx, snow.ID(1), "main", "basehash", "msg", nil, nil)
	require.Error(t, err)
	require.True(t, domain.IsErrorConflict(err))
}

func TestPush_FirstCommitSuccess(t *testing.T) {
	uc, perm, repo, pushRepo, ctx := newPushFixture(t)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{ID: 5, ProjectID: 1, Name: "main"}, nil)

	chA, fhA := chunkAndFileHash(t, "aaa")
	chB, fhB := chunkAndFileHash(t, "bbb")
	fileA := &domain.PushFile{Path: "a.txt", Mode: 0o644, SizeBytes: 3, FileHash: fhA, ChunkHashes: []domain.Hash{chA}}
	fileB := &domain.PushFile{Path: "dir/b.bin", Mode: 0o755, SizeBytes: 3, IsBinary: true, FileHash: fhB, ChunkHashes: []domain.Hash{chB}}

	dirHash := treehash.TreeHash([]treehash.FileEntry{{Name: "b.bin", Mode: 0o755, Hash: fhB}}, nil)
	rootHash := treehash.TreeHash(
		[]treehash.FileEntry{{Name: "a.txt", Mode: 0o644, Hash: fhA}},
		[]treehash.TreeEntry{{Name: "dir", Hash: dirHash}})
	wantCommitHash := treehash.CommitHash(rootHash, nil, "msg")

	var got ApplyPushRequest
	pushRepo.EXPECT().ApplyPush(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, req ApplyPushRequest) error {
			got = req
			return nil
		})

	result, err := uc.Push(ctx, snow.ID(1), "main", "", "msg",
		[]*domain.PushFile{fileB, fileA},
		nil)
	require.NoError(t, err)
	require.Equal(t, result.TreeHash, rootHash)
	require.Equal(t, result.CommitHash, wantCommitHash)
	require.NotZero(t, result.CommitID)

	require.Equal(t, snow.ID(5), got.BranchID)
	require.Equal(t, snow.ID(1), got.ProjectID)
	require.Equal(t, snow.ID(7), got.UserID)
	require.Equal(t, "msg", got.Message)
	require.Equal(t, wantCommitHash, got.CommitHash)
	require.Nil(t, got.ParentID)

	require.Len(t, got.Nodes, 2)
	require.Equal(t, "root", got.Nodes[0].Name)
	require.Equal(t, rootHash, got.Nodes[0].Hash)
	require.Nil(t, got.Nodes[0].ParentID)
	require.Equal(t, "dir", got.Nodes[1].Name)
	require.Equal(t, dirHash, got.Nodes[1].Hash)
	require.Equal(t, int64(1), *got.Nodes[1].ParentID)

	require.Len(t, got.Files, 2)
	byName := map[string]PushFileRow{}
	for _, f := range got.Files {
		byName[f.Name] = f
	}
	require.Equal(t, fileA.FileHash, byName["a.txt"].Hash)
	require.Equal(t, []domain.Hash{chA}, byName["a.txt"].ChunkHashes)
	require.Equal(t, int64(1), byName["a.txt"].TreeID)
	require.Equal(t, fileB.FileHash, byName["b.bin"].Hash)
	require.Equal(t, int64(2), byName["b.bin"].TreeID)
	require.True(t, byName["b.bin"].IsBinary)
}

func TestPush_IncrementalWithRemoval(t *testing.T) {
	uc, perm, repo, pushRepo, ctx := newPushFixture(t)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	oldRootHash := treehash.TreeHash(
		[]treehash.FileEntry{{Name: "keep.txt", Mode: 0o644, Hash: domain.Hash{3}}},
		[]treehash.TreeEntry{{Name: "d", Hash: domain.Hash{4}}})
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{ID: 5, ProjectID: 1, Name: "main", CommitID: idPtr(snow.ID(9))}, nil)
	repo.EXPECT().GetCommit(gomock.Any(), snow.ID(9)).
		Return(&domain.Commit{ID: 9, TreeID: 100, Hash: domain.Hash{5}}, nil)
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(100)).
		Return(&domain.TreeNode{ID: 100, Hash: oldRootHash, Name: "root"}, nil).
		Times(2)
	repo.EXPECT().ListFilesByTree(gomock.Any(), int64(100)).
		Return([]*domain.File{{Name: "keep.txt", Hash: domain.Hash{3}, Mode: 0o644}},
			nil)
	repo.EXPECT().ListTreeChildren(gomock.Any(), int64(100)).
		Return([]*domain.TreeNode{{ID: 101, Name: "d", Hash: domain.Hash{4}}}, nil)

	chY, fhY := chunkAndFileHash(t, "y")
	repo.EXPECT().ListFilesByTree(gomock.Any(), int64(101)).
		Return([]*domain.File{{Name: "x.txt", Hash: domain.Hash{7}, Mode: 0o644, Chunks: []domain.Chunk{{Hash: domain.Hash{8}}}}}, nil)
	repo.EXPECT().ListTreeChildren(gomock.Any(), int64(101)).Return(nil, nil)

	newDHash := treehash.TreeHash(
		[]treehash.FileEntry{{Name: "x.txt", Mode: 0o644, Hash: domain.Hash{7}},
			{Name: "y.txt", Mode: 0o644, Hash: fhY}}, nil)
	newRootHash := treehash.TreeHash(nil, []treehash.TreeEntry{{Name: "d", Hash: newDHash}})
	wantCommit := treehash.CommitHash(newRootHash, []domain.Hash{domain.Hash{5}}, "msg")

	var got ApplyPushRequest
	pushRepo.EXPECT().ApplyPush(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, req ApplyPushRequest) error {
			got = req
			return nil
		})

	fileY := &domain.PushFile{Path: "d/y.txt", Mode: 0o644, SizeBytes: 1, FileHash: fhY, ChunkHashes: []domain.Hash{chY}}
	result, err := uc.Push(ctx, snow.ID(1), "main", oldRootHash.String(), "msg",
		[]*domain.PushFile{fileY},
		[]string{"keep.txt"})
	require.NoError(t, err)
	require.Equal(t, newRootHash, result.TreeHash)
	require.Equal(t, wantCommit, result.CommitHash)
	require.NotNil(t, got.ParentID)
	require.Equal(t, snow.ID(9), *got.ParentID)

	require.Len(t, got.Nodes, 2) // root + d rebuilt; keep.txt dropped from root
	require.Len(t, got.Files, 2) // x.txt retained + y.txt new
	found := map[string]bool{}
	for _, f := range got.Files {
		if f.Name == "x.txt" {
			found["x"] = true
			require.Equal(t, []domain.Hash{domain.Hash{8}}, f.ChunkHashes)
			require.Equal(t, int64(2), f.TreeID)
		}
		if f.Name == "y.txt" {
			found["y"] = true
			require.Equal(t, []domain.Hash{chY}, f.ChunkHashes)
		}
		if f.Name == "keep.txt" {
			t.Fatal("removed file must not be part of the new tree")
		}
	}
	require.True(t, found["x"] && found["y"])
}

func TestPush_StaleBaseSkipsApply(t *testing.T) {
	uc, perm, repo, _, ctx := newPushFixture(t)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{ID: 5, ProjectID: 1, Name: "main", CommitID: idPtr(snow.ID(9))}, nil)
	repo.EXPECT().GetCommit(gomock.Any(), snow.ID(9)).
		Return(&domain.Commit{ID: 9, TreeID: 100, Hash: domain.Hash{5}}, nil)
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(100)).
		Return(&domain.TreeNode{ID: 100, Hash: domain.Hash{6}, Name: "root"}, nil)

	ch, fh := chunkAndFileHash(t, "unwanted")
	_, err := uc.Push(ctx, snow.ID(1), "main", "wrong-base", "msg",
		[]*domain.PushFile{{Path: "a.txt", Mode: 0o644, FileHash: fh, ChunkHashes: []domain.Hash{ch}}},
		nil)
	require.True(t, domain.IsErrorConflict(err))
}

func require400(t *testing.T, err error, msg string) {
	t.Helper()
	require.Error(t, err)
	var dErr *domain.Error
	require.ErrorAs(t, err, &dErr)
	require.Equal(t, 400, dErr.Code)
	require.Contains(t, dErr.Message, msg)
}

func idPtr(id snow.ID) *snow.ID { return &id }
