package usecase_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/db/dbtest"
	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/sqlite"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/treehash"
	"github.com/nipalab/nipa/internal/usecase"
)

// manifestDir describes a directory to seed into tree_nodes/files/chunks.
type manifestDir struct {
	name  string
	files []manifestFile
	dirs  []*manifestDir
	// empty seeds a row carrying the empty tree hash, like a directory whose
	// files were all removed.
	empty bool

	hash domain.Hash
}

type manifestFile struct {
	name   string
	chunks int
	salt   string
}

func (f manifestFile) chunkHashes() []domain.Hash {
	hashes := make([]domain.Hash, 0, f.chunks)
	for i := 0; i < f.chunks; i++ {
		hashes = append(hashes, chunker.Sum([]byte(fmt.Sprintf("%s#%d#%s", f.name, i, f.salt))))
	}
	return hashes
}

// computeHash fills the real tree hash of dir and its subtree, so identical
// directories share a hash exactly as pushed trees do.
func (d *manifestDir) computeHash() domain.Hash {
	if d.empty {
		d.hash = usecase.EmptyTreeHash
		return d.hash
	}
	files := make([]treehash.FileEntry, 0, len(d.files))
	for _, f := range d.files {
		files = append(files, treehash.FileEntry{Name: f.name, Hash: treehash.FileHash(f.chunkHashes()), Mode: 0o644})
	}
	trees := make([]treehash.TreeEntry, 0, len(d.dirs))
	for _, sub := range d.dirs {
		trees = append(trees, treehash.TreeEntry{Name: sub.name, Hash: sub.computeHash()})
	}
	d.hash = treehash.TreeHash(files, trees)
	return d.hash
}

// treeSeeder writes trees straight into the server tables inside one
// transaction, the way ApplyPush leaves them: content rows own files and
// children, refs are bare rows that only share a hash.
type treeSeeder struct {
	tb       testing.TB
	tx       *sql.Tx
	node     *sql.Stmt
	file     *sql.Stmt
	chunk    *sql.Stmt
	link     *sql.Stmt
	chunkIDs map[domain.Hash]int64
}

func seedTrees(tb testing.TB, db *sql.DB, fn func(s *treeSeeder)) {
	tb.Helper()
	tx, err := db.Begin()
	require.NoError(tb, err)
	prepare := func(query string) *sql.Stmt {
		stmt, err := tx.Prepare(query)
		require.NoError(tb, err)
		return stmt
	}
	s := &treeSeeder{
		tb:       tb,
		tx:       tx,
		node:     prepare(`INSERT INTO tree_nodes (hash, name, mode, parent_tree_id) VALUES (?, ?, ?, ?)`),
		file:     prepare(`INSERT INTO files (name, mode, tree_id, hash, size_bytes, is_binary) VALUES (?, ?, ?, ?, ?, ?)`),
		chunk:    prepare(`INSERT INTO chunks (project_id, hash, size_bytes) VALUES (1, ?, ?)`),
		link:     prepare(`INSERT INTO file_chunks (file_id, chunk_id, chunk_index) VALUES (?, ?, ?)`),
		chunkIDs: map[domain.Hash]int64{},
	}
	fn(s)
	require.NoError(tb, tx.Commit())
}

func (s *treeSeeder) insert(stmt *sql.Stmt, args ...any) int64 {
	res, err := stmt.Exec(args...)
	require.NoError(s.tb, err)
	id, err := res.LastInsertId()
	require.NoError(s.tb, err)
	return id
}

// ref inserts a bare node row: same hash, no files, no children.
func (s *treeSeeder) ref(dir *manifestDir, parent sql.NullInt64) int64 {
	return s.insert(s.node, dir.hash[:], dir.name, 0o755, parent)
}

// content inserts dir with its files, chunk links and whole subtree.
func (s *treeSeeder) content(dir *manifestDir, parent sql.NullInt64) int64 {
	id := s.ref(dir, parent)
	if dir.empty {
		return id
	}
	s.files(dir, id)
	for _, sub := range dir.dirs {
		s.content(sub, sql.NullInt64{Int64: id, Valid: true})
	}
	return id
}

// shallow inserts dir with its own files and one ref per subdirectory, like
// the root a push writes when only a root file changed.
func (s *treeSeeder) shallow(dir *manifestDir) int64 {
	id := s.ref(dir, sql.NullInt64{})
	s.files(dir, id)
	for _, sub := range dir.dirs {
		s.ref(sub, sql.NullInt64{Int64: id, Valid: true})
	}
	return id
}

func (s *treeSeeder) files(dir *manifestDir, treeID int64) {
	for _, f := range dir.files {
		hashes := f.chunkHashes()
		fileHash := treehash.FileHash(hashes)
		fileID := s.insert(s.file, f.name, 0o644, treeID, fileHash[:], int64(100*f.chunks), false)
		// Link in reverse so chunk order has to come from chunk_index.
		for i := len(hashes) - 1; i >= 0; i-- {
			chunkID, ok := s.chunkIDs[hashes[i]]
			if !ok {
				chunkID = s.insert(s.chunk, hashes[i][:], 100)
				s.chunkIDs[hashes[i]] = chunkID
			}
			_, err := s.link.Exec(fileID, chunkID, i)
			require.NoError(s.tb, err)
		}
	}
}

func file(name string, chunks int) manifestFile { return manifestFile{name: name, chunks: chunks} }

func dir(name string, files []manifestFile, dirs ...*manifestDir) *manifestDir {
	return &manifestDir{name: name, files: files, dirs: dirs}
}

func twinDir(name string) *manifestDir {
	return dir(name, []manifestFile{file("x.txt", 1), file("w.txt", 2)},
		dir("sub", []manifestFile{file("y.txt", 1)},
			dir("deeper", []manifestFile{file("z.txt", 2)})))
}

// equivalenceTree covers refs, identical directories at two paths, a
// directory with only subdirectories, an empty directory, files with zero,
// one and several chunks, and the root team files.
func equivalenceTree(readmeSalt string) *manifestDir {
	root := dir("root",
		[]manifestFile{
			file(".nipaassets.yml", 1), file(".nipaignore", 1), file(".nipaprovenance.yml", 1),
			{name: "README.md", chunks: 1, salt: readmeSalt}, file("zero.bin", 0),
		},
		dir("assets", []manifestFile{file("logo.png", 3)},
			dir("secret", []manifestFile{file("key.txt", 1)}),
			dir("textures", []manifestFile{file("wood.png", 2)},
				dir("hd", []manifestFile{file("a.png", 4), file("b.png", 1)}))),
		twinDir("copy1"),
		twinDir("copy2"),
		dir("docs", []manifestFile{file("guide.md", 1)}),
		&manifestDir{name: "emptydir", empty: true},
		dir("hollow", nil, dir("inner", nil, dir("deep", []manifestFile{file("file.txt", 1)}))),
		dir("src", []manifestFile{file("main.go", 1)},
			dir("lib", []manifestFile{file("a.go", 1), file("b.go", 2)})),
	)
	root.computeHash()
	return root
}

func readDefaults(denied ...string) []*domain.ProjectPathPermission {
	defaults := []*domain.ProjectPathPermission{{PathPrefix: "", Permission: domain.PermissionRead}}
	for _, prefix := range denied {
		defaults = append(defaults, &domain.ProjectPathPermission{PathPrefix: prefix})
	}
	return defaults
}

func readRules(prefixes ...string) []*domain.PBACRule {
	rules := make([]*domain.PBACRule, 0, len(prefixes))
	for _, prefix := range prefixes {
		rules = append(rules, &domain.PBACRule{PathPrefix: prefix, Permission: domain.PermissionRead})
	}
	return rules
}

func mustPrefixSet(t *testing.T, prefixes ...string) domain.PrefixSet {
	t.Helper()
	set, err := domain.NewPrefixSet(prefixes)
	require.NoError(t, err)
	return set
}

func childID(t *testing.T, repo *sqlite.BranchRepository, parent int64, path ...string) int64 {
	t.Helper()
	id := parent
	for _, name := range path {
		child, err := repo.GetTreeChildByName(context.Background(), id, name)
		require.NoError(t, err)
		id = child.ID
	}
	return id
}

func TestLoadTreeManifest_MatchesRecursiveReference(t *testing.T) {
	ctx := context.Background()
	db := dbtest.SQLite(t)
	repo := sqlite.NewBranchRepository(db)
	uc := usecase.NewBranch(nil, repo, mustSnowNode(t))

	var fullRoot, refRoot int64
	seedTrees(t, db, func(s *treeSeeder) {
		// A ref to the first tree's "assets" that predates its content row.
		first := equivalenceTree("v1")
		s.ref(first.dirs[0], sql.NullInt64{})
		fullRoot = s.content(first, sql.NullInt64{})
		// The second commit changed one root file: every directory is a ref.
		refRoot = s.shallow(equivalenceTree("v2"))
	})

	filters := []struct {
		name   string
		filter *usecase.PathFilter
	}{
		{"allow all", usecase.AllowAllFilter()},
		{"deny files", usecase.NewTestPathFilter(nil, readDefaults("assets/secret/key.txt", "copy1/x.txt", "README.md", ".nipaignore"))},
		{"deny subtrees", usecase.NewTestPathFilter(nil, readDefaults("assets/textures", "copy2/sub/deeper", "hollow/inner/deep"))},
		{"deny every file of a directory", usecase.NewTestPathFilter(nil, readDefaults("docs/guide.md", "src/main.go"))},
		{"allow nested prefix only", usecase.NewTestPathFilter(readRules("src/lib"), nil)},
		{"allow several prefixes", usecase.NewTestPathFilter(readRules("assets/textures/hd", "copy1"), nil)},
		{"allow nothing", usecase.NewTestPathFilter(nil, nil)},
		{"nil filter", nil},
	}
	sparses := []struct {
		name     string
		prefixes []string
	}{
		{"no sparse", nil},
		{"single prefix", []string{"assets"}},
		{"nested prefix", []string{"assets/textures/hd"}},
		{"multiple prefixes", []string{"src", "docs", "copy2/sub"}},
		{"below a shared directory", []string{"copy1/sub/deeper", "copy2/sub/deeper/z.txt"}},
		{"only subdirectories", []string{"hollow/inner"}},
		{"single file", []string{"copy1/x.txt"}},
		{"missing prefix", []string{"nope"}},
	}
	starts := []struct {
		name string
		path []string
	}{
		{"root", nil},
		{"assets", []string{"assets"}},
		{"copy2", []string{"copy2"}},
		{"hollow inner", []string{"hollow", "inner"}},
		{"emptydir", []string{"emptydir"}},
	}
	roots := []struct {
		name string
		id   int64
	}{{"content tree", fullRoot}, {"ref tree", refRoot}}

	cases := 0
	for _, root := range roots {
		for _, start := range starts {
			startID := childID(t, repo, root.id, start.path...)
			startPath := ""
			for _, segment := range start.path {
				startPath = joinPath(startPath, segment)
			}
			for _, f := range filters {
				for _, sp := range sparses {
					for _, recursive := range []bool{true, false} {
						name := fmt.Sprintf("%s/%s/%s/%s/recursive=%t", root.name, start.name, f.name, sp.name, recursive)
						sparse := mustPrefixSet(t, sp.prefixes...)

						want, err := repo.GetTreeNode(ctx, startID)
						require.NoError(t, err, name)
						require.NoError(t, uc.LoadTreeManifestRecursive(ctx, want, startPath, recursive, f.filter, sparse), name)
						got, err := repo.GetTreeNode(ctx, startID)
						require.NoError(t, err, name)
						require.NoError(t, uc.LoadTreeManifest(ctx, got, startPath, recursive, f.filter, sparse), name)

						require.Equal(t, want, got, name)
						requireNoSharedNodes(t, got, name)
						require.Equal(t, usecase.RehashTree(want), usecase.RehashTree(got), name)
						require.Equal(t, want, got, name)
						cases++
					}
				}
			}
		}
	}
	require.Equal(t, len(roots)*len(starts)*len(filters)*len(sparses)*2, cases)

	// The matrix must have exercised the interesting shapes at least once.
	full, err := repo.GetTreeNode(ctx, refRoot)
	require.NoError(t, err)
	require.NoError(t, uc.LoadTreeManifest(ctx, full, "", true, usecase.AllowAllFilter(), nil))
	names := make([]string, 0, len(full.TreeChildren))
	for _, child := range full.TreeChildren {
		names = append(names, child.Name)
	}
	require.Equal(t, []string{"assets", "copy1", "copy2", "docs", "hollow", "src"}, names, "the empty directory is dropped")
	require.Equal(t, 23, countManifestFiles(full))
	require.Equal(t, full.TreeChildren[1].TreeChildren, full.TreeChildren[2].TreeChildren, "identical directories load the same content")
	logo := full.TreeChildren[0].FileChildren[0]
	require.Equal(t, "logo.png", logo.Name)
	require.Len(t, logo.Chunks, 3)
	for i, hash := range file("logo.png", 3).chunkHashes() {
		require.Equal(t, hash, logo.Chunks[i].Hash, "chunks stay in chunk-index order")
	}
}

func joinPath(parent, name string) string {
	if parent == "" {
		return name
	}
	return parent + "/" + name
}

func mustSnowNode(tb testing.TB) snow.Node {
	tb.Helper()
	node, err := snow.NewNode(1)
	require.NoError(tb, err)
	return node
}

func countManifestFiles(node *domain.TreeNode) int {
	n := len(node.FileChildren)
	for _, child := range node.TreeChildren {
		n += countManifestFiles(child)
	}
	return n
}

// requireNoSharedNodes fails when two places of the tree hold the same
// TreeNode or File value or the same chunk slice, which would let an in-place
// change at one path leak into another.
func requireNoSharedNodes(t *testing.T, root *domain.TreeNode, name string) {
	t.Helper()
	nodes := map[*domain.TreeNode]bool{}
	files := map[*domain.File]bool{}
	chunks := map[*domain.Chunk]bool{}
	var walk func(node *domain.TreeNode)
	walk = func(node *domain.TreeNode) {
		require.False(t, nodes[node], "%s: tree node %q appears twice", name, node.Name)
		nodes[node] = true
		for _, f := range node.FileChildren {
			require.False(t, files[f], "%s: file %q appears twice", name, f.Name)
			files[f] = true
			if len(f.Chunks) > 0 {
				require.False(t, chunks[&f.Chunks[0]], "%s: file %q shares its chunk slice", name, f.Name)
				chunks[&f.Chunks[0]] = true
			}
		}
		for _, child := range node.TreeChildren {
			walk(child)
		}
	}
	walk(root)
}

// countingBranchRepo counts the tree reads a manifest load issues.
type countingBranchRepo struct {
	*sqlite.BranchRepository
	fileBatches, childBatches int
	batchedIDs                int
	singleCalls               int
}

func (r *countingBranchRepo) ListFilesByTrees(ctx context.Context, ids []int64) (map[int64][]*domain.File, error) {
	r.fileBatches++
	r.batchedIDs += len(ids)
	return r.BranchRepository.ListFilesByTrees(ctx, ids)
}

func (r *countingBranchRepo) ListTreeChildrenByTrees(ctx context.Context, ids []int64) (map[int64][]*domain.TreeNode, error) {
	r.childBatches++
	return r.BranchRepository.ListTreeChildrenByTrees(ctx, ids)
}

func (r *countingBranchRepo) ListFilesByTree(ctx context.Context, id int64) ([]*domain.File, error) {
	r.singleCalls++
	return r.BranchRepository.ListFilesByTree(ctx, id)
}

func (r *countingBranchRepo) ListTreeChildren(ctx context.Context, id int64) ([]*domain.TreeNode, error) {
	r.singleCalls++
	return r.BranchRepository.ListTreeChildren(ctx, id)
}

// wideTree builds top*mid*leaf leaf directories three levels below the root,
// each holding filesPerLeaf single-chunk files.
func wideTree(top, mid, leaf, filesPerLeaf int) (root *manifestDir, dirs, files int) {
	root = dir("root", []manifestFile{file("README.md", 1)})
	files = 1
	for i := 0; i < top; i++ {
		topDir := dir(fmt.Sprintf("t%03d", i), nil)
		for j := 0; j < mid; j++ {
			midDir := dir(fmt.Sprintf("m%03d", j), nil)
			for k := 0; k < leaf; k++ {
				leafDir := dir(fmt.Sprintf("l%03d", k), nil)
				for f := 0; f < filesPerLeaf; f++ {
					leafDir.files = append(leafDir.files, manifestFile{
						name: fmt.Sprintf("f%04d.bin", f), chunks: 1, salt: fmt.Sprintf("%d/%d/%d", i, j, k),
					})
				}
				midDir.dirs = append(midDir.dirs, leafDir)
				files += filesPerLeaf
			}
			topDir.dirs = append(topDir.dirs, midDir)
		}
		root.dirs = append(root.dirs, topDir)
	}
	root.computeHash()
	return root, 1 + top + top*mid + top*mid*leaf, files
}

func TestLoadTreeManifest_QueriesScaleWithDepth(t *testing.T) {
	ctx := context.Background()
	db := dbtest.SQLite(t)
	repo := &countingBranchRepo{BranchRepository: sqlite.NewBranchRepository(db)}
	uc := usecase.NewBranch(nil, repo, mustSnowNode(t))

	tree, dirs, files := wideTree(6, 8, 7, 2)
	require.Equal(t, 391, dirs)
	var rootID int64
	seedTrees(t, db, func(s *treeSeeder) { rootID = s.content(tree, sql.NullInt64{}) })

	load := func(recursive bool, filter *usecase.PathFilter, sparse domain.PrefixSet) *domain.TreeNode {
		*repo = countingBranchRepo{BranchRepository: repo.BranchRepository}
		root, err := repo.GetTreeNode(ctx, rootID)
		require.NoError(t, err)
		require.NoError(t, uc.LoadTreeManifest(ctx, root, "", recursive, filter, sparse))
		require.Zero(t, repo.singleCalls, "the loader must not fall back to per-directory reads")
		return root
	}

	// Four levels (root, 6, 48, 336 directories): one files batch and one
	// children batch each, however many directories a level holds.
	root := load(true, usecase.AllowAllFilter(), nil)
	require.Equal(t, files, countManifestFiles(root))
	require.Equal(t, 4, repo.fileBatches)
	require.Equal(t, 4, repo.childBatches)
	require.Equal(t, dirs, repo.batchedIDs)

	root = load(false, usecase.AllowAllFilter(), nil)
	require.Len(t, root.FileChildren, 1)
	require.Empty(t, root.TreeChildren)
	require.Equal(t, 1, repo.fileBatches)
	require.Zero(t, repo.childBatches)

	// Pruned subtrees are never part of a batch: a sparse manifest of one
	// middle directory reads root, one top, one middle and its 7 leaves.
	root = load(true, usecase.AllowAllFilter(), mustPrefixSet(t, "t002/m003"))
	require.Equal(t, 7*2, countManifestFiles(root))
	require.Equal(t, 4, repo.fileBatches)
	require.Equal(t, 1+1+1+7, repo.batchedIDs)

	root = load(true, usecase.NewTestPathFilter(readRules("t001"), nil), nil)
	require.Equal(t, 8*7*2, countManifestFiles(root))
	require.Equal(t, 4, repo.fileBatches)
	require.Equal(t, 1+1+8+8*7, repo.batchedIDs)
}

func TestLoadTreeManifest_RepositoryErrors(t *testing.T) {
	ctx := context.Background()
	db := dbtest.SQLite(t)
	repo := sqlite.NewBranchRepository(db)
	uc := usecase.NewBranch(nil, repo, mustSnowNode(t))

	tree, _, _ := wideTree(1, 1, 1, 1)
	var rootID int64
	seedTrees(t, db, func(s *treeSeeder) { rootID = s.content(tree, sql.NullInt64{}) })
	root, err := repo.GetTreeNode(ctx, rootID)
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, `DROP TABLE file_chunks`)
	require.NoError(t, err)
	require.Error(t, uc.LoadTreeManifest(ctx, root, "", true, usecase.AllowAllFilter(), nil), "files batch")

	_, err = db.ExecContext(ctx, `DELETE FROM files WHERE tree_id = ?`, rootID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `ALTER TABLE tree_nodes RENAME COLUMN name TO label`)
	require.NoError(t, err)
	require.Error(t, uc.LoadTreeManifest(ctx, root, "", true, usecase.AllowAllFilter(), nil), "children batch")
}

// BenchmarkLoadTreeManifest compares the per-directory loader with the
// level-by-level one on a tree in the size class of the 100k-file load test:
// 1,649 directories and 97,601 files.
func BenchmarkLoadTreeManifest(b *testing.B) {
	ctx := context.Background()
	db := dbtest.SQLite(b)
	repo := &countingBranchRepo{BranchRepository: sqlite.NewBranchRepository(db)}
	uc := usecase.NewBranch(nil, repo, mustSnowNode(b))

	tree, dirs, files := wideTree(8, 5, 40, 61)
	var rootID int64
	seedTrees(b, db, func(s *treeSeeder) { rootID = s.content(tree, sql.NullInt64{}) })
	b.Logf("tree: %d directories, %d files", dirs, files)

	run := func(name string, load func(root *domain.TreeNode) error) {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				*repo = countingBranchRepo{BranchRepository: repo.BranchRepository}
				root, err := repo.GetTreeNode(ctx, rootID)
				require.NoError(b, err)
				require.NoError(b, load(root))
				if got := countManifestFiles(root); got != files {
					b.Fatalf("loaded %d files, want %d", got, files)
				}
			}
			b.ReportMetric(float64(repo.singleCalls+repo.fileBatches+repo.childBatches), "repo-calls/op")
		})
	}
	run("per-directory", func(root *domain.TreeNode) error {
		return uc.LoadTreeManifestRecursive(ctx, root, "", true, usecase.AllowAllFilter(), nil)
	})
	run("level-batched", func(root *domain.TreeNode) error {
		return uc.LoadTreeManifest(ctx, root, "", true, usecase.AllowAllFilter(), nil)
	})
}
