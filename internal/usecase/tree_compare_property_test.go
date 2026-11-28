package usecase

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/diff"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/treehash"
)

// propFile is one file of a generated tree.
type propFile struct {
	content string
	mode    int
	binary  bool
}

type propModel map[string]propFile

func (m propModel) clone() propModel {
	out := make(propModel, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (m propModel) paths() []string {
	out := make([]string, 0, len(m))
	for p := range m {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func (m propModel) dirs() []string {
	seen := map[string]bool{}
	for p := range m {
		for i := range p {
			if p[i] == '/' {
				seen[p[:i]] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for d := range seen {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

func (m propModel) removeDir(dir string) {
	for p := range m {
		if strings.HasPrefix(p, dir+"/") {
			delete(m, p)
		}
	}
}

// set writes a file, dropping a file where a directory is needed and a
// directory where the file goes (file/folder swaps).
func (m propModel) set(path string, f propFile) {
	for i := range path {
		if path[i] == '/' {
			delete(m, path[:i])
		}
	}
	m.removeDir(path)
	m[path] = f
}

// propGen mutates models like the e2e deltaGen, plus what only a server-side
// comparison can meet: raw modes of one class (0o644/0o664), a binary flag
// flipped without a content change, and multi-line text edits that merge.
type propGen struct {
	rng  *rand.Rand
	tick int
}

var (
	propDirNames  = []string{"a", "b", "c"}
	propFileNames = []string{"f1.txt", "f2.txt", "x.bin"}
	propRootFiles = []string{"README.md", "f1.txt"}
	propModes     = []int{0o644, 0o664, 0o755, 0o444}
)

func (g *propGen) pick(list []string) string { return list[g.rng.Intn(len(list))] }

func (g *propGen) dirPath(max int) string {
	parts := make([]string, g.rng.Intn(max+1))
	for i := range parts {
		parts[i] = g.pick(propDirNames)
	}
	return strings.Join(parts, "/")
}

func (g *propGen) filePath() string {
	if dir := g.dirPath(3); dir != "" {
		return dir + "/" + g.pick(propFileNames)
	}
	return g.pick(propRootFiles)
}

func (g *propGen) file() propFile {
	g.tick++
	f := propFile{mode: propModes[g.rng.Intn(len(propModes))]}
	switch g.rng.Intn(8) {
	case 0:
	case 1:
		f.content, f.binary = fmt.Sprintf("bin\x00%d", g.tick), true
	default:
		f.content = fmt.Sprintf("one %d\ntwo\nthree\nfour %d\nfive\n", g.tick, g.rng.Intn(3))
	}
	return f
}

func (g *propGen) mutate(m propModel) {
	for ops := 1 + g.rng.Intn(4); ops > 0; ops-- {
		paths, dirs := m.paths(), m.dirs()
		switch op := g.rng.Intn(13); {
		case op <= 1 || len(paths) == 0:
			m.set(g.filePath(), g.file())
		case op <= 3:
			// Edit one line: two such edits merge unless they hit the same line.
			p := g.pick(paths)
			f := m[p]
			lines := strings.Split(f.content, "\n")
			i := g.rng.Intn(len(lines))
			lines[i] = fmt.Sprintf("edit %d", g.rng.Intn(4))
			f.content = strings.Join(lines, "\n")
			m[p] = f
		case op == 4:
			delete(m, g.pick(paths))
		case op == 5:
			p := g.pick(paths)
			f := m[p]
			f.mode = propModes[g.rng.Intn(len(propModes))]
			m[p] = f
		case op == 6:
			p := g.pick(paths)
			f := m[p]
			f.binary = !f.binary
			m[p] = f
		case op == 7 && len(dirs) > 0:
			g.transplant(m, g.pick(dirs), g.rng.Intn(2) == 0)
		case op == 8 && len(dirs) > 0:
			m.removeDir(g.pick(dirs))
		case op == 9:
			m.set(g.pick(paths)+"/"+g.pick(propFileNames), g.file())
		case op == 10 && len(dirs) > 0:
			m.set(g.pick(dirs), g.file())
		default:
			m.set(g.filePath(), g.file())
		}
	}
}

func (g *propGen) transplant(m propModel, from string, move bool) {
	to := g.dirPath(2)
	if to == "" {
		to = g.pick(propDirNames)
	}
	if to == from || strings.HasPrefix(to, from+"/") || strings.HasPrefix(from, to+"/") {
		return
	}
	moved := propModel{}
	for p, f := range m {
		if strings.HasPrefix(p, from+"/") {
			moved[to+strings.TrimPrefix(p, from)] = f
			if move {
				delete(m, p)
			}
		}
	}
	m.removeDir(to)
	for _, p := range moved.paths() {
		m.set(p, moved[p])
	}
}

// hashedTreeRepo stores generated trees the way the database does: every
// tree gets its own node rows, and reads resolve a node to the oldest node
// with the same hash that holds rows (files for file reads, files or children
// for child reads), so a tree whose hash an older tree already has reads the
// older rows.
type hashedTreeRepo struct {
	branchRepository
	nodes      map[int64]*domain.TreeNode
	files      map[int64][]*domain.File
	children   map[int64][]int64
	byHash     map[domain.Hash][]int64
	commits    map[snow.ID]*domain.Commit
	fileChunks map[int64][]domain.Chunk
	nextID     int64
	nextFile   int64
	// reads counts the batched and single-directory reads.
	reads int
}

func newHashedTreeRepo() *hashedTreeRepo {
	return &hashedTreeRepo{
		nodes: map[int64]*domain.TreeNode{}, files: map[int64][]*domain.File{}, children: map[int64][]int64{},
		byHash: map[domain.Hash][]int64{}, commits: map[snow.ID]*domain.Commit{}, fileChunks: map[int64][]domain.Chunk{},
	}
}

func (r *hashedTreeRepo) addDir(name string, model propModel, prefix string, chunks *memChunks) *domain.TreeNode {
	r.nextID++
	id := r.nextID
	fileNames, dirNames := map[string]propFile{}, map[string]bool{}
	for p, f := range model {
		rest, ok := strings.CutPrefix(p, prefix)
		if !ok {
			continue
		}
		if i := strings.IndexByte(rest, '/'); i >= 0 {
			dirNames[rest[:i]] = true
		} else {
			fileNames[rest] = f
		}
	}
	var fileEntries []treehash.FileEntry
	for _, n := range propModel(fileNames).paths() {
		f := fileNames[n]
		var list []domain.Chunk
		if f.content != "" {
			hash := chunker.Sum([]byte(f.content))
			chunks.data[hash] = []byte(f.content)
			list = []domain.Chunk{{Hash: hash, SizeBytes: int64(len(f.content))}}
		}
		hashes := make([]domain.Hash, len(list))
		for i, c := range list {
			hashes[i] = c.Hash
		}
		r.nextFile++
		file := &domain.File{
			ID: r.nextFile, Name: n, Mode: f.mode, SizeBytes: int64(len(f.content)), IsBinary: f.binary,
			Encoding: chunker.EncodingRaw, Hash: treehash.FileHash(hashes), TreeID: id,
		}
		r.fileChunks[file.ID] = list
		r.files[id] = append(r.files[id], file)
		fileEntries = append(fileEntries, treehash.FileEntry{Name: n, Hash: file.Hash, Mode: f.mode})
	}
	names := make([]string, 0, len(dirNames))
	for n := range dirNames {
		names = append(names, n)
	}
	sort.Strings(names)
	var treeEntries []treehash.TreeEntry
	for _, n := range names {
		child := r.addDir(n, model, prefix+n+"/", chunks)
		treeEntries = append(treeEntries, treehash.TreeEntry{Name: n, Hash: child.Hash})
		r.children[id] = append(r.children[id], child.ID)
	}
	node := &domain.TreeNode{ID: id, Name: name, Hash: treehash.TreeHash(fileEntries, treeEntries)}
	r.nodes[id] = node
	r.byHash[node.Hash] = append(r.byHash[node.Hash], id)
	return node
}

func (r *hashedTreeRepo) addCommit(id snow.ID, parent *snow.ID, model propModel, chunks *memChunks) {
	root := r.addDir("root", model, "", chunks)
	r.commits[id] = &domain.Commit{
		ID: id, ProjectID: 1, TreeID: root.ID, Parent1ID: parent, UserID: id,
		Hash: domain.Hash{byte(id), byte(id >> 8), 0xcc}, Message: fmt.Sprintf("commit %d", id),
	}
}

func (r *hashedTreeRepo) contentNode(id int64, withChildren bool) int64 {
	for _, candidate := range r.byHash[r.nodes[id].Hash] {
		if len(r.files[candidate]) > 0 || withChildren && len(r.children[candidate]) > 0 {
			return candidate
		}
	}
	return 0
}

func (r *hashedTreeRepo) GetCommit(_ context.Context, id snow.ID) (*domain.Commit, error) {
	if c, ok := r.commits[id]; ok {
		return c, nil
	}
	return nil, domain.NewErrorRecordNotFound()
}

func (r *hashedTreeRepo) GetTreeNode(_ context.Context, id int64) (*domain.TreeNode, error) {
	cp := *r.nodes[id]
	return &cp, nil
}

func (r *hashedTreeRepo) filesOf(id int64, withChunks bool) []*domain.File {
	var out []*domain.File
	for _, f := range r.files[r.contentNode(id, false)] {
		cp := *f
		if withChunks {
			cp.Chunks = r.fileChunks[f.ID]
		}
		out = append(out, &cp)
	}
	return out
}

func (r *hashedTreeRepo) childrenOf(id int64) []*domain.TreeNode {
	var out []*domain.TreeNode
	for _, kid := range r.children[r.contentNode(id, true)] {
		cp := *r.nodes[kid]
		out = append(out, &cp)
	}
	return out
}

func (r *hashedTreeRepo) ListFilesByTree(_ context.Context, id int64) ([]*domain.File, error) {
	r.reads++
	return r.filesOf(id, true), nil
}

func (r *hashedTreeRepo) ListTreeChildren(_ context.Context, id int64) ([]*domain.TreeNode, error) {
	r.reads++
	return r.childrenOf(id), nil
}

func (r *hashedTreeRepo) byIDs(ids []int64, read func(int64) int) {
	r.reads++
	for _, id := range ids {
		read(id)
	}
}

func (r *hashedTreeRepo) ListFilesByTrees(_ context.Context, ids []int64) (map[int64][]*domain.File, error) {
	out := map[int64][]*domain.File{}
	r.byIDs(ids, func(id int64) int { out[id] = r.filesOf(id, true); return 0 })
	return out, nil
}

func (r *hashedTreeRepo) ListFileMetaByTrees(_ context.Context, ids []int64) (map[int64][]*domain.File, error) {
	out := map[int64][]*domain.File{}
	r.byIDs(ids, func(id int64) int { out[id] = r.filesOf(id, false); return 0 })
	return out, nil
}

func (r *hashedTreeRepo) ListTreeChildrenByTrees(_ context.Context, ids []int64) (map[int64][]*domain.TreeNode, error) {
	out := map[int64][]*domain.TreeNode{}
	r.byIDs(ids, func(id int64) int { out[id] = r.childrenOf(id); return 0 })
	return out, nil
}

func (r *hashedTreeRepo) ListChunksByFiles(_ context.Context, ids []int64) (map[int64][]domain.Chunk, error) {
	r.reads++
	out := map[int64][]domain.Chunk{}
	for _, id := range ids {
		if list := r.fileChunks[id]; len(list) > 0 {
			out[id] = list
		}
	}
	return out, nil
}

// memChunks is a chunk store a merge can write text merges to and read back.
type memChunks struct {
	data map[domain.Hash][]byte
}

func (m *memChunks) Get(_ context.Context, _ snow.ID, hash domain.Hash) ([]byte, error) {
	if data, ok := m.data[hash]; ok {
		return data, nil
	}
	return nil, errors.New("chunk not found")
}

func (m *memChunks) StoreChunks(_ context.Context, _ snow.ID, chunks []ChunkContent) ([]ChunkRow, error) {
	rows := make([]ChunkRow, 0, len(chunks))
	for _, c := range chunks {
		m.data[c.Hash] = c.Data
		rows = append(rows, ChunkRow{Hash: c.Hash, SizeBytes: int64(len(c.Data))})
	}
	return rows, nil
}

func (m *memChunks) RecordStoredChunks(context.Context, snow.ID, []ChunkRow) error { return nil }

// propFilters are the read views the diff comparison runs under.
func propFilters() map[string]*PathFilter {
	return map[string]*PathFilter{
		"allow all":       AllowAllFilter(),
		"one subtree":     restrictedReadFilter("a"),
		"nested subtrees": restrictedReadFilter("b/a", "c", "README.md"),
		"denied paths": {set: &permissionSet{defaults: []*domain.ProjectPathPermission{
			{PathPrefix: "", Permission: domain.PermissionRead},
			{PathPrefix: "a/b", Permission: 0},
			{PathPrefix: "c/f1.txt", Permission: 0},
			{PathPrefix: "b/x.bin", Permission: 0},
		}}, permission: domain.PermissionRead},
	}
}

// propHistory is one generated project: a main line, two branches off one of
// its commits and an unrelated line.
type propHistory struct {
	repo   *hashedTreeRepo
	chunks *memChunks
	all    []snow.ID
	base   snow.ID
	ours   []snow.ID
	theirs []snow.ID
	other  []snow.ID
}

func buildPropHistory(seed int64) *propHistory {
	g := &propGen{rng: rand.New(rand.NewSource(seed))}
	h := &propHistory{repo: newHashedTreeRepo(), chunks: &memChunks{data: map[domain.Hash][]byte{}}}
	next := snow.ID(100)
	chain := func(start propModel, parent *snow.ID, n int, first func(propModel)) []snow.ID {
		var ids []snow.ID
		model := start
		for i := 0; i < n; i++ {
			model = model.clone()
			if i == 0 && first != nil {
				first(model)
			}
			g.mutate(model)
			next++
			h.repo.addCommit(next, parent, model, h.chunks)
			id := next
			parent = &id
			ids = append(ids, id)
		}
		return ids
	}
	root := propModel{}
	for i := 0; i < 16; i++ {
		root.set(g.filePath(), g.file())
	}
	// Both branches edit different lines of one shared text file, which
	// merges unless a random mutation hits it too.
	shared := "shared/notes.txt"
	root.set(shared, propFile{content: "one\ntwo\nthree\nfour\nfive\n", mode: 0o644})
	edit := func(line int) func(propModel) {
		return func(m propModel) {
			if f, ok := m[shared]; ok {
				lines := strings.Split(f.content, "\n")
				lines[line] = fmt.Sprintf("line %d by seed %d", line, seed)
				f.content = strings.Join(lines, "\n")
				m[shared] = f
			}
		}
	}
	next++
	h.repo.addCommit(next, nil, root, h.chunks)
	h.base = next
	baseID := h.base
	h.ours = chain(root, &baseID, 1+g.rng.Intn(3), edit(0))
	h.theirs = chain(root, &baseID, 1+g.rng.Intn(3), edit(4))
	h.other = chain(propModel{}, nil, 2, nil)
	h.all = append(append(append([]snow.ID{h.base}, h.ours...), h.theirs...), h.other...)
	return h
}

func (h *propHistory) root(t *testing.T, id snow.ID) *domain.TreeNode {
	t.Helper()
	if id == 0 {
		return nil
	}
	root, err := h.repo.GetTreeNode(context.Background(), h.repo.commits[id].TreeID)
	require.NoError(t, err)
	return root
}

// fullEntries is the comparison oracle: the whole manifest of a commit as
// the filter sees it, flattened.
func (h *propHistory) fullEntries(t *testing.T, uc *Branch, id snow.ID, filter *PathFilter) map[string]diff.Entry {
	t.Helper()
	root := h.root(t, id)
	if root == nil {
		return map[string]diff.Entry{}
	}
	require.NoError(t, uc.loadTreeManifest(context.Background(), root, "", true, filter, nil))
	return diff.FromTree(root)
}

var propSeeds = []int64{1, 2, 3, 5, 8, 13, 21, 34, 55, 89, 144, 233}

// TestCompareTrees_MatchesFullComparison checks, over random histories, read
// filters and every commit pair (the empty tree included), that the diff built
// from the hash-pruned batched walk is exactly the diff of the two full
// manifests, and the diff of the per-directory walk it replaced.
func TestCompareTrees_MatchesFullComparison(t *testing.T) {
	cases, changed := 0, 0
	for _, seed := range propSeeds {
		h := buildPropHistory(seed)
		uc := NewBranch(nil, h.repo, newTestBranchNode(t))
		uc.chunks = h.chunks
		ctx := context.Background()
		load := uc.loadChunk(ctx, 1)
		ids := append([]snow.ID{0}, h.all...)
		for name, filter := range propFilters() {
			for _, from := range ids {
				for _, to := range ids {
					label := fmt.Sprintf("seed %d, %s, %d -> %d", seed, name, from, to)
					want := diff.TreeDiffEntries(h.fullEntries(t, uc, from, filter), h.fullEntries(t, uc, to, filter), load)

					h.repo.reads = 0
					gotBase, gotHead, err := uc.changedTreeEntries(ctx, h.root(t, from), h.root(t, to), filter)
					require.NoError(t, err, label)
					require.Equal(t, want, diff.TreeDiffEntries(gotBase, gotHead, load), label)
					if from == to {
						require.Zero(t, h.repo.reads, "%s: identical trees read nothing", label)
					}

					refBase, refHead, err := uc.changedTreeEntriesReference(ctx, h.root(t, from), h.root(t, to), filter)
					require.NoError(t, err, label)
					require.Equal(t, want, diff.TreeDiffEntries(refBase, refHead, load), label)
					cases++
					if len(want) > 0 {
						changed++
					}
				}
			}
		}
	}
	t.Logf("%d comparisons, %d with changes", cases, changed)
	require.Greater(t, changed, cases/2)
}

// TestCompareTrees_ThreeWayMatchesFullComparison checks the three-tree form:
// with every tree deciding, the reported paths are exactly the paths where
// some tree differs from another in the full manifests, and every tree's file
// is reported at each of them.
func TestCompareTrees_ThreeWayMatchesFullComparison(t *testing.T) {
	for _, seed := range propSeeds {
		h := buildPropHistory(seed)
		uc := NewBranch(nil, h.repo, newTestBranchNode(t))
		ctx := context.Background()
		triples := [][3]snow.ID{
			{h.base, h.ours[len(h.ours)-1], h.theirs[len(h.theirs)-1]},
			{0, h.ours[0], h.other[len(h.other)-1]},
			{h.base, h.base, h.theirs[0]},
			{h.ours[0], h.theirs[0], h.other[0]},
		}
		for _, triple := range triples {
			full := make([]map[string]diff.Entry, 3)
			roots := make([]*domain.TreeNode, 3)
			for i, id := range triple {
				full[i] = h.fullEntries(t, uc, id, AllowAllFilter())
				roots[i] = h.root(t, id)
			}
			want := map[string][3]*diff.Entry{}
			for i := range full {
				for path := range full[i] {
					var at [3]*diff.Entry
					for j := range full {
						if e, ok := full[j][path]; ok {
							at[j] = &e
						}
					}
					same := true
					for j := 1; j < 3; j++ {
						if (at[0] == nil) != (at[j] == nil) || at[0] != nil && (at[0].Hash != at[j].Hash || at[0].Mode != at[j].Mode) {
							same = false
						}
					}
					if !same {
						want[path] = at
					}
				}
			}
			changed, err := compareTrees(ctx, h.repo, treeView{filter: AllowAllFilter()}, roots, nil)
			require.NoError(t, err)
			got := map[string][3]*diff.Entry{}
			for _, file := range changed {
				var at [3]*diff.Entry
				for side, f := range file.files {
					if f != nil {
						out := map[string]diff.Entry{}
						diff.AddFileEntries(out, file.dir, []*domain.File{f})
						e := out[file.path()]
						at[side] = &e
					}
				}
				got[file.path()] = at
			}
			require.Equal(t, want, got, "seed %d, triple %v", seed, triple)
		}
	}
}

type recordedLanding struct {
	files   []*domain.PushFile
	removed []string
	label   string
	commit  snow.ID
}

func recordLandings(out *[]recordedLanding) func(mergeLanding) error {
	return func(step mergeLanding) error {
		files := append([]*domain.PushFile(nil), step.files...)
		sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
		rec := recordedLanding{files: files, removed: step.removed, label: step.label}
		if step.commit != nil {
			rec.commit = step.commit.ID
		}
		*out = append(*out, rec)
		return nil
	}
}

// TestReplayMerge_MatchesFullTreeMerge checks, over random histories, that a
// merge (squash lands the same tree) and a rebase read from the changed paths
// only land exactly what the full-tree implementation landed: the same
// commits with the same files and removals, the same stored text merges, the
// same conflicts, for diverged branches, a source on top of the target, an
// identical source and unrelated histories.
func TestReplayMerge_MatchesFullTreeMerge(t *testing.T) {
	outcomes := map[string]int{}
	for _, seed := range propSeeds {
		h := buildPropHistory(seed)
		uc := NewBranch(nil, h.repo, newTestBranchNode(t))
		uc.chunks = h.chunks
		uc.chunkUploads = h.chunks
		ctx := context.Background()
		ours, theirs := h.ours[len(h.ours)-1], h.theirs[len(h.theirs)-1]
		base := h.base
		scenarios := []struct {
			name          string
			base          *snow.ID
			target, other snow.ID
		}{
			{"diverged", &base, ours, theirs},
			{"diverged reversed", &base, theirs, ours},
			{"source ahead", &base, base, theirs},
			{"same tree", &base, ours, ours},
			{"source behind", &base, theirs, base},
			{"unrelated", nil, ours, h.other[len(h.other)-1]},
		}
		for _, sc := range scenarios {
			for _, strategy := range []string{domain.MergeStrategyMerge, domain.MergeStrategyRebase} {
				label := fmt.Sprintf("seed %d, %s, %s", seed, sc.name, strategy)
				var want, got []recordedLanding
				wantTarget, wantMerged, wantStored, wantErr := uc.replayMergeReference(ctx, 1, strategy, sc.base, sc.target, sc.other, recordLandings(&want))
				state, gotStored, gotErr := uc.replayMerge(ctx, 1, strategy, sc.base, sc.target, sc.other, recordLandings(&got))
				if wantErr != nil {
					require.Error(t, gotErr, label)
					require.Equal(t, wantErr.Error(), gotErr.Error(), label)
					outcomes["conflict"]++
					continue
				}
				require.NoError(t, gotErr, label)
				require.Equal(t, want, got, label)
				require.Equal(t, wantStored, gotStored, label)
				if wantTarget == nil {
					require.Nil(t, state, label)
					outcomes["nothing to replay"]++
					continue
				}
				// ensureMergedWrites sees the same changed paths.
				wantFiles, wantRemoved := mergeDelta(wantTarget, wantMerged)
				gotFiles, gotRemoved := mergeDelta(state.target, state.current)
				sort.Slice(wantFiles, func(i, j int) bool { return wantFiles[i].Path < wantFiles[j].Path })
				sort.Slice(gotFiles, func(i, j int) bool { return gotFiles[i].Path < gotFiles[j].Path })
				require.Equal(t, wantFiles, gotFiles, label)
				require.Equal(t, wantRemoved, gotRemoved, label)
				if len(wantStored) > 0 {
					outcomes["text merge"]++
				}
				outcomes["landed"]++
			}
		}
	}
	t.Logf("merge outcomes: %v", outcomes)
	require.Positive(t, outcomes["landed"])
	require.Positive(t, outcomes["conflict"])
	require.Positive(t, outcomes["text merge"])
	require.Positive(t, outcomes["nothing to replay"])
}

// TestCompareTrees_HashUncoveredFields pins why skipping directories by
// stored hash is exact although a tree hash covers names, file hashes and
// mode classes only: a commit that changes only a file's raw mode within its
// class or its binary flag writes a directory with the hash its parent
// already has, and every read of that hash returns the older rows, so the
// full manifests show no difference and neither does the walk. In a directory
// that changed for another reason the new rows are read, and the difference
// is reported exactly as diff.Compare and merge.ThreeWay see it (raw mode
// yes, binary flag alone no).
func TestCompareTrees_HashUncoveredFields(t *testing.T) {
	ctx := context.Background()
	chunks := &memChunks{data: map[domain.Hash][]byte{}}
	repo := newHashedTreeRepo()
	text := propFile{content: "a\n", mode: 0o644}
	base := propModel{"d/f.txt": text, "d/g.txt": text, "e/h.txt": text}
	repo.addCommit(1, nil, base, chunks)
	// Raw mode within the class and the binary flag only: same hashes.
	quiet := base.clone()
	quiet["d/f.txt"] = propFile{content: "a\n", mode: 0o664}
	quiet["d/g.txt"] = propFile{content: "a\n", mode: 0o644, binary: true}
	parent := snow.ID(1)
	repo.addCommit(2, &parent, quiet, chunks)
	// The same, next to a real change in the same directory.
	loud := quiet.clone()
	loud["d/new.txt"] = text
	repo.addCommit(3, &parent, loud, chunks)
	uc := NewBranch(nil, repo, newTestBranchNode(t))
	h := &propHistory{repo: repo, chunks: chunks}

	require.Equal(t, repo.nodes[repo.commits[1].TreeID].Hash, repo.nodes[repo.commits[2].TreeID].Hash)
	full := h.fullEntries(t, uc, 2, AllowAllFilter())
	require.Equal(t, 0o644, full["d/f.txt"].Mode, "the older rows are read")
	require.False(t, full["d/g.txt"].IsBinary)

	repo.reads = 0
	b2, h2, err := uc.changedTreeEntries(ctx, h.root(t, 1), h.root(t, 2), AllowAllFilter())
	require.NoError(t, err)
	require.Empty(t, diff.Compare(b2, h2))
	require.Zero(t, repo.reads)

	b3, h3, err := uc.changedTreeEntries(ctx, h.root(t, 1), h.root(t, 3), AllowAllFilter())
	require.NoError(t, err)
	want := diff.Compare(h.fullEntries(t, uc, 1, AllowAllFilter()), h.fullEntries(t, uc, 3, AllowAllFilter()))
	require.Equal(t, want, diff.Compare(b3, h3))
	paths := []string{}
	for _, c := range want {
		paths = append(paths, c.Path)
	}
	require.Equal(t, []string{"d/f.txt", "d/new.txt"}, paths, "raw mode is a change, the binary flag alone is not")
}
