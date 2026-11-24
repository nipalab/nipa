package localrepo

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/domain"
	sqlcLocalrepo "github.com/nipalab/nipa/internal/repository/sqlc/localrepo"
	"github.com/nipalab/nipa/internal/treehash"
)

// pushSpec is a file as the push scan describes it: a Unix permission, the
// client's binary flag and encoding.
type pushSpec struct {
	seed     byte
	chunks   int
	perm     int
	binary   bool
	encoding string
}

func spec(seed byte) pushSpec {
	return pushSpec{seed: seed, chunks: 1, perm: 0o644, encoding: chunker.EncodingZstd}
}

func (s pushSpec) chunkHashes() []domain.Hash {
	hashes := make([]domain.Hash, 0, s.chunks)
	for i := 0; i < s.chunks; i++ {
		hashes = append(hashes, domain.Hash{s.seed, byte(i + 1)})
	}
	return hashes
}

func (s pushSpec) pushFile(path string) *domain.PushFile {
	hashes := s.chunkHashes()
	return &domain.PushFile{
		Path:        path,
		Mode:        s.perm,
		SizeBytes:   int64(s.chunks) * 10,
		IsBinary:    s.binary,
		Encoding:    s.encoding,
		FileHash:    chunker.FileHash(hashes),
		ChunkHashes: hashes,
	}
}

// manifestFile is the file as a manifest of the server's tree carries it: the
// mode class, the binary flag forced on lockable assets, the normalized
// encoding.
func (s pushSpec) manifestFile(path string) *domain.File {
	hashes := s.chunkHashes()
	chunks := make([]domain.Chunk, 0, len(hashes))
	for _, h := range hashes {
		chunks = append(chunks, domain.Chunk{Hash: h})
	}
	encoding := s.encoding
	if encoding == "" {
		encoding = chunker.EncodingRaw
	}
	return &domain.File{
		Name:      path[strings.LastIndex(path, "/")+1:],
		Hash:      chunker.FileHash(hashes),
		Mode:      domain.ClassifyPerm(fs.FileMode(uint32(s.perm))),
		SizeBytes: int64(s.chunks) * 10,
		IsBinary:  s.binary || chunker.IsLockablePath(path),
		Encoding:  encoding,
		Chunks:    chunks,
	}
}

// serverStoredMode is the mode the server stores for a pushed permission
// (grpc/server.pbFileModeToInt), which is what its tree hash classifies.
func serverStoredMode(perm int) int {
	switch domain.ClassifyPerm(fs.FileMode(uint32(perm))) {
	case domain.FileModeClassReadOnly:
		return 0o444
	case domain.FileModeClassExecutable:
		return 0o755
	default:
		return 0o644
	}
}

// manifestTree builds the manifest the server sends for a tree holding files:
// no empty directories, every directory hashed over its content the way
// usecase.rehashTree does. A nil map of files still has a root.
func manifestTree(files map[string]pushSpec) *domain.TreeNode {
	root := &domain.TreeNode{Name: "root"}
	dirs := map[string]*domain.TreeNode{"": root}
	perms := map[*domain.File]int{}
	var ensure func(dir string) *domain.TreeNode
	ensure = func(dir string) *domain.TreeNode {
		if node, ok := dirs[dir]; ok {
			return node
		}
		parent, name := "", dir
		if i := strings.LastIndex(dir, "/"); i >= 0 {
			parent, name = dir[:i], dir[i+1:]
		}
		node := &domain.TreeNode{Name: name}
		dirs[dir] = node
		p := ensure(parent)
		p.TreeChildren = append(p.TreeChildren, node)
		return node
	}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		dir := ""
		if i := strings.LastIndex(p, "/"); i >= 0 {
			dir = p[:i]
		}
		file := files[p].manifestFile(p)
		perms[file] = files[p].perm
		node := ensure(dir)
		node.FileChildren = append(node.FileChildren, file)
	}
	var rehash func(node *domain.TreeNode) domain.Hash
	rehash = func(node *domain.TreeNode) domain.Hash {
		entries := make([]treehash.FileEntry, 0, len(node.FileChildren))
		for _, f := range node.FileChildren {
			entries = append(entries, treehash.FileEntry{Name: f.Name, Hash: f.Hash, Mode: serverStoredMode(perms[f])})
		}
		trees := make([]treehash.TreeEntry, 0, len(node.TreeChildren))
		for _, c := range node.TreeChildren {
			trees = append(trees, treehash.TreeEntry{Name: c.Name, Hash: rehash(c)})
		}
		node.Hash = treehash.TreeHash(entries, trees)
		return node.Hash
	}
	rehash(root)
	return root
}

// fullState is every row an apply may touch, as text.
type fullState struct {
	tree   storedState
	synced string
	stat   []string
}

func readFullState(t *testing.T, lr *LocalRepo) fullState {
	t.Helper()
	st := fullState{tree: readState(t, lr)}
	require.NoError(t, lr.db.QueryRow(`SELECT COALESCE((SELECT value FROM meta WHERE key='synced_tree'), '')`).Scan(&st.synced))
	rows, err := lr.db.Query(`SELECT path FROM stat_cache ORDER BY path`)
	require.NoError(t, err)
	for rows.Next() {
		var path string
		require.NoError(t, rows.Scan(&path))
		st.stat = append(st.stat, path)
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	return st
}

func copySpecs(in map[string]pushSpec) map[string]pushSpec {
	out := make(map[string]pushSpec, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// pushCase is a base tree and the push applied to it.
type pushCase struct {
	base    map[string]pushSpec
	pushed  map[string]pushSpec
	removed []string
}

func (c pushCase) after() map[string]pushSpec {
	after := copySpecs(c.base)
	for _, path := range c.removed {
		delete(after, path)
	}
	for path, s := range c.pushed {
		after[path] = s
	}
	return after
}

func (c pushCase) apply(baseCommit, commit string, treeHash domain.Hash) clientDomain.PushApply {
	in := clientDomain.PushApply{
		Removed:      c.removed,
		BaseCommitID: baseCommit,
		CommitID:     commit,
		TreeHash:     treeHash,
	}
	paths := make([]string, 0, len(c.pushed))
	for p := range c.pushed {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		in.Files = append(in.Files, c.pushed[p].pushFile(p))
	}
	return in
}

var applyBase = map[string]pushSpec{
	"root.txt":            spec(1),
	"src/a.go":            spec(2),
	"src/b.go":            spec(3),
	"src/pkg/deep/x.go":   spec(4),
	"src/pkg/deep/y.go":   spec(5),
	"docs/readme.md":      spec(6),
	"assets/tex/hero.png": {seed: 7, chunks: 3, perm: 0o644, binary: true, encoding: chunker.EncodingRaw},
	"solo/chain/only.txt": spec(8),
	"bin/run.sh":          {seed: 9, chunks: 1, perm: 0o755, encoding: chunker.EncodingZstd},
}

// The core property: a row written by the local apply is the row
// SaveSyncedTree writes from the new head's manifest.
func TestApplyPush_EqualsSavingTheManifest(t *testing.T) {
	cases := map[string]pushCase{
		"modify a file": {
			pushed: map[string]pushSpec{"src/a.go": spec(20)},
		},
		"modify a root level file": {
			pushed: map[string]pushSpec{"root.txt": spec(21)},
		},
		"add a root level file": {
			pushed: map[string]pushSpec{"new.txt": spec(22)},
		},
		"add a file in an existing directory": {
			pushed: map[string]pushSpec{"src/c.go": spec(23)},
		},
		"add a file under new nested directories": {
			pushed: map[string]pushSpec{"src/gen/v1/api/types.go": spec(24), "fresh/top.txt": spec(25)},
		},
		"remove a file": {
			removed: []string{"src/b.go"},
		},
		"remove a root level file": {
			removed: []string{"root.txt"},
		},
		"remove the last file of a directory": {
			removed: []string{"docs/readme.md"},
		},
		"remove the last file of a nested chain": {
			removed: []string{"solo/chain/only.txt"},
		},
		"remove a whole subtree": {
			removed: []string{"src/pkg/deep/x.go", "src/pkg/deep/y.go"},
		},
		"add and remove in one push": {
			pushed:  map[string]pushSpec{"src/pkg/deep/z.go": spec(26), "docs/guide/intro.md": spec(27)},
			removed: []string{"src/pkg/deep/x.go", "solo/chain/only.txt", "root.txt"},
		},
		"replace the last file of a directory": {
			pushed:  map[string]pushSpec{"docs/index.md": spec(28)},
			removed: []string{"docs/readme.md"},
		},
		"mode becomes executable": {
			pushed: map[string]pushSpec{"src/a.go": {seed: 2, chunks: 1, perm: 0o755, encoding: chunker.EncodingZstd}},
		},
		"mode becomes read only": {
			pushed: map[string]pushSpec{"src/a.go": {seed: 2, chunks: 1, perm: 0o444, encoding: chunker.EncodingZstd}},
		},
		"executable becomes read write": {
			pushed: map[string]pushSpec{"bin/run.sh": {seed: 9, chunks: 1, perm: 0o664, encoding: chunker.EncodingZstd}},
		},
		"binary flag and encoding change": {
			pushed: map[string]pushSpec{"src/a.go": {seed: 30, chunks: 2, perm: 0o644, binary: true, encoding: chunker.EncodingRaw}},
		},
		"text content under a lockable extension is stored binary": {
			pushed: map[string]pushSpec{"assets/tex/mask.psd": {seed: 31, chunks: 1, perm: 0o644, encoding: chunker.EncodingRaw}},
		},
		"unset encoding is stored raw": {
			pushed: map[string]pushSpec{"src/raw.dat": {seed: 32, chunks: 1, perm: 0o644}},
		},
		"empty file": {
			pushed: map[string]pushSpec{"src/empty.txt": {seed: 33, chunks: 0, perm: 0o644, encoding: chunker.EncodingZstd}},
		},
		"removing a path that is not tracked changes nothing else": {
			pushed:  map[string]pushSpec{"src/a.go": spec(34)},
			removed: []string{"ghost/dir/none.txt"},
		},
		"file named like a sibling directory": {
			pushed: map[string]pushSpec{"src/pkg.txt": spec(35), "src/pkg/deep.txt": spec(36)},
		},
		"remove everything": {
			removed: []string{
				"root.txt", "src/a.go", "src/b.go", "src/pkg/deep/x.go", "src/pkg/deep/y.go",
				"docs/readme.md", "assets/tex/hero.png", "solo/chain/only.txt", "bin/run.sh",
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			tc.base = applyBase
			want := manifestTree(tc.after())

			applied := newSyncedRepo(t)
			require.NoError(t, applied.SaveSyncedTree(manifestTree(tc.base), "c1", nil))
			require.NoError(t, applied.ApplyPush(tc.apply("c1", "c2", want.Hash)))

			saved := newSyncedRepo(t)
			require.NoError(t, saved.SaveSyncedTree(manifestTree(tc.base), "c1", nil))
			require.NoError(t, saved.SaveSyncedTree(want, "c2", nil))

			got := readFullState(t, applied)
			require.Equal(t, readFullState(t, saved), got)
			require.Equal(t, want.Hash.String(), got.tree.treeHash)

			record, err := applied.LoadSyncedTree()
			require.NoError(t, err)
			require.Equal(t, &clientDomain.SyncedTree{CommitID: "c2", Sparse: []string{}}, record)
		})
	}
}

func TestApplyPush_FirstPushToEmptyRepository(t *testing.T) {
	tc := pushCase{pushed: map[string]pushSpec{"a.txt": spec(1), "src/main.go": spec(2)}}
	want := manifestTree(tc.after())

	applied := newSyncedRepo(t)
	// A clone of an empty branch saves no tree and no record.
	require.NoError(t, applied.SaveSyncedTree(nil, "", nil))
	require.NoError(t, applied.ApplyPush(tc.apply("", "c1", want.Hash)))

	saved := newSyncedRepo(t)
	require.NoError(t, saved.SaveSyncedTree(want, "c1", nil))
	require.Equal(t, readFullState(t, saved), readFullState(t, applied))

	// A repository that was never saved at all behaves the same.
	fresh := newSyncedRepo(t)
	require.NoError(t, fresh.ApplyPush(tc.apply("", "c1", want.Hash)))
	require.Equal(t, readFullState(t, saved), readFullState(t, fresh))
}

func TestApplyPush_SuccessivePushes(t *testing.T) {
	applied := newSyncedRepo(t)
	saved := newSyncedRepo(t)
	require.NoError(t, applied.SaveSyncedTree(manifestTree(applyBase), "c0", nil))

	current := copySpecs(applyBase)
	steps := []pushCase{
		{pushed: map[string]pushSpec{"src/pkg/deep/x.go": spec(40)}},
		{removed: []string{"docs/readme.md"}, pushed: map[string]pushSpec{"docs/a/b/c.md": spec(41)}},
		{removed: []string{"docs/a/b/c.md"}},
		{pushed: map[string]pushSpec{"docs/readme.md": spec(6)}},
	}
	for i, step := range steps {
		step.base = current
		current = step.after()
		want := manifestTree(current)
		base, commit := fmt.Sprintf("c%d", i), fmt.Sprintf("c%d", i+1)
		require.NoError(t, applied.ApplyPush(step.apply(base, commit, want.Hash)))
		require.NoError(t, saved.SaveSyncedTree(want, commit, nil))
		require.Equal(t, readFullState(t, saved), readFullState(t, applied), "step %d", i)
	}
	// The last step put the first tree's docs back.
	require.Equal(t, manifestTree(applyBase).TreeChildren[2].Hash, manifestTree(current).TreeChildren[2].Hash)
}

// The server drops an empty directory below a directory it rebuilds and
// keeps one elsewhere; so does the apply.
func TestApplyPush_DropsEmptyDirectoryBelowARebuiltOne(t *testing.T) {
	withEmpty := func(files map[string]pushSpec) *domain.TreeNode {
		root := manifestTree(files)
		var src *domain.TreeNode
		for _, child := range root.TreeChildren {
			if child.Name == "src" {
				src = child
			}
		}
		src.TreeChildren = append(src.TreeChildren, &domain.TreeNode{Name: "void", Hash: emptyTreeHash})
		return root
	}
	tc := pushCase{base: applyBase, pushed: map[string]pushSpec{"src/a.go": spec(50)}}

	// The base holds src/void; its hash is not part of the stored src hash
	// here, which is fine: src is rebuilt and only the stored child rows count.
	base := withEmpty(applyBase)
	lr := newSyncedRepo(t)
	require.NoError(t, lr.SaveSyncedTree(base, "c1", nil))
	require.Contains(t, strings.Join(readState(t, lr).nodes, "\n"), "/src/void|")

	want := manifestTree(tc.after())
	require.NoError(t, lr.ApplyPush(tc.apply("c1", "c2", want.Hash)))

	saved := newSyncedRepo(t)
	require.NoError(t, saved.SaveSyncedTree(want, "c2", nil))
	require.Equal(t, readFullState(t, saved), readFullState(t, lr))

	// Outside the rebuilt directories nothing is read or written, so the
	// empty directory's row stays, as its tree does on the server.
	kept := newSyncedRepo(t)
	require.NoError(t, kept.SaveSyncedTree(withEmpty(applyBase), "c1", nil))
	docs := pushCase{base: applyBase, pushed: map[string]pushSpec{"docs/readme.md": spec(51)}}
	require.NoError(t, kept.ApplyPush(docs.apply("c1", "c2", manifestTree(docs.after()).Hash)))
	require.Contains(t, strings.Join(readState(t, kept).nodes, "\n"), "/src/void|")
}

func TestApplyPush_MismatchLeavesEverythingUntouched(t *testing.T) {
	lr := newSyncedRepo(t)
	require.NoError(t, lr.SaveSyncedTree(manifestTree(applyBase), "c1", nil))
	require.NoError(t, lr.SaveStatEntries(map[string]clientDomain.StatEntry{
		"src/a.go": {SizeBytes: 1, Hash: domain.Hash{1}},
		"src/b.go": {SizeBytes: 1, Hash: domain.Hash{1}},
	}))
	before := readFullState(t, lr)

	tc := pushCase{
		base:    applyBase,
		pushed:  map[string]pushSpec{"src/a.go": spec(60), "brand/new/file.txt": spec(61)},
		removed: []string{"src/b.go", "solo/chain/only.txt"},
	}
	right := manifestTree(tc.after()).Hash
	wrong := domain.Hash{0xde, 0xad}

	err := lr.ApplyPush(tc.apply("c1", "c2", wrong))
	var mismatch *clientDomain.PushApplyMismatchError
	require.ErrorAs(t, err, &mismatch)
	require.Equal(t, wrong, mismatch.Expected)
	require.Equal(t, right, mismatch.Computed)
	require.Contains(t, err.Error(), right.String())
	require.Contains(t, err.Error(), wrong.String())
	require.Equal(t, before, readFullState(t, lr), "nothing may be persisted on a mismatch")

	// The same push with the right hash still applies afterwards.
	require.NoError(t, lr.ApplyPush(tc.apply("c1", "c2", right)))
	require.NotEqual(t, before, readFullState(t, lr))
}

// A tree the server sees differently (hidden paths, a legacy hash) yields a
// different root, never a wrong local tree.
func TestApplyPush_MismatchWhenTheStoredTreeIsNotTheServerBase(t *testing.T) {
	visible := copySpecs(applyBase)
	delete(visible, "docs/readme.md")
	lr := newSyncedRepo(t)
	require.NoError(t, lr.SaveSyncedTree(manifestTree(visible), "c1", nil))
	before := readFullState(t, lr)

	tc := pushCase{base: applyBase, pushed: map[string]pushSpec{"src/a.go": spec(70)}}
	err := lr.ApplyPush(tc.apply("c1", "c2", manifestTree(tc.after()).Hash))
	var mismatch *clientDomain.PushApplyMismatchError
	require.ErrorAs(t, err, &mismatch)
	require.Equal(t, before, readFullState(t, lr))
}

func TestApplyPush_RefusesAnUnknownBase(t *testing.T) {
	tc := pushCase{base: applyBase, pushed: map[string]pushSpec{"src/a.go": spec(80)}}
	want := manifestTree(tc.after()).Hash
	tree := func() *domain.TreeNode { return manifestTree(applyBase) }

	cases := map[string]struct {
		setup func(t *testing.T, lr *LocalRepo)
		in    func() clientDomain.PushApply
	}{
		"tree saved without a record": {
			setup: func(t *testing.T, lr *LocalRepo) { require.NoError(t, lr.SaveTree(tree())) },
			in:    func() clientDomain.PushApply { return tc.apply("c1", "c2", want) },
		},
		"record names another commit": {
			setup: func(t *testing.T, lr *LocalRepo) { require.NoError(t, lr.SaveSyncedTree(tree(), "c0", nil)) },
			in:    func() clientDomain.PushApply { return tc.apply("c1", "c2", want) },
		},
		"stored tree is sparse": {
			setup: func(t *testing.T, lr *LocalRepo) {
				require.NoError(t, lr.SaveSyncedTree(tree(), "c1", []string{"src"}))
			},
			in: func() clientDomain.PushApply { return tc.apply("c1", "c2", want) },
		},
		"sparse checkout": {
			setup: func(t *testing.T, lr *LocalRepo) { require.NoError(t, lr.SaveSyncedTree(tree(), "c1", nil)) },
			in: func() clientDomain.PushApply {
				in := tc.apply("c1", "c2", want)
				in.Sparse = []string{"src"}
				return in
			},
		},
		"no pushed commit": {
			setup: func(t *testing.T, lr *LocalRepo) { require.NoError(t, lr.SaveSyncedTree(tree(), "c1", nil)) },
			in:    func() clientDomain.PushApply { return tc.apply("c1", "", want) },
		},
		"no base commit but a recorded tree": {
			setup: func(t *testing.T, lr *LocalRepo) { require.NoError(t, lr.SaveSyncedTree(tree(), "c1", nil)) },
			in:    func() clientDomain.PushApply { return tc.apply("", "c2", want) },
		},
		"no base commit but stored rows": {
			setup: func(t *testing.T, lr *LocalRepo) { require.NoError(t, lr.SaveTree(tree())) },
			in:    func() clientDomain.PushApply { return tc.apply("", "c2", want) },
		},
		"unreadable record": {
			setup: func(t *testing.T, lr *LocalRepo) {
				require.NoError(t, lr.SaveSyncedTree(tree(), "c1", nil))
				_, err := lr.db.Exec(`UPDATE meta SET value = 'not json' WHERE key = 'synced_tree'`)
				require.NoError(t, err)
			},
			in: func() clientDomain.PushApply { return tc.apply("c1", "c2", want) },
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			lr := newSyncedRepo(t)
			c.setup(t, lr)
			before := readFullState(t, lr)
			require.ErrorIs(t, lr.ApplyPush(c.in()), clientDomain.ErrPushApplyBase)
			require.Equal(t, before, readFullState(t, lr))
		})
	}
}

func TestApplyPush_SweepsStatRowsOfRemovedPaths(t *testing.T) {
	lr := newSyncedRepo(t)
	require.NoError(t, lr.SaveSyncedTree(manifestTree(applyBase), "c1", nil))
	entry := clientDomain.StatEntry{SizeBytes: 1, Hash: domain.Hash{1}}
	require.NoError(t, lr.SaveStatEntries(map[string]clientDomain.StatEntry{
		"src/a.go":            entry,
		"src/b.go":            entry,
		"solo/chain/only.txt": entry,
		"docs/readme.md":      entry,
		"src/new.go":          entry,
	}))

	tc := pushCase{
		base:    applyBase,
		pushed:  map[string]pushSpec{"src/a.go": spec(90), "src/new.go": spec(91)},
		removed: []string{"src/b.go", "solo/chain/only.txt"},
	}
	require.NoError(t, lr.ApplyPush(tc.apply("c1", "c2", manifestTree(tc.after()).Hash)))

	require.Equal(t, []string{"docs/readme.md", "src/a.go", "src/new.go"}, readFullState(t, lr).stat,
		"fingerprints of removed paths go, those of pushed and untouched files stay")
}

func TestApplyPush_RejectsMalformedInput(t *testing.T) {
	lr := newSyncedRepo(t)
	require.NoError(t, lr.SaveSyncedTree(manifestTree(applyBase), "c1", nil))
	before := readFullState(t, lr)

	require.Error(t, lr.ApplyPush(clientDomain.PushApply{BaseCommitID: "c1", CommitID: "c2", Files: []*domain.PushFile{nil}}))
	require.Error(t, lr.ApplyPush(clientDomain.PushApply{BaseCommitID: "c1", CommitID: "c2", Files: []*domain.PushFile{{}}}))
	require.Error(t, lr.ApplyPush(clientDomain.PushApply{BaseCommitID: "c1", CommitID: "c2", Removed: []string{""}}))
	require.Equal(t, before, readFullState(t, lr))

	require.EqualError(t, NewLocalRepo().ApplyPush(clientDomain.PushApply{CommitID: "c2"}), "local repo not initialized")
}

func TestApplyPush_UnknownEncodingIsKeptAsSent(t *testing.T) {
	file := pushSpec{seed: 1, chunks: 1, perm: 0o644, encoding: "brotli"}.pushFile("a.txt")
	require.Equal(t, "brotli", pushedFileRow(file, "").Encoding, "the server rejects it; the row is never written")
}

func TestApplyPush_FailsOnBrokenDatabase(t *testing.T) {
	tc := pushCase{
		base:    applyBase,
		pushed:  map[string]pushSpec{"src/a.go": spec(95), "fresh/dir/f.txt": spec(96)},
		removed: []string{"docs/readme.md", "src/b.go"},
	}
	in := tc.apply("c1", "c2", manifestTree(tc.after()).Hash)

	// Each statement an apply runs fails once, in turn; none may leave a
	// partial tree behind.
	for _, table := range []string{"meta", "tree_nodes", "files", "stat_cache"} {
		for _, op := range []string{"INSERT", "UPDATE", "DELETE"} {
			t.Run(table+" "+op, func(t *testing.T) {
				lr := newSyncedRepo(t)
				require.NoError(t, lr.SaveSyncedTree(manifestTree(applyBase), "c1", nil))
				require.NoError(t, lr.SaveStatEntries(map[string]clientDomain.StatEntry{"src/b.go": {SizeBytes: 1}}))
				before := readFullState(t, lr)
				_, err := lr.db.Exec(fmt.Sprintf(
					`CREATE TRIGGER fail_write BEFORE %s ON %s BEGIN SELECT RAISE(ABORT, 'write refused'); END`, op, table))
				require.NoError(t, err)

				err = lr.ApplyPush(in)
				if err == nil {
					// The apply does not run this kind of statement on this table.
					return
				}
				require.ErrorContains(t, err, "write refused")
				require.Equal(t, before, readFullState(t, lr))
			})
		}
	}

	closed := newSyncedRepo(t)
	require.NoError(t, closed.SaveSyncedTree(manifestTree(applyBase), "c1", nil))
	require.NoError(t, closed.db.Close())
	require.Error(t, closed.ApplyPush(in))

	for _, table := range []string{"meta", "tree_nodes", "files"} {
		t.Run("read "+table, func(t *testing.T) {
			lr := newSyncedRepo(t)
			require.NoError(t, lr.SaveSyncedTree(manifestTree(applyBase), "c1", nil))
			_, err := lr.db.Exec(`ALTER TABLE ` + table + ` RENAME TO gone`)
			require.NoError(t, err)
			require.Error(t, lr.ApplyPush(in))
		})
	}
}

// countingDB records the statements run through it.
type countingDB struct {
	sqlcLocalrepo.DBTX
	queries []string
}

func (c *countingDB) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	c.queries = append(c.queries, fmt.Sprint(args...))
	return c.DBTX.QueryContext(ctx, query, args...)
}

func (c *countingDB) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
	c.queries = append(c.queries, fmt.Sprint(args...))
	return c.DBTX.QueryRowContext(ctx, query, args...)
}

// A one-file push into a large tree costs reads and writes in proportion to
// the path depth, never to the size of the tree.
func TestApplyPush_CostFollowsPathDepth(t *testing.T) {
	const dirs, perDir = 60, 80
	files := make(map[string]pushSpec, dirs*perDir+1)
	for d := 0; d < dirs; d++ {
		for f := 0; f < perDir; f++ {
			files[fmt.Sprintf("d%03d/sub/f%03d.txt", d, f)] = pushSpec{seed: byte(d), chunks: 1, perm: 0o644, encoding: chunker.EncodingZstd}
		}
	}
	const target = "d007/sub/deeper/still/leaf.txt"
	files[target] = spec(200)

	lr := newSyncedRepo(t)
	require.NoError(t, lr.SaveSyncedTree(manifestTree(files), "c1", nil))

	tc := pushCase{base: files, pushed: map[string]pushSpec{target: spec(201)}}
	want := manifestTree(tc.after())
	in := tc.apply("c1", "c2", want.Hash)

	// Reads: the plan only asks about the directories on the path.
	counter := &countingDB{DBTX: lr.db}
	plan, err := planPushApply(context.Background(), sqlcLocalrepo.New(counter), in.Files, in.Removed)
	require.NoError(t, err)
	require.Equal(t, want.Hash, plan.root)
	onPath := map[string]bool{"": true, "/d007": true, "/d007/sub": true, "/d007/sub/deeper": true, "/d007/sub/deeper/still": true}
	for _, arg := range counter.queries {
		require.True(t, onPath[arg], "read outside the pushed path: %q", arg)
	}
	require.Len(t, counter.queries, 3*len(onPath), "node, files and children of each directory on the path")
	require.Len(t, plan.fileUpserts, 1)
	require.Len(t, plan.nodeUpserts, len(onPath))
	require.Empty(t, plan.fileDeletes)
	require.Empty(t, plan.nodeDeletes)

	// Writes: one file row and one row per directory on the path.
	for _, table := range []string{"tree_nodes", "files"} {
		for _, op := range []string{"INSERT", "UPDATE", "DELETE"} {
			_, err := lr.db.Exec(fmt.Sprintf(
				`CREATE TRIGGER log_%s_%s AFTER %s ON %s BEGIN INSERT INTO meta (key, value) VALUES ('w' || hex(randomblob(8)), '%s'); END`,
				table, op, op, table, table))
			require.NoError(t, err)
		}
	}
	require.NoError(t, lr.ApplyPush(in))
	var written int
	require.NoError(t, lr.db.QueryRow(`SELECT COUNT(*) FROM meta WHERE key LIKE 'w%'`).Scan(&written))
	require.Equal(t, 1+len(onPath), written, "%d files are stored", len(files))

	_, err = lr.db.Exec(`DELETE FROM meta WHERE key LIKE 'w%'`)
	require.NoError(t, err)
	saved := newSyncedRepo(t)
	require.NoError(t, saved.SaveSyncedTree(want, "c2", nil))
	require.Equal(t, readFullState(t, saved), readFullState(t, lr))
}
