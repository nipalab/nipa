package usecase

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/treedelta"
	"github.com/nipalab/nipa/internal/treehash"
)

const deltaURL = "http://example.com/org/project"

type deltaCall struct {
	target treedelta.Target
	paths  []string
	base   treedelta.Base
	delta  bool
}

// deltaServer is a server holding a few commits, each a set of text files.
// It answers manifests in full and, when a base is named, as the delta the
// real server computes: a directory with the same hash is unchanged, a
// changed one lists every entry. The knobs make it misbehave.
type deltaServer struct {
	t        *testing.T
	commits  map[string]map[string]string
	branches map[string]string
	tags     map[string]string
	view     []byte
	chunks   map[serverDomain.Hash][]byte

	decline     bool
	wrongRoot   bool
	wrongCommit bool
	// keepUnheld lists a file the base does not hold as unchanged.
	keepUnheld bool
	// unsafeName adds a file with a name no working copy may write.
	unsafeName string
	deltaErr   error
	fullErr    error

	calls     []deltaCall
	downloads int
	scopes    []domain.ChunkScope
}

func newDeltaServer(t *testing.T) *deltaServer {
	return &deltaServer{
		t:        t,
		commits:  map[string]map[string]string{},
		branches: map[string]string{},
		tags:     map[string]string{},
		view:     []byte("the-view-token"),
		chunks:   map[serverDomain.Hash][]byte{},
	}
}

// commit stores files as a new commit and returns its id.
func (s *deltaServer) commit(files map[string]string) string {
	id := snow.ID(100 + len(s.commits)).Base36()
	s.commits[id] = files
	return id
}

func (s *deltaServer) head(branch, commit string) { s.branches[branch] = commit }

// manifest builds the manifest of a commit restricted to the sparse prefixes.
func (s *deltaServer) manifest(commit string, sparse []string) *serverDomain.TreeNode {
	files, ok := s.commits[commit]
	require.True(s.t, ok, "unknown commit %q", commit)
	root := &serverDomain.TreeNode{Name: "root", TreeChildren: []*serverDomain.TreeNode{}, FileChildren: []*serverDomain.File{}}
	dirs := map[string]*serverDomain.TreeNode{"": root}
	var ensure func(dir string) *serverDomain.TreeNode
	ensure = func(dir string) *serverDomain.TreeNode {
		if node, ok := dirs[dir]; ok {
			return node
		}
		parent, name := "", dir
		if i := strings.LastIndex(dir, "/"); i >= 0 {
			parent, name = dir[:i], dir[i+1:]
		}
		node := &serverDomain.TreeNode{Name: name, TreeChildren: []*serverDomain.TreeNode{}, FileChildren: []*serverDomain.File{}}
		dirs[dir] = node
		up := ensure(parent)
		up.TreeChildren = append(up.TreeChildren, node)
		return node
	}
	paths := make([]string, 0, len(files))
	for p := range files {
		covered := len(sparse) == 0
		for _, prefix := range sparse {
			covered = covered || p == prefix || strings.HasPrefix(p, prefix+"/")
		}
		if covered {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	for _, p := range paths {
		fileHash, chunks, stored, encoding := testEncodedFile(s.t, p, files[p])
		for h, data := range stored {
			s.chunks[h] = data
		}
		dir, name := "", p
		if i := strings.LastIndex(p, "/"); i >= 0 {
			dir, name = p[:i], p[i+1:]
		}
		mode := serverDomain.FileModeClassReadWrite
		if strings.HasSuffix(name, ".sh") {
			mode = serverDomain.FileModeClassExecutable
		}
		node := ensure(dir)
		node.FileChildren = append(node.FileChildren, &serverDomain.File{
			Name: name, Mode: mode, SizeBytes: int64(len(files[p])), Encoding: encoding, Hash: fileHash, Chunks: chunks,
		})
	}
	var rehash func(node *serverDomain.TreeNode) serverDomain.Hash
	rehash = func(node *serverDomain.TreeNode) serverDomain.Hash {
		entries := make([]treehash.FileEntry, 0, len(node.FileChildren))
		for _, f := range node.FileChildren {
			entries = append(entries, treehash.FileEntry{Name: f.Name, Hash: f.Hash, Mode: treedelta.HashMode(f.Mode)})
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

func diffTrees(base, target *serverDomain.TreeNode) *treedelta.Dir {
	out := &treedelta.Dir{Name: target.Name}
	if base != nil && base.Hash == target.Hash {
		out.Unchanged = true
		return out
	}
	heldFiles := map[string]*serverDomain.File{}
	heldDirs := map[string]*serverDomain.TreeNode{}
	if base != nil {
		for _, f := range base.FileChildren {
			heldFiles[f.Name] = f
		}
		for _, d := range base.TreeChildren {
			heldDirs[d.Name] = d
		}
	}
	for _, f := range target.FileChildren {
		entry := treedelta.File{Name: f.Name}
		if old := heldFiles[f.Name]; old == nil || old.Hash != f.Hash || old.Mode != f.Mode {
			entry.Changed = f
		}
		out.Files = append(out.Files, entry)
	}
	for _, d := range target.TreeChildren {
		out.Dirs = append(out.Dirs, diffTrees(heldDirs[d.Name], d))
	}
	return out
}

func (s *deltaServer) Connect(context.Context, string) error { return nil }

func (s *deltaServer) GetBranchByName(_ context.Context, _, _, name string) (*serverDomain.Branch, error) {
	branch := &serverDomain.Branch{Name: name}
	if commit := s.branches[name]; commit != "" {
		id, err := snow.ParseBase36(commit)
		require.NoError(s.t, err)
		branch.CommitID = &id
	}
	return branch, nil
}

func (s *deltaServer) GetTagByName(_ context.Context, _, _, name string) (*domain.Tag, error) {
	return &domain.Tag{Name: name, CommitID: s.tags[name]}, nil
}

func (s *deltaServer) GetTreeNodeManifest(context.Context, string, string, string, []string) (*serverDomain.TreeNode, error) {
	s.t.Fatal("a client with the delta call must not use the plain branch manifest")
	return nil, nil
}

func (s *deltaServer) GetCommitTreeManifest(context.Context, string, string, string, []string) (*serverDomain.TreeNode, error) {
	s.t.Fatal("a client with the delta call must not use the plain commit manifest")
	return nil, nil
}

func (s *deltaServer) DownloadChunks(_ context.Context, scope domain.ChunkScope, hashes []serverDomain.Hash, onChunk func(h serverDomain.Hash, data []byte) error) error {
	s.scopes = append(s.scopes, scope)
	s.downloads += len(hashes)
	for _, h := range hashes {
		if err := onChunk(h, s.chunks[h]); err != nil {
			return err
		}
	}
	return nil
}

func (s *deltaServer) GetTreeManifestDelta(_ context.Context, _, _ string, target treedelta.Target, paths []string, base treedelta.Base) (*treedelta.Manifest, error) {
	call := deltaCall{target: target, paths: paths, base: base}
	commit := target.CommitID
	if commit == "" {
		commit = s.branches[target.Branch]
	}
	out := &treedelta.Manifest{View: s.view}
	if commit == "" {
		s.calls = append(s.calls, call)
		return out, nil
	}
	want := s.manifest(commit, paths)
	if base.CommitID == "" || s.decline || string(base.View) != string(s.view) {
		s.calls = append(s.calls, call)
		if s.fullErr != nil {
			return nil, s.fullErr
		}
		out.Full = want
		return out, nil
	}
	call.delta = true
	s.calls = append(s.calls, call)
	if s.deltaErr != nil {
		return nil, s.deltaErr
	}
	held := s.manifest(base.CommitID, paths)
	out.Delta = &treedelta.Delta{Root: diffTrees(held, want)}
	out.CommitID = commit
	if base.TreeHash != nil && *base.TreeHash == held.Hash && !out.Delta.Root.Unchanged {
		hash := want.Hash
		out.Delta.RootHash = &hash
	}
	if s.wrongRoot {
		out.Delta.RootHash = &serverDomain.Hash{0xbe, 0xef}
	}
	if s.wrongCommit {
		out.CommitID = "elsewhere"
	}
	if s.keepUnheld {
		out.Delta.Root.Unchanged = false
		out.Delta.Root.Files = append(out.Delta.Root.Files, treedelta.File{Name: "never-held.txt"})
		out.Delta.RootHash = nil
	}
	if s.unsafeName != "" {
		fileHash, chunks, _, encoding := testEncodedFile(s.t, "x", "x")
		out.Delta.Root.Unchanged = false
		out.Delta.Root.Files = append(out.Delta.Root.Files, treedelta.File{Name: s.unsafeName, Changed: &serverDomain.File{
			Name: s.unsafeName, Mode: 2, SizeBytes: 1, Encoding: encoding, Hash: fileHash, Chunks: chunks,
		}})
		out.Delta.RootHash = nil
	}
	return out, nil
}

// deltaClone is a real working copy brought to a commit of the server by a
// full sync, as a clone leaves it.
type deltaClone struct {
	t      *testing.T
	root   string
	lr     *localrepo.LocalRepo
	server *deltaServer
}

var deltaBaseFiles = map[string]string{
	"README.md":        "readme\n",
	"src/main.go":      "package main\n",
	"src/util.go":      "package main // util\n",
	"src/pkg/deep.go":  "package pkg\n",
	"docs/guide.md":    "# guide\n",
	"docs/old/note.md": "note\n",
	"assets/logo.txt":  "logo\n",
	"bin/run.sh":       "#!/bin/sh\n",
}

var deltaNextFiles = map[string]string{
	"README.md":       "readme\n",
	"src/main.go":     "package main\n\nfunc main() {}\n",
	"src/pkg/deep.go": "package pkg\n",
	"src/gen/api.go":  "package gen\n",
	"docs/guide.md":   "# guide\n",
	"assets/logo.txt": "logo\n",
	"bin/run.sh":      "#!/bin/sh\n",
	"top.txt":         "top\n",
}

func newDeltaClone(t *testing.T, server *deltaServer, sparse ...string) (*deltaClone, string) {
	t.Helper()
	base := server.commit(deltaBaseFiles)
	server.head("main", base)
	c := &deltaClone{t: t, root: t.TempDir(), lr: localrepo.NewLocalRepo(), server: server}
	t.Cleanup(func() { _ = c.lr.Close() })
	require.NoError(t, c.lr.Init(c.root))
	require.NoError(t, c.lr.SaveConfig(domain.Config{Url: deltaURL, Branch: "main", Sparse: sparse}))
	require.NoError(t, c.update(false).Run(context.Background(), c.root))
	server.calls = nil
	return c, base
}

func (c *deltaClone) update(force bool) *Update {
	return newTestUpdate(c.t, c.lr, c.server).WithForce(force)
}

func (c *deltaClone) path(p string) string { return filepath.Join(c.root, filepath.FromSlash(p)) }

// working lists the files of the working copy with their content.
func (c *deltaClone) working() map[string]string {
	c.t.Helper()
	out := map[string]string{}
	require.NoError(c.t, filepath.WalkDir(c.root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(c.root, p)
		if d.IsDir() {
			if rel == localrepo.ConfigDir {
				return filepath.SkipDir
			}
			return nil
		}
		data, err := os.ReadFile(p)
		out[filepath.ToSlash(rel)] = string(data)
		return err
	}))
	return out
}

// requireAt checks the clone holds commit exactly as a fresh full sync of it
// does: working files, snapshot rows, tree hash, record and commit.
func (c *deltaClone) requireAt(commit string, sparse ...string) {
	c.t.Helper()
	want := c.server.manifest(commit, sparse)
	files := map[string]string{}
	walkTreeFiles(want, "", func(path string, _ *serverDomain.File) {
		files[path] = c.server.commits[commit][path]
	})
	require.Equal(c.t, files, c.working())

	fresh := localrepo.NewLocalRepo()
	require.NoError(c.t, fresh.Init(c.t.TempDir()))
	defer func() { _ = fresh.Close() }()
	require.NoError(c.t, fresh.SaveSyncedTreeView(want, commit, sparse, c.server.view))
	wantSnap, err := fresh.Snapshot()
	require.NoError(c.t, err)
	gotSnap, err := c.lr.Snapshot()
	require.NoError(c.t, err)
	require.Equal(c.t, wantSnap, gotSnap)

	wantSynced, err := fresh.LoadSyncedTree()
	require.NoError(c.t, err)
	gotSynced, err := c.lr.LoadSyncedTree()
	require.NoError(c.t, err)
	require.Equal(c.t, wantSynced, gotSynced)

	local, err := c.lr.LoadCommit()
	require.NoError(c.t, err)
	require.Equal(c.t, commit, local.CommitID)
}

// syncTreeAttrs returns the attributes of the last sync.tree span.
func syncTreeAttrs(t *testing.T, recorder *tracetest.SpanRecorder) map[string]string {
	t.Helper()
	var attrs map[string]string
	for _, span := range recorder.Ended() {
		if span.Name() != "sync.tree" {
			continue
		}
		attrs = map[string]string{}
		for _, kv := range span.Attributes() {
			attrs[string(kv.Key)] = kv.Value.AsString()
		}
	}
	require.NotNil(t, attrs, "no sync.tree span recorded")
	return attrs
}

var (
	attrsDelta = map[string]string{"nipa.sync.tree_sync": "delta"}
)

func attrsFull(reason string) map[string]string {
	return map[string]string{"nipa.sync.tree_sync": "full", "nipa.sync.tree_sync_reason": reason}
}

func TestUpdate_FirstSyncStoresTheViewToken(t *testing.T) {
	recorder := recordSpans(t)
	server := newDeltaServer(t)
	base := server.commit(deltaBaseFiles)
	server.head("main", base)
	c := &deltaClone{t: t, root: t.TempDir(), lr: localrepo.NewLocalRepo(), server: server}
	t.Cleanup(func() { _ = c.lr.Close() })
	require.NoError(t, c.lr.Init(c.root))
	require.NoError(t, c.lr.SaveConfig(domain.Config{Url: deltaURL, Branch: "main"}))

	require.NoError(t, c.update(false).Run(context.Background(), c.root))
	require.Equal(t, attrsFull("record"), syncTreeAttrs(t, recorder))
	// By commit, with no base: the head was resolved first.
	require.Equal(t, []deltaCall{{target: treedelta.Target{CommitID: base}}}, server.calls)
	c.requireAt(base)
}

func TestUpdate_AppliesADelta(t *testing.T) {
	recorder := recordSpans(t)
	server := newDeltaServer(t)
	c, base := newDeltaClone(t, server)
	next := server.commit(deltaNextFiles)
	server.head("main", next)
	downloads := server.downloads

	result, err := c.update(false).RunResult(context.Background(), c.root)
	require.NoError(t, err)
	require.False(t, result.UpToDate)
	require.Equal(t, attrsDelta, syncTreeAttrs(t, recorder))
	c.requireAt(next)

	// One request: by commit, naming the base, its view and its root hash.
	held := server.manifest(base, nil).Hash
	require.Equal(t, []deltaCall{{
		target: treedelta.Target{CommitID: next},
		base:   treedelta.Base{CommitID: base, View: server.view, TreeHash: &held},
		delta:  true,
	}}, server.calls)
	require.Equal(t, 3, server.downloads-downloads, "only the three written files are downloaded")
	// The download names the target commit and, as its scope, the written
	// files alone, so the server's visibility check walks only their
	// directories.
	last := server.scopes[len(server.scopes)-1]
	require.Equal(t, []string{next}, last.CommitIDs)
	require.Equal(t, []string{"src/gen/api.go", "src/main.go", "top.txt"}, last.Paths)

	// Up to date now, and the next change is a delta again.
	result, err = c.update(false).RunResult(context.Background(), c.root)
	require.NoError(t, err)
	require.True(t, result.UpToDate)
	back := server.commit(deltaBaseFiles)
	server.head("main", back)
	require.NoError(t, c.update(false).Run(context.Background(), c.root))
	require.Equal(t, attrsDelta, syncTreeAttrs(t, recorder))
	c.requireAt(back)
}

func TestUpdate_DeltaLooksOnlyAtTheFilesItTouches(t *testing.T) {
	server := newDeltaServer(t)
	c, _ := newDeltaClone(t, server)
	next := server.commit(deltaNextFiles)
	server.head("main", next)

	// Untouched files: one edited, one deleted. Neither is in the delta's
	// way, and an unforced update restores neither.
	require.NoError(t, os.WriteFile(c.path("assets/logo.txt"), []byte("my logo\n"), 0o644))
	require.NoError(t, os.Remove(c.path("README.md")))
	counting := &readCountingRepo{LocalRepo: c.lr}
	require.NoError(t, newTestUpdate(t, counting, server).Run(context.Background(), c.root))

	working := c.working()
	require.Equal(t, "my logo\n", working["assets/logo.txt"])
	require.NotContains(t, working, "README.md")
	require.Equal(t, deltaNextFiles["src/main.go"], working["src/main.go"])
	require.NotContains(t, working, "src/util.go")
	require.Zero(t, counting.snapshots, "the snapshot is never loaded")
	require.Zero(t, counting.statLoads, "the stat cache is never loaded")
	require.Zero(t, counting.sweeps, "the object cache is never swept")

	// A forced update then restores the deleted file and still leaves the
	// edit of a file the target does not change.
	server.head("main", server.commit(deltaNextFiles))
	require.NoError(t, c.update(true).Run(context.Background(), c.root))
	working = c.working()
	require.Equal(t, "readme\n", working["README.md"])
	require.Equal(t, "my logo\n", working["assets/logo.txt"])
}

func TestUpdate_DeltaRefusesToReplaceLocalChanges(t *testing.T) {
	recorder := recordSpans(t)
	server := newDeltaServer(t)
	c, base := newDeltaClone(t, server)
	next := server.commit(deltaNextFiles)
	server.head("main", next)

	// An edit of a file the delta modifies, an edit of one it removes and an
	// untracked file where it adds one.
	require.NoError(t, os.WriteFile(c.path("src/main.go"), []byte("package main // mine\n"), 0o644))
	require.NoError(t, os.WriteFile(c.path("src/util.go"), []byte("package main // kept\n"), 0o644))
	require.NoError(t, os.WriteFile(c.path("top.txt"), []byte("mine\n"), 0o644))
	before := c.working()

	err := c.update(false).Run(context.Background(), c.root)
	require.Error(t, err)
	require.ErrorContains(t, err, "src/main.go")
	require.ErrorContains(t, err, "top.txt")
	require.Equal(t, before, c.working(), "nothing is written")
	synced, err := c.lr.LoadSyncedTree()
	require.NoError(t, err)
	require.Equal(t, base, synced.CommitID, "the stored tree is still the base")
	local, err := c.lr.LoadCommit()
	require.NoError(t, err)
	require.Equal(t, base, local.CommitID)

	// Forced, the delta is still used: the edits in its way are replaced and
	// the edited file it removes is kept on disk, as in the full sync.
	require.NoError(t, c.update(true).Run(context.Background(), c.root))
	require.Equal(t, attrsDelta, syncTreeAttrs(t, recorder))
	working := c.working()
	require.Equal(t, deltaNextFiles["src/main.go"], working["src/main.go"])
	require.Equal(t, "top\n", working["top.txt"])
	require.Equal(t, "package main // kept\n", working["src/util.go"])
	synced, err = c.lr.LoadSyncedTree()
	require.NoError(t, err)
	require.Equal(t, next, synced.CommitID)
}

func TestUpdate_UntrackedFileEqualToTheIncomingOneIsNoConflict(t *testing.T) {
	server := newDeltaServer(t)
	c, _ := newDeltaClone(t, server)
	next := server.commit(deltaNextFiles)
	server.head("main", next)
	require.NoError(t, os.WriteFile(c.path("top.txt"), []byte("top\n"), 0o644))
	require.NoError(t, c.update(false).Run(context.Background(), c.root))
	c.requireAt(next)
}

func TestUpdate_ForcedDeltaAtTheSameCommitRestoresMissingFiles(t *testing.T) {
	recorder := recordSpans(t)
	server := newDeltaServer(t)
	c, base := newDeltaClone(t, server)
	require.NoError(t, os.Remove(c.path("src/util.go")))
	require.NoError(t, os.RemoveAll(c.path("docs")))

	require.NoError(t, c.update(true).Run(context.Background(), c.root))
	require.Equal(t, attrsDelta, syncTreeAttrs(t, recorder))
	require.True(t, server.calls[0].delta)
	c.requireAt(base)
}

// Every way a delta cannot be used ends in the full manifest, silently.
func TestUpdate_FallsBackToTheFullManifest(t *testing.T) {
	cases := map[string]struct {
		arrange func(c *deltaClone)
		reason  string
		// calls is how many manifest requests the update makes, deltas how
		// many of them named a base.
		calls, deltas int
	}{
		"the server declines": {
			arrange: func(c *deltaClone) { c.server.decline = true },
			reason:  "declined", calls: 1,
		},
		"the view changed": {
			arrange: func(c *deltaClone) { c.server.view = []byte("another-view") },
			reason:  "declined", calls: 1,
		},
		"the root hash does not match": {
			arrange: func(c *deltaClone) { c.server.wrongRoot = true },
			reason:  "mismatch", calls: 2, deltas: 1,
		},
		"the delta keeps a file the tree does not hold": {
			arrange: func(c *deltaClone) { c.server.keepUnheld = true },
			reason:  "base", calls: 2, deltas: 1,
		},
		"the delta leads to another commit": {
			arrange: func(c *deltaClone) { c.server.wrongCommit = true },
			reason:  "commit", calls: 2, deltas: 1,
		},
		"no record": {
			arrange: func(c *deltaClone) { require.NoError(c.t, c.lr.ClearSyncedTree()) },
			reason:  "record", calls: 1,
		},
		"the sparse set changed": {
			arrange: func(c *deltaClone) {
				require.NoError(c.t, c.lr.SaveConfig(domain.Config{Url: deltaURL, Branch: "main", Sparse: []string{"src", "docs", "assets", "bin", "README.md", "top.txt"}}))
			},
			reason: "sparse", calls: 1,
		},
		"the stored tree has no view token": {
			arrange: func(c *deltaClone) {
				synced, err := c.lr.LoadSyncedTree()
				require.NoError(c.t, err)
				require.NoError(c.t, c.lr.SaveSyncedTree(c.server.manifest(synced.CommitID, nil), synced.CommitID, nil))
			},
			reason: "view", calls: 1,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			recorder := recordSpans(t)
			server := newDeltaServer(t)
			c, _ := newDeltaClone(t, server)
			next := server.commit(deltaNextFiles)
			server.head("main", next)
			tc.arrange(c)

			require.NoError(t, c.update(false).Run(context.Background(), c.root))
			require.Equal(t, attrsFull(tc.reason), syncTreeAttrs(t, recorder))
			require.Len(t, server.calls, tc.calls)
			deltas := 0
			for _, call := range server.calls {
				require.Equal(t, next, call.target.CommitID)
				if call.delta {
					deltas++
				}
			}
			require.Equal(t, tc.deltas, deltas)

			sparse := []string(nil)
			if tc.reason == "sparse" {
				sparse = []string{"README.md", "assets", "bin", "docs", "src", "top.txt"}
			}
			// The full path leaves what a fresh sync leaves, token included.
			want := server.manifest(next, sparse)
			snap, err := c.lr.Snapshot()
			require.NoError(t, err)
			require.Equal(t, want.Hash.String(), snap.TreeHash)
			synced, err := c.lr.LoadSyncedTree()
			require.NoError(t, err)
			require.Equal(t, next, synced.CommitID)
			require.Equal(t, server.view, synced.View)
			require.Equal(t, deltaNextFiles, c.working())
		})
	}
}

// failingDeltaRepo is a repository whose delta plan or apply fails.
type failingDeltaRepo struct {
	*localrepo.LocalRepo
	planErr, applyErr error
	applies           int
}

func (r *failingDeltaRepo) PlanTreeDelta(in domain.TreeDeltaApply) (*domain.TreeDeltaChanges, error) {
	if r.planErr != nil {
		return nil, r.planErr
	}
	return r.LocalRepo.PlanTreeDelta(in)
}

func (r *failingDeltaRepo) ApplyTreeDelta(in domain.TreeDeltaApply, planned *domain.TreeDeltaChanges) (*domain.TreeDeltaChanges, error) {
	r.applies++
	if r.applyErr != nil {
		return nil, r.applyErr
	}
	return r.LocalRepo.ApplyTreeDelta(in, planned)
}

func TestUpdate_FallsBackWhenTheRowsCannotBeWritten(t *testing.T) {
	for name, repo := range map[string]*failingDeltaRepo{
		"plan":              {planErr: errors.New("disk on fire")},
		"apply":             {applyErr: errors.New("disk on fire")},
		"apply, wrong base": {applyErr: domain.ErrTreeDeltaBase},
		"apply, mismatch":   {applyErr: &domain.TreeDeltaMismatchError{}},
	} {
		t.Run(name, func(t *testing.T) {
			recorder := recordSpans(t)
			server := newDeltaServer(t)
			c, _ := newDeltaClone(t, server)
			next := server.commit(deltaNextFiles)
			server.head("main", next)
			repo.LocalRepo = c.lr

			require.NoError(t, newTestUpdate(t, repo, server).Run(context.Background(), c.root))
			reason := "error"
			switch {
			case errors.Is(repo.applyErr, domain.ErrTreeDeltaBase):
				reason = "base"
			case repo.applyErr != nil && name == "apply, mismatch":
				reason = "mismatch"
			}
			require.Equal(t, attrsFull(reason), syncTreeAttrs(t, recorder))
			require.Len(t, server.calls, 2)
			require.True(t, server.calls[0].delta)
			require.False(t, server.calls[1].delta)
			c.requireAt(next)
		})
	}
}

func TestUpdate_DeltaErrorsThatAreNotFallbacks(t *testing.T) {
	t.Run("the request fails", func(t *testing.T) {
		server := newDeltaServer(t)
		c, base := newDeltaClone(t, server)
		server.head("main", server.commit(deltaNextFiles))
		server.deltaErr = errors.New("unavailable")
		require.ErrorContains(t, c.update(false).Run(context.Background(), c.root), "unavailable")
		c.requireAt(base)
	})
	t.Run("the full manifest after a fallback fails", func(t *testing.T) {
		server := newDeltaServer(t)
		c, _ := newDeltaClone(t, server)
		server.head("main", server.commit(deltaNextFiles))
		server.wrongRoot, server.fullErr = true, errors.New("gone")
		require.ErrorContains(t, c.update(false).Run(context.Background(), c.root), "gone")
	})
	t.Run("an unsafe name", func(t *testing.T) {
		server := newDeltaServer(t)
		c, base := newDeltaClone(t, server)
		server.head("main", server.commit(deltaNextFiles))
		server.unsafeName = ".nipa"
		err := c.update(false).Run(context.Background(), c.root)
		require.ErrorContains(t, err, "unsafe path")
		c.requireAt(base)
	})
	t.Run("the working copy cannot be read for a forced restore", func(t *testing.T) {
		server := newDeltaServer(t)
		c, _ := newDeltaClone(t, server)
		server.head("main", server.commit(deltaNextFiles))
		broken := &snapshotFailingRepo{LocalRepo: c.lr}
		err := newTestUpdate(t, broken, server).WithForce(true).Run(context.Background(), c.root)
		require.ErrorContains(t, err, "snapshot unreadable")
	})
}

type snapshotFailingRepo struct{ *localrepo.LocalRepo }

func (r *snapshotFailingRepo) Snapshot() (*domain.Snapshot, error) {
	return nil, errors.New("snapshot unreadable")
}

func TestUpdate_SparseCheckoutUsesDeltas(t *testing.T) {
	recorder := recordSpans(t)
	server := newDeltaServer(t)
	c, base := newDeltaClone(t, server, "src", "docs")
	c.requireAt(base, "docs", "src")
	next := server.commit(deltaNextFiles)
	server.head("main", next)

	require.NoError(t, c.update(false).Run(context.Background(), c.root))
	require.Equal(t, attrsDelta, syncTreeAttrs(t, recorder))
	require.Len(t, server.calls, 1)
	require.Equal(t, []string{"src", "docs"}, server.calls[0].paths)
	require.Equal(t, base, server.calls[0].base.CommitID)
	c.requireAt(next, "docs", "src")
	require.NotContains(t, c.working(), "top.txt", "outside the sparse set")
}

func TestUpdate_EmptyBranchAsksByBranch(t *testing.T) {
	recorder := recordSpans(t)
	server := newDeltaServer(t)
	c := &deltaClone{t: t, root: t.TempDir(), lr: localrepo.NewLocalRepo(), server: server}
	t.Cleanup(func() { _ = c.lr.Close() })
	require.NoError(t, c.lr.Init(c.root))
	require.NoError(t, c.lr.SaveConfig(domain.Config{Url: deltaURL, Branch: "main"}))

	require.NoError(t, c.update(false).Run(context.Background(), c.root))
	require.Equal(t, attrsFull("empty"), syncTreeAttrs(t, recorder))
	require.Equal(t, []deltaCall{{target: treedelta.Target{Branch: "main"}}}, server.calls)
	synced, err := c.lr.LoadSyncedTree()
	require.NoError(t, err)
	require.Nil(t, synced)
}

func TestSwitch_AppliesADelta(t *testing.T) {
	recorder := recordSpans(t)
	server := newDeltaServer(t)
	c, base := newDeltaClone(t, server)
	feature := server.commit(deltaNextFiles)
	server.head("feature", feature)

	require.NoError(t, c.update(false).Switch(context.Background(), c.root, "feature"))
	require.Equal(t, attrsDelta, syncTreeAttrs(t, recorder))
	require.Len(t, server.calls, 1)
	require.Equal(t, treedelta.Target{CommitID: feature}, server.calls[0].target)
	require.Equal(t, base, server.calls[0].base.CommitID)
	c.requireAt(feature)
	cfg, err := c.lr.LoadConfig()
	require.NoError(t, err)
	require.Equal(t, "feature", cfg.Branch)

	// And back, forced.
	require.NoError(t, c.update(true).Switch(context.Background(), c.root, "main"))
	require.Equal(t, attrsDelta, syncTreeAttrs(t, recorder))
	c.requireAt(base)

	// A mismatching delta falls back on a switch too.
	server.wrongRoot = true
	require.NoError(t, c.update(false).Switch(context.Background(), c.root, "feature"))
	require.Equal(t, attrsFull("mismatch"), syncTreeAttrs(t, recorder))
	c.requireAt(feature)
}

func TestSwitch_RefusesLocalChangesInTheWayOfADelta(t *testing.T) {
	server := newDeltaServer(t)
	c, base := newDeltaClone(t, server)
	server.head("feature", server.commit(deltaNextFiles))
	require.NoError(t, os.WriteFile(c.path("src/main.go"), []byte("package main // mine\n"), 0o644))

	err := c.update(false).Switch(context.Background(), c.root, "feature")
	require.ErrorContains(t, err, "src/main.go")
	cfg, cfgErr := c.lr.LoadConfig()
	require.NoError(t, cfgErr)
	require.Equal(t, "main", cfg.Branch)
	synced, err := c.lr.LoadSyncedTree()
	require.NoError(t, err)
	require.Equal(t, base, synced.CommitID)
}

func TestSwitchTag_AndDetachedUpdate_ApplyDeltas(t *testing.T) {
	recorder := recordSpans(t)
	server := newDeltaServer(t)
	c, base := newDeltaClone(t, server)
	tagged := server.commit(deltaNextFiles)
	server.tags["v1"] = tagged

	require.NoError(t, c.update(false).SwitchTag(context.Background(), c.root, "v1"))
	require.Equal(t, attrsDelta, syncTreeAttrs(t, recorder))
	require.Equal(t, treedelta.Target{CommitID: tagged}, server.calls[0].target)
	require.Equal(t, base, server.calls[0].base.CommitID)
	c.requireAt(tagged)

	// Detached and synced: nothing to do.
	result, err := c.update(false).RunResult(context.Background(), c.root)
	require.NoError(t, err)
	require.True(t, result.UpToDate)

	// A detached update whose stored tree lags behind the checked-out commit
	// (an interrupted switch) moves by a delta from what is stored.
	other := server.commit(deltaBaseFiles)
	require.NoError(t, c.lr.SaveCommit(other, ""))
	server.calls = nil
	result, err = c.update(false).RunResult(context.Background(), c.root)
	require.NoError(t, err)
	require.False(t, result.UpToDate)
	require.Equal(t, attrsDelta, syncTreeAttrs(t, recorder))
	require.Equal(t, treedelta.Target{CommitID: other}, server.calls[0].target)
	require.Equal(t, tagged, server.calls[0].base.CommitID)
	c.requireAt(other)

	// Forced, detached, same commit: a delta with nothing in it.
	server.calls = nil
	require.NoError(t, os.Remove(c.path("README.md")))
	require.NoError(t, c.update(true).Run(context.Background(), c.root))
	require.Equal(t, attrsDelta, syncTreeAttrs(t, recorder))
	c.requireAt(other)
}

// A delta sync prunes the objects its rows orphaned, and nothing else.
func TestUpdate_DeltaPrunesOnlyWhatItOrphaned(t *testing.T) {
	server := newDeltaServer(t)
	c, base := newDeltaClone(t, server)
	chunksOf := func(commit, path string) []serverDomain.Hash {
		var out []serverDomain.Hash
		walkTreeFiles(server.manifest(commit, nil), "", func(p string, f *serverDomain.File) {
			if p == path {
				for _, ch := range f.Chunks {
					out = append(out, ch.Hash)
				}
			}
		})
		require.NotEmpty(t, out)
		return out
	}
	cached := func(hashes []serverDomain.Hash) bool {
		missing, err := c.lr.MissingChunks(hashes)
		require.NoError(t, err)
		return len(missing) == 0
	}
	oldMain, oldUtil, kept := chunksOf(base, "src/main.go"), chunksOf(base, "src/util.go"), chunksOf(base, "README.md")
	require.True(t, cached(oldMain) && cached(oldUtil) && cached(kept), "text content is cached by the first sync")
	// A leftover no row references: the targeted prune leaves it to the next sweep.
	leftover := serverDomain.Hash{0x77}
	require.NoError(t, c.lr.StoreChunks([]*serverDomain.ChunkData{{Hash: leftover, Data: []byte("x")}}))

	next := server.commit(deltaNextFiles)
	server.head("main", next)
	require.NoError(t, c.update(false).Run(context.Background(), c.root))

	require.False(t, cached(oldMain), "the replaced version is pruned")
	require.False(t, cached(oldUtil), "the removed file's content is pruned")
	require.True(t, cached(kept))
	require.True(t, cached(chunksOf(next, "src/main.go")))
	require.True(t, cached(chunksOf(next, "top.txt")))
	require.True(t, cached([]serverDomain.Hash{leftover}))
}

func TestFetchManifest(t *testing.T) {
	ctx := context.Background()
	legacyTree := &serverDomain.TreeNode{Name: "legacy"}
	legacy := func() (*serverDomain.TreeNode, error) { return legacyTree, nil }

	// A client without the call answers through legacy, without a token.
	tree, view, err := fetchManifest(ctx, &stubUpdateClient{}, "o", "p", treedelta.Target{Branch: "main"}, nil, legacy)
	require.NoError(t, err)
	require.Same(t, legacyTree, tree)
	require.Nil(t, view)

	answer := &scriptedDeltaClient{manifest: &treedelta.Manifest{Full: &serverDomain.TreeNode{Name: "full"}, View: []byte("v")}}
	tree, view, err = fetchManifest(ctx, answer, "o", "p", treedelta.Target{CommitID: "c"}, []string{"a"}, legacy)
	require.NoError(t, err)
	require.Equal(t, "full", tree.Name)
	require.Equal(t, []byte("v"), view)
	require.Equal(t, treedelta.Target{CommitID: "c"}, answer.target)
	require.Equal(t, treedelta.Base{}, answer.base)

	answer.manifest = nil
	tree, view, err = fetchManifest(ctx, answer, "o", "p", treedelta.Target{}, nil, legacy)
	require.NoError(t, err)
	require.Nil(t, tree)
	require.Nil(t, view)

	answer.manifest = &treedelta.Manifest{Delta: &treedelta.Delta{Root: &treedelta.Dir{}}}
	_, _, err = fetchManifest(ctx, answer, "o", "p", treedelta.Target{}, nil, legacy)
	require.ErrorContains(t, err, "nobody asked for")

	answer.err = errors.New("offline")
	_, _, err = fetchManifest(ctx, answer, "o", "p", treedelta.Target{}, nil, legacy)
	require.ErrorContains(t, err, "offline")
}

type scriptedDeltaClient struct {
	manifest *treedelta.Manifest
	err      error
	target   treedelta.Target
	base     treedelta.Base
	calls    int
}

func (c *scriptedDeltaClient) GetTreeManifestDelta(_ context.Context, _, _ string, target treedelta.Target, _ []string, base treedelta.Base) (*treedelta.Manifest, error) {
	c.calls++
	c.target, c.base = target, base
	return c.manifest, c.err
}

// viewStubRepo is the stub repository with the view-keeping save.
type viewStubRepo struct {
	*stubLocalRepo
	view []byte
}

func (r *viewStubRepo) SaveSyncedTreeView(root *serverDomain.TreeNode, commitID string, sparse []string, view []byte) error {
	r.view = view
	return r.SaveSyncedTree(root, commitID, sparse)
}

func TestSaveSyncedTree_KeepsTheViewWhenTheRepositoryCan(t *testing.T) {
	tree := &serverDomain.TreeNode{Name: "root"}
	plain := &stubLocalRepo{}
	require.NoError(t, saveSyncedTree(plain, tree, "c", nil, []byte("v")))
	require.Equal(t, 1, plain.treeSaves)

	keeper := &viewStubRepo{stubLocalRepo: &stubLocalRepo{}}
	require.NoError(t, saveSyncedTree(keeper, tree, "c", nil, []byte("v")))
	require.Equal(t, []byte("v"), keeper.view)
	// No token: the plain save, which records none.
	keeper.view = []byte("stale")
	require.NoError(t, saveSyncedTree(keeper, tree, "c", nil, nil))
	require.Equal(t, []byte("stale"), keeper.view)
	require.Equal(t, 2, keeper.treeSaves)
}

// A repository or client without the delta calls keeps the plain manifests.
func TestUpdate_DeltaBaseNeedsBothSides(t *testing.T) {
	server := newDeltaServer(t)
	goal := syncGoal{commitID: "c1"}

	plainRepo := newTestUpdate(t, &stubLocalRepo{}, server)
	_, reason := plainRepo.deltaBase(goal, nil)
	require.Equal(t, "repo", reason)

	plainClient := newTestUpdate(t, localrepo.NewLocalRepo(), &stubUpdateClient{})
	_, reason = plainClient.deltaBase(goal, nil)
	require.Equal(t, "client", reason)

	// A record that cannot be read is no base, not an error.
	lr := localrepo.NewLocalRepo()
	_, reason = newTestUpdate(t, lr, server).deltaBase(goal, nil)
	require.Equal(t, "record", reason)

	// An unreadable stored root hash only costs the proof.
	c, base := newDeltaClone(t, server)
	require.NoError(t, c.lr.SaveSyncedTreeView(&serverDomain.TreeNode{Name: "root"}, base, nil, server.view))
	got, reason := c.update(false).deltaBase(syncGoal{commitID: "next"}, nil)
	require.Empty(t, reason)
	require.Equal(t, base, got.CommitID)
	require.NotNil(t, got.TreeHash)
	require.NoError(t, c.lr.SaveSyncedTreeView(nil, base, nil, server.view))
	got, reason = c.update(false).deltaBase(syncGoal{commitID: "next"}, nil)
	require.Empty(t, reason)
	require.Nil(t, got.TreeHash)
}

// Clone and the post-push refetch record the token of the manifest they save.
func TestClone_RecordsTheViewToken(t *testing.T) {
	server := newDeltaServer(t)
	head := server.commit(deltaBaseFiles)
	server.head("main", head)
	client := &deltaRepoClient{stubRepoInterface: &stubRepoInterface{}, deltaServer: server}
	id, err := snow.ParseBase36(head)
	require.NoError(t, err)
	client.branch = &serverDomain.Branch{Name: "main", CommitID: &id}

	lr := localrepo.NewLocalRepo()
	t.Cleanup(func() { _ = lr.Close() })
	auth := NewAuth(nil, &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: signTestToken(t, "secret")}}, nil)
	target := filepath.Join(t.TempDir(), "clone")
	require.NoError(t, NewRepo(auth, client, lr).Clone(context.Background(), deltaURL, "example.com", "org", "project", "main", nil, target))

	require.Equal(t, []deltaCall{{target: treedelta.Target{CommitID: head}}}, server.calls)
	synced, err := lr.LoadSyncedTree()
	require.NoError(t, err)
	require.Equal(t, &domain.SyncedTree{CommitID: head, Sparse: []string{}, View: server.view}, synced)
	data, err := os.ReadFile(filepath.Join(target, "src", "main.go"))
	require.NoError(t, err)
	require.Equal(t, deltaBaseFiles["src/main.go"], string(data))
}

// deltaRepoClient is the clone stub answering manifests and chunks from a
// deltaServer.
type deltaRepoClient struct {
	*stubRepoInterface
	*deltaServer
}

func (c *deltaRepoClient) GetBranchByName(ctx context.Context, org, project, name string) (*serverDomain.Branch, error) {
	return c.stubRepoInterface.GetBranchByName(ctx, org, project, name)
}

func (c *deltaRepoClient) GetTreeNodeManifest(ctx context.Context, org, project, branch string, paths []string) (*serverDomain.TreeNode, error) {
	return c.deltaServer.GetTreeNodeManifest(ctx, org, project, branch, paths)
}

func (c *deltaRepoClient) DownloadChunks(ctx context.Context, scope domain.ChunkScope, hashes []serverDomain.Hash, onChunk func(h serverDomain.Hash, data []byte) error) error {
	return c.deltaServer.DownloadChunks(ctx, scope, hashes, onChunk)
}

// deltaPushClient is the push stub with the view-carrying manifest call.
type deltaPushClient struct {
	*stubPushClient
	scriptedDeltaClient
}

func TestPush_RefetchRecordsTheViewToken(t *testing.T) {
	root, local, stub := treeSyncFixture(t, []string{"src"})
	keeper := &viewStubRepo{stubLocalRepo: local}
	client := &deltaPushClient{stubPushClient: stub}
	client.scriptedDeltaClient.manifest = &treedelta.Manifest{Full: &serverDomain.TreeNode{Name: "root", Hash: treeSyncHash}, View: []byte("pushed-view")}
	auth := NewAuth(nil, &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: signTestToken(t, "secret")}}, nil)

	require.NoError(t, NewPush(auth, client, keeper).Run(context.Background(), root, "msg"))
	require.Equal(t, 1, client.scriptedDeltaClient.calls)
	require.Equal(t, treedelta.Target{CommitID: treeSyncCommit.Base36()}, client.scriptedDeltaClient.target, "the pushed commit, by id")
	require.Zero(t, stub.manifestCalls, "the plain branch manifest is not used")
	require.Equal(t, []byte("pushed-view"), keeper.view)
	require.Equal(t, treeSyncCommit.Base36(), local.syncedTree.CommitID)
}

func TestNarrowChunkScope(t *testing.T) {
	scope := domain.ChunkScope{Org: "o", Project: "p", CommitIDs: []string{"c"}, Paths: []string{"src"}}
	files := func(paths ...string) []materializedFile {
		out := make([]materializedFile, 0, len(paths))
		for _, p := range paths {
			out = append(out, materializedFile{Path: p})
		}
		return out
	}

	narrowed := narrowChunkScope(scope, files("src/a.go", "src/deep/b.go"))
	require.Equal(t, []string{"src/a.go", "src/deep/b.go"}, narrowed.Paths)
	require.Equal(t, scope.CommitIDs, narrowed.CommitIDs)
	require.Equal(t, []string{"src"}, scope.Paths, "the caller's scope is not changed")

	// Nothing to write, too much to name, or a path the server would read
	// as another prefix: the scope stays.
	require.Equal(t, scope, narrowChunkScope(scope, nil))
	many := make([]string, maxNarrowedScopePaths+1)
	for i := range many {
		many[i] = "src/f" + string(rune('a'+i%26)) + strings.Repeat("x", i/26)
	}
	require.Equal(t, scope, narrowChunkScope(scope, files(many...)))
	require.Len(t, narrowChunkScope(scope, files(many[1:]...)).Paths, maxNarrowedScopePaths)
	for _, odd := range []string{" lead.txt", "trail.txt ", `back\slash.txt`, "a/../b.txt", "a//b.txt", "./a.txt"} {
		require.Equal(t, scope, narrowChunkScope(scope, files("src/a.go", odd)), odd)
	}
}
