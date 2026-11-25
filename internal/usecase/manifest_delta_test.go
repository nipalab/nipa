package usecase_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/db/dbtest"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/sqlite"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/treedelta"
	"github.com/nipalab/nipa/internal/usecase"
)

// stubPermission is the permission usecase of a caller with one fixed filter.
type stubPermission struct {
	filter    *usecase.PathFilter
	noAccess  bool
	filterErr error
}

func (p *stubPermission) HasProjectAccess(context.Context, snow.ID, domain.Permission) bool {
	return !p.noAccess
}

func (p *stubPermission) HasPathAccess(context.Context, snow.ID, string, domain.Permission) bool {
	return !p.noAccess
}

func (p *stubPermission) CompileFilter(context.Context, snow.ID, domain.Permission) (*usecase.PathFilter, error) {
	return p.filter, p.filterErr
}

func (p *stubPermission) AdminHasProject(context.Context, snow.ID) bool { return false }

// pushed inserts dir the way a push leaves it: a directory whose hash is
// already stored becomes a bare reference, anything else a content row with
// its files and its children.
func (s *treeSeeder) pushed(dir *manifestDir, parent sql.NullInt64, stored map[domain.Hash]bool) int64 {
	id := s.ref(dir, parent)
	if dir.empty || stored[dir.hash] {
		return id
	}
	stored[dir.hash] = true
	s.files(dir, id)
	for _, sub := range dir.dirs {
		s.pushed(sub, sql.NullInt64{Int64: id, Valid: true}, stored)
	}
	return id
}

func (d *manifestDir) find(path string) *manifestDir {
	node := d
	for _, name := range strings.Split(path, "/") {
		var next *manifestDir
		for _, sub := range node.dirs {
			if sub.name == name {
				next = sub
			}
		}
		if next == nil {
			return nil
		}
		node = next
	}
	return node
}

func (d *manifestDir) removeDir(name string) {
	kept := d.dirs[:0]
	for _, sub := range d.dirs {
		if sub.name != name {
			kept = append(kept, sub)
		}
	}
	d.dirs = kept
}

func (d *manifestDir) setFile(name, salt string) {
	for i := range d.files {
		if d.files[i].name == name {
			d.files[i].salt = salt
			return
		}
	}
	d.files = append(d.files, manifestFile{name: name, chunks: 2, salt: salt})
}

func (d *manifestDir) removeFile(name string) {
	kept := d.files[:0]
	for _, f := range d.files {
		if f.name != name {
			kept = append(kept, f)
		}
	}
	d.files = kept
}

// deltaFixture is a database holding several commits of project 1.
type deltaFixture struct {
	t       *testing.T
	db      *sql.DB
	repo    *deltaCountingRepo
	perm    *stubPermission
	uc      *usecase.Branch
	commits map[string]snow.ID
	next    int64
}

func newDeltaFixture(t *testing.T) *deltaFixture {
	t.Helper()
	db := dbtest.SQLite(t)
	f := &deltaFixture{
		t:       t,
		db:      db,
		repo:    &deltaCountingRepo{BranchRepository: sqlite.NewBranchRepository(db)},
		perm:    &stubPermission{filter: usecase.AllowAllFilter()},
		commits: map[string]snow.ID{},
		next:    1000,
	}
	f.uc = usecase.NewBranch(f.perm, f.repo, mustSnowNode(t))
	return f
}

// commit seeds the trees like consecutive pushes and records one commit of
// project per tree.
func (f *deltaFixture) commit(project int64, trees map[string]*manifestDir, order ...string) {
	f.t.Helper()
	roots := map[string]int64{}
	stored := map[domain.Hash]bool{}
	seedTrees(f.t, f.db, func(s *treeSeeder) {
		for _, name := range order {
			trees[name].computeHash()
			roots[name] = s.pushed(trees[name], sql.NullInt64{}, stored)
		}
	})
	for _, name := range order {
		f.next++
		hash := domain.Hash{byte(f.next), byte(f.next >> 8), 0xc0}
		_, err := f.db.Exec(`INSERT INTO commits (id, hash, project_id, tree_id, user_id, message) VALUES (?, ?, ?, ?, 1, ?)`,
			f.next, hash[:], project, roots[name], name)
		require.NoError(f.t, err)
		f.commits[name] = snow.ID(f.next)
	}
}

func (f *deltaFixture) full(name string, sparse ...string) *domain.TreeNode {
	f.t.Helper()
	root, err := f.uc.GetCommitTreeManifest(context.Background(), 1, f.commits[name], sparse, true)
	require.NoError(f.t, err)
	return root
}

func (f *deltaFixture) view(sparse ...string) []byte {
	f.t.Helper()
	view, err := f.uc.ManifestView(context.Background(), 1, sparse, true)
	require.NoError(f.t, err)
	return view
}

// delta asks for base -> target in the current view, naming the base root
// hash the full manifest carries.
func (f *deltaFixture) delta(base, target string, sparse ...string) *usecase.ManifestDelta {
	f.t.Helper()
	hash := f.full(base, sparse...).Hash
	id := f.commits[target]
	got, err := f.uc.GetTreeManifestDelta(context.Background(), 1, usecase.ManifestTarget{CommitID: &id}, sparse,
		usecase.ManifestBase{CommitID: f.commits[base], View: f.view(sparse...), TreeHash: &hash})
	require.NoError(f.t, err)
	return got
}

// wireTree turns a manifest of the usecase into what the client decodes from
// the stream: no row ids or timestamps, mode classes, never-nil child lists.
func wireTree(node *domain.TreeNode) *domain.TreeNode {
	if node == nil {
		return nil
	}
	out := &domain.TreeNode{Name: node.Name, Hash: node.Hash, TreeChildren: []*domain.TreeNode{}, FileChildren: []*domain.File{}}
	for _, file := range node.FileChildren {
		out.FileChildren = append(out.FileChildren, wireFile(file))
	}
	for _, child := range node.TreeChildren {
		out.TreeChildren = append(out.TreeChildren, wireTree(child))
	}
	return out
}

func wireFile(file *domain.File) *domain.File {
	out := &domain.File{
		Name: file.Name, Hash: file.Hash, Mode: domain.ClassifyStoredMode(file.Mode),
		SizeBytes: file.SizeBytes, IsBinary: file.IsBinary, Encoding: file.Encoding,
	}
	for _, chunk := range file.Chunks {
		out.Chunks = append(out.Chunks, domain.Chunk{Hash: chunk.Hash, SizeBytes: chunk.SizeBytes})
	}
	return out
}

func wireDelta(delta *treedelta.Delta) *treedelta.Delta {
	var dir func(d *treedelta.Dir) *treedelta.Dir
	dir = func(d *treedelta.Dir) *treedelta.Dir {
		out := &treedelta.Dir{Name: d.Name, Unchanged: d.Unchanged}
		for _, file := range d.Files {
			entry := treedelta.File{Name: file.Name}
			if file.Changed != nil {
				entry.Changed = wireFile(file.Changed)
			}
			out.Files = append(out.Files, entry)
		}
		for _, sub := range d.Dirs {
			out.Dirs = append(out.Dirs, dir(sub))
		}
		return out
	}
	return &treedelta.Delta{Root: dir(delta.Root), RootHash: delta.RootHash}
}

// requireDeltaLeadsTo applies the delta to the base manifest and compares the
// result with the target manifest of the same view.
func (f *deltaFixture) requireDeltaLeadsTo(got *usecase.ManifestDelta, base, target string, sparse ...string) {
	f.t.Helper()
	require.NotNil(f.t, got, "the usecase declined")
	require.Equal(f.t, f.commits[target], got.Commit.ID)
	want := wireTree(f.full(target, sparse...))
	applied, err := treedelta.Apply(wireTree(f.full(base, sparse...)), wireDelta(got.Delta))
	require.NoError(f.t, err)
	require.Equal(f.t, want, applied)
	if got.Delta.RootHash != nil {
		require.Equal(f.t, want.Hash, *got.Delta.RootHash)
	}
}

// deltaCountingRepo counts the tree reads of a delta and the rows they return.
type deltaCountingRepo struct {
	*sqlite.BranchRepository
	metaBatches, childBatches, chunkBatches int
	ids, fileRows, childRows, chunkRows     int
	other                                   int
	// failTreeNode fails the n-th GetTreeNode call (1-based).
	failTreeNode, treeNodes int
}

func (r *deltaCountingRepo) reset() {
	*r = deltaCountingRepo{BranchRepository: r.BranchRepository}
}

func (r *deltaCountingRepo) GetTreeNode(ctx context.Context, id int64) (*domain.TreeNode, error) {
	r.treeNodes++
	if r.treeNodes == r.failTreeNode {
		return nil, errors.New("tree node unavailable")
	}
	return r.BranchRepository.GetTreeNode(ctx, id)
}

func (r *deltaCountingRepo) ListFileMetaByTrees(ctx context.Context, ids []int64) (map[int64][]*domain.File, error) {
	out, err := r.BranchRepository.ListFileMetaByTrees(ctx, ids)
	r.metaBatches++
	r.ids += len(ids)
	for _, files := range out {
		r.fileRows += len(files)
	}
	return out, err
}

func (r *deltaCountingRepo) ListTreeChildrenByTrees(ctx context.Context, ids []int64) (map[int64][]*domain.TreeNode, error) {
	out, err := r.BranchRepository.ListTreeChildrenByTrees(ctx, ids)
	r.childBatches++
	for _, children := range out {
		r.childRows += len(children)
	}
	return out, err
}

func (r *deltaCountingRepo) ListChunksByFiles(ctx context.Context, ids []int64) (map[int64][]domain.Chunk, error) {
	out, err := r.BranchRepository.ListChunksByFiles(ctx, ids)
	r.chunkBatches++
	for _, chunks := range out {
		r.chunkRows += len(chunks)
	}
	return out, err
}

func (r *deltaCountingRepo) ListFilesByTrees(ctx context.Context, ids []int64) (map[int64][]*domain.File, error) {
	r.other++
	return r.BranchRepository.ListFilesByTrees(ctx, ids)
}

func (r *deltaCountingRepo) ListFilesByTree(ctx context.Context, id int64) ([]*domain.File, error) {
	r.other++
	return r.BranchRepository.ListFilesByTree(ctx, id)
}

func (r *deltaCountingRepo) ListFileMetaByTree(ctx context.Context, id int64) ([]*domain.File, error) {
	r.other++
	return r.BranchRepository.ListFileMetaByTree(ctx, id)
}

func (r *deltaCountingRepo) ListTreeChildren(ctx context.Context, id int64) ([]*domain.TreeNode, error) {
	r.other++
	return r.BranchRepository.ListTreeChildren(ctx, id)
}

func (r *deltaCountingRepo) GetTreeChildByName(ctx context.Context, parent int64, name string) (*domain.TreeNode, error) {
	r.other++
	return r.BranchRepository.GetTreeChildByName(ctx, parent, name)
}

func TestManifestDelta_CostFollowsTheChange(t *testing.T) {
	f := newDeltaFixture(t)
	base, dirs, files := wideTree(8, 10, 25, 2)
	require.Equal(t, 2089, dirs)
	require.Equal(t, 4001, files)
	// The target changes two files of one leaf, one of another, and adds a
	// file to a middle directory under another top directory.
	target, _, _ := wideTree(8, 10, 25, 2)
	target.find("t000/m000/l000").setFile("f0000.bin", "changed")
	target.find("t000/m000/l000").setFile("f0001.bin", "changed")
	target.find("t000/m001/l005").setFile("f0001.bin", "changed")
	target.find("t007/m003").setFile("new.bin", "added")
	f.commit(1, map[string]*manifestDir{"base": base, "target": target}, "base", "target")

	ctx := context.Background()
	baseFull := f.full("base")
	view := f.view()
	targetID := f.commits["target"]
	f.repo.reset()
	got, err := f.uc.GetTreeManifestDelta(ctx, 1, usecase.ManifestTarget{CommitID: &targetID}, nil,
		usecase.ManifestBase{CommitID: f.commits["base"], View: view, TreeHash: &baseFull.Hash})
	require.NoError(t, err)
	require.NotNil(t, got)
	counts := *f.repo

	// Eight directories differ: the root, two top, three middle and two
	// leaf directories. Each of the four levels costs one file batch and one
	// children batch over both sides, and the four touched files one chunk
	// batch; nothing is read per directory.
	t.Logf("changed tree of %d directories and %d files: %d file batches, %d children batches, %d chunk batches over %d directory ids; %d file rows, %d child rows, %d chunk rows",
		dirs, files, counts.metaBatches, counts.childBatches, counts.chunkBatches, counts.ids, counts.fileRows, counts.childRows, counts.chunkRows)
	require.Equal(t, 4, counts.metaBatches)
	require.Equal(t, 4, counts.childBatches)
	require.Equal(t, 1, counts.chunkBatches)
	require.Zero(t, counts.other)
	require.Equal(t, 2*8, counts.ids)
	// Root 1+1, leaves (2+2)*2, the middle directory 0+1.
	require.Equal(t, 11, counts.fileRows)
	// Root 8+8, top (10+10)*2, middle (25+25)*3.
	require.Equal(t, 206, counts.childRows)
	// Four touched files of two chunks each (setFile) except the three that
	// kept their single chunk.
	require.Equal(t, 3+2, counts.chunkRows)

	require.NotNil(t, got.Delta.RootHash, "a sound tree in an unfiltered view has a provable root hash")
	f.requireDeltaLeadsTo(got, "base", "target")
	root := got.Delta.Root
	require.False(t, root.Unchanged)
	require.Len(t, root.Dirs, 8)
	unchanged := 0
	for _, top := range root.Dirs {
		if top.Unchanged {
			unchanged++
		}
	}
	require.Equal(t, 6, unchanged)
	require.Equal(t, []treedelta.File{{Name: "README.md"}}, root.Files)

	// An unchanged tree costs the two commits and their roots, nothing else.
	f.repo.reset()
	baseID := f.commits["base"]
	same, err := f.uc.GetTreeManifestDelta(ctx, 1, usecase.ManifestTarget{CommitID: &baseID}, nil,
		usecase.ManifestBase{CommitID: baseID, View: view, TreeHash: &baseFull.Hash})
	require.NoError(t, err)
	require.True(t, same.Delta.Root.Unchanged)
	require.Equal(t, baseFull.Hash, *same.Delta.RootHash)
	require.Zero(t, f.repo.metaBatches+f.repo.childBatches+f.repo.chunkBatches+f.repo.other)

	// The other way round costs the same.
	f.repo.reset()
	back := f.delta("target", "base")
	f.requireDeltaLeadsTo(back, "target", "base")
}

// deltaShapes are targets that differ from equivalenceTree("v1") in ways the
// walk must each handle.
func deltaShapes() (map[string]*manifestDir, []string) {
	build := func(mutate func(root *manifestDir)) *manifestDir {
		root := equivalenceTree("v1")
		mutate(root)
		return root
	}
	shapes := map[string]*manifestDir{
		"base":       build(func(*manifestDir) {}),
		"root file":  equivalenceTree("v2"),
		"deep file":  build(func(r *manifestDir) { r.find("assets/textures/hd").setFile("a.png", "new") }),
		"hidden one": build(func(r *manifestDir) { r.find("assets/secret").setFile("key.txt", "rotated") }),
		"added dir": build(func(r *manifestDir) {
			r.dirs = append(r.dirs, dir("zeta", []manifestFile{file("z1", 1)}, dir("inner", []manifestFile{file("z2", 3)})))
		}),
		"removed dir":  build(func(r *manifestDir) { r.removeDir("docs") }),
		"removed deep": build(func(r *manifestDir) { r.find("assets").removeDir("textures") }),
		"emptied dir":  build(func(r *manifestDir) { r.find("docs").removeFile("guide.md"); r.find("docs").empty = true }),
		"file to dir": build(func(r *manifestDir) {
			r.removeFile("README.md")
			r.dirs = append([]*manifestDir{dir("README.md", []manifestFile{file("index", 1)})}, r.dirs...)
		}),
		"dir to file": build(func(r *manifestDir) { r.removeDir("docs"); r.setFile("docs", "now a file") }),
		"moved dir": build(func(r *manifestDir) {
			moved := r.find("src/lib")
			r.find("src").removeDir("lib")
			r.find("docs").dirs = append(r.find("docs").dirs, moved)
		}),
		"twin diverged": build(func(r *manifestDir) { r.find("copy2/sub/deeper").setFile("z.txt", "diverged") }),
		"third twin":    build(func(r *manifestDir) { r.dirs = append(r.dirs, twinDir("copy3")) }),
		"hollow gone": build(func(r *manifestDir) {
			r.find("hollow/inner/deep").removeFile("file.txt")
			r.find("hollow/inner/deep").empty = true
		}),
		"team file": build(func(r *manifestDir) { r.setFile(".nipaignore", "new rules"); r.removeFile(".nipaassets.yml") }),
		"empty":     {name: "root", empty: true},
	}
	order := []string{
		"base", "root file", "deep file", "hidden one", "added dir", "removed dir", "removed deep", "emptied dir", "file to dir",
		"dir to file", "moved dir", "twin diverged", "third twin", "hollow gone", "team file", "empty",
	}
	return shapes, order
}

func TestManifestDelta_EveryShapeInEveryView(t *testing.T) {
	f := newDeltaFixture(t)
	shapes, order := deltaShapes()
	f.commit(1, shapes, order...)

	filters := []struct {
		name   string
		filter *usecase.PathFilter
	}{
		{"allow all", usecase.AllowAllFilter()},
		{"allow all by rule", usecase.NewTestPathFilter(readRules(""), nil)},
		{"deny files", usecase.NewTestPathFilter(nil, readDefaults("assets/secret/key.txt", "copy1/x.txt", "README.md", ".nipaignore"))},
		{"deny subtrees", usecase.NewTestPathFilter(nil, readDefaults("assets/textures", "copy2/sub/deeper", "hollow/inner/deep", "zeta/inner"))},
		{"deny every file of a directory", usecase.NewTestPathFilter(nil, readDefaults("docs/guide.md", "src/main.go"))},
		{"allow nested prefix only", usecase.NewTestPathFilter(readRules("src/lib"), nil)},
		{"allow several prefixes", usecase.NewTestPathFilter(readRules("assets/textures/hd", "copy1", "docs"), nil)},
		{"allow nothing", usecase.NewTestPathFilter(nil, nil)},
	}
	sparses := [][]string{nil, {"assets"}, {"assets/textures/hd"}, {"src", "docs", "copy2/sub"}, {"copy1/sub/deeper", "hollow/inner"}}

	ctx := context.Background()
	cases, declined, proven := 0, 0, 0
	for _, flt := range filters {
		f.perm.filter = flt.filter
		for _, sparse := range sparses {
			view := f.view(sparse...)
			// The full manifest of every commit in this view, as the client
			// holds it; nil when the view cannot be fetched for the commit.
			fulls := map[string]*domain.TreeNode{}
			for _, name := range order {
				if full, err := f.uc.GetCommitTreeManifest(ctx, 1, f.commits[name], sparse, true); err == nil {
					fulls[name] = wireTree(full)
				}
			}
			for bi, base := range order {
				baseFull := fulls[base]
				if baseFull == nil {
					// The client could not hold this base in this view.
					continue
				}
				for ti, target := range order {
					// Every shape from and to the first tree, the diagonal,
					// and a spread of the other pairs.
					if bi != 0 && ti != 0 && bi != ti && (bi+2*ti)%5 != 0 {
						continue
					}
					label := fmt.Sprintf("%s, sparse %v, %s -> %s", flt.name, sparse, base, target)
					id := f.commits[target]
					got, err := f.uc.GetTreeManifestDelta(ctx, 1, usecase.ManifestTarget{CommitID: &id}, sparse,
						usecase.ManifestBase{CommitID: f.commits[base], View: view, TreeHash: &baseFull.Hash})
					require.NoError(t, err, label)
					want := fulls[target]
					if want == nil {
						require.Nil(t, got, "%s: a target the full manifest refuses is declined", label)
						declined++
						continue
					}
					require.NotNil(t, got, label)
					applied, err := treedelta.Apply(baseFull, wireDelta(got.Delta))
					require.NoError(t, err, label)
					require.Equal(t, want, applied, label)
					if got.Delta.RootHash != nil {
						require.Equal(t, want.Hash, *got.Delta.RootHash, label)
						proven++
					}
					cases++
				}
			}
		}
	}
	t.Logf("%d cases, %d declined for a missing sparse path, %d with a proven root hash", cases, declined, proven)
	require.Greater(t, cases, 1500)
	require.Positive(t, declined)
	// The fixture tree holds an empty directory row, which a manifest drops:
	// its stored root hash is not the manifest's, so the only provable root
	// hash is the one of the empty tree.
	require.Positive(t, proven)

	// A change the view hides leaves the whole delta unchanged, although the
	// stored hashes differ all the way up.
	f.perm.filter = usecase.NewTestPathFilter(nil, readDefaults("assets/secret/key.txt"))
	hidden := f.delta("base", "hidden one")
	require.True(t, hidden.Delta.Root.Unchanged)
	require.Nil(t, hidden.Delta.RootHash)
	f.requireDeltaLeadsTo(hidden, "base", "hidden one")

	// Otherwise only the path to the change is listed, the rest by name.
	f.perm.filter = usecase.AllowAllFilter()
	deep := f.delta("base", "deep file")
	f.requireDeltaLeadsTo(deep, "base", "deep file")
	var lines []string
	var describe func(d *treedelta.Dir, depth int)
	describe = func(d *treedelta.Dir, depth int) {
		state := "changed"
		if d.Unchanged {
			state = "unchanged"
		}
		lines = append(lines, fmt.Sprintf("%s%s/ %s", strings.Repeat(" ", depth), d.Name, state))
		for _, file := range d.Files {
			state := "changed"
			if file.Unchanged() {
				state = "unchanged"
			}
			lines = append(lines, fmt.Sprintf("%s %s %s", strings.Repeat(" ", depth), file.Name, state))
		}
		for _, sub := range d.Dirs {
			describe(sub, depth+1)
		}
	}
	describe(deep.Delta.Root, 0)
	require.Equal(t, []string{
		"root/ changed",
		" .nipaassets.yml unchanged", " .nipaignore unchanged", " .nipaprovenance.yml unchanged", " README.md unchanged", " zero.bin unchanged",
		" assets/ changed",
		"  logo.png unchanged",
		"  secret/ unchanged",
		"  textures/ changed",
		"   wood.png unchanged",
		"   hd/ changed",
		"    a.png changed", "    b.png unchanged",
		" copy1/ unchanged", " copy2/ unchanged", " docs/ unchanged", " hollow/ unchanged", " src/ unchanged",
	}, lines)
	changed := deep.Delta.Root.Dirs[0].Dirs[1].Dirs[0].Files[0].Changed
	require.Len(t, changed.Chunks, 4)
	for i, hash := range (manifestFile{name: "a.png", chunks: 4, salt: "new"}).chunkHashes() {
		require.Equal(t, hash, changed.Chunks[i].Hash, "chunks stay in chunk-index order")
	}
}

func TestManifestDelta_RootHashOnlyWhenProvable(t *testing.T) {
	sound := func(salt string) *manifestDir {
		return dir("root", []manifestFile{{name: "README.md", chunks: 1, salt: salt}},
			dir("docs", []manifestFile{file("guide.md", 1)}),
			dir("src", []manifestFile{{name: "main.go", chunks: 1, salt: salt}}, dir("lib", []manifestFile{file("a.go", 1)})))
	}
	f := newDeltaFixture(t)
	withEmpty := sound("v3")
	withEmpty.find("src").dirs = append(withEmpty.find("src").dirs, &manifestDir{name: "void", empty: true})
	f.commit(1, map[string]*manifestDir{"v1": sound("v1"), "v2": sound("v2"), "void": withEmpty}, "v1", "v2", "void")
	ctx := context.Background()

	// Sound trees, nothing hidden: the stored root hash is the manifest's.
	got := f.delta("v1", "v2")
	require.NotNil(t, got.Delta.RootHash)
	require.Equal(t, f.full("v2").Hash, *got.Delta.RootHash)
	f.requireDeltaLeadsTo(got, "v1", "v2")

	// Without the client's base root hash there is nothing to start from.
	v2 := f.commits["v2"]
	unnamed, err := f.uc.GetTreeManifestDelta(ctx, 1, usecase.ManifestTarget{CommitID: &v2}, nil,
		usecase.ManifestBase{CommitID: f.commits["v1"], View: f.view()})
	require.NoError(t, err)
	require.Nil(t, unnamed.Delta.RootHash)
	f.requireDeltaLeadsTo(unnamed, "v1", "v2")

	// A view that hides something has other hashes than the stored ones.
	f.perm.filter = usecase.NewTestPathFilter(nil, readDefaults("docs"))
	got = f.delta("v1", "v2")
	require.Nil(t, got.Delta.RootHash)
	f.requireDeltaLeadsTo(got, "v1", "v2")
	f.perm.filter = usecase.AllowAllFilter()
	got = f.delta("v1", "v2", "src")
	require.Nil(t, got.Delta.RootHash)
	f.requireDeltaLeadsTo(got, "v1", "v2", "src")

	// A changed directory that does not hash to its stored hash from what the
	// manifest lists (here an empty directory row the manifest drops).
	got = f.delta("v2", "void")
	require.Nil(t, got.Delta.RootHash)
	f.requireDeltaLeadsTo(got, "v2", "void")
	got = f.delta("void", "v1")
	require.Nil(t, got.Delta.RootHash)
	f.requireDeltaLeadsTo(got, "void", "v1")

	// Legacy hashes: the stored hashes of v1 and v2 predate file modes, so an
	// executable inside an unchanged directory makes every manifest hash on
	// the way up differ from the stored one. Equal stored hashes still mean
	// equal subtrees, so the delta is right; it just has no root hash.
	_, err = f.db.Exec(`UPDATE files SET mode = 493 WHERE name = 'guide.md'`)
	require.NoError(t, err)
	stored, err := f.repo.GetTreeNode(ctx, mustTreeID(t, f, "v1"))
	require.NoError(t, err)
	require.NotEqual(t, stored.Hash, f.full("v1").Hash)
	got = f.delta("v1", "v2")
	require.Nil(t, got.Delta.RootHash)
	require.True(t, got.Delta.Root.Dirs[0].Unchanged, "docs is unchanged")
	f.requireDeltaLeadsTo(got, "v1", "v2")
	// Even a client that names the stored root hash gets none once a walked
	// directory fails the check.
	_, err = f.db.Exec(`UPDATE files SET mode = 493 WHERE name = 'main.go'`)
	require.NoError(t, err)
	forced, err := f.uc.GetTreeManifestDelta(ctx, 1, usecase.ManifestTarget{CommitID: &v2}, nil,
		usecase.ManifestBase{CommitID: f.commits["v1"], View: f.view(), TreeHash: &stored.Hash})
	require.NoError(t, err)
	require.Nil(t, forced.Delta.RootHash)
}

func mustTreeID(t *testing.T, f *deltaFixture, commit string) int64 {
	t.Helper()
	c, err := f.repo.GetCommit(context.Background(), f.commits[commit])
	require.NoError(t, err)
	return c.TreeID
}

func TestManifestDelta_Declines(t *testing.T) {
	f := newDeltaFixture(t)
	shapes, _ := deltaShapes()
	f.commit(1, shapes, "base", "root file", "removed dir")
	_, err := f.db.Exec(`INSERT INTO projects (id, org_id, slug, name) VALUES (2, 1, 'other', 'Other')`)
	require.NoError(t, err)
	f.commit(2, map[string]*manifestDir{"foreign": dir("root", []manifestFile{{name: "foreign.txt", chunks: 1, salt: "foreign"}})}, "foreign")
	_, err = f.db.Exec(`UPDATE branches SET commit_id = ? WHERE id = 1`, f.commits["root file"].Int64())
	require.NoError(t, err)
	_, err = f.db.Exec(`INSERT INTO branches (id, project_id, name, key) VALUES (77, 1, 'bare', 'bare')`)
	require.NoError(t, err)

	ctx := context.Background()
	view := f.view()
	target := f.commits["root file"]
	ask := func(target usecase.ManifestTarget, paths []string, base usecase.ManifestBase) *usecase.ManifestDelta {
		t.Helper()
		got, err := f.uc.GetTreeManifestDelta(ctx, 1, target, paths, base)
		require.NoError(t, err, "declining is never an error")
		return got
	}
	good := usecase.ManifestBase{CommitID: f.commits["base"], View: view}
	require.NotNil(t, ask(usecase.ManifestTarget{CommitID: &target}, nil, good), "the control request gets a delta")
	branchDelta := ask(usecase.ManifestTarget{Branch: "main"}, nil, good)
	require.NotNil(t, branchDelta, "the branch form gets a delta to the head")
	require.Equal(t, target, branchDelta.Commit.ID)

	unknown := snow.ID(987654321)
	foreign := f.commits["foreign"]
	cases := []struct {
		name   string
		target usecase.ManifestTarget
		paths  []string
		base   usecase.ManifestBase
	}{
		{"unknown base commit", usecase.ManifestTarget{CommitID: &target}, nil, usecase.ManifestBase{CommitID: unknown, View: view}},
		{"base of another project", usecase.ManifestTarget{CommitID: &target}, nil, usecase.ManifestBase{CommitID: foreign, View: view}},
		{"no view token", usecase.ManifestTarget{CommitID: &target}, nil, usecase.ManifestBase{CommitID: f.commits["base"]}},
		{"a token of another sparse set", usecase.ManifestTarget{CommitID: &target}, []string{"assets"}, good},
		{"a token of no sparse set", usecase.ManifestTarget{CommitID: &target}, nil, usecase.ManifestBase{CommitID: f.commits["base"], View: f.view("assets")}},
		{"a token of a non-recursive manifest", usecase.ManifestTarget{CommitID: &target}, nil, usecase.ManifestBase{CommitID: f.commits["base"], View: mustView(t, f, false)}},
		{"garbage token", usecase.ManifestTarget{CommitID: &target}, nil, usecase.ManifestBase{CommitID: f.commits["base"], View: []byte("nope")}},
		{"unknown target commit", usecase.ManifestTarget{CommitID: &unknown}, nil, good},
		{"target of another project", usecase.ManifestTarget{CommitID: &foreign}, nil, good},
		{"unknown branch", usecase.ManifestTarget{Branch: "nope"}, nil, good},
		{"branch without commits", usecase.ManifestTarget{Branch: "bare"}, nil, good},
		{"invalid sparse path", usecase.ManifestTarget{CommitID: &target}, []string{"../up"}, good},
	}
	for _, tc := range cases {
		require.Nil(t, ask(tc.target, tc.paths, tc.base), tc.name)
	}

	// A sparse path the target does not have: the full manifest reports it.
	docs := f.view("docs")
	gone := f.commits["removed dir"]
	require.Nil(t, ask(usecase.ManifestTarget{CommitID: &gone}, []string{"docs"}, usecase.ManifestBase{CommitID: f.commits["base"], View: docs}))
	_, err = f.uc.GetCommitTreeManifest(ctx, 1, gone, []string{"docs"}, true)
	require.True(t, domain.IsErrorNotFound(err))
	// The other way round the base lacks it, which is the client's business.
	back := f.commits["base"]
	require.NotNil(t, ask(usecase.ManifestTarget{CommitID: &back}, []string{"docs"}, usecase.ManifestBase{CommitID: gone, View: docs}))

	// The read rules changed since the base was fetched.
	f.perm.filter = usecase.NewTestPathFilter(nil, readDefaults("assets/secret"))
	require.Nil(t, ask(usecase.ManifestTarget{CommitID: &target}, nil, good), "filter changed")
	require.NotNil(t, ask(usecase.ManifestTarget{CommitID: &target}, nil, usecase.ManifestBase{CommitID: f.commits["base"], View: f.view()}))
	// A filter without a fingerprint never gets a delta.
	f.perm.filter = nil
	nilView, err := f.uc.ManifestView(ctx, 1, nil, true)
	require.NoError(t, err)
	require.Nil(t, nilView)
	require.Nil(t, ask(usecase.ManifestTarget{CommitID: &target}, nil, good))

	// The caller may not read the project (any more).
	f.perm.filter = usecase.AllowAllFilter()
	f.perm.noAccess = true
	require.Nil(t, ask(usecase.ManifestTarget{CommitID: &target}, nil, good), "base not readable")
	f.perm.noAccess = false
	f.perm.filterErr = domain.NewErrorNoPermission()
	require.Nil(t, ask(usecase.ManifestTarget{CommitID: &target}, nil, good))

	// Server-side failures are errors, not declines.
	boom := errors.New("rules unavailable")
	f.perm.filterErr = boom
	_, err = f.uc.GetTreeManifestDelta(ctx, 1, usecase.ManifestTarget{CommitID: &target}, nil, good)
	require.ErrorIs(t, err, boom)
	_, err = f.uc.ManifestView(ctx, 1, nil, true)
	require.ErrorIs(t, err, boom)
	f.perm.filterErr = nil
	_, err = f.uc.ManifestView(ctx, 1, []string{"../up"}, true)
	require.Error(t, err)
}

func mustView(t *testing.T, f *deltaFixture, recursive bool) []byte {
	t.Helper()
	view, err := f.uc.ManifestView(context.Background(), 1, nil, recursive)
	require.NoError(t, err)
	return view
}

func TestManifestDelta_RepositoryErrors(t *testing.T) {
	ctx := context.Background()
	run := func(name string, breakDB string, wantErr bool, failTreeNode ...int) {
		t.Run(name, func(t *testing.T) {
			f := newDeltaFixture(t)
			shapes, _ := deltaShapes()
			f.commit(1, shapes, "base", "deep file")
			view := f.view()
			target := f.commits["deep file"]
			_, err := f.db.Exec(breakDB)
			require.NoError(t, err)
			f.repo.reset()
			if len(failTreeNode) > 0 {
				f.repo.failTreeNode = failTreeNode[0]
			}
			_, err = f.uc.GetTreeManifestDelta(ctx, 1, usecase.ManifestTarget{CommitID: &target}, nil,
				usecase.ManifestBase{CommitID: f.commits["base"], View: view})
			if wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
	run("control", `SELECT 1`, false)
	run("chunk batch", `DROP TABLE file_chunks`, true)
	run("file batch", `ALTER TABLE files RENAME COLUMN name TO label`, true)
	run("children batch", `ALTER TABLE tree_nodes RENAME COLUMN name TO label`, true)
	run("target root", `SELECT 1`, true, 1)
	run("base root", `SELECT 1`, true, 2)
	run("commits", `ALTER TABLE commits RENAME COLUMN message TO label`, true)
}

func TestManifestView_Fingerprint(t *testing.T) {
	f := newDeltaFixture(t)
	token := func(filter *usecase.PathFilter, sparse ...string) string {
		t.Helper()
		f.perm.filter = filter
		return string(f.view(sparse...))
	}
	uid, gid := snow.ID(7), snow.ID(9)
	rule := func(prefix string, permission domain.Permission) *domain.PBACRule {
		return &domain.PBACRule{PathPrefix: prefix, Permission: permission}
	}
	def := func(prefix string, permission domain.Permission) *domain.ProjectPathPermission {
		return &domain.ProjectPathPermission{PathPrefix: prefix, Permission: permission}
	}
	read, write := domain.PermissionRead, domain.PermissionWrite
	base := token(usecase.NewTestPathFilter(
		[]*domain.PBACRule{rule("a", read), rule("b/c", read|write)},
		[]*domain.ProjectPathPermission{def("", read), def("secret", 0)}))
	require.Len(t, base, 32)

	// What does not change what is readable does not change the token: the
	// order, who a rule is for, ids, other permission bits, rules that grant
	// no read at all and duplicates.
	same := usecase.NewTestPathFilter(
		[]*domain.PBACRule{
			{ID: 12, GroupID: &gid, GroupName: "artists", PathPrefix: "b/c", Permission: read},
			rule("zzz", write),
			{ID: 3, UserID: &uid, PathPrefix: "a", Permission: read | domain.PermissionLock},
			rule("a", read),
		},
		[]*domain.ProjectPathPermission{{ID: 5, ProjectID: 1, PathPrefix: "secret", Permission: write}, def("", read|write)})
	require.Equal(t, base, token(same))

	// What does, does.
	different := map[string]*usecase.PathFilter{
		"another rule":      usecase.NewTestPathFilter([]*domain.PBACRule{rule("a", read), rule("b/c", read), rule("d", read)}, []*domain.ProjectPathPermission{def("", read), def("secret", 0)}),
		"a rule less":       usecase.NewTestPathFilter([]*domain.PBACRule{rule("a", read)}, []*domain.ProjectPathPermission{def("", read), def("secret", 0)}),
		"a rule moved":      usecase.NewTestPathFilter([]*domain.PBACRule{rule("a", read), rule("b", read)}, []*domain.ProjectPathPermission{def("", read), def("secret", 0)}),
		"a default opened":  usecase.NewTestPathFilter([]*domain.PBACRule{rule("a", read), rule("b/c", read)}, []*domain.ProjectPathPermission{def("", read), def("secret", read)}),
		"a default removed": usecase.NewTestPathFilter([]*domain.PBACRule{rule("a", read), rule("b/c", read)}, []*domain.ProjectPathPermission{def("", read)}),
		"a default closed":  usecase.NewTestPathFilter([]*domain.PBACRule{rule("a", read), rule("b/c", read)}, []*domain.ProjectPathPermission{def("", 0), def("secret", 0)}),
		"rule as default":   usecase.NewTestPathFilter([]*domain.PBACRule{rule("b/c", read)}, []*domain.ProjectPathPermission{def("", read), def("secret", 0), def("a", read)}),
		"no rules":          usecase.NewTestPathFilter(nil, nil),
		"everything":        usecase.AllowAllFilter(),
	}
	seen := map[string]string{base: "base"}
	for name, filter := range different {
		got := token(filter)
		require.NotContains(t, seen, got, "%s has the token of %s", name, seen[got])
		seen[got] = name
	}

	// Sparse sets count as sets; the recursive flag counts.
	all := usecase.AllowAllFilter()
	require.Equal(t, token(all, "b", "a/"), token(all, "a", "b", "a"))
	require.NotEqual(t, token(all, "a"), token(all, "a", "b"))
	require.NotEqual(t, token(all), token(all, "a"))
	require.NotEqual(t, token(all, "ab"), token(all, "a", "b"))
	require.NotEqual(t, string(mustView(t, f, true)), string(mustView(t, f, false)))
}
