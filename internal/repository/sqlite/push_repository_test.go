package sqlite

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/treehash"
	"github.com/nipalab/nipa/internal/usecase"
)

func newPushTestEnv(t *testing.T) (*PushRepository, *BranchRepository, *sql.DB, snow.ID, snow.ID) {
	t.Helper()
	db, q := newSQLiteTestDB(t)
	projectID := seedProject(t, q, 1, "push-project")
	branchID := seedBranch(t, db, projectID, "main", sql.NullInt64{})
	seedUser(t, q, "pusher", "pusher@example.com", sql.NullString{})
	return NewPushRepository(db), NewBranchRepository(db), db, projectID, branchID
}

func chunkRow(t *testing.T, data string) (domain.Hash, int64, domain.Hash) {
	t.Helper()
	sum := chunker.Sum([]byte(data))
	fileHash := treehash.FileHash([]domain.Hash{sum})
	return sum, int64(len(data)), fileHash
}

func TestPushRepositorySQLite_FirstPush(t *testing.T) {
	ctx := context.Background()
	repo, branchRepo, _, projectID, branchID := newPushTestEnv(t)

	chunkHash, size, fileHash := chunkRow(t, "hello world")
	commitID := snow.ID(12345)

	require.NoError(t, repo.InsertChunkIfNotExists(ctx, chunkHash, size))
	err := repo.ApplyPush(ctx, usecase.ApplyPushRequest{
		ProjectID:  projectID,
		BranchID:   branchID,
		CommitID:   commitID,
		CommitHash: treehash.CommitHash(treehash.TreeHash(nil, nil), nil, "first"),
		UserID:     snow.ID(1),
		Message:    "first commit",
		Nodes: []usecase.PushNodeRow{
			{ID: 1, Hash: treehash.TreeHash(nil, nil), Name: "root", Mode: 0o444},
			{ID: 2, Hash: treehash.TreeHash(nil, nil), Name: "assets", Mode: 0o444, ParentID: ptr(int64(1))},
		},
		Files: []usecase.PushFileRow{
			{Name: "logo.bin", Mode: 0o644, SizeBytes: size, IsBinary: true, Hash: fileHash, TreeID: 2, ChunkHashes: []domain.Hash{chunkHash}},
		},
	})
	require.NoError(t, err)

	branch := getBranch(t, branchRepo, projectID, branchID)
	require.NotNil(t, branch.CommitID)
	commit, err := branchRepo.GetCommit(ctx, *branch.CommitID)
	require.NoError(t, err)
	require.Equal(t, commitID, commit.ID)
	require.Equal(t, "first commit", commit.Message)
	require.Nil(t, commit.Parent1ID)

	root := getTreeNode(t, branchRepo, commit.TreeID)
	require.Equal(t, "root", root.Name)
	children := getTreeChildren(t, branchRepo, root.ID)
	require.Len(t, children, 1)
	require.Equal(t, "assets", children[0].Name)
	files := getFilesByTree(t, branchRepo, children[0].ID)
	require.Len(t, files, 1)
	require.Equal(t, "logo.bin", files[0].Name)
	require.Equal(t, fileHash, files[0].Hash)
	require.Len(t, files[0].Chunks, 1)
	require.Equal(t, chunkHash, files[0].Chunks[0].Hash)
	require.True(t, files[0].IsBinary)
}

func TestPushRepositorySQLite_IncrementalPushKeepsOldCommitReadable(t *testing.T) {
	ctx := context.Background()
	repo, branchRepo, _, projectID, branchID := newPushTestEnv(t)

	ch1, sz1, fh1 := chunkRow(t, "alpha")
	ch2, sz2, fh2 := chunkRow(t, "beta")
	require.NoError(t, repo.InsertChunkIfNotExists(ctx, ch1, sz1))
	require.NoError(t, repo.InsertChunkIfNotExists(ctx, ch2, sz2))

	require.NoError(t, repo.ApplyPush(ctx, usecase.ApplyPushRequest{
		ProjectID: projectID, BranchID: branchID, CommitID: snow.ID(1), UserID: snow.ID(1), Message: "c1",
		CommitHash: treehash.CommitHash(treehash.TreeHash(nil, nil), nil, "c1"),
		Nodes: []usecase.PushNodeRow{
			{ID: 1, Hash: treehash.TreeHash(nil, nil), Name: "root", Mode: 0o444},
			{ID: 2, Hash: treehash.TreeHash(nil, nil), Name: "d1", Mode: 0o444, ParentID: ptr(int64(1))},
			{ID: 3, Hash: domain.Hash{3}, Name: "d2", Mode: 0o444, ParentID: ptr(int64(1))},
		},
		Files: []usecase.PushFileRow{
			{Name: "a.txt", Mode: 0o644, SizeBytes: sz1, Hash: fh1, TreeID: 2, ChunkHashes: []domain.Hash{ch1}},
			{Name: "keep.txt", Mode: 0o644, SizeBytes: sz1, IsBinary: true, Hash: fh1, TreeID: 3, ChunkHashes: []domain.Hash{ch1}},
		},
	}))

	require.NoError(t, repo.ApplyPush(ctx, usecase.ApplyPushRequest{
		ProjectID: projectID, BranchID: branchID, CommitID: snow.ID(2), UserID: snow.ID(1), Message: "c2",
		CommitHash: treehash.CommitHash(treehash.TreeHash(nil, nil), nil, "c2"),
		Nodes: []usecase.PushNodeRow{
			{ID: 1, Hash: treehash.TreeHash(nil, nil), Name: "root", Mode: 0o444},
			{ID: 2, Hash: treehash.TreeHash(nil, nil), Name: "d1", Mode: 0o444, ParentID: ptr(int64(1))},
			{ID: 3, Hash: domain.Hash{9}, Name: "d2", Mode: 0o444, ParentID: ptr(int64(1))},
		},
		Files: []usecase.PushFileRow{
			{Name: "a.txt", Mode: 0o644, SizeBytes: sz2, Hash: fh2, TreeID: 2, ChunkHashes: []domain.Hash{ch2}},
			{Name: "keep.txt", Mode: 0o644, SizeBytes: sz1, IsBinary: true, Hash: fh1, TreeID: 3, ChunkHashes: []domain.Hash{ch1}},
		},
	}))

	// Old commit must still be readable after the branch moved.
	commit1, err := branchRepo.GetCommit(ctx, snow.ID(1))
	require.NoError(t, err)
	require.Equal(t, "c1", commit1.Message)
	oldRoot := getTreeNode(t, branchRepo, commit1.TreeID)
	oldD1 := getTreeChildByName(t, branchRepo, oldRoot.ID, "d1")
	require.Len(t, getFilesByTree(t, branchRepo, oldD1.ID), 1)

	// New head sees both the rewritten and the retained files.
	branch := getBranch(t, branchRepo, projectID, branchID)
	commit2, err := branchRepo.GetCommit(ctx, *branch.CommitID)
	require.NoError(t, err)
	root := getTreeNode(t, branchRepo, commit2.TreeID)
	d1 := getTreeChildByName(t, branchRepo, root.ID, "d1")
	files := getFilesByTree(t, branchRepo, d1.ID)
	require.Len(t, files, 1)
	require.Equal(t, "a.txt", files[0].Name)
	require.Equal(t, fh2, files[0].Hash)
	d2 := getTreeChildByName(t, branchRepo, root.ID, "d2")
	keep := getFilesByTree(t, branchRepo, d2.ID)
	require.Len(t, keep, 1)
	require.Equal(t, "keep.txt", keep[0].Name)
	require.Equal(t, fh1, keep[0].Hash)
}

func TestPushRepositorySQLite_RemoveAllFiles(t *testing.T) {
	ctx := context.Background()
	repo, branchRepo, _, projectID, branchID := newPushTestEnv(t)

	ch, sz, fh := chunkRow(t, "gone")
	require.NoError(t, repo.InsertChunkIfNotExists(ctx, ch, sz))
	require.NoError(t, repo.ApplyPush(ctx, usecase.ApplyPushRequest{
		ProjectID: projectID, BranchID: branchID, CommitID: snow.ID(1), UserID: snow.ID(1), Message: "c1",
		CommitHash: treehash.CommitHash(treehash.TreeHash(nil, nil), nil, "c1"),
		Nodes: []usecase.PushNodeRow{
			{ID: 1, Hash: treehash.TreeHash(nil, nil), Name: "root", Mode: 0o444},
		},
		Files: []usecase.PushFileRow{{Name: "gone.txt", Mode: 0o644, SizeBytes: sz, Hash: fh, TreeID: 1, ChunkHashes: []domain.Hash{ch}}},
	}))
	require.NoError(t, repo.ApplyPush(ctx, usecase.ApplyPushRequest{
		ProjectID: projectID, BranchID: branchID, CommitID: snow.ID(2), UserID: snow.ID(1), Message: "c2",
		CommitHash: treehash.CommitHash(treehash.TreeHash(nil, nil), nil, "c2"),
		Nodes: []usecase.PushNodeRow{
			{ID: 1, Hash: treehash.TreeHash(nil, nil), Name: "root", Mode: 0o444},
		},
	}))

	branch := getBranch(t, branchRepo, projectID, branchID)
	commit2, err := branchRepo.GetCommit(ctx, *branch.CommitID)
	require.NoError(t, err)
	root := getTreeNode(t, branchRepo, commit2.TreeID)
	require.Empty(t, getFilesByTree(t, branchRepo, root.ID))
}

func TestPushRepositorySQLite_MissingChunkDataRejected(t *testing.T) {
	ctx := context.Background()
	repo, _, _, projectID, branchID := newPushTestEnv(t)

	chunkHash, _, fh := chunkRow(t, "hello")
	err := repo.ApplyPush(ctx, usecase.ApplyPushRequest{
		ProjectID:  projectID,
		BranchID:   branchID,
		CommitID:   snow.ID(1),
		CommitHash: treehash.CommitHash(treehash.TreeHash(nil, nil), nil, "first"),
		UserID:     snow.ID(1),
		Message:    "first",
		Nodes: []usecase.PushNodeRow{
			{ID: 1, Hash: treehash.TreeHash(nil, nil), Name: "root", Mode: 0o444},
		},
		Files: []usecase.PushFileRow{{Name: "x", Mode: 0o644, SizeBytes: 5, Hash: fh, TreeID: 1, ChunkHashes: []domain.Hash{chunkHash}}},
	})
	require.Error(t, err)
	var dErr *domain.Error
	require.ErrorAs(t, err, &dErr)
	require.Equal(t, 400, dErr.Code)
}

func ptr[T any](v T) *T { return &v }

func getBranch(t *testing.T, repo *BranchRepository, projectID, branchID snow.ID) *domain.Branch {
	t.Helper()
	b, err := repo.GetByProjectIDAndID(context.Background(), projectID, branchID)
	require.NoError(t, err)
	return b
}

func getTreeNode(t *testing.T, repo *BranchRepository, id int64) *domain.TreeNode {
	t.Helper()
	n, err := repo.GetTreeNode(context.Background(), id)
	require.NoError(t, err)
	return n
}

func getTreeChildren(t *testing.T, repo *BranchRepository, id int64) []*domain.TreeNode {
	t.Helper()
	c, err := repo.ListTreeChildren(context.Background(), id)
	require.NoError(t, err)
	return c
}

func getFilesByTree(t *testing.T, repo *BranchRepository, id int64) []*domain.File {
	t.Helper()
	f, err := repo.ListFilesByTree(context.Background(), id)
	require.NoError(t, err)
	return f
}

func getTreeChildByName(t *testing.T, repo *BranchRepository, parentID int64, name string) *domain.TreeNode {
	t.Helper()
	n, err := repo.GetTreeChildByName(context.Background(), parentID, name)
	require.NoError(t, err)
	return n
}
