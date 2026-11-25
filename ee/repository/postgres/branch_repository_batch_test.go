package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

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

func (s *BranchRepositorySuite) TestBatchTreeReadersMatchSingle() {
	repo := NewBranchRepository(s.db)

	ids, alpha, beta := seedBatchTree(s.T(), s.db)
	_, files := assertBatchMatchesSingle(s.T(), repo, ids)
	assertBatchResultsNotAliased(s.T(), files, alpha, beta)
}

func (s *BranchRepositorySuite) TestBatchTreeReaders_NoIDs() {
	ctx := context.Background()
	repo := NewBranchRepository(s.db)
	seedFile(s.T(), s.db, seedTreeNode(s.T(), s.db, "root", sql.NullInt64{}), "a.txt", 1)

	for _, ids := range [][]int64{nil, {}} {
		children, err := repo.ListTreeChildrenByTrees(ctx, ids)
		s.Require().NoError(err)
		s.Empty(children)
		files, err := repo.ListFilesByTrees(ctx, ids)
		s.Require().NoError(err)
		s.Empty(files)
	}
}

func (s *BranchRepositorySuite) TestBatchTreeReaders_Errors() {
	ctx := context.Background()
	repo := NewBranchRepository(s.db)
	root := seedTreeNode(s.T(), s.db, "root", sql.NullInt64{})
	seedChunk(s.T(), s.db, seedFile(s.T(), s.db, root, "a.txt", 1), 0)

	_, err := s.db.ExecContext(ctx, `DROP TABLE file_chunks CASCADE`)
	s.Require().NoError(err)
	_, err = repo.ListFilesByTrees(ctx, []int64{root})
	s.Require().Error(err)

	_, err = s.db.ExecContext(ctx, `DROP TABLE files CASCADE`)
	s.Require().NoError(err)
	_, err = repo.ListFilesByTrees(ctx, []int64{root})
	s.Require().Error(err)
	_, err = repo.ListTreeChildrenByTrees(ctx, []int64{root})
	s.Require().Error(err)
}

// allowAllPermissions lets the manifest scale harness drive the real Branch
// usecase without PBAC rows.
type allowAllPermissions struct{}

func (allowAllPermissions) HasProjectAccess(context.Context, snow.ID, domain.Permission) bool {
	return true
}

func (allowAllPermissions) HasPathAccess(context.Context, snow.ID, string, domain.Permission) bool {
	return true
}

func (allowAllPermissions) CompileFilter(context.Context, snow.ID, domain.Permission) (*usecase.PathFilter, error) {
	return usecase.AllowAllFilter(), nil
}

func (allowAllPermissions) AdminHasProject(context.Context, snow.ID) bool { return true }

// perDirectoryManifest is the per-directory walk loadTreeManifest used before
// it loaded whole levels: two repository calls (three statements) per directory.
func perDirectoryManifest(ctx context.Context, repo *BranchRepository, node *domain.TreeNode, calls *int) error {
	files, err := repo.ListFilesByTree(ctx, node.ID)
	if err != nil {
		return err
	}
	children, err := repo.ListTreeChildren(ctx, node.ID)
	if err != nil {
		return err
	}
	*calls += 2
	node.FileChildren = files
	for _, child := range children {
		if err := perDirectoryManifest(ctx, repo, child, calls); err != nil {
			return err
		}
		node.TreeChildren = append(node.TreeChildren, child)
	}
	return nil
}

func countManifestFiles(node *domain.TreeNode) int {
	n := len(node.FileChildren)
	for _, child := range node.TreeChildren {
		n += countManifestFiles(child)
	}
	return n
}

// TestManifestLoadScale times one full manifest on postgres with the old
// per-directory walk and with the level-batched loader, on a tree of 8*5*N leaf
// directories holding 61 single-chunk files each (N=40: 1,649 directories and
// 97,600 files, the size class of the 100k-file load test). It is skipped
// unless NIPA_BENCH_TREE_LEAVES is set.
func (s *BranchRepositorySuite) TestManifestLoadScale() {
	leaves, err := strconv.Atoi(os.Getenv("NIPA_BENCH_TREE_LEAVES"))
	if err != nil || leaves <= 0 {
		s.T().Skip("set NIPA_BENCH_TREE_LEAVES=<leaf directories per middle directory> to run the manifest harness")
	}
	const top, mid, filesPerLeaf = 8, 5, 61
	t := s.T()
	ctx := context.Background()
	repo := NewBranchRepository(s.db)
	child := func(parent int64) sql.NullInt64 { return sql.NullInt64{Int64: parent, Valid: true} }

	root := seedTreeNode(t, s.db, "root", sql.NullInt64{})
	dirs := 1
	for i := 0; i < top; i++ {
		topID := seedTreeNode(t, s.db, fmt.Sprintf("t%03d", i), child(root))
		for j := 0; j < mid; j++ {
			midID := seedTreeNode(t, s.db, fmt.Sprintf("m%03d", j), child(topID))
			for k := 0; k < leaves; k++ {
				leafID := seedTreeNode(t, s.db, fmt.Sprintf("l%03d", k), child(midID))
				_, err := s.db.ExecContext(ctx, `
					INSERT INTO files (name, mode, tree_id, hash, size_bytes, is_binary)
					SELECT 'f' || lpad(g::text, 4, '0') || '.bin', 420, $1::bigint,
					       sha256(($1::text || '/' || g::text)::bytea), 100, false
					FROM generate_series(1, $2::int) g`, leafID, filesPerLeaf)
				require.NoError(t, err)
			}
			dirs += leaves
		}
		dirs += 1 + mid
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO chunks (project_id, hash, size_bytes) SELECT 1, hash, 100 FROM files`)
	require.NoError(t, err)
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO file_chunks (file_id, chunk_id, chunk_index)
		SELECT f.id, c.id, 0 FROM files f JOIN chunks c ON c.project_id = 1 AND c.hash = f.hash`)
	require.NoError(t, err)
	_, err = s.db.ExecContext(ctx, `ANALYZE`)
	require.NoError(t, err)
	files := top * mid * leaves * filesPerLeaf
	t.Logf("tree: %d directories, %d files", dirs, files)

	projectID := seedProject(t, s.q, 1, "manifest-scale")
	commitID := seedCommit(t, s.db, s.q, projectID, root)
	uc := usecase.NewBranch(allowAllPermissions{}, repo, newTestNode(t))

	best := func(label string, fn func()) {
		var fastest time.Duration
		for i := 0; i < 3; i++ {
			start := time.Now()
			fn()
			if elapsed := time.Since(start); fastest == 0 || elapsed < fastest {
				fastest = elapsed
			}
		}
		t.Logf("%-28s %s (best of 3)", label, fastest.Round(time.Millisecond))
	}
	best("per-directory manifest", func() {
		node, err := repo.GetTreeNode(ctx, root)
		require.NoError(t, err)
		calls := 0
		require.NoError(t, perDirectoryManifest(ctx, repo, node, &calls))
		require.Equal(t, 2*dirs, calls)
		require.Equal(t, files, countManifestFiles(node))
	})
	best("level-batched manifest", func() {
		_, node, err := uc.GetCommit(ctx, projectID, commitID)
		require.NoError(t, err)
		require.Equal(t, files, countManifestFiles(node))
	})
}
