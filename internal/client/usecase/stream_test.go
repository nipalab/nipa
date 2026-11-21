package usecase

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

// streamingFake is a downloader that can stream: chunks the stream wants go
// through it from concurrent goroutines, the rest through the callback.
type streamingFake struct {
	data map[serverDomain.Hash][]byte
	// body overrides the reader a streamed chunk is served from.
	body func(h serverDomain.Hash) io.Reader
	// breakFirst serves half of a chunk and a read error before the real body.
	breakFirst bool
	// twice streams every chunk a second time, as a duplicate URL would.
	twice bool

	mu       sync.Mutex
	streamed map[serverDomain.Hash]int
	buffered map[serverDomain.Hash]int
}

func newStreamingFake(data map[serverDomain.Hash][]byte) *streamingFake {
	return &streamingFake{data: data, streamed: map[serverDomain.Hash]int{}, buffered: map[serverDomain.Hash]int{}}
}

func (d *streamingFake) DownloadChunks(ctx context.Context, scope domain.ChunkScope, hashes []serverDomain.Hash, onChunk func(serverDomain.Hash, []byte) error) error {
	return d.DownloadChunksStream(ctx, scope, hashes, nil, onChunk)
}

var errBrokenTransfer = errors.New("connection reset")

func (d *streamingFake) DownloadChunksStream(_ context.Context, _ domain.ChunkScope, hashes []serverDomain.Hash, stream *domain.ChunkStream, onChunk func(serverDomain.Hash, []byte) error) error {
	var (
		wg       sync.WaitGroup
		once     sync.Once
		firstErr error
	)
	fail := func(err error) {
		if err != nil {
			once.Do(func() { firstErr = err })
		}
	}
	for _, h := range hashes {
		if stream == nil || !stream.Wants(h) {
			d.mu.Lock()
			d.buffered[h]++
			d.mu.Unlock()
			fail(onChunk(h, d.data[h]))
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			fail(d.serve(stream, h))
		}()
	}
	wg.Wait()
	return firstErr
}

func (d *streamingFake) serve(stream *domain.ChunkStream, h serverDomain.Hash) error {
	d.mu.Lock()
	d.streamed[h]++
	d.mu.Unlock()
	data := d.data[h]
	if d.breakFirst {
		broken := io.MultiReader(bytes.NewReader(data[:len(data)/2]), errorReader{errBrokenTransfer})
		if err := stream.Write(h, broken); !errors.Is(err, errBrokenTransfer) {
			return errors.Join(errors.New("a broken body must fail the write with the read error"), err)
		}
	}
	body := io.Reader(bytes.NewReader(data))
	if d.body != nil {
		body = d.body(h)
	}
	if err := stream.Write(h, body); err != nil {
		return err
	}
	if d.twice {
		return stream.Write(h, bytes.NewReader(data))
	}
	return nil
}

type errorReader struct{ err error }

func (e errorReader) Read([]byte) (int, error) { return 0, e.err }

func sharedChunkTree(data map[serverDomain.Hash][]byte) *serverDomain.TreeNode {
	return &serverDomain.TreeNode{
		Name: "root",
		FileChildren: []*serverDomain.File{
			rawManifestFile("a.bin", data, "AAAA", "BBBBBB", "AAAA", "CC"),
			rawManifestFile("empty.bin", data),
		},
		TreeChildren: []*serverDomain.TreeNode{{
			Name:         "dir",
			FileChildren: []*serverDomain.File{rawManifestFile("b.bin", data, "CC", "AAAA", "DDD")},
		}},
	}
}

func TestSyncWorkingCopy_StreamsRawChunksIntoWorkingFiles(t *testing.T) {
	for name, tweak := range map[string]func(*streamingFake){
		"clean":                  func(*streamingFake) {},
		"broken first attempt":   func(d *streamingFake) { d.breakFirst = true },
		"chunk delivered twice":  func(d *streamingFake) { d.twice = true },
		"broken, then duplicate": func(d *streamingFake) { d.breakFirst, d.twice = true, true },
	} {
		t.Run(name, func(t *testing.T) {
			lr, root := newDirectRepo(t)
			data := map[serverDomain.Hash][]byte{}
			tree := sharedChunkTree(data)
			client := newStreamingFake(data)
			tweak(client)
			progress := &recordingProgress{}

			require.NoError(t, syncWorkingCopy(context.Background(), client, lr, root, tree, domain.ChunkScope{Org: "o", Project: "p"}, progress))

			require.Equal(t, "AAAABBBBBBAAAACC", string(readRepoFile(t, root, "a.bin")))
			require.Equal(t, "CCAAAADDD", string(readRepoFile(t, root, "dir/b.bin")))
			require.Empty(t, readRepoFile(t, root, "empty.bin"))
			require.Len(t, client.streamed, 4, "every raw chunk is streamed")
			require.Empty(t, client.buffered, "no raw chunk is buffered")
			require.Zero(t, objectCount(t, root), "streamed chunks never enter the object cache")
			requireNoTempFiles(t, root)
			require.Len(t, progress.progressObjects, 4, "each chunk is booked once")
			require.Equal(t, 4, progress.progressObjects[3])
			require.Equal(t, int64(len("AAAA")+len("BBBBBB")+len("CC")+len("DDD")), progress.progressBytes[3])
			require.Equal(t, 1, progress.endCalls)
		})
	}
}

func TestSyncWorkingCopy_StreamsCachedChunksIntoTheObjectCache(t *testing.T) {
	lr, root := newDirectRepo(t)
	require.NoError(t, lr.PinPaths([]string{"pinned"}))
	data := map[serverDomain.Hash][]byte{}
	textHash, textChunks, stored, encoding := testEncodedFile(t, "notes.txt", "some text\n")
	for h, b := range stored {
		data[h] = b
	}
	tree := &serverDomain.TreeNode{
		Name: "root",
		FileChildren: []*serverDomain.File{
			rawManifestFile("free.bin", data, "SHARED", "FREE"),
			{Name: "notes.txt", Mode: 2, SizeBytes: int64(len("some text\n")), Encoding: encoding, Hash: textHash, Chunks: textChunks},
		},
		TreeChildren: []*serverDomain.TreeNode{{
			Name:         "pinned",
			FileChildren: []*serverDomain.File{rawManifestFile("p.bin", data, "SHARED", "PIN")},
		}},
	}
	client := newStreamingFake(data)

	require.NoError(t, syncWorkingCopy(context.Background(), client, lr, root, tree, domain.ChunkScope{Org: "o", Project: "p"}))

	require.Equal(t, "SHAREDFREE", string(readRepoFile(t, root, "free.bin")))
	require.Equal(t, "SHAREDPIN", string(readRepoFile(t, root, "pinned/p.bin")))
	require.Equal(t, "some text\n", string(readRepoFile(t, root, "notes.txt")))
	require.Len(t, client.streamed, 4, "cached chunks are streamed too, into the object cache")
	require.Empty(t, client.buffered)
	require.Equal(t, 3, objectCount(t, root), "the pinned and text chunks are cached, the free one is not")
}

func TestSyncWorkingCopy_StreamRejectsWrongContent(t *testing.T) {
	good := []byte("AAAA")
	h := chunker.Sum(good)
	for name, tc := range map[string]struct {
		body    string
		wantErr string
	}{
		"wrong bytes": {body: "XXXX", wantErr: "chunk hash mismatch"},
		"too long":    {body: "AAAAA", wantErr: "content length mismatch"},
		"too short":   {body: "AAA", wantErr: "content length mismatch"},
	} {
		t.Run(name, func(t *testing.T) {
			lr, root := newDirectRepo(t)
			writeRepoFile(t, root, "a.bin", "previous")
			data := map[serverDomain.Hash][]byte{}
			tree := &serverDomain.TreeNode{Name: "root", FileChildren: []*serverDomain.File{rawManifestFile("a.bin", data, string(good), "TAIL")}}
			client := newStreamingFake(data)
			client.body = func(got serverDomain.Hash) io.Reader {
				if got == h {
					return bytes.NewReader([]byte(tc.body))
				}
				return bytes.NewReader(data[got])
			}

			err := syncWorkingCopy(context.Background(), client, lr, root, tree, domain.ChunkScope{Org: "o", Project: "p"})
			require.ErrorContains(t, err, tc.wantErr)
			require.Equal(t, "previous", string(readRepoFile(t, root, "a.bin")), "a rejected chunk never reaches the working file")
			requireNoTempFiles(t, root)
		})
	}
}

func TestSyncWorkingCopy_StreamRejectsConflictingChunkSizes(t *testing.T) {
	lr, root := newDirectRepo(t)
	data := map[serverDomain.Hash][]byte{}
	a := rawManifestFile("a.bin", data, "AAAA")
	b := rawManifestFile("b.bin", data, "AAAA")
	b.Chunks[0].SizeBytes, b.SizeBytes = 5, 5
	tree := &serverDomain.TreeNode{Name: "root", FileChildren: []*serverDomain.File{a, b}}

	err := syncWorkingCopy(context.Background(), newStreamingFake(data), lr, root, tree, domain.ChunkScope{Org: "o", Project: "p"})
	require.ErrorContains(t, err, "content length mismatch")
	requireNoTempFiles(t, root)
}

func TestDirectPlan_StreamFailsOnUnwritableTemp(t *testing.T) {
	root := t.TempDir()
	data := map[serverDomain.Hash][]byte{}
	files := flattenTree(&serverDomain.TreeNode{Name: "root", FileChildren: []*serverDomain.File{
		rawManifestFile("a.bin", data, "AAAA"),
		rawManifestFile("b.bin", data, "AAAA"),
	}}, "")
	plan, err := newDirectPlan(root, []materializeJob{{file: files[0]}, {file: files[1]}})
	require.NoError(t, err)
	defer plan.cleanup()
	h := chunker.Sum([]byte("AAAA"))

	require.NoError(t, os.Remove(plan.files[1].tmp))
	_, err = plan.receive(h, bytes.NewReader([]byte("AAAA")))
	require.Error(t, err, "the copy to the second file has nowhere to go")

	require.NoError(t, os.Remove(plan.files[0].tmp))
	_, err = plan.receive(h, bytes.NewReader([]byte("AAAA")))
	require.Error(t, err)
	require.Error(t, copyTarget(plan.targets[h][0], plan.targets[h][1]))
}

// TestDirectPlan_RetryAfterOversizedBrokenBody covers a retried chunk whose
// failed attempt delivered one byte past its size: that byte must never reach
// the neighbouring chunk already in place, or the good retry would leave it
// corrupt.
func TestDirectPlan_RetryAfterOversizedBrokenBody(t *testing.T) {
	root := t.TempDir()
	data := map[serverDomain.Hash][]byte{}
	files := flattenTree(&serverDomain.TreeNode{Name: "root", FileChildren: []*serverDomain.File{
		rawManifestFile("a.bin", data, "AAAA", "TAIL"),
	}}, "")
	plan, err := newDirectPlan(root, []materializeJob{{file: files[0]}})
	require.NoError(t, err)
	defer plan.cleanup()
	head, tail := chunker.Sum([]byte("AAAA")), chunker.Sum([]byte("TAIL"))

	require.NoError(t, plan.write(tail, []byte("TAIL")), "the tail comes from the local cache first")

	broken := io.MultiReader(bytes.NewReader([]byte("AAAAX")), &failingReader{err: errors.New("connection reset")})
	n, err := plan.receive(head, broken)
	require.Error(t, err)
	require.Equal(t, int64(5), n, "the excess byte is still counted")

	n, err = plan.receive(head, bytes.NewReader([]byte("AAAA")))
	require.NoError(t, err)
	require.Equal(t, int64(4), n)
	require.NoError(t, plan.settle(head))
	require.True(t, plan.files[0].done.Load())

	got, err := os.ReadFile(plan.files[0].tmp)
	require.NoError(t, err)
	require.Equal(t, "AAAATAIL", string(got))
}

func TestDirectPlan_ReceiveReportsBreakAfterFullChunk(t *testing.T) {
	root := t.TempDir()
	data := map[serverDomain.Hash][]byte{}
	files := flattenTree(&serverDomain.TreeNode{Name: "root", FileChildren: []*serverDomain.File{
		rawManifestFile("a.bin", data, "AAAA"),
	}}, "")
	plan, err := newDirectPlan(root, []materializeJob{{file: files[0]}})
	require.NoError(t, err)
	defer plan.cleanup()

	reset := errors.New("connection reset")
	body := io.MultiReader(bytes.NewReader([]byte("AAAA")), &failingReader{err: reset})
	_, err = plan.receive(chunker.Sum([]byte("AAAA")), body)
	require.ErrorIs(t, err, reset, "a transfer that breaks right after the chunk is not taken as complete")
}

func TestDownloadChunksStream_StopsAfterStoreFailure(t *testing.T) {
	data := map[serverDomain.Hash][]byte{}
	bad := chunker.Sum([]byte("expected"))
	data[bad] = []byte("something else")
	streamedHash := chunker.Sum([]byte("S"))
	data[streamedHash] = []byte("S")
	client := newStreamingFake(data)
	target := &chunkStreamTarget{
		wants:   func(h serverDomain.Hash) bool { return h == streamedHash },
		receive: func(serverDomain.Hash, io.Reader) (int64, error) { return 1, nil },
		settle:  func(serverDomain.Hash) error { return errors.New("settle failed") },
	}
	sink := func(*serverDomain.ChunkData) error { return nil }

	err := downloadChunksStream(context.Background(), client, domain.ChunkScope{}, []serverDomain.Hash{streamedHash}, 1, sink, target)
	require.ErrorContains(t, err, "settle failed")

	err = downloadChunksStream(context.Background(), client, domain.ChunkScope{}, []serverDomain.Hash{bad, streamedHash}, 1, sink, target)
	require.ErrorContains(t, err, "chunk hash mismatch", "a buffered chunk that fails verification fails the download")
}

// TestSyncWorkingCopy_StreamingKeepsMemoryFlat is the point of streaming: a
// sync of many large raw chunks allocates far less than the content it moves.
func TestSyncWorkingCopy_StreamingKeepsMemoryFlat(t *testing.T) {
	const chunkSize, chunks = 4 << 20, 16
	lr, root := newDirectRepo(t)
	file := &serverDomain.File{Name: "big.bin", Mode: 2, Encoding: chunker.EncodingRaw, IsBinary: true}
	seeds := map[serverDomain.Hash]byte{}
	var hashes []serverDomain.Hash
	buf := make([]byte, chunkSize)
	for i := range chunks {
		for j := range buf {
			buf[j] = byte(i + 1)
		}
		h := chunker.Sum(buf)
		seeds[h] = byte(i + 1)
		hashes = append(hashes, h)
		file.Chunks = append(file.Chunks, serverDomain.Chunk{Hash: h, SizeBytes: chunkSize})
		file.SizeBytes += chunkSize
	}
	file.Hash = chunker.FileHash(hashes)
	client := newStreamingFake(map[serverDomain.Hash][]byte{})
	client.body = func(h serverDomain.Hash) io.Reader { return io.LimitReader(repeatReader(seeds[h]), chunkSize) }
	tree := &serverDomain.TreeNode{Name: "root", FileChildren: []*serverDomain.File{file}}

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	require.NoError(t, syncWorkingCopy(context.Background(), client, lr, root, tree, domain.ChunkScope{Org: "o", Project: "p"}))
	runtime.ReadMemStats(&after)

	info, err := os.Stat(filepath.Join(root, "big.bin"))
	require.NoError(t, err)
	require.Equal(t, int64(chunkSize*chunks), info.Size())
	allocated := after.TotalAlloc - before.TotalAlloc
	require.Less(t, allocated, uint64(chunkSize*chunks/4), "streaming %d MiB allocated %d MiB", chunkSize*chunks>>20, allocated>>20)
}

type repeatReader byte

func (r repeatReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(r)
	}
	return len(p), nil
}

// bufferOnlyRepo hides the stream store of a real repository, like a
// repository implementation that can only store whole chunks.
type bufferOnlyRepo struct{ lr workingCopyLocalRepo }

func (b bufferOnlyRepo) Snapshot() (*domain.Snapshot, error) { return b.lr.Snapshot() }
func (b bufferOnlyRepo) MissingChunks(h []serverDomain.Hash) ([]serverDomain.Hash, error) {
	return b.lr.MissingChunks(h)
}
func (b bufferOnlyRepo) StoreChunks(c []*serverDomain.ChunkData) error { return b.lr.StoreChunks(c) }
func (b bufferOnlyRepo) OpenChunk(h serverDomain.Hash) (io.ReadCloser, error) {
	return b.lr.OpenChunk(h)
}
func (b bufferOnlyRepo) LoadChunk(h serverDomain.Hash) ([]byte, error) { return b.lr.LoadChunk(h) }
func (b bufferOnlyRepo) SaveStatEntries(e map[string]domain.StatEntry) error {
	return b.lr.SaveStatEntries(e)
}
func (b bufferOnlyRepo) ListPins() ([]string, error) { return b.lr.(pinLister).ListPins() }

func pinnedAndFreeTree(t *testing.T, data map[serverDomain.Hash][]byte) *serverDomain.TreeNode {
	t.Helper()
	return &serverDomain.TreeNode{
		Name:         "root",
		FileChildren: []*serverDomain.File{rawManifestFile("free.bin", data, "SHARED", "FREE")},
		TreeChildren: []*serverDomain.TreeNode{{
			Name:         "pinned",
			FileChildren: []*serverDomain.File{rawManifestFile("p.bin", data, "SHARED", "PIN")},
		}},
	}
}

func TestSyncWorkingCopy_BuffersCachedChunksWithoutAStreamStore(t *testing.T) {
	lr, root := newDirectRepo(t)
	require.NoError(t, lr.PinPaths([]string{"pinned"}))
	data := map[serverDomain.Hash][]byte{}
	client := newStreamingFake(data)

	require.NoError(t, syncWorkingCopy(context.Background(), client, bufferOnlyRepo{lr}, root, pinnedAndFreeTree(t, data), domain.ChunkScope{Org: "o", Project: "p"}))

	require.Equal(t, "SHAREDFREE", string(readRepoFile(t, root, "free.bin")))
	require.Equal(t, "SHAREDPIN", string(readRepoFile(t, root, "pinned/p.bin")))
	require.Equal(t, map[serverDomain.Hash]int{chunker.Sum([]byte("FREE")): 1}, client.streamed, "only the chunk no cached file needs is streamed")
	require.Len(t, client.buffered, 2)
	require.Equal(t, 2, objectCount(t, root))
}

func TestSyncWorkingCopy_StreamedCachedChunkIsVerified(t *testing.T) {
	lr, root := newDirectRepo(t)
	require.NoError(t, lr.PinPaths([]string{"pinned"}))
	data := map[serverDomain.Hash][]byte{}
	tree := pinnedAndFreeTree(t, data)
	data[chunker.Sum([]byte("SHARED"))] = []byte("FORGED")
	client := newStreamingFake(data)

	err := syncWorkingCopy(context.Background(), client, lr, root, tree, domain.ChunkScope{Org: "o", Project: "p"})
	require.ErrorContains(t, err, "chunk hash mismatch")
	require.Zero(t, objectCount(t, root)-countValidObjects(t, lr, data), "the forged chunk is not stored under the hash it claims")
	requireNoTempFiles(t, root)
	_, statErr := os.Stat(filepath.Join(root, "free.bin"))
	require.True(t, os.IsNotExist(statErr), "no working file is written from a failed sync")
}

// countValidObjects counts the cached objects whose content matches a hash
// the test data really has.
func countValidObjects(t *testing.T, lr workingCopyLocalRepo, data map[serverDomain.Hash][]byte) int {
	t.Helper()
	n := 0
	for h := range data {
		if stored, err := lr.LoadChunk(h); err == nil && chunker.Sum(stored) == h {
			n++
		}
	}
	return n
}

func TestGuardedRemoveCached_TrustsAFreshFingerprint(t *testing.T) {
	root := t.TempDir()
	base := domain.SnapshotFile{Path: "old.bin", Hash: serverDomain.Hash{1}}
	fingerprint := func(hash serverDomain.Hash) map[string]domain.StatEntry {
		writeRepoFile(t, root, "old.bin", "bytes that hash to neither")
		info, err := os.Stat(filepath.Join(root, "old.bin"))
		require.NoError(t, err)
		return map[string]domain.StatEntry{"old.bin": statEntryFromInfo(info, hash)}
	}

	removed, err := guardedRemoveCached(root, base, fingerprint(serverDomain.Hash{2}))
	require.NoError(t, err)
	require.False(t, removed, "a fingerprint of other content keeps the file without reading it")
	require.FileExists(t, filepath.Join(root, "old.bin"))

	removed, err = guardedRemoveCached(root, base, fingerprint(base.Hash))
	require.NoError(t, err)
	require.True(t, removed, "a fingerprint of the synced content removes it without reading it")
	require.NoFileExists(t, filepath.Join(root, "old.bin"))

	stale := fingerprint(base.Hash)
	writeRepoFile(t, root, "old.bin", "edited after the fingerprint, longer")
	removed, err = guardedRemoveCached(root, base, stale)
	require.NoError(t, err)
	require.False(t, removed, "a stale fingerprint is not trusted: the file is hashed and kept")
}

func TestRemoveLeftFiles(t *testing.T) {
	root := t.TempDir()
	hashOf := func(content string) serverDomain.Hash {
		h, _, _, err := chunkFile("f", []byte(content), chunker.EncodingRaw)
		require.NoError(t, err)
		return h
	}
	var removals []domain.SnapshotFile
	for _, name := range []string{"a.bin", "b.bin", "c.bin"} {
		writeRepoFile(t, root, name, "synced "+name)
		removals = append(removals, domain.SnapshotFile{Path: name, Hash: hashOf("synced " + name), Encoding: chunker.EncodingRaw})
	}
	writeRepoFile(t, root, "b.bin", "edited locally")

	require.NoError(t, removeLeftFiles(context.Background(), root, &stubLocalRepo{}, removals))
	require.NoFileExists(t, filepath.Join(root, "a.bin"))
	require.FileExists(t, filepath.Join(root, "b.bin"), "a locally edited file that left the tree is kept")
	require.NoFileExists(t, filepath.Join(root, "c.bin"))

	require.NoError(t, removeLeftFiles(context.Background(), root, struct{}{}, nil))
	wantErr := errors.New("stat cache failed")
	require.ErrorIs(t, removeLeftFiles(context.Background(), root, &stubLocalRepo{statCacheErr: wantErr}, removals), wantErr)

	require.NoError(t, os.Remove(filepath.Join(root, "b.bin")))
	require.NoError(t, os.Mkdir(filepath.Join(root, "b.bin"), 0o755))
	require.ErrorContains(t, removeLeftFiles(context.Background(), root, struct{}{}, removals), "remove b.bin")
}

func TestTreeFileHelpers(t *testing.T) {
	data := map[serverDomain.Hash][]byte{}
	tree := sharedChunkTree(data)
	var paths []string
	walkTreeFiles(tree, "", func(path string, f *serverDomain.File) {
		paths = append(paths, path)
		require.Equal(t, f.Hash, treeFileHash(f), "the streamed hash equals the file hash")
		require.Equal(t, f.Hash, materializedFromTree(path, f).FileHash)
	})
	require.Equal(t, []string{"a.bin", "empty.bin", "dir/b.bin"}, paths)
	walkTreeFiles(nil, "", func(string, *serverDomain.File) { t.Fatal("a nil tree has no files") })
	require.Len(t, flattenTree(tree, ""), 3)
}
