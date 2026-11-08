package usecase

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

type recordingDownloader struct {
	mu        sync.Mutex
	data      map[serverDomain.Hash][]byte
	requested []serverDomain.Hash
	failAfter int
	failErr   error
}

func (d *recordingDownloader) DownloadChunks(_ context.Context, _ domain.ChunkScope, hashes []serverDomain.Hash, onChunk func(serverDomain.Hash, []byte) error) error {
	d.mu.Lock()
	d.requested = append(d.requested, hashes...)
	d.mu.Unlock()
	for i, h := range hashes {
		if d.failErr != nil && i == d.failAfter {
			return d.failErr
		}
		if err := onChunk(h, d.data[h]); err != nil {
			return err
		}
	}
	return nil
}

func newDirectRepo(t *testing.T) (*localrepo.LocalRepo, string) {
	t.Helper()
	root := t.TempDir()
	lr := localrepo.NewLocalRepo()
	t.Cleanup(func() { _ = lr.Close() })
	require.NoError(t, lr.Init(root))
	return lr, root
}

func rawManifestFile(name string, data map[serverDomain.Hash][]byte, parts ...string) *serverDomain.File {
	f := &serverDomain.File{Name: name, Mode: 2, Encoding: chunker.EncodingRaw, IsBinary: true}
	hashes := make([]serverDomain.Hash, 0, len(parts))
	for _, p := range parts {
		h := chunker.Sum([]byte(p))
		data[h] = []byte(p)
		hashes = append(hashes, h)
		f.Chunks = append(f.Chunks, serverDomain.Chunk{Hash: h, SizeBytes: int64(len(p))})
		f.SizeBytes += int64(len(p))
	}
	f.Hash = chunker.FileHash(hashes)
	return f
}

func objectCount(t *testing.T, root string) int {
	t.Helper()
	n := 0
	require.NoError(t, filepath.WalkDir(filepath.Join(root, ".nipa", "objects"), func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			n++
		}
		return nil
	}))
	return n
}

func requireNoTempFiles(t *testing.T, root string) {
	t.Helper()
	require.NoError(t, filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		require.False(t, strings.HasPrefix(d.Name(), ".nipa-materialize-"), "leftover temp file %s", p)
		return nil
	}))
}

func syncTree(t *testing.T, lr *localrepo.LocalRepo, root string, client chunkDownloader, tree *serverDomain.TreeNode) error {
	t.Helper()
	err := syncWorkingCopy(context.Background(), client, lr, root, tree, domain.ChunkScope{Org: "o", Project: "p"})
	if err == nil {
		require.NoError(t, lr.SaveTree(tree))
	}
	return err
}

func TestSyncWorkingCopy_DirectWritesOffsetsAndSharedChunks(t *testing.T) {
	lr, root := newDirectRepo(t)
	data := map[serverDomain.Hash][]byte{}
	tree := &serverDomain.TreeNode{
		Name: "root",
		FileChildren: []*serverDomain.File{
			rawManifestFile("a.bin", data, "AAAA", "BBBBBB", "AAAA", "CC"),
			rawManifestFile("empty.bin", data),
		},
		TreeChildren: []*serverDomain.TreeNode{{
			Name:         "dir",
			FileChildren: []*serverDomain.File{rawManifestFile("b.bin", data, "BBBBBB")},
		}},
	}
	client := &recordingDownloader{data: data}

	require.NoError(t, syncTree(t, lr, root, client, tree))

	require.Equal(t, "AAAABBBBBBAAAACC", string(readRepoFile(t, root, "a.bin")))
	require.Equal(t, "BBBBBB", string(readRepoFile(t, root, "dir/b.bin")))
	require.Empty(t, readRepoFile(t, root, "empty.bin"))
	require.Len(t, client.requested, 3, "each chunk is downloaded once")
	require.Zero(t, objectCount(t, root), "raw unpinned chunks never reach the object cache")
	requireNoTempFiles(t, root)

	stats, err := lr.LoadStatCache()
	require.NoError(t, err)
	require.Contains(t, stats, "a.bin")
	require.Contains(t, stats, "dir/b.bin")
	require.Equal(t, tree.FileChildren[0].Hash, stats["a.bin"].Hash)
}

func TestSyncWorkingCopy_FallsBackToObjectCache(t *testing.T) {
	lr, root := newDirectRepo(t)
	require.NoError(t, lr.PinPaths([]string{"pinned"}))
	data := map[serverDomain.Hash][]byte{}

	unknown := rawManifestFile("unknown.bin", data, "unknown sizes")
	unknown.Chunks[0].SizeBytes = 0
	pinned := rawManifestFile("tex.bin", data, "pinned bytes")
	textHash, textChunks, textStored, textEncoding := testEncodedFile(t, "notes.txt", "hello text")
	for h, b := range textStored {
		data[h] = b
	}
	tree := &serverDomain.TreeNode{
		Name: "root",
		FileChildren: []*serverDomain.File{
			unknown,
			{Name: "notes.txt", Mode: 2, Encoding: textEncoding, Hash: textHash, Chunks: textChunks, SizeBytes: int64(len("hello text"))},
		},
		TreeChildren: []*serverDomain.TreeNode{{Name: "pinned", FileChildren: []*serverDomain.File{pinned}}},
	}
	require.Equal(t, chunker.EncodingZstd, textEncoding)

	require.NoError(t, syncTree(t, lr, root, &recordingDownloader{data: data}, tree))

	require.Equal(t, "unknown sizes", string(readRepoFile(t, root, "unknown.bin")))
	require.Equal(t, "pinned bytes", string(readRepoFile(t, root, "pinned/tex.bin")))
	require.Equal(t, "hello text", string(readRepoFile(t, root, "notes.txt")))
	for _, h := range []serverDomain.Hash{unknown.Chunks[0].Hash, pinned.Chunks[0].Hash, textChunks[0].Hash} {
		_, err := lr.LoadChunk(h)
		require.NoError(t, err, "fallback content is cached")
	}
}

func TestSyncWorkingCopy_DirectFailureKeepsWorkingFiles(t *testing.T) {
	lr, root := newDirectRepo(t)
	data := map[serverDomain.Hash][]byte{}
	oldTree := &serverDomain.TreeNode{Name: "root", FileChildren: []*serverDomain.File{
		rawManifestFile("a.bin", data, "old a"),
		rawManifestFile("b.bin", data, "old b"),
	}}
	require.NoError(t, syncTree(t, lr, root, &recordingDownloader{data: data}, oldTree))

	newTree := &serverDomain.TreeNode{Name: "root", FileChildren: []*serverDomain.File{
		rawManifestFile("a.bin", data, "new a 1", "new a 2"),
		rawManifestFile("b.bin", data, "new b"),
	}}
	wantErr := errors.New("connection reset")
	err := syncTree(t, lr, root, &recordingDownloader{data: data, failAfter: 1, failErr: wantErr}, newTree)
	require.ErrorIs(t, err, wantErr)

	require.Equal(t, "old a", string(readRepoFile(t, root, "a.bin")))
	require.Equal(t, "old b", string(readRepoFile(t, root, "b.bin")))
	requireNoTempFiles(t, root)
}

func TestSyncWorkingCopy_DirectLengthMismatch(t *testing.T) {
	lr, root := newDirectRepo(t)
	data := map[serverDomain.Hash][]byte{}
	f := rawManifestFile("a.bin", data, "four", "four")
	f.Chunks[0].SizeBytes, f.Chunks[1].SizeBytes = 3, 5
	tree := &serverDomain.TreeNode{Name: "root", FileChildren: []*serverDomain.File{f}}

	err := syncTree(t, lr, root, &recordingDownloader{data: data}, tree)
	var failed *materializeError
	require.ErrorAs(t, err, &failed)
	require.Equal(t, "a.bin", failed.path)
	require.Contains(t, err.Error(), "content length mismatch")
	requireNoTempFiles(t, root)
	_, statErr := os.Stat(filepath.Join(root, "a.bin"))
	require.True(t, os.IsNotExist(statErr))
}

func TestSyncWorkingCopy_DirectKeepsReadOnlyFile(t *testing.T) {
	lr, root := newDirectRepo(t)
	data := map[serverDomain.Hash][]byte{}
	require.NoError(t, syncTree(t, lr, root, &recordingDownloader{data: data},
		&serverDomain.TreeNode{Name: "root", FileChildren: []*serverDomain.File{rawManifestFile("a.bin", data, "v1")}}))
	makeReadOnly(t, filepath.Join(root, "a.bin"))

	require.NoError(t, syncTree(t, lr, root, &recordingDownloader{data: data},
		&serverDomain.TreeNode{Name: "root", FileChildren: []*serverDomain.File{rawManifestFile("a.bin", data, "version two")}}))

	require.Equal(t, "version two", string(readRepoFile(t, root, "a.bin")))
	requireReadOnly(t, filepath.Join(root, "a.bin"), true)
}

func TestSyncWorkingCopy_DirectCopiesCachedChunks(t *testing.T) {
	lr, root := newDirectRepo(t)
	data := map[serverDomain.Hash][]byte{}
	f := rawManifestFile("a.bin", data, "cached part", "remote part")
	cachedHash := f.Chunks[0].Hash
	require.NoError(t, lr.StoreChunks([]*serverDomain.ChunkData{{Hash: cachedHash, Data: data[cachedHash]}}))
	client := &recordingDownloader{data: data}

	require.NoError(t, syncTree(t, lr, root, client, &serverDomain.TreeNode{Name: "root", FileChildren: []*serverDomain.File{f}}))

	require.Equal(t, "cached partremote part", string(readRepoFile(t, root, "a.bin")))
	require.Equal(t, []serverDomain.Hash{f.Chunks[1].Hash}, client.requested)
}

type failingLoadRepo struct {
	*localrepo.LocalRepo
}

func (failingLoadRepo) LoadChunk(serverDomain.Hash) ([]byte, error) {
	return nil, errors.New("disk read failed")
}

func TestSyncWorkingCopy_DirectCachedChunkLoadFailure(t *testing.T) {
	lr, root := newDirectRepo(t)
	data := map[serverDomain.Hash][]byte{}
	f := rawManifestFile("a.bin", data, "cached part")
	require.NoError(t, lr.StoreChunks([]*serverDomain.ChunkData{{Hash: f.Chunks[0].Hash, Data: data[f.Chunks[0].Hash]}}))

	err := syncWorkingCopy(context.Background(), &recordingDownloader{data: data}, failingLoadRepo{lr}, root,
		&serverDomain.TreeNode{Name: "root", FileChildren: []*serverDomain.File{f}}, domain.ChunkScope{})
	require.ErrorContains(t, err, "disk read failed")
	requireNoTempFiles(t, root)
}

func TestSyncWorkingCopy_DirectTempCreationFailure(t *testing.T) {
	lr, root := newDirectRepo(t)
	writeRepoFile(t, root, "x", "a file where a directory is wanted")
	data := map[serverDomain.Hash][]byte{}
	tree := &serverDomain.TreeNode{
		Name:         "root",
		FileChildren: []*serverDomain.File{rawManifestFile("a.bin", data, "a")},
		TreeChildren: []*serverDomain.TreeNode{{Name: "x", FileChildren: []*serverDomain.File{rawManifestFile("y.bin", data, "y")}}},
	}

	err := syncTree(t, lr, root, &recordingDownloader{data: data}, tree)
	var failed *materializeError
	require.ErrorAs(t, err, &failed)
	require.Equal(t, "x/y.bin", failed.path)
	requireNoTempFiles(t, root)
}

func TestSyncWorkingCopy_PinListError(t *testing.T) {
	err := syncWorkingCopy(context.Background(), &recordingDownloader{}, pinErrorRepo{&stubLocalRepo{snapshot: &domain.Snapshot{}}}, t.TempDir(),
		&serverDomain.TreeNode{Name: "root"}, domain.ChunkScope{})
	require.ErrorContains(t, err, "pins unavailable")
}

type pinErrorRepo struct {
	*stubLocalRepo
}

func (pinErrorRepo) ListPins() ([]string, error) { return nil, errors.New("pins unavailable") }

func TestDirectPlan_FinishReportsIncompleteFile(t *testing.T) {
	root := t.TempDir()
	f := materializedFile{Path: "a.bin", SizeBytes: 2, ChunkHashes: []serverDomain.Hash{{1}}, ChunkSizes: []int64{2}}
	plan, err := newDirectPlan(root, []materializeJob{{file: f}})
	require.NoError(t, err)
	require.ErrorContains(t, plan.finish(), "incomplete download")
	plan.cleanup()
	requireNoTempFiles(t, root)
}

func TestDirectEligible(t *testing.T) {
	base := materializedFile{Path: "a.bin", Encoding: chunker.EncodingRaw, SizeBytes: 4, ChunkHashes: []serverDomain.Hash{{1}, {2}}, ChunkSizes: []int64{1, 3}}
	require.True(t, directEligible(base, nil))
	require.False(t, directEligible(base, []string{"a.bin"}))

	short := base
	short.ChunkSizes = []int64{4}
	require.False(t, directEligible(short, nil))

	wrongTotal := base
	wrongTotal.SizeBytes = 5
	require.False(t, directEligible(wrongTotal, nil))

	zstd := base
	zstd.Encoding = chunker.EncodingZstd
	require.False(t, directEligible(zstd, nil))
}

func TestUpdate_Run_KeepsRawChunksOutOfObjects(t *testing.T) {
	lr, root := newDirectRepo(t)
	require.NoError(t, lr.SaveConfig(domain.Config{Url: "http://example.com/org/project", Branch: "main"}))
	data := map[serverDomain.Hash][]byte{}
	big := bytes.Repeat([]byte("x"), 1<<16)
	tree := &serverDomain.TreeNode{Name: "root", FileChildren: []*serverDomain.File{rawManifestFile("big.bin", data, string(big), "tail")}}
	client := &stubUpdateClient{manifest: tree, downloadData: data}

	require.NoError(t, newTestUpdate(t, lr, client).Run(context.Background(), root))

	require.Equal(t, append(big, "tail"...), readRepoFile(t, root, "big.bin"))
	require.Zero(t, objectCount(t, root))
}
