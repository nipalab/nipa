package usecase

import (
	"context"
	"errors"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

// restoreServer is a server a restore can rebuild files from: it streams
// chunks and answers the pinned commit's manifest.
type restoreServer struct {
	*streamingFake
	tree          *serverDomain.TreeNode
	manifestErr   error
	manifestPaths [][]string
	manifestDirs  []string
	connectErr    error
}

func (s *restoreServer) GetCommitDirManifest(_ context.Context, _, _, _, dir string) (*serverDomain.TreeNode, error) {
	s.manifestDirs = append(s.manifestDirs, dir)
	return s.tree, s.manifestErr
}

func (s *restoreServer) Connect(context.Context, string) error { return s.connectErr }

func (s *restoreServer) GetCommitTreeManifest(_ context.Context, _, _, _ string, paths []string) (*serverDomain.TreeNode, error) {
	s.manifestPaths = append(s.manifestPaths, paths)
	return s.tree, s.manifestErr
}

func (s *restoreServer) served() int { return len(s.streamed) + len(s.buffered) }

// binaryFile builds a manifest file whose chunks are the ones the client's
// own chunker produces, so an edited working file shares most of them.
func binaryFile(t *testing.T, name string, content []byte, data map[serverDomain.Hash][]byte) *serverDomain.File {
	t.Helper()
	_, chunks, err := chunker.EncodeBytes(content, name, true, chunker.EncodingRaw)
	require.NoError(t, err)
	f := &serverDomain.File{Name: name, Mode: 2, Encoding: chunker.EncodingRaw, IsBinary: true, SizeBytes: int64(len(content))}
	hashes := make([]serverDomain.Hash, 0, len(chunks))
	for _, c := range chunks {
		data[c.Hash] = append([]byte(nil), c.Data...)
		hashes = append(hashes, c.Hash)
		f.Chunks = append(f.Chunks, serverDomain.Chunk{Hash: c.Hash, SizeBytes: c.SizeBytes})
	}
	f.Hash = chunker.FileHash(hashes)
	return f
}

func randomBytes(seed int64, n int) []byte {
	out := make([]byte, n)
	_, _ = rand.New(rand.NewSource(seed)).Read(out)
	return out
}

type directRestore struct {
	lr      *localrepo.LocalRepo
	root    string
	server  *restoreServer
	wc      *WorkingCopy
	content []byte
	file    *serverDomain.File
}

// newDirectRestore is a clone synced to a tree with one 3 MiB binary
// (big.bin, several chunks) and a text file, pinned to a commit, whose object
// cache holds no raw content.
func newDirectRestore(t *testing.T) *directRestore {
	t.Helper()
	lr, root := newDirectRepo(t)
	require.NoError(t, lr.SaveConfig(domain.Config{Url: "http://example.com/org/project", Branch: "main"}))
	data := map[serverDomain.Hash][]byte{}
	content := randomBytes(1, 3<<20)
	file := binaryFile(t, "big.bin", content, data)
	require.Greater(t, len(file.Chunks), 2, "the fixture needs several chunks")
	tree := &serverDomain.TreeNode{Name: "root", FileChildren: []*serverDomain.File{
		file,
		textManifestFile(t, "notes.txt", "notes\n", data),
	}}
	server := &restoreServer{streamingFake: newStreamingFake(data), tree: tree}
	require.NoError(t, syncTree(t, lr, root, server, tree))
	require.NoError(t, lr.SaveCommit("c1", ""))
	server.streamed, server.buffered = map[serverDomain.Hash]int{}, map[serverDomain.Hash]int{}
	wc, err := NewWorkingCopy(lr, root)
	require.NoError(t, err)
	wc.WithDownloader(nil, server)
	return &directRestore{lr: lr, root: root, server: server, wc: wc, content: content, file: file}
}

func (d *directRestore) edit(t *testing.T) {
	t.Helper()
	edited := append([]byte(nil), d.content...)
	copy(edited[len(edited)-16:], "local edit here!")
	path := filepath.Join(d.root, "big.bin")
	require.NoError(t, os.WriteFile(path, edited, 0o644))
}

func TestRestore_RebuildsEditedBinaryInPlace(t *testing.T) {
	d := newDirectRestore(t)
	d.edit(t)

	require.NoError(t, d.wc.Restore(context.Background(), []string{"big.bin"}))

	require.Equal(t, d.content, readRepoFile(t, d.root, "big.bin"))
	last := d.file.Chunks[len(d.file.Chunks)-1].Hash
	require.Equal(t, map[serverDomain.Hash]int{last: 1}, d.server.streamed, "only the chunk the edit destroyed is downloaded")
	require.Empty(t, d.server.buffered)
	require.Equal(t, []string{""}, d.server.manifestDirs, "a root file is read from the root's own manifest")
	require.Empty(t, d.server.manifestPaths)
	requireNoTempFiles(t, d.root)

	require.NoError(t, d.wc.Restore(context.Background(), []string{"big.bin"}))
	require.Len(t, d.server.manifestDirs, 1, "the restore recorded a fingerprint, so a repeat does nothing")
}

func TestRestore_RebuildsDeletedBinaryWithoutTheCache(t *testing.T) {
	d := newDirectRestore(t)
	before := objectCount(t, d.root)
	require.NoError(t, os.Remove(filepath.Join(d.root, "big.bin")))

	require.NoError(t, d.wc.Restore(context.Background(), []string{"big.bin"}))

	require.Equal(t, d.content, readRepoFile(t, d.root, "big.bin"))
	require.Len(t, d.server.streamed, len(d.file.Chunks))
	require.Equal(t, before, objectCount(t, d.root), "no chunk of the binary is written to the object cache")
}

func TestRestore_ModeChangeReusesTheWorkingFile(t *testing.T) {
	requireExecBits(t)
	d := newDirectRestore(t)
	require.NoError(t, os.Chmod(filepath.Join(d.root, "big.bin"), 0o755))

	require.NoError(t, d.wc.Restore(context.Background(), []string{"big.bin"}))

	require.Zero(t, d.server.served(), "content that is still in the working file is not downloaded")
	info, err := os.Stat(filepath.Join(d.root, "big.bin"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o644), info.Mode().Perm())
	require.Equal(t, d.content, readRepoFile(t, d.root, "big.bin"))
}

func TestRestore_FallsBackToTheCachePath(t *testing.T) {
	for name, breakServer := range map[string]func(d *directRestore){
		"manifest unavailable": func(d *directRestore) { d.server.manifestErr = errors.New("no manifest") },
		"manifest has other content": func(d *directRestore) {
			other := binaryFile(t, "big.bin", randomBytes(2, 1<<20), map[serverDomain.Hash][]byte{})
			d.server.tree = &serverDomain.TreeNode{Name: "root", FileChildren: []*serverDomain.File{other}}
		},
		"no pinned commit": func(d *directRestore) { require.NoError(t, d.lr.SaveCommit("", "")) },
	} {
		t.Run(name, func(t *testing.T) {
			d := newDirectRestore(t)
			breakServer(d)
			d.edit(t)

			require.NoError(t, d.wc.Restore(context.Background(), []string{"big.bin"}))
			require.Equal(t, d.content, readRepoFile(t, d.root, "big.bin"))
			require.Equal(t, 1, d.server.served(), "the cache path reuses the working file and downloads the rest")
		})
	}
}

func TestRestore_OfflineWithUncachedBinary(t *testing.T) {
	d := newDirectRestore(t)
	d.server.connectErr = errors.New("dial failed")
	d.edit(t)

	err := d.wc.Restore(context.Background(), []string{"big.bin"})
	require.ErrorContains(t, err, "dial failed", "the cache path reports why the server could not be used")
}

func TestRestore_DirectDownloadFailureLeavesTheFile(t *testing.T) {
	d := newDirectRestore(t)
	d.edit(t)
	edited := readRepoFile(t, d.root, "big.bin")
	last := d.file.Chunks[len(d.file.Chunks)-1].Hash
	d.server.data[last] = []byte("not the chunk")

	err := d.wc.Restore(context.Background(), []string{"big.bin"})
	require.ErrorContains(t, err, "restore big.bin")
	require.Equal(t, edited, readRepoFile(t, d.root, "big.bin"), "a failed restore changes nothing")
	requireNoTempFiles(t, d.root)
}

func TestRestore_ManyFilesAskForTheWholeCheckout(t *testing.T) {
	prev := maxRestoreManifestPaths
	maxRestoreManifestPaths = 0
	t.Cleanup(func() { maxRestoreManifestPaths = prev })
	d := newDirectRestore(t)
	d.edit(t)

	require.NoError(t, d.wc.Restore(context.Background(), []string{"big.bin"}))
	require.Equal(t, [][]string{nil}, d.server.manifestPaths)
	require.Equal(t, d.content, readRepoFile(t, d.root, "big.bin"))
}

// treeOnlyServer is a restoreServer without GetCommitDirManifest.
type treeOnlyServer struct {
	s *restoreServer
}

func (o treeOnlyServer) Connect(ctx context.Context, host string) error {
	return o.s.Connect(ctx, host)
}

func (o treeOnlyServer) GetCommitTreeManifest(ctx context.Context, org, project, commitID string, paths []string) (*serverDomain.TreeNode, error) {
	return o.s.GetCommitTreeManifest(ctx, org, project, commitID, paths)
}

func (o treeOnlyServer) DownloadChunks(ctx context.Context, scope domain.ChunkScope, hashes []serverDomain.Hash, onChunk func(serverDomain.Hash, []byte) error) error {
	return o.s.DownloadChunks(ctx, scope, hashes, onChunk)
}

func TestRestore_RootFileWithoutDirManifestsAsksForTheWholeCheckout(t *testing.T) {
	d := newDirectRestore(t)
	d.wc.WithDownloader(nil, treeOnlyServer{d.server})
	d.edit(t)

	require.NoError(t, d.wc.Restore(context.Background(), []string{"big.bin"}))
	require.Equal(t, [][]string{nil}, d.server.manifestPaths)
	require.Equal(t, d.content, readRepoFile(t, d.root, "big.bin"))
}

// TestRestore_AsksTheManifestForFoldersNotFiles covers the server's path
// filter: it takes folders, and answers 404 for a file path.
func TestRestore_AsksTheManifestForFoldersNotFiles(t *testing.T) {
	lr, root := newDirectRepo(t)
	require.NoError(t, lr.SaveConfig(domain.Config{Url: "http://example.com/org/project", Branch: "main"}))
	data := map[serverDomain.Hash][]byte{}
	dir := func(name string, files ...*serverDomain.File) *serverDomain.TreeNode {
		return &serverDomain.TreeNode{Name: name, FileChildren: files}
	}
	tree := &serverDomain.TreeNode{Name: "root",
		FileChildren: []*serverDomain.File{rawManifestFile("top.bin", data, "top content")},
		TreeChildren: []*serverDomain.TreeNode{
			dir("sfx", rawManifestFile("c.bin", data, "sfx c")),
			dir("art", rawManifestFile("a.bin", data, "art a"), rawManifestFile("b.bin", data, "art b")),
		},
	}
	server := &restoreServer{streamingFake: newStreamingFake(data), tree: tree}
	require.NoError(t, syncTree(t, lr, root, server, tree))
	require.NoError(t, lr.SaveCommit("c1", ""))
	wc, err := NewWorkingCopy(lr, root)
	require.NoError(t, err)
	wc.WithDownloader(nil, server)
	files := []string{"art/a.bin", "art/b.bin", "sfx/c.bin", "top.bin"}
	remove := func() {
		for _, p := range files {
			require.NoError(t, os.Remove(filepath.Join(root, filepath.FromSlash(p))))
		}
	}

	remove()
	require.NoError(t, wc.Restore(context.Background(), []string{"."}))
	require.Equal(t, [][]string{{"art", "sfx"}}, server.manifestPaths, "one request for the folders of the restored files")
	require.Equal(t, []string{""}, server.manifestDirs, "the root's own files are read without its subtree")
	require.Equal(t, "art b", string(readRepoFile(t, root, "art/b.bin")))
	require.Equal(t, "top content", string(readRepoFile(t, root, "top.bin")))
	require.Zero(t, objectCount(t, root), "every file took the direct path")

	remove()
	require.NoError(t, os.WriteFile(filepath.Join(root, "top.bin"), []byte("top content"), 0o644))
	server.manifestPaths, server.manifestDirs = nil, nil
	require.NoError(t, wc.Restore(context.Background(), []string{"art", "sfx"}))
	require.Equal(t, [][]string{{"art", "sfx"}}, server.manifestPaths)
	require.Empty(t, server.manifestDirs, "no root file, no root request")

	remove()
	server.manifestErr = errors.New("no manifest")
	require.NoError(t, wc.Restore(context.Background(), []string{"art"}), "a failed folder request falls back to the cache path")
	require.Equal(t, "art a", string(readRepoFile(t, root, "art/a.bin")))
}

// countingRepo counts the whole-table reads a restore makes.
type countingRepo struct {
	*localrepo.LocalRepo
	snapshots, statCaches int
}

func (c *countingRepo) Snapshot() (*domain.Snapshot, error) {
	c.snapshots++
	return c.LocalRepo.Snapshot()
}

func (c *countingRepo) LoadStatCache() (map[string]domain.StatEntry, error) {
	c.statCaches++
	return c.LocalRepo.LoadStatCache()
}

func TestRestore_OneFileReadsOnlyItsRows(t *testing.T) {
	d := newDirectRestore(t)
	repo := &countingRepo{LocalRepo: d.lr}
	wc, err := NewWorkingCopy(repo, d.root)
	require.NoError(t, err)
	wc.WithDownloader(nil, d.server)
	d.edit(t)
	writeRepoFile(t, d.root, "notes.txt", "scribbles\n")

	require.NoError(t, wc.Restore(context.Background(), []string{"big.bin", "notes.txt"}))
	require.Equal(t, d.content, readRepoFile(t, d.root, "big.bin"))
	require.Equal(t, "notes\n", string(readRepoFile(t, d.root, "notes.txt")))
	require.Zero(t, repo.snapshots, "naming tracked files needs neither the whole snapshot nor a prune")
	require.Zero(t, repo.statCaches)

	writeRepoFile(t, d.root, "notes.txt", "scribbles\n")
	require.NoError(t, wc.Restore(context.Background(), []string{"."}))
	require.Equal(t, 1, repo.snapshots, "a directory target expands over the whole snapshot")
	require.Zero(t, repo.statCaches, "a small restore still reads only its fingerprint rows")

	prev := maxSingleRowStatReads
	maxSingleRowStatReads = 0
	t.Cleanup(func() { maxSingleRowStatReads = prev })
	require.NoError(t, wc.Restore(context.Background(), []string{"."}))
	require.Equal(t, 1, repo.statCaches, "a large restore scans the cache once")

	require.ErrorContains(t, wc.Restore(context.Background(), []string{"missing.bin"}), "not tracked")
	require.ErrorContains(t, wc.Restore(context.Background(), []string{".nipa/config"}), "cannot operate on path inside")
}

func TestDirectPlan_SalvageNeedsAnUnchangedFile(t *testing.T) {
	root := t.TempDir()
	content := randomBytes(3, 1<<20)
	data := map[serverDomain.Hash][]byte{}
	file := binaryFile(t, "a.bin", content, data)
	mf := flattenTree(&serverDomain.TreeNode{Name: "root", FileChildren: []*serverDomain.File{file}}, "")[0]
	newPlan := func() (*directPlan, map[serverDomain.Hash]struct{}) {
		plan, err := newDirectPlan(t.TempDir(), []materializeJob{{file: mf}})
		require.NoError(t, err)
		t.Cleanup(plan.cleanup)
		missing := map[serverDomain.Hash]struct{}{}
		for _, c := range file.Chunks {
			missing[c.Hash] = struct{}{}
		}
		return plan, missing
	}
	writeRepoFile(t, root, "a.bin", string(content))
	info, err := os.Stat(filepath.Join(root, "a.bin"))
	require.NoError(t, err)
	layout := workingLayout{chunks: file.Chunks, info: info}

	plan, missing := newPlan()
	require.NoError(t, plan.salvage(root, "a.bin", layout, missing))
	require.Empty(t, missing, "every chunk of an unchanged file is reused")
	require.True(t, plan.files[0].done.Load())

	plan, missing = newPlan()
	require.NoError(t, plan.salvage(root, "gone.bin", layout, missing))
	require.NoError(t, plan.salvage(root, "a.bin", workingLayout{}, missing))
	require.Len(t, missing, len(file.Chunks), "no file or no layout salvages nothing")

	require.NoError(t, os.Chtimes(filepath.Join(root, "a.bin"), time.Now(), info.ModTime().Add(time.Hour)))
	require.NoError(t, plan.salvage(root, "a.bin", layout, missing))
	require.Len(t, missing, len(file.Chunks), "a file touched since it was hashed is not trusted")

	wrongSize := workingLayout{chunks: []serverDomain.Chunk{{Hash: file.Chunks[0].Hash, SizeBytes: 1}}, info: info}
	require.NoError(t, os.Chtimes(filepath.Join(root, "a.bin"), time.Now(), info.ModTime()))
	require.ErrorContains(t, plan.salvage(root, "a.bin", wrongSize, missing), "content length mismatch")

	require.NoError(t, os.Remove(filepath.Join(plan.root, plan.files[0].tmp)))
	require.Error(t, plan.salvage(root, "a.bin", layout, missing), "a missing temp file fails the copy")
}

func TestLayoutFor(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.bin", "content")
	wc := newWorkingCopy(t, &stubLocalRepo{snapshot: &domain.Snapshot{}}, root)

	recorded := workingLayout{chunks: []serverDomain.Chunk{{SizeBytes: 7}}}
	require.Equal(t, recorded, wc.layoutFor("a.bin", map[string]workingLayout{"a.bin": recorded}))
	require.Len(t, wc.layoutFor("a.bin", nil).chunks, 1, "an unhashed file is read once")
	require.Empty(t, wc.layoutFor("missing.bin", nil).chunks)
	require.NoError(t, os.Mkdir(filepath.Join(root, "dir"), 0o755))
	require.Empty(t, wc.layoutFor("dir", nil).chunks)

	wc.hashFile = func(string, string) (serverDomain.Hash, error) { return serverDomain.Hash{}, nil }
	require.Empty(t, wc.layoutFor("a.bin", nil).chunks, "a replaced hasher has no layout to offer")
}

func TestRestore_CopiesChunksTheCacheAlreadyHolds(t *testing.T) {
	d := newDirectRestore(t)
	first := d.file.Chunks[0].Hash
	require.NoError(t, d.lr.StoreChunk(first, d.server.data[first]))
	require.NoError(t, os.Remove(filepath.Join(d.root, "big.bin")))

	require.NoError(t, d.wc.Restore(context.Background(), []string{"big.bin"}))

	require.Equal(t, d.content, readRepoFile(t, d.root, "big.bin"))
	require.NotContains(t, d.server.streamed, first, "a cached chunk is copied, not downloaded")
	require.Len(t, d.server.streamed, len(d.file.Chunks)-1)
}

func TestRestore_LoadChunkAndErrorNaming(t *testing.T) {
	d := newDirectRestore(t)
	_, err := d.wc.loadChunk(serverDomain.Hash{0xee})
	require.Error(t, err, "a chunk that is not cached cannot be loaded")

	plain := errors.New("plain failure")
	require.Equal(t, plain, restoreError(plain))
	named := restoreError(&materializeError{path: "a.bin", err: plain})
	require.EqualError(t, named, "restore a.bin: plain failure")
	require.ErrorIs(t, named, plain)
}
