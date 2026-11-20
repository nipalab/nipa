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

func TestSyncWorkingCopy_BuffersChunksTheCacheKeeps(t *testing.T) {
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
	require.Equal(t, map[serverDomain.Hash]int{chunker.Sum([]byte("FREE")): 1}, client.streamed, "only the chunk no cached file needs is streamed")
	require.Len(t, client.buffered, 3)
	require.Equal(t, 3, objectCount(t, root), "the pinned and text chunks are cached")
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
