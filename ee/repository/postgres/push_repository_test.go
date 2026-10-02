package postgres

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/treehash"
	"github.com/nipalab/nipa/internal/usecase"
)

type PushRepositorySuite struct {
	baseSuite
}

func TestPushRepositorySuite(t *testing.T) {
	suite.Run(t, new(PushRepositorySuite))
}

func (s *PushRepositorySuite) newPushTestEnv() (*PushRepository, *BranchRepository, snow.ID, snow.ID) {
	s.T().Helper()

	projectID := seedProject(s.T(), s.q, 1, "push-project")
	branchID := seedBranch(s.T(), s.db, projectID, "main", sql.NullInt64{})
	seedUser(s.T(), s.q, "pusher", "pusher@example.com", sql.NullString{})
	return NewPushRepository(s.db), NewBranchRepository(s.db), projectID, branchID
}

func chunkRow(t *testing.T, data string) (domain.Hash, int64, domain.Hash) {
	t.Helper()
	sum := chunker.Sum([]byte(data))
	fileHash := treehash.FileHash([]domain.Hash{sum})
	return sum, int64(len(data)), fileHash
}

func (s *PushRepositorySuite) TestHasChunk() {
	ctx := context.Background()
	repo, _, _, _ := s.newPushTestEnv()

	present, size, _ := chunkRow(s.T(), "present")
	absent, _, _ := chunkRow(s.T(), "absent")

	s.Require().NoError(repo.InsertChunkIfNotExists(ctx, present, size))

	ok, err := repo.HasChunk(ctx, present)
	s.Require().NoError(err)
	s.True(ok)

	ok, err = repo.HasChunk(ctx, absent)
	s.Require().NoError(err)
	s.False(ok)
}

func (s *PushRepositorySuite) TestFirstPush() {
	ctx := context.Background()
	repo, branchRepo, projectID, branchID := s.newPushTestEnv()

	chunkHash, size, fileHash := chunkRow(s.T(), "hello world")
	commitID := snow.ID(12345)

	s.Require().NoError(repo.InsertChunkIfNotExists(ctx, chunkHash, size))
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
	s.Require().NoError(err)

	branch := getBranch(s.T(), branchRepo, projectID, branchID)
	s.Require().NotNil(branch.CommitID)
	commit, err := branchRepo.GetCommit(ctx, *branch.CommitID)
	s.Require().NoError(err)
	s.Equal(commitID, commit.ID)
	s.Equal("first commit", commit.Message)
	s.Nil(commit.Parent1ID)

	root := getTreeNode(s.T(), branchRepo, commit.TreeID)
	s.Equal("root", root.Name)
	children := getTreeChildren(s.T(), branchRepo, root.ID)
	s.Len(children, 1)
	s.Equal("assets", children[0].Name)
	files := getFilesByTree(s.T(), branchRepo, children[0].ID)
	s.Len(files, 1)
	s.Equal("logo.bin", files[0].Name)
	s.Equal(fileHash, files[0].Hash)
	s.Len(files[0].Chunks, 1)
	s.Equal(chunkHash, files[0].Chunks[0].Hash)
	s.True(files[0].IsBinary)
}

func (s *PushRepositorySuite) TestIncrementalPushKeepsOldCommitReadable() {
	ctx := context.Background()
	repo, branchRepo, projectID, branchID := s.newPushTestEnv()

	ch1, sz1, fh1 := chunkRow(s.T(), "alpha")
	ch2, sz2, fh2 := chunkRow(s.T(), "beta")
	s.Require().NoError(repo.InsertChunkIfNotExists(ctx, ch1, sz1))
	s.Require().NoError(repo.InsertChunkIfNotExists(ctx, ch2, sz2))

	d1Hash1 := treehash.TreeHash([]treehash.FileEntry{{Name: "a.txt", Mode: 0o644, Hash: fh1}}, nil)
	d1Hash2 := treehash.TreeHash([]treehash.FileEntry{{Name: "a.txt", Mode: 0o644, Hash: fh2}}, nil)
	d2Hash := treehash.TreeHash([]treehash.FileEntry{{Name: "keep.txt", Mode: 0o644, Hash: fh1}}, nil)
	rootHash1 := treehash.TreeHash(nil, []treehash.TreeEntry{
		{Name: "d1", Hash: d1Hash1},
		{Name: "d2", Hash: d2Hash},
	})
	rootHash2 := treehash.TreeHash(nil, []treehash.TreeEntry{
		{Name: "d1", Hash: d1Hash2},
		{Name: "d2", Hash: d2Hash},
	})

	s.Require().NoError(repo.ApplyPush(ctx, usecase.ApplyPushRequest{
		ProjectID: projectID, BranchID: branchID, CommitID: snow.ID(1), UserID: snow.ID(1), Message: "c1",
		CommitHash: treehash.CommitHash(rootHash1, nil, "c1"),
		Nodes: []usecase.PushNodeRow{
			{ID: 1, Hash: rootHash1, Name: "root", Mode: 0o444},
			{ID: 2, Hash: d1Hash1, Name: "d1", Mode: 0o444, ParentID: ptr(int64(1))},
			{ID: 3, Hash: d2Hash, Name: "d2", Mode: 0o444, ParentID: ptr(int64(1))},
		},
		Files: []usecase.PushFileRow{
			{Name: "a.txt", Mode: 0o644, SizeBytes: sz1, Hash: fh1, TreeID: 2, ChunkHashes: []domain.Hash{ch1}},
			{Name: "keep.txt", Mode: 0o644, SizeBytes: sz1, IsBinary: true, Hash: fh1, TreeID: 3, ChunkHashes: []domain.Hash{ch1}},
		},
	}))

	s.Require().NoError(repo.ApplyPush(ctx, usecase.ApplyPushRequest{
		ProjectID: projectID, BranchID: branchID, CommitID: snow.ID(2), UserID: snow.ID(1), Message: "c2",
		CommitHash: treehash.CommitHash(rootHash2, nil, "c2"),
		Nodes: []usecase.PushNodeRow{
			{ID: 1, Hash: rootHash2, Name: "root", Mode: 0o444},
			{ID: 2, Hash: d1Hash2, Name: "d1", Mode: 0o444, ParentID: ptr(int64(1))},
			// d2 is untouched: only a hash reference, its rows live in commit 1.
			{ID: 3, Hash: d2Hash, Name: "d2", Mode: 0o444, ParentID: ptr(int64(1))},
		},
		Files: []usecase.PushFileRow{
			{Name: "a.txt", Mode: 0o644, SizeBytes: sz2, Hash: fh2, TreeID: 2, ChunkHashes: []domain.Hash{ch2}},
		},
	}))

	// Old commit must still be readable after the branch moved.
	commit1, err := branchRepo.GetCommit(ctx, snow.ID(1))
	s.Require().NoError(err)
	s.Equal("c1", commit1.Message)
	oldRoot := getTreeNode(s.T(), branchRepo, commit1.TreeID)
	oldD1 := getTreeChildByName(s.T(), branchRepo, oldRoot.ID, "d1")
	s.Len(getFilesByTree(s.T(), branchRepo, oldD1.ID), 1)

	// New head sees both the rewritten and the retained files.
	branch := getBranch(s.T(), branchRepo, projectID, branchID)
	commit2, err := branchRepo.GetCommit(ctx, *branch.CommitID)
	s.Require().NoError(err)
	root := getTreeNode(s.T(), branchRepo, commit2.TreeID)
	d1 := getTreeChildByName(s.T(), branchRepo, root.ID, "d1")
	files := getFilesByTree(s.T(), branchRepo, d1.ID)
	s.Len(files, 1)
	s.Equal("a.txt", files[0].Name)
	s.Equal(fh2, files[0].Hash)
	d2 := getTreeChildByName(s.T(), branchRepo, root.ID, "d2")
	keep := getFilesByTree(s.T(), branchRepo, d2.ID)
	s.Len(keep, 1)
	s.Equal("keep.txt", keep[0].Name)
	s.Equal(fh1, keep[0].Hash)
}

func (s *PushRepositorySuite) TestRemoveAllFiles() {
	ctx := context.Background()
	repo, branchRepo, projectID, branchID := s.newPushTestEnv()

	ch, sz, fh := chunkRow(s.T(), "gone")
	s.Require().NoError(repo.InsertChunkIfNotExists(ctx, ch, sz))
	rootHash1 := treehash.TreeHash([]treehash.FileEntry{{Name: "gone.txt", Mode: 0o644, Hash: fh}}, nil)
	s.Require().NoError(repo.ApplyPush(ctx, usecase.ApplyPushRequest{
		ProjectID: projectID, BranchID: branchID, CommitID: snow.ID(1), UserID: snow.ID(1), Message: "c1",
		CommitHash: treehash.CommitHash(rootHash1, nil, "c1"),
		Nodes: []usecase.PushNodeRow{
			{ID: 1, Hash: rootHash1, Name: "root", Mode: 0o444},
		},
		Files: []usecase.PushFileRow{{Name: "gone.txt", Mode: 0o644, SizeBytes: sz, Hash: fh, TreeID: 1, ChunkHashes: []domain.Hash{ch}}},
	}))
	s.Require().NoError(repo.ApplyPush(ctx, usecase.ApplyPushRequest{
		ProjectID: projectID, BranchID: branchID, CommitID: snow.ID(2), UserID: snow.ID(1), Message: "c2",
		CommitHash: treehash.CommitHash(treehash.TreeHash(nil, nil), nil, "c2"),
		Nodes: []usecase.PushNodeRow{
			{ID: 1, Hash: treehash.TreeHash(nil, nil), Name: "root", Mode: 0o444},
		},
	}))

	branch := getBranch(s.T(), branchRepo, projectID, branchID)
	commit2, err := branchRepo.GetCommit(ctx, *branch.CommitID)
	s.Require().NoError(err)
	root := getTreeNode(s.T(), branchRepo, commit2.TreeID)
	s.Empty(getFilesByTree(s.T(), branchRepo, root.ID))
}

func (s *PushRepositorySuite) TestMissingChunkDataRejected() {
	ctx := context.Background()
	repo, _, projectID, branchID := s.newPushTestEnv()

	chunkHash, _, fh := chunkRow(s.T(), "hello")
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
	s.Require().Error(err)
	var dErr *domain.Error
	s.Require().ErrorAs(err, &dErr)
	s.Equal(400, dErr.Code)
}

func (s *PushRepositorySuite) TestMergeCommitWithParent2() {
	ctx := context.Background()
	repo, branchRepo, projectID, branchID := s.newPushTestEnv()

	ch, sz, fh := chunkRow(s.T(), "data")
	s.Require().NoError(repo.InsertChunkIfNotExists(ctx, ch, sz))
	rootHashA := treehash.TreeHash([]treehash.FileEntry{{Name: "a.txt", Mode: 0o644, Hash: fh}}, nil)
	rootHashB := treehash.TreeHash([]treehash.FileEntry{{Name: "b.txt", Mode: 0o644, Hash: fh}}, nil)

	s.Require().NoError(repo.ApplyPush(ctx, usecase.ApplyPushRequest{
		ProjectID: projectID, BranchID: branchID, CommitID: snow.ID(1), UserID: snow.ID(1), Message: "A",
		CommitHash: treehash.CommitHash(rootHashA, nil, "A"),
		Nodes:      []usecase.PushNodeRow{{ID: 1, Hash: rootHashA, Name: "root", Mode: 0o444}},
		Files:      []usecase.PushFileRow{{Name: "a.txt", Mode: 0o644, SizeBytes: sz, Hash: fh, TreeID: 1, ChunkHashes: []domain.Hash{ch}}},
	}))

	s.Require().NoError(repo.ApplyPush(ctx, usecase.ApplyPushRequest{
		ProjectID: projectID, BranchID: branchID, CommitID: snow.ID(2), UserID: snow.ID(1), Message: "B",
		CommitHash: treehash.CommitHash(rootHashB, nil, "B"),
		ParentID:   ptr(snow.ID(1)),
		Nodes:      []usecase.PushNodeRow{{ID: 1, Hash: rootHashB, Name: "root", Mode: 0o444}},
		Files:      []usecase.PushFileRow{{Name: "b.txt", Mode: 0o644, SizeBytes: sz, Hash: fh, TreeID: 1, ChunkHashes: []domain.Hash{ch}}},
	}))

	s.Require().NoError(repo.ApplyPush(ctx, usecase.ApplyPushRequest{
		ProjectID: projectID, BranchID: branchID, CommitID: snow.ID(3), UserID: snow.ID(1), Message: "merge",
		CommitHash: treehash.CommitHash(treehash.TreeHash(nil, nil), []domain.Hash{{1}, {2}}, "merge"),
		ParentID:   ptr(snow.ID(2)),
		ParentID2:  ptr(snow.ID(1)),
		Nodes:      []usecase.PushNodeRow{{ID: 1, Hash: treehash.TreeHash(nil, nil), Name: "root", Mode: 0o444}},
	}))

	branch := getBranch(s.T(), branchRepo, projectID, branchID)
	s.Require().NotNil(branch.CommitID)
	merge, err := branchRepo.GetCommit(ctx, *branch.CommitID)
	s.Require().NoError(err)
	s.Equal(snow.ID(3), merge.ID)
	s.Require().NotNil(merge.Parent1ID)
	s.Equal(snow.ID(2), *merge.Parent1ID)
	s.Require().NotNil(merge.Parent2ID)
	s.Equal(snow.ID(1), *merge.Parent2ID)

	parentB, err := branchRepo.GetCommit(ctx, snow.ID(2))
	s.Require().NoError(err)
	s.Equal("B", parentB.Message)
	rb := getTreeNode(s.T(), branchRepo, parentB.TreeID)
	s.Equal("b.txt", getFilesByTree(s.T(), branchRepo, rb.ID)[0].Name)

	parentA, err := branchRepo.GetCommit(ctx, snow.ID(1))
	s.Require().NoError(err)
	s.Equal("A", parentA.Message)
	ra := getTreeNode(s.T(), branchRepo, parentA.TreeID)
	s.Equal("a.txt", getFilesByTree(s.T(), branchRepo, ra.ID)[0].Name)
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
