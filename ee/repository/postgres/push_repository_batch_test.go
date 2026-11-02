// Enterprise Edition: see ee/LICENSE.
package postgres

import (
	"context"
	"fmt"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/treehash"
	"github.com/nipalab/nipa/internal/usecase"
)

func batchNodes(dirs int) []usecase.PushNodeRow {
	nodes := []usecase.PushNodeRow{{ID: 1, Hash: chunker.Sum([]byte("batch root")), Name: "root", Mode: 0o444}}
	for d := 0; d < dirs; d++ {
		name := fmt.Sprintf("d%d", d)
		nodes = append(nodes, usecase.PushNodeRow{ID: int64(d + 2), Hash: chunker.Sum([]byte("batch " + name)), Name: name, Mode: 0o444, ParentID: ptr(int64(1))})
	}
	return nodes
}

func batchRequest(projectID, branchID snow.ID, commit int64, nodes []usecase.PushNodeRow, files []usecase.PushFileRow) usecase.ApplyPushRequest {
	return usecase.ApplyPushRequest{
		ProjectID:  projectID,
		BranchID:   branchID,
		CommitID:   snow.ID(commit),
		CommitHash: treehash.CommitHash(treehash.TreeHash(nil, nil), nil, fmt.Sprintf("batch %d", commit)),
		UserID:     snow.ID(1),
		Message:    "batch",
		Nodes:      nodes,
		Files:      files,
	}
}

func (s *PushRepositorySuite) TestBatchedPushCrossesBatchBoundaries() {
	t := s.T()
	ctx := context.Background()
	repo, branchRepo, projectID, branchID := s.newPushTestEnv()
	db := s.db

	// More distinct chunks than one lookup batch, more files than one insert
	// batch and more links than one link batch.
	const (
		poolSize = 650
		fileCnt  = 5100
		perFile  = 4
		dirs     = 7
	)
	pool := make([]domain.Hash, poolSize)
	for i := range pool {
		hash, size, _ := chunkRow(t, fmt.Sprintf("pool chunk %04d", i))
		pool[i] = hash
		require.NoError(t, repo.InsertChunkIfNotExists(ctx, hash, size))
		require.NoError(t, repo.LinkProjectChunk(ctx, projectID, hash))
	}

	type expectation struct {
		chunks []domain.Hash
		hash   domain.Hash
	}
	want := map[string]expectation{}
	files := make([]usecase.PushFileRow, 0, fileCnt)
	for i := 0; i < fileCnt; i++ {
		chunks := make([]domain.Hash, perFile)
		for k := range chunks {
			chunks[k] = pool[(i*perFile+k)%poolSize]
		}
		name := fmt.Sprintf("f%05d.bin", i)
		fileHash := treehash.FileHash(chunks)
		dir := i % dirs
		files = append(files, usecase.PushFileRow{
			Name: name, Mode: 0o644, SizeBytes: int64(i), IsBinary: i%2 == 0, Encoding: "raw",
			Hash: fileHash, TreeID: int64(dir + 2), ChunkHashes: chunks,
		})
		want[fmt.Sprintf("d%d/%s", dir, name)] = expectation{chunks: chunks, hash: fileHash}
	}

	require.NoError(t, repo.ApplyPush(ctx, batchRequest(projectID, branchID, 5001, batchNodes(dirs), files)))

	var fileRows, linkRows int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM files`).Scan(&fileRows))
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM file_chunks`).Scan(&linkRows))
	require.Equal(t, fileCnt, fileRows)
	require.Equal(t, fileCnt*perFile, linkRows)

	branch := getBranch(t, branchRepo, projectID, branchID)
	commit, err := branchRepo.GetCommit(ctx, *branch.CommitID)
	require.NoError(t, err)
	seen := 0
	for _, dir := range getTreeChildren(t, branchRepo, getTreeNode(t, branchRepo, commit.TreeID).ID) {
		for _, f := range getFilesByTree(t, branchRepo, dir.ID) {
			exp, ok := want[dir.Name+"/"+f.Name]
			require.True(t, ok, "unexpected file %s/%s", dir.Name, f.Name)
			require.Equal(t, exp.hash, f.Hash)
			require.Len(t, f.Chunks, perFile)
			for k, c := range f.Chunks {
				require.Equal(t, exp.chunks[k], c.Hash, "chunk order of %s/%s", dir.Name, f.Name)
			}
			seen++
		}
	}
	require.Equal(t, fileCnt, seen)
}

func (s *PushRepositorySuite) TestBatchedPushKeepsAwkwardFieldValues() {
	t := s.T()
	ctx := context.Background()
	repo, branchRepo, projectID, branchID := s.newPushTestEnv()

	a, sizeA, _ := chunkRow(t, "awkward A")
	b, sizeB, _ := chunkRow(t, "awkward B")
	for _, h := range []struct {
		hash domain.Hash
		size int64
	}{{a, sizeA}, {b, sizeB}} {
		require.NoError(t, repo.InsertChunkIfNotExists(ctx, h.hash, h.size))
		require.NoError(t, repo.LinkProjectChunk(ctx, projectID, h.hash))
	}

	zeroHash := domain.Hash{}
	zeroHash[31] = 0xff
	names := []string{`naïve "quoted" \ back.txt`, "日本語 ファイル.png", "emoji-🎮.bin", "it's [brackets] {braces}.txt", "tab\tand newline\n.txt"}
	files := make([]usecase.PushFileRow, 0, len(names))
	for i, name := range names {
		chunks := []domain.Hash{a, b, a}
		files = append(files, usecase.PushFileRow{
			Name: name, Mode: 0o755 - i, SizeBytes: 1<<40 + int64(i), IsBinary: i%2 == 1, Encoding: []string{"raw", "zstd"}[i%2],
			Hash: zeroHash, TreeID: 2, ChunkHashes: chunks,
		})
	}

	require.NoError(t, repo.ApplyPush(ctx, batchRequest(projectID, branchID, 5002, batchNodes(1), files)))

	branch := getBranch(t, branchRepo, projectID, branchID)
	commit, err := branchRepo.GetCommit(ctx, *branch.CommitID)
	require.NoError(t, err)
	dir := getTreeChildren(t, branchRepo, getTreeNode(t, branchRepo, commit.TreeID).ID)[0]
	got := map[string]*domain.File{}
	for _, f := range getFilesByTree(t, branchRepo, dir.ID) {
		got[f.Name] = f
	}
	require.Len(t, got, len(names))
	for i, name := range names {
		f := got[name]
		require.NotNil(t, f, "file %q", name)
		require.Equal(t, 0o755-i, f.Mode)
		require.Equal(t, int64(1<<40+i), f.SizeBytes)
		require.Equal(t, i%2 == 1, f.IsBinary)
		require.Equal(t, []string{"raw", "zstd"}[i%2], f.Encoding)
		require.Equal(t, zeroHash, f.Hash)
		require.Len(t, f.Chunks, 3)
		require.Equal(t, []domain.Hash{a, b, a}, []domain.Hash{f.Chunks[0].Hash, f.Chunks[1].Hash, f.Chunks[2].Hash},
			"the same chunk may appear twice in one file")
	}
}

func (s *PushRepositorySuite) TestBatchedPushRejectsMissingChunkInLaterBatch() {
	t := s.T()
	ctx := context.Background()
	repo, branchRepo, projectID, branchID := s.newPushTestEnv()
	db := s.db
	before := getBranch(t, branchRepo, projectID, branchID)

	const total = 1100
	chunks := make([]domain.Hash, total)
	for i := range chunks {
		hash, size, _ := chunkRow(t, fmt.Sprintf("late %04d", i))
		chunks[i] = hash
		require.NoError(t, repo.InsertChunkIfNotExists(ctx, hash, size))
		if i != total-1 {
			require.NoError(t, repo.LinkProjectChunk(ctx, projectID, hash))
		}
	}
	files := make([]usecase.PushFileRow, 0, total)
	for i, hash := range chunks {
		files = append(files, usecase.PushFileRow{
			Name: fmt.Sprintf("f%04d", i), Mode: 0o644, SizeBytes: 1, Hash: treehash.FileHash([]domain.Hash{hash}),
			TreeID: 2, ChunkHashes: []domain.Hash{hash},
		})
	}

	err := repo.ApplyPush(ctx, batchRequest(projectID, branchID, 5003, batchNodes(1), files))
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing chunk data for "+chunks[total-1].String())

	var fileRows, nodeRows int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM files`).Scan(&fileRows))
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM tree_nodes`).Scan(&nodeRows))
	require.Zero(t, fileRows, "a rejected push writes nothing")
	require.Zero(t, nodeRows)
	require.Equal(t, before.CommitID, getBranch(t, branchRepo, projectID, branchID).CommitID)
}

func (s *PushRepositorySuite) TestBatchedPushRequiresTheProjectsOwnChunkLink() {
	t := s.T()
	ctx := context.Background()
	repo, _, projectID, branchID := s.newPushTestEnv()
	other := seedProject(t, s.q, 1, "other-project")

	hash, size, fileHash := chunkRow(t, "someone else's chunk")
	require.NoError(t, repo.InsertChunkIfNotExists(ctx, hash, size))
	require.NoError(t, repo.LinkProjectChunk(ctx, other, hash))

	files := []usecase.PushFileRow{{Name: "stolen.bin", Mode: 0o644, SizeBytes: size, Hash: fileHash, TreeID: 2, ChunkHashes: []domain.Hash{hash}}}
	err := repo.ApplyPush(ctx, batchRequest(projectID, branchID, 5004, batchNodes(1), files))
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing chunk data for "+hash.String(),
		"a chunk that exists but is linked to another project must not be usable")
}

func (s *PushRepositorySuite) TestBatchedPushRollsBackOnDuplicateFileName() {
	t := s.T()
	ctx := context.Background()
	repo, branchRepo, projectID, branchID := s.newPushTestEnv()
	before := getBranch(t, branchRepo, projectID, branchID)

	hash, size, fileHash := chunkRow(t, "duplicate name chunk")
	require.NoError(t, repo.InsertChunkIfNotExists(ctx, hash, size))
	require.NoError(t, repo.LinkProjectChunk(ctx, projectID, hash))
	twin := usecase.PushFileRow{Name: "same.txt", Mode: 0o644, SizeBytes: size, Hash: fileHash, TreeID: 2, ChunkHashes: []domain.Hash{hash}}

	err := repo.ApplyPush(ctx, batchRequest(projectID, branchID, 5005, batchNodes(1), []usecase.PushFileRow{twin, twin}))
	require.Error(t, err)

	for _, table := range []string{"files", "file_chunks", "tree_nodes", "commits"} {
		var rows int
		require.NoError(t, s.db.QueryRow(`SELECT COUNT(*) FROM `+table).Scan(&rows))
		require.Zero(t, rows, "%s must be rolled back", table)
	}
	require.Equal(t, before.CommitID, getBranch(t, branchRepo, projectID, branchID).CommitID)
}
