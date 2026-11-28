package daemon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/client/thumbcache"
	"github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/diff"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/preview"
)

// thumbFile is one file the fake runner serves: its stat and its bytes.
type thumbFile struct {
	stat usecase.BlobStat
	data []byte
	// readSize, when set, is the size the read reports instead of the stat's.
	readSize int64
}

// thumbRunner is a DiffRunner fake that counts stats and full reads.
type thumbRunner struct {
	mu    sync.Mutex
	files map[string]*thumbFile
	stats atomic.Int32
	reads atomic.Int32
	// afterRead, when set, runs once a read completed (e.g. to modify the file).
	afterRead func(path string)
}

func (r *thumbRunner) file(path string) (*thumbFile, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	f, ok := r.files[path]
	if !ok {
		return nil, false
	}
	cp := *f
	return &cp, true
}

func (r *thumbRunner) set(path string, f thumbFile) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.files[path] = &f
}

func (r *thumbRunner) Run(context.Context, string, []string, usecase.DiffOptions) ([]diff.FileDiff, error) {
	return nil, nil
}

func (r *thumbRunner) StatBlob(_ context.Context, _ string, req usecase.BlobRequest) (usecase.BlobStat, error) {
	r.stats.Add(1)
	f, ok := r.file(req.Path)
	if !ok {
		return usecase.BlobStat{}, errors.New("missing")
	}
	return f.stat, nil
}

func (r *thumbRunner) ReadBlob(_ context.Context, _ string, req usecase.BlobRequest, fn func(diff.Entry, io.Reader) error) error {
	r.reads.Add(1)
	f, ok := r.file(req.Path)
	if !ok {
		return errors.New("missing")
	}
	entry := f.stat.Entry
	if f.readSize != 0 {
		entry.SizeBytes = f.readSize
	}
	err := fn(entry, bytes.NewReader(f.data))
	if r.afterRead != nil {
		r.afterRead(req.Path)
	}
	return err
}

func workingStat(path string, size, mtime int64) usecase.BlobStat {
	return usecase.BlobStat{Entry: diff.Entry{Path: path, SizeBytes: size, IsBinary: true}, AbsPath: "/clone/" + path, ModTimeNS: mtime}
}

func storedStat(path string, size int64, hash byte) usecase.BlobStat {
	return usecase.BlobStat{Entry: diff.Entry{Path: path, SizeBytes: size, IsBinary: true, Hash: serverDomain.Hash{hash}}}
}

func thumbServer(t *testing.T, runner DiffRunner) (*Server, string) {
	t.Helper()
	root := newTestClone(t)
	srv := newOpServer(t, RepoOps{Diff: runner})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)
	return srv, root
}

// countDecodes wraps the thumbnail decoder and counts its calls.
func countDecodes(t *testing.T) *atomic.Int32 {
	t.Helper()
	var n atomic.Int32
	old := decodeImageForSize
	decodeImageForSize = func(name string, data []byte, sel preview.Select, size int) (*preview.Image, error) {
		n.Add(1)
		return old(name, data, sel, size)
	}
	t.Cleanup(func() { decodeImageForSize = old })
	return &n
}

func thumbnail(t *testing.T, srv *Server, req *daemonpb.PreviewRequest) []*daemonpb.PreviewEvent {
	t.Helper()
	stream := previewStream()
	require.NoError(t, srv.Preview(req, stream))
	return stream.all()
}

func cacheEntries(t *testing.T, srv *Server) int {
	t.Helper()
	n := 0
	require.NoError(t, filepath.WalkDir(srv.thumbs.store().Dir(), func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			n++
		}
		return err
	}))
	return n
}

func TestServer_ThumbnailMissThenHit(t *testing.T) {
	data := pngBytes(t, noiseImage(600))
	runner := &thumbRunner{files: map[string]*thumbFile{
		"a.png": {stat: workingStat("a.png", int64(len(data)), 100), data: data},
	}}
	srv, root := thumbServer(t, runner)
	decodes := countDecodes(t)
	req := &daemonpb.PreviewRequest{Root: root, New: &daemonpb.BlobRef{Path: "a.png"}, MaxSize: 128}

	miss := thumbnail(t, srv, req)
	got := collectPreview(miss)
	require.Nil(t, got.failure)
	require.EqualValues(t, 600, got.images["new"].GetWidth())
	require.EqualValues(t, 128, got.images["new"].GetPreviewWidth())
	require.Equal(t, int32(1), decodes.Load())
	require.Equal(t, int32(1), runner.reads.Load())
	require.Equal(t, 1, cacheEntries(t, srv))

	hit := thumbnail(t, srv, req)
	require.Equal(t, int32(1), decodes.Load(), "a hit does not decode")
	require.Equal(t, int32(1), runner.reads.Load(), "a hit does not read the file")
	require.Len(t, hit, len(miss))
	for i := range miss {
		require.True(t, proto.Equal(miss[i], hit[i]), "event %d differs", i)
	}

	// The same rendering serves the old side too, labelled as such.
	oldSide := collectPreview(thumbnail(t, srv, &daemonpb.PreviewRequest{Root: root, Old: &daemonpb.BlobRef{Path: "a.png"}, MaxSize: 128}))
	require.Equal(t, "old", oldSide.images["old"].GetSide())
	require.Equal(t, got.pngs["new"], oldSide.pngs["old"])
	require.Equal(t, int32(1), decodes.Load())

	// Another size, level or face is another entry.
	for _, other := range []*daemonpb.PreviewRequest{
		{Root: root, New: &daemonpb.BlobRef{Path: "a.png"}, MaxSize: 64},
		{Root: root, New: &daemonpb.BlobRef{Path: "a.png"}, MaxSize: 128, Level: 1},
		{Root: root, New: &daemonpb.BlobRef{Path: "a.png"}, MaxSize: 128, Face: 1},
	} {
		thumbnail(t, srv, other)
	}
	require.Equal(t, int32(4), decodes.Load())
}

func TestServer_ThumbnailWorkingFileChangeMisses(t *testing.T) {
	small := pngBytes(t, solidImage(32, 32, color.NRGBA{R: 1, A: 255}))
	runner := &thumbRunner{files: map[string]*thumbFile{
		"a.png": {stat: workingStat("a.png", int64(len(small)), 100), data: small},
	}}
	srv, root := thumbServer(t, runner)
	decodes := countDecodes(t)
	req := &daemonpb.PreviewRequest{Root: root, New: &daemonpb.BlobRef{Path: "a.png"}, MaxSize: 128}

	thumbnail(t, srv, req)
	thumbnail(t, srv, req)
	require.Equal(t, int32(1), decodes.Load())

	runner.set("a.png", thumbFile{stat: workingStat("a.png", int64(len(small)), 200), data: small})
	thumbnail(t, srv, req)
	require.Equal(t, int32(2), decodes.Load(), "a new mtime misses")

	bigger := pngBytes(t, solidImage(64, 64, color.NRGBA{G: 1, A: 255}))
	runner.set("a.png", thumbFile{stat: workingStat("a.png", int64(len(bigger)), 200), data: bigger})
	got := collectPreview(thumbnail(t, srv, req))
	require.Equal(t, int32(3), decodes.Load(), "a new size misses")
	require.EqualValues(t, 64, got.images["new"].GetWidth())
}

func TestServer_ThumbnailNotCachedWhenFileChangesDuringRead(t *testing.T) {
	data := pngBytes(t, solidImage(8, 8, color.NRGBA{A: 255}))
	runner := &thumbRunner{files: map[string]*thumbFile{
		"a.png": {stat: workingStat("a.png", int64(len(data)), 100), data: data},
		"b.png": {stat: workingStat("b.png", int64(len(data)), 100), data: data, readSize: int64(len(data)) + 1},
		"c.png": {stat: storedStat("c.png", int64(len(data)), 7), data: data},
	}}
	runner.afterRead = func(path string) {
		if path == "a.png" {
			runner.set("a.png", thumbFile{stat: workingStat("a.png", int64(len(data)), 999), data: data})
		}
	}
	srv, root := thumbServer(t, runner)
	got := collectPreview(thumbnail(t, srv, &daemonpb.PreviewRequest{Root: root, New: &daemonpb.BlobRef{Path: "a.png"}, MaxSize: 128}))
	require.Nil(t, got.failure)
	require.Equal(t, 0, cacheEntries(t, srv), "the file changed after the stat")

	// b's read size disagrees with its stat: not cached either.
	runner.afterRead = nil
	thumbnail(t, srv, &daemonpb.PreviewRequest{Root: root, New: &daemonpb.BlobRef{Path: "b.png"}, MaxSize: 128})
	require.Equal(t, 0, cacheEntries(t, srv))

	// A stored version is cached when the read hash matches the stat.
	thumbnail(t, srv, &daemonpb.PreviewRequest{Root: root, New: &daemonpb.BlobRef{Source: "revision", Revision: "main", Path: "c.png"}, MaxSize: 128})
	require.Equal(t, 1, cacheEntries(t, srv))
}

// mismatchRunner reports one hash from StatBlob and another from ReadBlob.
type mismatchRunner struct{ thumbRunner }

func (r *mismatchRunner) ReadBlob(ctx context.Context, root string, req usecase.BlobRequest, fn func(diff.Entry, io.Reader) error) error {
	return r.thumbRunner.ReadBlob(ctx, root, req, func(e diff.Entry, rd io.Reader) error {
		e.Hash = serverDomain.Hash{0xee}
		return fn(e, rd)
	})
}

func TestServer_ThumbnailStoredHashMismatchNotCached(t *testing.T) {
	data := pngBytes(t, solidImage(8, 8, color.NRGBA{A: 255}))
	runner := &mismatchRunner{thumbRunner{files: map[string]*thumbFile{
		"c.png": {stat: storedStat("c.png", int64(len(data)), 7), data: data},
	}}}
	srv, root := thumbServer(t, runner)
	thumbnail(t, srv, &daemonpb.PreviewRequest{Root: root, New: &daemonpb.BlobRef{Source: "revision", Revision: "main", Path: "c.png"}, MaxSize: 128})
	require.Equal(t, 0, cacheEntries(t, srv))
}

func TestServer_ThumbnailStoredVersionsShareByHash(t *testing.T) {
	data := pngBytes(t, solidImage(20, 10, color.NRGBA{B: 9, A: 255}))
	runner := &thumbRunner{files: map[string]*thumbFile{
		"art/a.png": {stat: storedStat("art/a.png", int64(len(data)), 5), data: data},
		"art/b.png": {stat: storedStat("art/b.png", int64(len(data)), 5), data: data},
		"art/b.tga": {stat: storedStat("art/b.tga", int64(len(data)), 5), data: data},
	}}
	srv, root := thumbServer(t, runner)
	decodes := countDecodes(t)
	ref := func(p string) *daemonpb.PreviewRequest {
		return &daemonpb.PreviewRequest{Root: root, New: &daemonpb.BlobRef{Source: "revision", Revision: "main", Path: p}, MaxSize: 128}
	}
	thumbnail(t, srv, ref("art/a.png"))
	thumbnail(t, srv, ref("art/b.png"))
	require.Equal(t, int32(1), decodes.Load(), "same content and extension share an entry")
	thumbnail(t, srv, ref("art/b.tga"))
	require.Equal(t, int32(2), decodes.Load(), "the extension picks the decoder, so it is part of the key")
}

func TestServer_ThumbnailUncachedFallbacks(t *testing.T) {
	data := pngBytes(t, solidImage(8, 8, color.NRGBA{A: 255}))
	// No stat support, and a stored version without a hash: rendered, not cached.
	runner := &thumbRunner{files: map[string]*thumbFile{
		"nohash.png": {stat: usecase.BlobStat{Entry: diff.Entry{Path: "nohash.png", SizeBytes: int64(len(data))}}, data: data},
	}}
	srv, root := thumbServer(t, runner)
	decodes := countDecodes(t)
	for range 2 {
		got := collectPreview(thumbnail(t, srv, &daemonpb.PreviewRequest{Root: root, New: &daemonpb.BlobRef{Source: "snapshot", Path: "nohash.png"}, MaxSize: 128}))
		require.Nil(t, got.failure)
	}
	require.Equal(t, int32(2), decodes.Load())

	stub := &stubDiffRunner{readBlob: func(_ context.Context, _ string, req usecase.BlobRequest, fn func(diff.Entry, io.Reader) error) error {
		return fn(diff.Entry{Path: req.Path}, bytes.NewReader(data))
	}}
	srv2, root2 := thumbServer(t, stub)
	got := collectPreview(thumbnail(t, srv2, &daemonpb.PreviewRequest{Root: root2, New: &daemonpb.BlobRef{Path: "x.png"}, MaxSize: 128}))
	require.Nil(t, got.failure, "a failed stat still renders")
	require.Equal(t, 0, cacheEntries(t, srv2))

	// Failures are sent and never cached.
	runner.set("bad.png", thumbFile{stat: workingStat("bad.png", 3, 1), data: []byte("bad")})
	got = collectPreview(thumbnail(t, srv, &daemonpb.PreviewRequest{Root: root, New: &daemonpb.BlobRef{Path: "bad.png"}, MaxSize: 128}))
	require.NotNil(t, got.failure)
	require.Equal(t, 0, cacheEntries(t, srv))
}

func TestServer_ThumbnailCacheUnavailable(t *testing.T) {
	data := pngBytes(t, solidImage(8, 8, color.NRGBA{A: 255}))
	runner := &thumbRunner{files: map[string]*thumbFile{"a.png": {stat: workingStat("a.png", int64(len(data)), 1), data: data}}}
	srv, root := thumbServer(t, runner)
	blocked := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(blocked, nil, 0o600))
	srv.thumbs = newThumbnails(filepath.Join(blocked, "thumbs"))

	got := collectPreview(thumbnail(t, srv, &daemonpb.PreviewRequest{Root: root, New: &daemonpb.BlobRef{Path: "a.png"}, MaxSize: 128}))
	require.Nil(t, got.failure)
	require.Nil(t, srv.thumbs.store())
	require.Equal(t, int32(0), runner.stats.Load(), "no cache, no stat")
}

func TestServer_ThumbnailDefaultCacheDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "env-thumbs")
	t.Setenv(thumbcache.EnvDir, dir)
	th := newThumbnails("")
	require.NotNil(t, th.store())
	require.Equal(t, dir, th.store().Dir())
}

func TestServer_ThumbnailCorruptRecordIsMiss(t *testing.T) {
	data := pngBytes(t, solidImage(8, 8, color.NRGBA{A: 255}))
	runner := &thumbRunner{files: map[string]*thumbFile{"a.png": {stat: workingStat("a.png", int64(len(data)), 1), data: data}}}
	srv, root := thumbServer(t, runner)
	decodes := countDecodes(t)
	req := &daemonpb.PreviewRequest{Root: root, New: &daemonpb.BlobRef{Path: "a.png"}, MaxSize: 128}
	key, ok := thumbnailKey(workingStat("a.png", int64(len(data)), 1), preview.Select{}, 128)
	require.True(t, ok)
	cache := srv.thumbs.store()
	for _, bad := range [][]byte{
		{0x80},             // truncated length
		{0x10, 1, 2},       // length beyond the value
		{0x02, 0xff, 0xff}, // not a proto message
		{0x00},             // no PNG
	} {
		require.NoError(t, cache.Put(key, bad))
		_, hit := loadThumbnail(cache, key)
		require.False(t, hit, "%x", bad)
	}
	require.NoError(t, cache.Put(key, []byte{0x02, 0xff, 0xff}))
	got := collectPreview(thumbnail(t, srv, req))
	require.Nil(t, got.failure)
	require.Equal(t, int32(1), decodes.Load())
	_, hit := loadThumbnail(cache, key)
	require.True(t, hit, "the miss rewrote the entry")
}

func TestServer_ThumbnailDecodeSlots(t *testing.T) {
	data := pngBytes(t, solidImage(8, 8, color.NRGBA{A: 255}))
	runner := &thumbRunner{files: map[string]*thumbFile{}}
	for i := range 5 {
		p := fmt.Sprintf("%d.png", i)
		runner.files[p] = &thumbFile{stat: workingStat(p, int64(len(data)), 1), data: data}
	}
	srv, root := thumbServer(t, runner)

	var running, peak atomic.Int32
	entered := make(chan struct{}, 8)
	unblock := make(chan struct{})
	old := decodeImageForSize
	decodeImageForSize = func(name string, b []byte, sel preview.Select, size int) (*preview.Image, error) {
		n := running.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		entered <- struct{}{}
		<-unblock
		running.Add(-1)
		return old(name, b, sel, size)
	}
	t.Cleanup(func() { decodeImageForSize = old })

	var wg sync.WaitGroup
	for i := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stream := previewStream()
			_ = srv.Preview(&daemonpb.PreviewRequest{Root: root, New: &daemonpb.BlobRef{Path: fmt.Sprintf("%d.png", i)}, MaxSize: 128}, stream)
		}()
	}
	<-entered
	<-entered
	select {
	case <-entered:
		t.Fatal("a third thumbnail decoded while two were running")
	case <-time.After(100 * time.Millisecond):
	}

	// A waiter gives up when its call is cancelled.
	ctx, cancel := context.WithCancel(context.Background())
	waitErr := make(chan error, 1)
	go func() {
		waitErr <- srv.Preview(&daemonpb.PreviewRequest{Root: root, New: &daemonpb.BlobRef{Path: "4.png"}, MaxSize: 128}, &eventStream[daemonpb.PreviewEvent]{ctx: ctx})
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-waitErr:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("a cancelled waiter kept waiting for a slot")
	}

	close(unblock)
	wg.Wait()
	require.Equal(t, int32(2), peak.Load())
}

func TestServer_TwoSidedPreviewBypassesThumbnailCache(t *testing.T) {
	data := pngBytes(t, solidImage(8, 8, color.NRGBA{A: 255}))
	runner := &thumbRunner{files: map[string]*thumbFile{
		"a.png": {stat: workingStat("a.png", int64(len(data)), 1), data: data},
	}}
	srv, root := thumbServer(t, runner)
	decodes := countDecodes(t)
	for range 2 {
		got := collectPreview(thumbnail(t, srv, &daemonpb.PreviewRequest{
			Root: root, Old: &daemonpb.BlobRef{Path: "a.png"}, New: &daemonpb.BlobRef{Path: "a.png"}, MaxSize: 128,
		}))
		require.NotNil(t, got.diff)
	}
	// A large one-sided preview is not a thumbnail either.
	thumbnail(t, srv, &daemonpb.PreviewRequest{Root: root, New: &daemonpb.BlobRef{Path: "a.png"}, MaxSize: maxThumbnailSize + 1})
	require.Equal(t, int32(0), runner.stats.Load())
	require.Equal(t, int32(0), decodes.Load(), "full previews decode the selected level")
	require.Equal(t, int32(5), runner.reads.Load())
	require.Equal(t, 0, cacheEntries(t, srv))
}

func TestServer_ThumbnailTextureUsesSmallMip(t *testing.T) {
	dds, err := os.ReadFile(filepath.Join("..", "..", "preview", "testdata", "dds_dxt1.dds")) // 16x8, 3 levels
	require.NoError(t, err)
	runner := &thumbRunner{files: map[string]*thumbFile{
		"t.dds": {stat: workingStat("t.dds", int64(len(dds)), 1), data: dds},
	}}
	srv, root := thumbServer(t, runner)
	var decodedWidth int
	stubDecoders(t, func(string, []byte, preview.Select) (*preview.Image, error) {
		t.Error("a thumbnail must not decode the selected level in full")
		return nil, errors.New("unexpected")
	}, nil)
	old := decodeImageForSize
	decodeImageForSize = func(name string, b []byte, sel preview.Select, size int) (*preview.Image, error) {
		img, err := old(name, b, sel, size)
		if img != nil {
			decodedWidth = img.Width()
		}
		return img, err
	}
	t.Cleanup(func() { decodeImageForSize = old })

	got := collectPreview(thumbnail(t, srv, &daemonpb.PreviewRequest{Root: root, New: &daemonpb.BlobRef{Path: "t.dds"}, MaxSize: 4}))
	require.Nil(t, got.failure)
	info := got.images["new"]
	require.Equal(t, 4, decodedWidth, "decoded the 4x2 mip")
	require.EqualValues(t, 16, info.GetWidth(), "reports the selected level's size")
	require.EqualValues(t, 8, info.GetHeight())
	require.EqualValues(t, 0, info.GetLevel())
	require.EqualValues(t, 3, info.GetLevels())
	require.EqualValues(t, 4, info.GetPreviewWidth())
	require.EqualValues(t, 2, info.GetPreviewHeight())
}

func TestThumbnailRequestAndKey(t *testing.T) {
	ref := &daemonpb.BlobRef{Path: "a.png"}
	for _, tc := range []struct {
		req  *daemonpb.PreviewRequest
		size int
		side string
		ok   bool
	}{
		{&daemonpb.PreviewRequest{New: ref}, 128, "new", true},
		{&daemonpb.PreviewRequest{Old: ref}, maxThumbnailSize, "old", true},
		{&daemonpb.PreviewRequest{New: ref}, maxThumbnailSize + 1, "", false},
		{&daemonpb.PreviewRequest{Old: ref, New: ref}, 128, "", false},
		{&daemonpb.PreviewRequest{}, 128, "", false},
	} {
		side, _, ok := thumbnailRequest(tc.req, tc.size)
		require.Equal(t, tc.ok, ok)
		require.Equal(t, tc.side, side)
	}

	base, ok := thumbnailKey(storedStat("a.png", 10, 1), preview.Select{}, 128)
	require.True(t, ok)
	for _, other := range []struct {
		st   usecase.BlobStat
		sel  preview.Select
		size int
	}{
		{storedStat("a.png", 10, 2), preview.Select{}, 128},
		{storedStat("a.PNG", 11, 1), preview.Select{}, 128},
		{storedStat("a.dds", 10, 1), preview.Select{}, 128},
		{storedStat("a.png", 10, 1), preview.Select{Level: 1}, 128},
		{storedStat("a.png", 10, 1), preview.Select{Face: 1}, 128},
		{storedStat("a.png", 10, 1), preview.Select{}, 64},
		{workingStat("a.png", 10, 1), preview.Select{}, 128},
	} {
		key, ok := thumbnailKey(other.st, other.sel, other.size)
		require.True(t, ok)
		require.NotEqual(t, base, key)
	}
	upper, _ := thumbnailKey(storedStat("b/a.PNG", 10, 1), preview.Select{}, 128)
	lower, _ := thumbnailKey(storedStat("c/a.png", 10, 1), preview.Select{}, 128)
	require.Equal(t, lower, upper, "stored keys depend on content, not path")
	require.Contains(t, base, fmt.Sprintf("v%d|", thumbnailFormatVersion))
	_, ok = thumbnailKey(usecase.BlobStat{Entry: diff.Entry{Path: "a.png"}}, preview.Select{}, 128)
	require.False(t, ok)
}

func TestSerialDiff_StatBlobSkipsLockForWorkingFiles(t *testing.T) {
	release := make(chan struct{})
	reading := make(chan struct{})
	serial := SerialDiff(&stubDiffRunner{
		readBlob: func(context.Context, string, usecase.BlobRequest, func(diff.Entry, io.Reader) error) error {
			close(reading)
			<-release
			return nil
		},
		statBlob: func(_ context.Context, _ string, req usecase.BlobRequest) (usecase.BlobStat, error) {
			return usecase.BlobStat{Entry: diff.Entry{Path: req.Path}}, nil
		},
	})
	go func() {
		_ = serial.ReadBlob(context.Background(), "r", usecase.BlobRequest{Path: "big.png"}, nil)
	}()
	<-reading

	_, err := serial.StatBlob(context.Background(), "r", usecase.BlobRequest{Path: "a.png"})
	require.NoError(t, err, "working stat does not wait for the lock")
	_, err = serial.StatBlob(context.Background(), "r", usecase.BlobRequest{Source: usecase.BlobWorking, Path: "a.png"})
	require.NoError(t, err)

	done := make(chan struct{})
	go func() {
		_, _ = serial.StatBlob(context.Background(), "r", usecase.BlobRequest{Source: usecase.BlobRevision, Path: "a.png"})
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("a revision stat must wait for the lock")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	<-done
}

// BenchmarkThumbnail4096 renders a 128 px thumbnail of a 4096² PNG on a
// cache miss (every iteration a new mtime) and on a hit.
func BenchmarkThumbnail4096(b *testing.B) {
	var buf bytes.Buffer
	if err := pngEncodeFast(&buf, noiseImage(4096)); err != nil {
		b.Fatal(err)
	}
	data := buf.Bytes()
	runner := &thumbRunner{files: map[string]*thumbFile{"a.png": {stat: workingStat("a.png", int64(len(data)), 1), data: data}}}
	root := newTestClone(b)
	srv := newOpServer(b, RepoOps{Diff: runner})
	if _, err := srv.repos.watch(root); err != nil {
		b.Fatal(err)
	}
	req := &daemonpb.PreviewRequest{Root: root, New: &daemonpb.BlobRef{Path: "a.png"}, MaxSize: 128}
	run := func(b *testing.B) {
		if err := srv.Preview(req, previewStream()); err != nil {
			b.Fatal(err)
		}
	}
	b.Run("miss", func(b *testing.B) {
		for i := range b.N {
			runner.set("a.png", thumbFile{stat: workingStat("a.png", int64(len(data)), int64(i)+10), data: data})
			run(b)
		}
	})
	b.Run("hit", func(b *testing.B) {
		run(b)
		b.ResetTimer()
		for range b.N {
			run(b)
		}
	})
}

func pngEncodeFast(w io.Writer, img *image.NRGBA) error {
	return (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(w, img)
}
