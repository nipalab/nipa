package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
)

// assertDeltaReadersMatchSingle checks the two batched readers of the delta
// manifest against their single-directory twins: ListFileMetaByTrees gives
// what ListFileMetaByTree gives per node, and ListChunksByFiles gives every
// file the chunk list ListFilesByTree loads for it.
func assertDeltaReadersMatchSingle(t *testing.T, repo *BranchRepository, ids []int64) {
	t.Helper()
	ctx := context.Background()
	const missingID = int64(1) << 40
	request := append(append([]int64{missingID}, ids...), ids[:3]...)

	meta, err := repo.ListFileMetaByTrees(ctx, request)
	require.NoError(t, err)
	require.NotContains(t, meta, missingID)

	var fileIDs []int64
	wantChunks := map[int64][]domain.Chunk{}
	withFiles := 0
	for _, id := range ids {
		wantMeta, err := repo.ListFileMetaByTree(ctx, id)
		require.NoError(t, err)
		if len(wantMeta) == 0 {
			require.NotContains(t, meta, id)
			continue
		}
		withFiles++
		require.Equal(t, wantMeta, meta[id], "file meta of node %d", id)
		full, err := repo.ListFilesByTree(ctx, id)
		require.NoError(t, err)
		for i, file := range full {
			require.Nil(t, wantMeta[i].Chunks, "meta rows carry no chunks")
			fileIDs = append(fileIDs, file.ID)
			if len(file.Chunks) > 0 {
				wantChunks[file.ID] = file.Chunks
			}
		}
	}
	require.Len(t, meta, withFiles)
	require.Greater(t, len(wantChunks), treeBatchSize, "the files must span more than one id batch")

	// Duplicates and unknown ids must not change the result.
	chunks, err := repo.ListChunksByFiles(ctx, append(append([]int64{missingID}, fileIDs...), fileIDs[:3]...))
	require.NoError(t, err)
	require.Equal(t, wantChunks, chunks)
	multi := 0
	for _, list := range chunks {
		if len(list) > 1 {
			multi++
		}
	}
	require.Positive(t, multi, "chunk-index order must be exercised")

	for _, empty := range [][]int64{nil, {}} {
		none, err := repo.ListFileMetaByTrees(ctx, empty)
		require.NoError(t, err)
		require.Empty(t, none)
		noChunks, err := repo.ListChunksByFiles(ctx, empty)
		require.NoError(t, err)
		require.Empty(t, noChunks)
	}
}

// seedManyChunkedFiles adds a directory with more single-chunk files than one
// id batch carries.
func seedManyChunkedFiles(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	many := seedTreeNode(t, db, "many", sql.NullInt64{})
	for i := 0; i < treeBatchSize+20; i++ {
		seedChunk(t, db, seedFile(t, db, many, fmt.Sprintf("m%04d.bin", i), int64(i)), 0)
	}
	return many
}

func TestBranchRepositorySQLite_DeltaReadersMatchSingle(t *testing.T) {
	db, _ := newSQLiteTestDB(t)
	repo := NewBranchRepository(db)

	ids, alpha, beta := seedBatchTree(t, db)
	ids = append(ids, seedManyChunkedFiles(t, db))
	assertDeltaReadersMatchSingle(t, repo, ids)

	meta, err := repo.ListFileMetaByTrees(context.Background(), []int64{alpha, beta})
	require.NoError(t, err)
	require.Equal(t, meta[alpha], meta[beta])
	require.NotSame(t, meta[alpha][0], meta[beta][0], "identical directories must not share File values")
}

func TestBranchRepositorySQLite_DeltaReaders_Errors(t *testing.T) {
	ctx := context.Background()
	db, _ := newSQLiteTestDB(t)
	repo := NewBranchRepository(db)
	root := seedTreeNode(t, db, "root", sql.NullInt64{})
	file := seedFile(t, db, root, "a.txt", 1)
	seedChunk(t, db, file, 0)

	_, err := db.ExecContext(ctx, `DROP TABLE file_chunks`)
	require.NoError(t, err)
	_, err = repo.ListChunksByFiles(ctx, []int64{file})
	require.Error(t, err)
	_, err = repo.ListFileMetaByTrees(ctx, []int64{root})
	require.NoError(t, err, "file meta does not read chunks")

	_, err = db.ExecContext(ctx, `DROP TABLE files`)
	require.NoError(t, err)
	_, err = repo.ListFileMetaByTrees(ctx, []int64{root})
	require.Error(t, err)
}
