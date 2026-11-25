package usecase

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/provenance"
)

// fullLoadAPI is what a local repository offered before it could read by
// path: fullLoadRepo exposes nothing else, so every usecase run through it
// takes the whole-snapshot path, the reference the scoped reads must match.
type fullLoadAPI interface {
	WorkingCopyRepo
	pushLocalRepo
	cacheLocalRepo
	shelfStateLoader
	sharedLocker
	exclusiveLocker
}

type fullLoadRepo struct{ fullLoadAPI }

func TestFullLoadRepo_HidesTheScopedReads(t *testing.T) {
	var scoped any = localrepo.NewLocalRepo()
	_, ok := scoped.(scopedSnapshotRepo)
	require.True(t, ok)
	_, ok = scoped.(targetedPruneRepo)
	require.True(t, ok)
	_, ok = scoped.(statEntryGetter)
	require.True(t, ok)

	var full any = fullLoadRepo{localrepo.NewLocalRepo()}
	_, ok = full.(scopedSnapshotRepo)
	require.False(t, ok)
	_, ok = full.(targetedPruneRepo)
	require.False(t, ok)
	_, ok = full.(statEntryGetter)
	require.False(t, ok)
}

// smallRepo is a real local repository holding a full checkout of the given
// files as a clone leaves it: nested tree rows, the synced-tree record, a
// trusted fingerprint per file, the working files, and the cached objects of
// the files the pristine store keeps (text, and raw content under a pin).
type smallRepo struct {
	root   string
	lr     *localrepo.LocalRepo
	chunks map[string][]serverDomain.Hash
}

const smallRepoCommit = "smallbase"

func newSmallRepo(t *testing.T, files map[string]string, pins ...string) *smallRepo {
	t.Helper()
	fx := &smallRepo{root: t.TempDir(), lr: localrepo.NewLocalRepo(), chunks: map[string][]serverDomain.Hash{}}
	t.Cleanup(func() { _ = fx.lr.Close() })
	require.NoError(t, fx.lr.Init(fx.root))
	require.NoError(t, fx.lr.SaveConfig(domain.Config{Url: "http://example.com/org/project", Branch: "main"}))
	require.NoError(t, fx.lr.PinPaths(pins))

	root := &serverDomain.TreeNode{Name: "root", Hash: serverDomain.Hash{0xf0}}
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
		node := &serverDomain.TreeNode{Name: name, Hash: serverDomain.Hash{0xf1, byte(len(dirs))}}
		dirs[dir] = node
		up := ensure(parent)
		up.TreeChildren = append(up.TreeChildren, node)
		return node
	}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	old := time.Now().Add(-time.Hour)
	stats := map[string]domain.StatEntry{}
	for _, p := range paths {
		content := files[p]
		fileHash, chunks, stored, encoding := testEncodedFile(t, p, content)
		dir, name := "", p
		if i := strings.LastIndex(p, "/"); i >= 0 {
			dir, name = p[:i], p[i+1:]
		}
		node := ensure(dir)
		node.FileChildren = append(node.FileChildren, &serverDomain.File{
			Name: name, Mode: 2, SizeBytes: int64(len(content)), IsBinary: chunker.IsBinary([]byte(content)),
			Encoding: encoding, Hash: fileHash, Chunks: chunks,
		})
		for _, c := range chunks {
			fx.chunks[p] = append(fx.chunks[p], c.Hash)
		}
		if encoding == chunker.EncodingZstd || pinsCover(pins, p) {
			for h, data := range stored {
				require.NoError(t, fx.lr.StoreChunks([]*serverDomain.ChunkData{{Hash: h, Data: data}}))
			}
		}
		fx.write(t, p, content)
		require.NoError(t, os.Chtimes(filepath.Join(fx.root, filepath.FromSlash(p)), old, old))
		entry, err := statEntryFor(fx.root, p, fileHash)
		require.NoError(t, err)
		stats[p] = entry
	}
	require.NoError(t, fx.lr.SaveSyncedTree(root, smallRepoCommit, nil))
	require.NoError(t, fx.lr.SaveCommit(smallRepoCommit, ""))
	require.NoError(t, fx.lr.SaveStatEntries(stats))
	return fx
}

func (fx *smallRepo) write(t *testing.T, path, content string) {
	t.Helper()
	writeRepoFile(t, fx.root, path, content)
}

func (fx *smallRepo) remove(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, os.RemoveAll(filepath.Join(fx.root, filepath.FromSlash(path))))
}

// objects lists the cached objects as hex hashes, sorted.
func (fx *smallRepo) objects(t *testing.T) []string {
	t.Helper()
	var out []string
	dir := filepath.Join(fx.root, ".nipa", "objects")
	require.NoError(t, filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		out = append(out, filepath.Base(filepath.Dir(p))+d.Name())
		return nil
	}))
	sort.Strings(out)
	return out
}

func (fx *smallRepo) cached(t *testing.T, hashes []serverDomain.Hash) bool {
	t.Helper()
	require.NotEmpty(t, hashes)
	missing, err := fx.lr.MissingChunks(hashes)
	require.NoError(t, err)
	require.True(t, len(missing) == 0 || len(missing) == len(hashes), "chunks of one file are cached together")
	return len(missing) == 0
}

func (fx *smallRepo) snapshotChunks(t *testing.T, path string) []serverDomain.Hash {
	t.Helper()
	rows, err := fx.lr.SnapshotFiles([]string{path})
	require.NoError(t, err)
	require.Contains(t, rows, path)
	return rows[path].Chunks
}

func (fx *smallRepo) staged(t *testing.T) []string {
	t.Helper()
	staged, err := fx.lr.ListStaged()
	require.NoError(t, err)
	return staged
}

// push stages targets and pushes them through repo, a view of fx.lr.
func (fx *smallRepo) push(t *testing.T, repo pushLocalRepo, targets ...string) *scalePushClient {
	t.Helper()
	wc, err := NewWorkingCopy(fx.lr, fx.root)
	require.NoError(t, err)
	require.NoError(t, wc.Add(context.Background(), targets, AddOptions{NoProvenance: true}))
	client := &scalePushClient{lr: fx.lr}
	require.NoError(t, newTestPush(t, repo, client).Run(context.Background(), fx.root, "msg"))
	return client
}

var smallChangeFiles = map[string]string{
	"a.txt":              "alpha\n",
	"src/main.go":        "package main\n",
	"src/util/helper.go": "package util\n",
	"src/util/data.bin":  "\x00\x01binary data\x02",
	"srcx/other.txt":     "other\n",
	"srcx/deep/more.txt": "more\n",
	"docs/readme.md":     "# readme\n",
	"docs/gone.md":       "going away\n",
	"build/tracked.o":    "tracked although ignored\n",
	"a%/p.txt":           "percent\n",
	"a_/q.txt":           "underscore\n",
	"ab/r.txt":           "sibling\n",
	"top.bin":            "\x00top binary\x01",
	"dup/one.txt":        "same content twice\n",
	"dup/two.txt":        "same content twice\n",
}

// smallChange edits the working copy: content, mode-only and binary changes,
// deletions of a file and of a directory, new files, ignored files.
func smallChange(t *testing.T, fx *smallRepo) {
	t.Helper()
	fx.write(t, ".nipaignore", "*.o\n*.log\nbuild/\nignored-dir/\n")
	fx.write(t, "src/main.go", "package main\n\nfunc main() {}\n")
	fx.write(t, "top.bin", "\x00top binary, edited\x01")
	fx.write(t, "dup/one.txt", "no longer the same\n")
	require.NoError(t, os.Chmod(filepath.Join(fx.root, "a.txt"), 0o755))
	fx.remove(t, "docs/gone.md")
	fx.remove(t, "srcx")
	fx.write(t, "src/new.txt", "new\n")
	fx.write(t, "new-dir/n.txt", "new dir\n")
	fx.write(t, "other/outside.txt", "outside\n")
	fx.write(t, "debug.log", "ignored file\n")
	fx.write(t, "build/out.o", "ignored in a tracked directory\n")
	fx.write(t, "ignored-dir/x.txt", "ignored directory\n")
	fx.write(t, "node_modules/pkg/index.js", "generated\n")
}

type addOutcome struct {
	err       string
	staged    []string
	report    ProvenanceReport
	generated []string
	records   []string
}

func addOutcomeOf(t *testing.T, fx *smallRepo, repo WorkingCopyRepo, targets []string, opts AddOptions) addOutcome {
	t.Helper()
	wc, err := NewWorkingCopy(repo, fx.root)
	require.NoError(t, err)
	rep, err := wc.AddWithReport(context.Background(), targets, opts)
	out := addOutcome{staged: fx.staged(t)}
	if err != nil {
		out.err = strings.ReplaceAll(err.Error(), fx.root, "<root>")
	}
	for _, g := range rep.Generated {
		out.generated = append(out.generated, g.Dir)
	}
	rep.Generated = nil
	out.report = rep
	if f, loadErr := LoadProvenance(fx.root); loadErr == nil && f != nil {
		out.records = f.Paths()
	}
	return out
}

// Every add reads only the rows its targets name, and stages exactly what
// the whole-snapshot path stages.
func TestAdd_ScopedReadsStageWhatTheFullLoadStages(t *testing.T) {
	cases := []struct {
		name       string
		targets    []string
		opts       AddOptions
		sparse     []string
		provenance bool
		// dirBecameFile replaces the tracked directory was-dir.log by an
		// ignored file of that name.
		dirBecameFile bool
		// open cases only compare the two paths with each other.
		open    bool
		want    []string
		wantErr string
	}{
		{name: "modified file", targets: []string{"src/main.go"}, want: []string{"src/main.go"}},
		{name: "mode-only change", targets: []string{"a.txt"}, want: []string{"a.txt"}},
		{name: "unchanged file", targets: []string{"src/util/helper.go"}},
		{name: "unchanged binary", targets: []string{"src/util/data.bin"}},
		{name: "modified binary", targets: []string{"top.bin"}, want: []string{"top.bin"}},
		{name: "untracked file", targets: []string{"src/new.txt"}, want: []string{"src/new.txt"}},
		{name: "several files", targets: []string{"src/main.go", "src/new.txt", "docs/gone.md", "docs/readme.md", "a.txt"},
			want: []string{"a.txt", "docs/gone.md", "src/main.go", "src/new.txt"}},
		{name: "deleted tracked file", targets: []string{"docs/gone.md"}, want: []string{"docs/gone.md"}},
		{name: "deleted tracked directory", targets: []string{"srcx"}, want: []string{"srcx/deep/more.txt", "srcx/other.txt"}},
		{name: "directory", targets: []string{"src"}, want: []string{"src/main.go", "src/new.txt"}},
		{name: "nested directory", targets: []string{"src/util"}},
		{name: "directory with a deletion", targets: []string{"docs"}, want: []string{"docs/gone.md"}},
		{name: "directory sharing a prefix", targets: []string{"ab"}},
		{name: "percent directory", targets: []string{"a%"}},
		{name: "underscore directory", targets: []string{"a_"}},
		{name: "missing path sharing a prefix", targets: []string{"sr"}, wantErr: `path "sr" does not exist`},
		{name: "missing path", targets: []string{"nope.txt"}, wantErr: `path "nope.txt" does not exist`},
		{name: "ignored file", targets: []string{"debug.log"}, wantErr: `path "debug.log" is ignored`},
		{name: "ignored file forced", targets: []string{"debug.log"}, opts: AddOptions{Force: true}, want: []string{"debug.log"}},
		{name: "ignored but tracked file", targets: []string{"build/tracked.o"}},
		{name: "ignored file beside a tracked one", targets: []string{"build/out.o"}, wantErr: `path "build/out.o" is ignored`},
		{name: "ignored directory holding a tracked file", targets: []string{"build"}},
		{name: "ignored directory", targets: []string{"ignored-dir"}, wantErr: `path "ignored-dir" is ignored`},
		{name: "ignored directory forced", targets: []string{"ignored-dir"}, opts: AddOptions{Force: true}, want: []string{"ignored-dir/x.txt"}},
		{name: "file in an ignored directory", targets: []string{"ignored-dir/x.txt"}, wantErr: "is ignored"},
		{name: "ignored file where a tracked directory was", targets: []string{"was-dir.log"}, dirBecameFile: true, want: []string{"was-dir.log"}},
		// Both paths fail alike: the deleted file's parent is a file now.
		{name: "root with a file where a tracked directory was", targets: []string{"."}, dirBecameFile: true, wantErr: "not a directory"},
		{name: "metadata path", targets: []string{".nipa/config"}, wantErr: "cannot operate on path inside"},
		{name: "outside the repository", targets: []string{"../x"}, wantErr: "outside the repository"},
		{name: "file outside the sparse set", targets: []string{"other/outside.txt"}, sparse: []string{"src", "docs"}, want: []string{"other/outside.txt"}},
		{name: "directory in a sparse checkout", targets: []string{"src", "other"}, sparse: []string{"src"}, want: []string{"other/outside.txt", "src/main.go", "src/new.txt"}},
		{name: "files then their directory then the root", targets: []string{"src/main.go", "src", "."}, open: true},
		{name: "root", targets: []string{"."}, open: true},
		{name: "root forced", targets: []string{""}, opts: AddOptions{Force: true}, open: true},
		{name: "error after a staged target", targets: []string{"src/main.go", "debug.log"}, wantErr: "is ignored"},
		{name: "provenance of a deleted file", targets: []string{"docs/gone.md", "src/new.txt"}, provenance: true,
			want: []string{provenance.FileName, "docs/gone.md", "src/new.txt"}},
		{name: "provenance over the root", targets: []string{"."}, provenance: true, open: true},
		{name: "no provenance", targets: []string{"docs"}, opts: AddOptions{NoProvenance: true}, provenance: true, want: []string{"docs/gone.md"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			build := func() *smallRepo {
				files := smallChangeFiles
				if tc.dirBecameFile {
					files = map[string]string{"was-dir.log/inner.txt": "inside\n"}
					for p, content := range smallChangeFiles {
						files[p] = content
					}
				}
				fx := newSmallRepo(t, files, "src/util")
				if tc.dirBecameFile {
					fx.remove(t, "was-dir.log")
					fx.write(t, "was-dir.log", "a file now\n")
				}
				if tc.sparse != nil {
					require.NoError(t, fx.lr.SaveConfig(domain.Config{Url: "http://example.com/org/project", Branch: "main", Sparse: tc.sparse}))
				}
				smallChange(t, fx)
				if tc.provenance {
					f := provenance.New()
					require.NoError(t, f.Set("docs/gone.md", &provenance.Record{Tool: "Manual", ContentHash: provenance.HashBytes([]byte("gone"))}))
					require.NoError(t, f.Set("docs/readme.md", &provenance.Record{Tool: "Manual", ContentHash: provenance.HashBytes([]byte("# readme\n"))}))
					require.NoError(t, f.Set("never/tracked.png", &provenance.Record{Tool: "Manual"}))
					require.NoError(t, SaveProvenance(fx.root, f))
				}
				return fx
			}
			reference := build()
			want := addOutcomeOf(t, reference, fullLoadRepo{reference.lr}, tc.targets, tc.opts)
			scoped := build()
			got := addOutcomeOf(t, scoped, scoped.lr, tc.targets, tc.opts)

			require.Equal(t, want, got)
			if tc.wantErr != "" {
				require.Contains(t, got.err, tc.wantErr)
				require.Empty(t, got.staged)
				return
			}
			require.Empty(t, got.err)
			if !tc.open {
				require.Equal(t, append([]string{}, tc.want...), append([]string{}, got.staged...))
			}
		})
	}
}

func TestAdd_RootStagesEveryChangeAndReportsGeneratedFolders(t *testing.T) {
	fx := newSmallRepo(t, smallChangeFiles)
	smallChange(t, fx)
	got := addOutcomeOf(t, fx, fx.lr, []string{"."}, AddOptions{})
	require.Empty(t, got.err)
	require.Equal(t, []string{
		".nipaignore", "a.txt", "docs/gone.md", "dup/one.txt", "new-dir/n.txt", "node_modules/pkg/index.js",
		"other/outside.txt", "src/main.go", "src/new.txt", "srcx/deep/more.txt", "srcx/other.txt", "top.bin",
	}, got.staged)
	require.Equal(t, []string{"node_modules"}, got.generated)
}

func TestRemove_ScopedReadsForgetWhatTheFullLoadForgets(t *testing.T) {
	run := func(view func(*smallRepo) WorkingCopyRepo) addOutcome {
		fx := newSmallRepo(t, smallChangeFiles)
		smallChange(t, fx)
		f := provenance.New()
		require.NoError(t, f.Set("src/new.txt", &provenance.Record{Tool: "Manual"}))
		require.NoError(t, f.Set("src/main.go", &provenance.Record{Tool: "Manual"}))
		require.NoError(t, SaveProvenance(fx.root, f))
		require.NoError(t, fx.lr.StageAddMany([]string{"src/new.txt", "src/main.go", "a.txt"}))
		wc, err := NewWorkingCopy(view(fx), fx.root)
		require.NoError(t, err)
		require.NoError(t, wc.Remove(context.Background(), []string{"src"}))
		loaded, err := LoadProvenance(fx.root)
		require.NoError(t, err)
		return addOutcome{staged: fx.staged(t), records: loaded.Paths()}
	}
	want := run(func(fx *smallRepo) WorkingCopyRepo { return fullLoadRepo{fx.lr} })
	got := run(func(fx *smallRepo) WorkingCopyRepo { return fx.lr })
	require.Equal(t, want, got)
	require.Equal(t, []string{"src/main.go"}, got.records, "only the record of the untracked file is dropped")
	require.Equal(t, []string{provenance.FileName, "a.txt"}, got.staged)
}

func TestRefreshStatEntries_ScopedReadsRefreshWhatTheFullLoadRefreshes(t *testing.T) {
	paths := []string{"src/main.go", "src/util/helper.go", "src/new.txt", "docs/gone.md", "top.bin", "src/main.go"}
	run := func(view func(*smallRepo) WorkingCopyRepo) map[string]serverDomain.Hash {
		fx := newSmallRepo(t, smallChangeFiles)
		before, err := fx.lr.LoadStatCache()
		require.NoError(t, err)
		smallChange(t, fx)
		wc, err := NewWorkingCopy(view(fx), fx.root)
		require.NoError(t, err)
		require.NoError(t, wc.RefreshStatEntries(paths))
		require.NoError(t, wc.RefreshStatEntries(nil))
		after, err := fx.lr.LoadStatCache()
		require.NoError(t, err)
		changed := map[string]serverDomain.Hash{}
		for p, entry := range after {
			if entry != before[p] {
				changed[p] = entry.Hash
			}
		}
		return changed
	}
	want := run(func(fx *smallRepo) WorkingCopyRepo { return fullLoadRepo{fx.lr} })
	got := run(func(fx *smallRepo) WorkingCopyRepo { return fx.lr })
	require.Equal(t, want, got)
	require.Len(t, got, 2, "the two modified tracked files are rehashed, nothing else")
	require.Contains(t, got, "src/main.go")
	require.Contains(t, got, "top.bin")
}

type pushOutcome struct {
	plan     *domain.Plan
	files    []serverDomain.PushFile
	removed  []string
	baseTree string
	base     string
	snapshot *domain.Snapshot
	synced   *domain.SyncedTree
	pins     []string
	objects  []string
	stat     map[string]serverDomain.Hash
}

func pushOutcomeOf(t *testing.T, fx *smallRepo, repo pushLocalRepo, targets []string) pushOutcome {
	t.Helper()
	wc, err := NewWorkingCopy(fx.lr, fx.root)
	require.NoError(t, err)
	require.NoError(t, wc.Add(context.Background(), targets, AddOptions{NoProvenance: true}))

	var out pushOutcome
	client := &scalePushClient{lr: fx.lr}
	push := newTestPush(t, repo, client)
	out.plan, err = push.Plan(context.Background(), fx.root)
	require.NoError(t, err)
	require.NoError(t, push.Run(context.Background(), fx.root, "msg"))
	for _, f := range client.files {
		out.files = append(out.files, *f)
	}
	out.removed, out.baseTree, out.base = client.removed, client.baseTree, client.base
	out.snapshot, err = fx.lr.Snapshot()
	require.NoError(t, err)
	out.synced, err = fx.lr.LoadSyncedTree()
	require.NoError(t, err)
	out.pins, err = fx.lr.ListPins()
	require.NoError(t, err)
	out.objects = fx.objects(t)
	stat, err := fx.lr.LoadStatCache()
	require.NoError(t, err)
	out.stat = map[string]serverDomain.Hash{}
	for p, entry := range stat {
		out.stat[p] = entry.Hash
	}
	require.Empty(t, fx.staged(t))
	return out
}

// A push that reads only the staged rows and prunes only the chunks it can
// have orphaned sends, stores and leaves cached exactly what the push that
// loads the whole snapshot and sweeps the whole cache does.
func TestPush_ScopedReadsPushWhatTheFullLoadPushes(t *testing.T) {
	cases := []struct {
		name    string
		targets []string
		pins    []string
	}{
		{name: "one modified text file", targets: []string{"src/main.go"}},
		{name: "mode-only change", targets: []string{"a.txt"}},
		{name: "one modified binary", targets: []string{"top.bin"}},
		{name: "modified binary under an exact pin", targets: []string{"top.bin"}, pins: []string{"top.bin"}},
		{name: "directory pin stays", targets: []string{"src"}, pins: []string{"src/util", "src/main.go"}},
		{name: "removed file", targets: []string{"docs/gone.md"}},
		{name: "removed directory", targets: []string{"srcx"}},
		{name: "new files", targets: []string{"src/new.txt", "new-dir"}},
		{name: "content shared with another file", targets: []string{"dup/one.txt"}},
		{name: "everything", targets: []string{"."}, pins: []string{"top.bin", "src/util"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			build := func() *smallRepo {
				fx := newSmallRepo(t, smallChangeFiles, tc.pins...)
				smallChange(t, fx)
				return fx
			}
			reference := build()
			want := pushOutcomeOf(t, reference, fullLoadRepo{reference.lr}, tc.targets)
			scoped := build()
			counting := &readCountingRepo{LocalRepo: scoped.lr}
			got := pushOutcomeOf(t, scoped, counting, tc.targets)

			require.Equal(t, want, got)
			require.NotEmpty(t, len(got.files)+len(got.removed))
			require.Zero(t, counting.snapshots, "the staged rows are read by path")
			require.Zero(t, counting.sweeps, "the cache is not listed")
		})
	}
}

func TestPush_PrunesExactlyTheChunksItOrphaned(t *testing.T) {
	fx := newSmallRepo(t, smallChangeFiles, "top.bin", "src/util")
	oldMain, oldTop, oldGone := fx.chunks["src/main.go"], fx.chunks["top.bin"], fx.chunks["docs/gone.md"]
	shared := fx.chunks["dup/one.txt"]
	require.Equal(t, shared, fx.chunks["dup/two.txt"])
	require.True(t, fx.cached(t, oldTop), "raw content under a pin is cached")
	smallChange(t, fx)

	counting := &readCountingRepo{LocalRepo: fx.lr}
	fx.push(t, counting, "src/main.go", "top.bin", "docs/gone.md", "dup/one.txt", "src/new.txt")

	require.False(t, fx.cached(t, oldMain), "the previous version of a pushed text file is orphaned")
	require.False(t, fx.cached(t, oldGone), "the content of a removed file is orphaned")
	require.False(t, fx.cached(t, oldTop), "the pin landed, so the raw content is no longer kept")
	require.False(t, fx.cached(t, fx.snapshotChunks(t, "top.bin")), "nor is what the push cached for the pinned path")
	require.True(t, fx.cached(t, shared), "another file with the same content still keeps the chunk")
	require.True(t, fx.cached(t, fx.snapshotChunks(t, "src/main.go")))
	require.True(t, fx.cached(t, fx.snapshotChunks(t, "src/new.txt")))
	require.True(t, fx.cached(t, fx.snapshotChunks(t, "dup/one.txt")))
	require.True(t, fx.cached(t, fx.chunks["src/util/data.bin"]), "raw content under a directory pin stays")
	require.True(t, fx.cached(t, fx.chunks["a.txt"]), "untouched files keep their content")
	require.Zero(t, counting.sweeps)
	require.Zero(t, counting.snapshots)
}

func TestPush_PruneKeepsShelvesAndLeftoversOfOtherOperations(t *testing.T) {
	fx := newSmallRepo(t, smallChangeFiles)
	smallChange(t, fx)
	stray := serverDomain.Hash{0xee, 0xee}
	require.NoError(t, fx.lr.StoreChunk(stray, []byte("left by something else")))
	shelfChunk := serverDomain.Hash{0x5e, 0x1f}
	writer, err := fx.lr.NewShelfWriter()
	require.NoError(t, err)
	require.NoError(t, writer.PutChunk(shelfChunk, []byte("shelved")))
	shelf := &domain.Shelf{Name: "wip", Branch: "main", CreatedAt: time.Now()}
	require.NoError(t, writer.Commit(shelf))
	shelves, err := fx.lr.ListShelves()
	require.NoError(t, err)
	require.Len(t, shelves, 1)

	old := fx.chunks["src/main.go"]
	fx.push(t, fx.lr, "src/main.go")

	require.False(t, fx.cached(t, old))
	require.True(t, fx.lr.HasShelfChunk(shelves[0].ID, shelfChunk), "shelves live outside the object cache")
	require.True(t, fx.cached(t, []serverDomain.Hash{stray}), "an object the push did not touch waits for the next full sweep")

	result, err := NewGC(fx.lr).Run(fx.root)
	require.NoError(t, err)
	require.Equal(t, 1, result.Objects, "the full sweep still collects it")
	require.True(t, fx.lr.HasShelfChunk(shelves[0].ID, shelfChunk))
}

func TestPush_StagedFileOfAFailedPushKeepsItsTrackedContent(t *testing.T) {
	fx := newSmallRepo(t, smallChangeFiles)
	smallChange(t, fx)
	// docs/readme.md stays staged from an earlier attempt and is pushed
	// unchanged beside the edit: its row and its cached content stay.
	require.NoError(t, fx.lr.StageAddMany([]string{"docs/readme.md"}))
	fx.push(t, fx.lr, "src/main.go")
	require.True(t, fx.cached(t, fx.chunks["docs/readme.md"]))
	require.Equal(t, fx.chunks["docs/readme.md"], fx.snapshotChunks(t, "docs/readme.md"))
}

func TestPruneChunks_SkipsLikeTheFullSweep(t *testing.T) {
	setup := func(t *testing.T) (*smallRepo, []serverDomain.Hash) {
		fx := newSmallRepo(t, smallChangeFiles)
		orphan := serverDomain.Hash{0xaa, 0xbb}
		require.NoError(t, fx.lr.StoreChunk(orphan, []byte("orphan")))
		return fx, []serverDomain.Hash{orphan}
	}
	requireSkipped := func(t *testing.T, fx *smallRepo, orphan []serverDomain.Hash, reason string) {
		result, handled, err := pruneChunks(fx.lr, orphan, nil)
		require.NoError(t, err)
		require.True(t, handled)
		require.True(t, result.Skipped)
		require.Equal(t, reason, result.SkipReason)
		require.True(t, fx.cached(t, orphan))
		full, err := pruneCache(fx.lr)
		require.NoError(t, err)
		require.Equal(t, result, full, "the full sweep skips for the same reason")
	}

	t.Run("pending merge", func(t *testing.T) {
		fx, orphan := setup(t)
		require.NoError(t, fx.lr.SaveMergeState(&domain.MergeState{SourceBranch: "feature"}))
		requireSkipped(t, fx, orphan, PruneSkipPending)
	})
	t.Run("pending revert", func(t *testing.T) {
		fx, orphan := setup(t)
		require.NoError(t, fx.lr.SaveRevertState(&domain.RevertState{CurrentTreeHash: "x"}))
		requireSkipped(t, fx, orphan, PruneSkipPending)
	})
	t.Run("pending shelf", func(t *testing.T) {
		fx, orphan := setup(t)
		require.NoError(t, fx.lr.SaveShelfState(&domain.ShelfState{ShelfID: "s1"}))
		requireSkipped(t, fx, orphan, PruneSkipPending)
	})
	t.Run("repository busy", func(t *testing.T) {
		fx, orphan := setup(t)
		release, err := fx.lr.LockShared()
		require.NoError(t, err)
		requireSkipped(t, fx, orphan, PruneSkipBusy)
		release()
		result, handled, err := pruneChunks(fx.lr, orphan, nil)
		require.NoError(t, err)
		require.True(t, handled)
		require.Equal(t, PruneResult{Objects: 1, Bytes: int64(len("orphan"))}, result)
		require.False(t, fx.cached(t, orphan))
	})
}

func TestPruneChunks_KeepsWhatTheFullSweepKeeps(t *testing.T) {
	fx := newSmallRepo(t, smallChangeFiles, "src/util")
	// Raw content outside a pin that sits in the cache anyway, as a diff or a
	// restore leaves it until its own prune.
	_, _, stored, _ := testEncodedFile(t, "top.bin", smallChangeFiles["top.bin"])
	for h, data := range stored {
		require.NoError(t, fx.lr.StoreChunk(h, data))
	}
	stray := serverDomain.Hash{0xcc}
	require.NoError(t, fx.lr.StoreChunk(stray, []byte("stray")))
	require.NoError(t, fx.lr.StageAddMany([]string{"a.txt"}))

	var candidates []serverDomain.Hash
	for _, hashes := range fx.chunks {
		candidates = append(candidates, hashes...)
	}
	absent := serverDomain.Hash{0xdd}
	candidates = append(candidates, absent, stray)

	// Reference: what the full sweep leaves in an identical repository.
	result, handled, err := pruneChunks(fx.lr, candidates, []string{"a.txt", "not/tracked.txt"})
	require.NoError(t, err)
	require.True(t, handled)
	require.Equal(t, len(fx.chunks["top.bin"])+1, result.Objects)
	after := fx.objects(t)
	full, err := pruneCache(fx.lr)
	require.NoError(t, err)
	require.Zero(t, full.Objects, "the full sweep finds nothing more to delete")
	require.Equal(t, after, fx.objects(t))
	require.True(t, fx.cached(t, fx.chunks["a.txt"]), "a staged tracked file keeps its content")
	require.True(t, fx.cached(t, fx.chunks["src/util/data.bin"]), "a pin keeps raw content")
	require.False(t, fx.cached(t, fx.chunks["top.bin"]))
	require.False(t, fx.cached(t, []serverDomain.Hash{stray}))

	// Nothing cached among the candidates: nothing to do, no lock needed.
	release, err := fx.lr.LockShared()
	require.NoError(t, err)
	defer release()
	result, handled, err = pruneChunks(fx.lr, []serverDomain.Hash{absent}, nil)
	require.NoError(t, err)
	require.True(t, handled)
	require.Equal(t, PruneResult{}, result)
	result, handled, err = pruneChunks(fx.lr, nil, nil)
	require.NoError(t, err)
	require.True(t, handled)
	require.Equal(t, PruneResult{}, result)
}

func TestPruneChunks_LeavesManyLookupsToTheFullSweep(t *testing.T) {
	fx := newSmallRepo(t, smallChangeFiles)
	var orphans []serverDomain.Hash
	for i := 0; i <= maxTargetedPruneChunks; i++ {
		h := serverDomain.Hash{0xab, byte(i)}
		require.NoError(t, fx.lr.StoreChunk(h, []byte("x")))
		orphans = append(orphans, h)
	}
	_, handled, err := pruneChunks(fx.lr, orphans, nil)
	require.NoError(t, err)
	require.False(t, handled)
	require.True(t, fx.cached(t, orphans), "nothing is deleted before the full sweep takes over")

	counting := &readCountingRepo{LocalRepo: fx.lr}
	pruneAfterPush(counting, &pushCacheEffect{applied: true, chunks: orphans}, nil)
	require.Equal(t, 1, counting.sweeps)
	require.False(t, fx.cached(t, orphans))

	// One fewer is looked up chunk by chunk.
	for _, h := range orphans[1:] {
		require.NoError(t, fx.lr.StoreChunk(h, []byte("x")))
	}
	counting = &readCountingRepo{LocalRepo: fx.lr}
	pruneAfterPush(counting, &pushCacheEffect{applied: true, chunks: orphans[1:]}, nil)
	require.Zero(t, counting.sweeps)
	require.Equal(t, maxTargetedPruneChunks, counting.chunkScans)
	require.False(t, fx.cached(t, orphans[1:]))
}

func TestPruneAfterPush_FullSweepWheneverTheEffectIsNotKnown(t *testing.T) {
	orphan := []serverDomain.Hash{{0xaa, 0xbb}}
	setup := func(t *testing.T) (*smallRepo, *readCountingRepo) {
		fx := newSmallRepo(t, smallChangeFiles)
		require.NoError(t, fx.lr.StoreChunk(orphan[0], []byte("orphan")))
		require.NoError(t, fx.lr.StoreChunk(serverDomain.Hash{0xcc}, []byte("stray")))
		return fx, &readCountingRepo{LocalRepo: fx.lr}
	}
	t.Run("failed push prunes nothing", func(t *testing.T) {
		fx, counting := setup(t)
		pruneAfterPush(counting, &pushCacheEffect{applied: true, chunks: orphan}, errors.New("rejected"))
		require.Zero(t, counting.sweeps)
		require.True(t, fx.cached(t, orphan))
	})
	t.Run("push that concluded a merge or revert", func(t *testing.T) {
		fx, counting := setup(t)
		pruneAfterPush(counting, nil, nil)
		require.Equal(t, 1, counting.sweeps)
		require.False(t, fx.cached(t, []serverDomain.Hash{{0xcc}}))
	})
	t.Run("tree saved from a fetched manifest", func(t *testing.T) {
		fx, counting := setup(t)
		effect := newPushCacheEffect(false, nil, []*serverDomain.PushFile{{Path: "a.txt", ChunkHashes: orphan}}, nil)
		require.Empty(t, effect.chunks)
		pruneAfterPush(counting, effect, nil)
		require.Equal(t, 1, counting.sweeps)
		require.False(t, fx.cached(t, orphan))
	})
	t.Run("repository without by-path reads", func(t *testing.T) {
		fx, _ := setup(t)
		pruneAfterPush(fullLoadRepo{fx.lr}, &pushCacheEffect{applied: true, chunks: orphan}, nil)
		require.False(t, fx.cached(t, []serverDomain.Hash{{0xcc}}), "the full sweep ran")
	})
	t.Run("known effect", func(t *testing.T) {
		fx, counting := setup(t)
		pruneAfterPush(counting, &pushCacheEffect{applied: true, chunks: orphan}, nil)
		require.Zero(t, counting.sweeps)
		require.False(t, fx.cached(t, orphan))
		require.True(t, fx.cached(t, []serverDomain.Hash{{0xcc}}))
	})
	t.Run("failed targeted prune is only logged", func(t *testing.T) {
		fx, counting := setup(t)
		counting.chunkScanErr = errors.New("scan failed")
		pruneAfterPush(counting, &pushCacheEffect{applied: true, chunks: orphan}, nil)
		require.Zero(t, counting.sweeps)
		require.True(t, fx.cached(t, orphan))
	})
}

func TestPush_ConcludingAMergeSweepsTheWholeCache(t *testing.T) {
	fx := newSmallRepo(t, smallChangeFiles)
	smallChange(t, fx)
	materialized := serverDomain.Hash{0x3e}
	require.NoError(t, fx.lr.StoreChunk(materialized, []byte("cached by the merge")))
	require.NoError(t, fx.lr.SaveMergeState(&domain.MergeState{
		SourceBranch: "feature", SourceCommitHash: "abc", TargetTreeHash: "target-tree", TargetCommitID: smallRepoCommit,
	}))
	counting := &readCountingRepo{LocalRepo: fx.lr}
	client := fx.push(t, counting, "src/main.go")
	require.Equal(t, "target-tree", client.baseTree)
	require.Equal(t, 1, counting.sweeps)
	require.False(t, fx.cached(t, []serverDomain.Hash{materialized}))
	state, err := fx.lr.LoadMergeState()
	require.NoError(t, err)
	require.Nil(t, state)
}

// readCountingRepo counts the reads whose cost follows the size of the
// repository, and the rows the by-path reads return.
type readCountingRepo struct {
	*localrepo.LocalRepo
	snapshots    int
	statLoads    int
	sweeps       int
	pathReads    int
	dirReads     int
	chunkScans   int
	rows         int
	chunkScanErr error
}

func (c *readCountingRepo) Snapshot() (*domain.Snapshot, error) {
	c.snapshots++
	return c.LocalRepo.Snapshot()
}

func (c *readCountingRepo) LoadStatCache() (map[string]domain.StatEntry, error) {
	c.statLoads++
	return c.LocalRepo.LoadStatCache()
}

func (c *readCountingRepo) PruneObjects(keep map[serverDomain.Hash]struct{}) (int, int64, error) {
	c.sweeps++
	return c.LocalRepo.PruneObjects(keep)
}

func (c *readCountingRepo) SnapshotFiles(paths []string) (map[string]domain.SnapshotFile, error) {
	c.pathReads++
	rows, err := c.LocalRepo.SnapshotFiles(paths)
	c.rows += len(rows)
	return rows, err
}

func (c *readCountingRepo) SnapshotFilesUnder(dir string) ([]domain.SnapshotFile, error) {
	c.dirReads++
	rows, err := c.LocalRepo.SnapshotFilesUnder(dir)
	c.rows += len(rows)
	return rows, err
}

func (c *readCountingRepo) StatEntries(paths []string) (map[string]domain.StatEntry, error) {
	c.pathReads++
	rows, err := c.LocalRepo.StatEntries(paths)
	c.rows += len(rows)
	return rows, err
}

func (c *readCountingRepo) FilesWithChunk(hash serverDomain.Hash) ([]domain.SnapshotFile, error) {
	c.chunkScans++
	if c.chunkScanErr != nil {
		return nil, c.chunkScanErr
	}
	rows, err := c.LocalRepo.FilesWithChunk(hash)
	c.rows += len(rows)
	return rows, err
}

// The reads of a one-file add and a one-file push do not depend on how many
// files the repository tracks: no whole-table load, and the same number of
// by-path reads and rows whatever the size.
func TestSmallChange_ReadsDoNotGrowWithTheRepository(t *testing.T) {
	type reads struct {
		pathReads, dirReads, chunkScans, rows int
	}
	measure := func(n int) (add, addDir, pushed reads) {
		fx := newScaleRepo(t, n, false)
		target := scalePath(n / 2)
		old := scaleHash(uint32(n/2), 1)
		require.NoError(t, fx.lr.StoreChunk(old, []byte("previous version")))
		fx.write(t, target, "an edit\n")
		sibling := filepath.Dir(target) + "/brand-new.txt"
		fx.write(t, sibling, "a new file\n")
		of := func(c *readCountingRepo) reads {
			require.Zero(t, c.snapshots, "no whole-snapshot load at %d files", n)
			require.Zero(t, c.statLoads, "no whole stat cache load at %d files", n)
			require.Zero(t, c.sweeps, "no cache listing at %d files", n)
			return reads{c.pathReads, c.dirReads, c.chunkScans, c.rows}
		}

		counting := &readCountingRepo{LocalRepo: fx.lr}
		wc, err := NewWorkingCopy(counting, fx.root)
		require.NoError(t, err)
		require.NoError(t, wc.Add(context.Background(), []string{target}))
		add = of(counting)

		// A directory costs its own files, whatever surrounds it.
		counting = &readCountingRepo{LocalRepo: fx.lr}
		wc, err = NewWorkingCopy(counting, fx.root)
		require.NoError(t, err)
		require.NoError(t, wc.Add(context.Background(), []string{filepath.Dir(target)}))
		addDir = of(counting)
		staged, err := fx.lr.ListStaged()
		require.NoError(t, err)
		require.Len(t, staged, scaleFilesPerDir+1, "files missing from disk are staged as deletions")
		require.NoError(t, fx.lr.StageRemove(staged))
		require.NoError(t, fx.lr.StageAddMany([]string{target, sibling}))

		counting = &readCountingRepo{LocalRepo: fx.lr}
		client := &scalePushClient{lr: fx.lr}
		push := newTestPush(t, counting, client)
		plan, err := push.Plan(context.Background(), fx.root)
		require.NoError(t, err)
		require.Len(t, plan.Changes, 2)
		require.NoError(t, push.Run(context.Background(), fx.root, "msg"))
		pushed = of(counting)
		missing, err := fx.lr.MissingChunks([]serverDomain.Hash{old})
		require.NoError(t, err)
		require.Len(t, missing, 1, "the previous version was pruned")
		return add, addDir, pushed
	}
	smallAdd, smallDir, smallPush := measure(2 * scaleFilesPerDir)
	bigAdd, bigDir, bigPush := measure(40 * scaleFilesPerDir)
	require.Equal(t, smallAdd, bigAdd)
	require.Equal(t, smallDir, bigDir)
	require.Equal(t, smallPush, bigPush)
	require.Equal(t, reads{pathReads: 2, rows: 2}, bigAdd, "one snapshot row and one fingerprint")
	require.Equal(t, 1, bigDir.dirReads)
	require.Equal(t, 1, bigPush.chunkScans, "one lookup for the one replaced chunk")
}
