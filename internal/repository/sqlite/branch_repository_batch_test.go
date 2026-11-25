package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
)

func seedTreeNodeWithHash(t *testing.T, db *sql.DB, hash []byte, name string, parentID sql.NullInt64) int64 {
	t.Helper()

	res, err := db.ExecContext(context.Background(),
		`INSERT INTO tree_nodes (hash, name, mode, parent_tree_id) VALUES (?, ?, ?, ?)`,
		hash, name, 0o755, parentID,
	)
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return id
}

// seedBatchTree seeds a tree that exercises every resolution case of the batched
// tree readers and returns all node ids worth querying.
func seedBatchTree(t *testing.T, db *sql.DB) (ids []int64, alpha, beta int64) {
	t.Helper()
	child := func(parent int64) sql.NullInt64 { return sql.NullInt64{Int64: parent, Valid: true} }
	add := func(id int64) int64 {
		ids = append(ids, id)
		return id
	}

	sharedHash := testHashBytes()
	// A ref created before its content node: the content has the higher id.
	add(seedTreeNodeWithHash(t, db, sharedHash, "early", sql.NullInt64{}))

	root := add(seedTreeNode(t, db, "root", sql.NullInt64{}))
	a := seedFile(t, db, root, "a.txt", 10)
	seedChunk(t, db, a, 1)
	seedChunk(t, db, a, 0)
	seedFile(t, db, root, "b.txt", 20)

	// alpha owns the rows; beta is a second path with identical content.
	alpha = add(seedTreeNodeWithHash(t, db, sharedHash, "alpha", child(root)))
	x := seedFile(t, db, alpha, "x.bin", 300)
	seedChunk(t, db, x, 2)
	seedChunk(t, db, x, 0)
	seedChunk(t, db, x, 1)
	y := seedFile(t, db, alpha, "y.txt", 5)
	seedChunk(t, db, y, 0)
	deep := add(seedTreeNode(t, db, "deep", child(alpha)))
	seedChunk(t, db, seedFile(t, db, deep, "z.txt", 7), 0)
	beta = add(seedTreeNodeWithHash(t, db, sharedHash, "beta", child(root)))

	add(seedTreeNode(t, db, "empty", child(root)))

	// A directory with only subdirectories, plus a ref to it.
	dirsHash := testHashBytes()
	onlyDirs := add(seedTreeNodeWithHash(t, db, dirsHash, "onlydirs", child(root)))
	inner := add(seedTreeNode(t, db, "inner", child(onlyDirs)))
	seedFile(t, db, inner, "leaf.txt", 1)
	add(seedTreeNode(t, db, "inner-b", child(onlyDirs)))
	add(seedTreeNodeWithHash(t, db, dirsHash, "onlydirs-ref", child(root)))

	// The children variant accepts a content node with files OR children, the
	// files variant only one with files: here they resolve to different rows.
	splitHash := testHashBytes()
	splitDirs := add(seedTreeNodeWithHash(t, db, splitHash, "split-dirs", child(root)))
	add(seedTreeNode(t, db, "sub", child(splitDirs)))
	splitFiles := add(seedTreeNodeWithHash(t, db, splitHash, "split-files", child(root)))
	seedChunk(t, db, seedFile(t, db, splitFiles, "s.txt", 3), 0)
	add(seedTreeNodeWithHash(t, db, splitHash, "split-ref", child(root)))

	// More directories than one id batch carries.
	wide := add(seedTreeNode(t, db, "wide", child(root)))
	for i := 0; i < treeBatchSize+50; i++ {
		dir := add(seedTreeNode(t, db, fmt.Sprintf("w%04d", i), child(wide)))
		if i%400 == 0 || i == treeBatchSize-1 || i == treeBatchSize {
			seedChunk(t, db, seedFile(t, db, dir, "w.txt", int64(i)), 0)
		}
	}
	return ids, alpha, beta
}

func assertBatchMatchesSingle(t *testing.T, repo *BranchRepository, ids []int64) (map[int64][]*domain.TreeNode, map[int64][]*domain.File) {
	t.Helper()
	ctx := context.Background()
	const missingID = int64(1) << 40
	// Duplicates and unknown ids must not change the result.
	request := append(append([]int64{missingID}, ids...), ids[:3]...)
	require.Greater(t, len(ids), treeBatchSize, "the tree must span more than one id batch")

	children, err := repo.ListTreeChildrenByTrees(ctx, request)
	require.NoError(t, err)
	files, err := repo.ListFilesByTrees(ctx, request)
	require.NoError(t, err)
	require.NotContains(t, children, missingID)
	require.NotContains(t, files, missingID)

	withChildren, withFiles, withChunks := 0, 0, 0
	for _, id := range ids {
		wantChildren, err := repo.ListTreeChildren(ctx, id)
		require.NoError(t, err)
		if len(wantChildren) == 0 {
			require.NotContains(t, children, id)
		} else {
			withChildren++
			require.Equal(t, wantChildren, children[id], "children of node %d", id)
		}

		wantFiles, err := repo.ListFilesByTree(ctx, id)
		require.NoError(t, err)
		if len(wantFiles) == 0 {
			require.NotContains(t, files, id)
			continue
		}
		withFiles++
		require.Equal(t, wantFiles, files[id], "files of node %d", id)
		for _, file := range wantFiles {
			if len(file.Chunks) > 1 {
				withChunks++
			}
		}
	}
	require.Len(t, children, withChildren)
	require.Len(t, files, withFiles)
	require.Positive(t, withChunks)
	return children, files
}

func assertBatchResultsNotAliased(t *testing.T, files map[int64][]*domain.File, alpha, beta int64) {
	t.Helper()
	require.Len(t, files[alpha], 2)
	require.Len(t, files[beta], 2)
	require.Equal(t, files[alpha], files[beta])
	require.NotSame(t, files[alpha][0], files[beta][0])
	require.Len(t, files[alpha][0].Chunks, 3)
	want := files[beta][0].Chunks[0]
	files[alpha][0].Chunks[0] = domain.Chunk{ID: -1}
	require.Equal(t, want, files[beta][0].Chunks[0], "identical directories must not share a chunk slice")
}

func TestBranchRepositorySQLite_BatchTreeReadersMatchSingle(t *testing.T) {
	db, _ := newSQLiteTestDB(t)
	repo := NewBranchRepository(db)

	ids, alpha, beta := seedBatchTree(t, db)
	_, files := assertBatchMatchesSingle(t, repo, ids)
	assertBatchResultsNotAliased(t, files, alpha, beta)
}

func TestBranchRepositorySQLite_BatchTreeReaders_NoIDs(t *testing.T) {
	ctx := context.Background()
	db, _ := newSQLiteTestDB(t)
	repo := NewBranchRepository(db)
	seedFile(t, db, seedTreeNode(t, db, "root", sql.NullInt64{}), "a.txt", 1)

	for _, ids := range [][]int64{nil, {}} {
		children, err := repo.ListTreeChildrenByTrees(ctx, ids)
		require.NoError(t, err)
		require.Empty(t, children)
		files, err := repo.ListFilesByTrees(ctx, ids)
		require.NoError(t, err)
		require.Empty(t, files)
	}
}

func TestBranchRepositorySQLite_BatchTreeReaders_Errors(t *testing.T) {
	ctx := context.Background()
	db, _ := newSQLiteTestDB(t)
	repo := NewBranchRepository(db)
	root := seedTreeNode(t, db, "root", sql.NullInt64{})
	seedChunk(t, db, seedFile(t, db, root, "a.txt", 1), 0)

	_, err := db.ExecContext(ctx, `DROP TABLE file_chunks`)
	require.NoError(t, err)
	_, err = repo.ListFilesByTrees(ctx, []int64{root})
	require.Error(t, err)

	_, err = db.ExecContext(ctx, `DROP TABLE files`)
	require.NoError(t, err)
	_, err = repo.ListFilesByTrees(ctx, []int64{root})
	require.Error(t, err)
	_, err = repo.ListTreeChildrenByTrees(ctx, []int64{root})
	require.Error(t, err)
}
