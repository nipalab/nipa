package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/treehash"
	"github.com/nipalab/nipa/internal/usecase"
)

// legacyFormatTreeHash is the tree hash before file modes were part of it.
func legacyFormatTreeHash(files []treehash.FileEntry, trees []treehash.TreeEntry) domain.Hash {
	plain := make([]treehash.FileEntry, len(files))
	for i, f := range files {
		plain[i] = treehash.FileEntry{Name: f.Name, Hash: f.Hash}
	}
	return treehash.TreeHash(plain, trees)
}

// TestTreeHashRepositorySQLite_Backfill stores three commits the way a server
// without mode-aware hashes did: a read-write lib.so, a mode-only change to
// 0755 (same hash, so reads still returned the read-write rows) and a commit
// keeping lib/ by reference. The backfill gives every directory the hash of
// its own rows, so the chmod commit reads back as executable.
func TestTreeHashRepositorySQLite_Backfill(t *testing.T) {
	ctx := context.Background()
	repo, branchRepo, db, projectID, branchID := newPushTestEnv(t)
	trees := NewTreeHashRepository(db)

	libChunk, libSize, libHash := chunkRow(t, "elf")
	aChunk, aSize, aHash := chunkRow(t, "a")
	bChunk, bSize, bHash := chunkRow(t, "b")
	for _, c := range []struct {
		hash domain.Hash
		size int64
	}{{libChunk, libSize}, {aChunk, aSize}, {bChunk, bSize}} {
		require.NoError(t, repo.InsertChunkIfNotExists(ctx, c.hash, c.size))
		require.NoError(t, repo.LinkProjectChunk(ctx, projectID, c.hash))
	}

	libRW := treehash.FileEntry{Name: "lib.so", Hash: libHash, Mode: 0o644}
	libX := treehash.FileEntry{Name: "lib.so", Hash: libHash, Mode: 0o755}
	fileA := treehash.FileEntry{Name: "a.txt", Hash: aHash, Mode: 0o644}
	fileB := treehash.FileEntry{Name: "a.txt", Hash: bHash, Mode: 0o644}
	oldLib := legacyFormatTreeHash([]treehash.FileEntry{libRW}, nil)
	oldRootA := legacyFormatTreeHash([]treehash.FileEntry{fileA}, []treehash.TreeEntry{{Name: "lib", Hash: oldLib}})
	oldRootB := legacyFormatTreeHash([]treehash.FileEntry{fileB}, []treehash.TreeEntry{{Name: "lib", Hash: oldLib}})

	libRow := func(mode int) usecase.PushFileRow {
		return usecase.PushFileRow{Name: "lib.so", Mode: mode, SizeBytes: libSize, Hash: libHash, TreeID: 2, ChunkHashes: []domain.Hash{libChunk}}
	}
	aRow := usecase.PushFileRow{Name: "a.txt", Mode: 0o644, SizeBytes: aSize, Hash: aHash, TreeID: 1, ChunkHashes: []domain.Hash{aChunk}}
	bRow := usecase.PushFileRow{Name: "a.txt", Mode: 0o644, SizeBytes: bSize, Hash: bHash, TreeID: 1, ChunkHashes: []domain.Hash{bChunk}}
	fullCommit := func(id snow.ID, parent *snow.ID, libMode int) usecase.ApplyPushRequest {
		return usecase.ApplyPushRequest{
			ProjectID: projectID, BranchID: branchID, CommitID: id, ParentID: parent,
			CommitHash: treehash.CommitHash(oldRootA, nil, id.Base36()),
			UserID:     snow.ID(1), Message: id.Base36(),
			Nodes: []usecase.PushNodeRow{
				{ID: 1, Hash: oldRootA, Name: "root", Mode: 0o444},
				{ID: 2, Hash: oldLib, Name: "lib", Mode: 0o444, ParentID: ptr(int64(1))},
			},
			Files: []usecase.PushFileRow{aRow, libRow(libMode)},
		}
	}
	require.NoError(t, repo.ApplyPush(ctx, fullCommit(snow.ID(101), nil, 0o644)))
	require.NoError(t, repo.ApplyPush(ctx, fullCommit(snow.ID(102), ptr(snow.ID(101)), 0o755)))
	require.NoError(t, repo.ApplyPush(ctx, usecase.ApplyPushRequest{
		ProjectID: projectID, BranchID: branchID, CommitID: snow.ID(103), ParentID: ptr(snow.ID(102)),
		CommitHash: treehash.CommitHash(oldRootB, nil, "keep lib"),
		UserID:     snow.ID(1), Message: "keep lib",
		Nodes: []usecase.PushNodeRow{
			{ID: 1, Hash: oldRootB, Name: "root", Mode: 0o444},
			{ID: 2, Hash: oldLib, Name: "lib", Mode: 0o444, ParentID: ptr(int64(1))},
		},
		Files: []usecase.PushFileRow{bRow},
	}))

	libMode := func(commitID snow.ID) int {
		t.Helper()
		commit, err := branchRepo.GetCommit(ctx, commitID)
		require.NoError(t, err)
		lib, err := branchRepo.GetTreeChildByName(ctx, commit.TreeID, "lib")
		require.NoError(t, err)
		files, err := branchRepo.ListFilesByTree(ctx, lib.ID)
		require.NoError(t, err)
		require.Len(t, files, 1)
		return files[0].Mode
	}
	rootHash := func(commitID snow.ID) (int64, domain.Hash) {
		t.Helper()
		commit, err := branchRepo.GetCommit(ctx, commitID)
		require.NoError(t, err)
		root, err := branchRepo.GetTreeNode(ctx, commit.TreeID)
		require.NoError(t, err)
		return root.ID, root.Hash
	}
	require.Equal(t, 0o644, libMode(snow.ID(102)), "before the backfill the chmod commit reads the oldest rows")

	res, err := usecase.BackfillTreeHashes(ctx, trees)
	require.NoError(t, err)
	require.Equal(t, 1, res.Seeds)
	require.Equal(t, 2, res.Updated, "only the chmod commit's lib/ and root change")

	newLibX := treehash.TreeHash([]treehash.FileEntry{libX}, nil)
	require.Equal(t, 0o755, libMode(snow.ID(102)))
	require.Equal(t, 0o644, libMode(snow.ID(101)))
	require.Equal(t, 0o644, libMode(snow.ID(103)), "the reference keeps the rows it resolved to")

	root101, hash101 := rootHash(snow.ID(101))
	root102, hash102 := rootHash(snow.ID(102))
	_, hash103 := rootHash(snow.ID(103))
	require.Equal(t, oldRootA, hash101)
	require.Equal(t, treehash.TreeHash([]treehash.FileEntry{fileA}, []treehash.TreeEntry{{Name: "lib", Hash: newLibX}}), hash102)
	require.Equal(t, oldRootB, hash103)

	legacy, err := trees.LegacyTreeHash(ctx, root102)
	require.NoError(t, err)
	require.NotNil(t, legacy)
	require.Equal(t, oldRootA, *legacy)
	legacy, err = trees.LegacyTreeHash(ctx, root101)
	require.NoError(t, err)
	require.Nil(t, legacy)

	// The watermark is stored: a second pass has nothing to do.
	res, err = usecase.BackfillTreeHashes(ctx, NewTreeHashRepository(db))
	require.NoError(t, err)
	require.Equal(t, res.FromID, res.UpToID)
	require.Zero(t, res.Updated)

	// A full rerun is a no-op too.
	_, err = db.ExecContext(ctx, "UPDATE tree_hash_backfill SET watermark = 0 WHERE id = 1")
	require.NoError(t, err)
	res, err = usecase.BackfillTreeHashes(ctx, trees)
	require.NoError(t, err)
	require.Equal(t, 1, res.Seeds)
	require.Zero(t, res.Updated)
	_, hash102again := rootHash(snow.ID(102))
	require.Equal(t, hash102, hash102again)
}

func TestTreeHashRepositorySQLite_EmptyDatabase(t *testing.T) {
	ctx := context.Background()
	_, _, db, _, _ := newPushTestEnv(t)
	trees := NewTreeHashRepository(db)
	res, err := usecase.BackfillTreeHashes(ctx, trees)
	require.NoError(t, err)
	require.Equal(t, usecase.TreeHashBackfillResult{}, res)
	legacy, err := trees.LegacyTreeHash(ctx, 42)
	require.NoError(t, err)
	require.Nil(t, legacy)
	_, err = trees.TreeHashNode(ctx, 42)
	require.True(t, domain.IsErrorNotFound(err))
}
