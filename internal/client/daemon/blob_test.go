package daemon

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/diff"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/preview"
)

type eventStream[T any] struct {
	grpc.ServerStream
	ctx    context.Context
	failAt int

	mu     sync.Mutex
	events []*T
}

func (s *eventStream[T]) Context() context.Context { return s.ctx }

func (s *eventStream[T]) Send(ev *T) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failAt > 0 && len(s.events)+1 >= s.failAt {
		return errors.New("stream closed")
	}
	s.events = append(s.events, ev)
	return nil
}

func (s *eventStream[T]) all() []*T {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*T(nil), s.events...)
}

func blobStream() *eventStream[daemonpb.BlobEvent] {
	return &eventStream[daemonpb.BlobEvent]{ctx: context.Background()}
}

func previewStream() *eventStream[daemonpb.PreviewEvent] {
	return &eventStream[daemonpb.PreviewEvent]{ctx: context.Background()}
}

type blobFile struct {
	entry diff.Entry
	data  []byte
}

func blobServer(t *testing.T, files map[string]blobFile, seen *[]usecase.BlobRequest) (*Server, string) {
	t.Helper()
	root := newTestClone(t)
	srv := newOpServer(t, RepoOps{Diff: &stubDiffRunner{readBlob: func(_ context.Context, _ string, req usecase.BlobRequest, fn func(diff.Entry, io.Reader) error) error {
		if seen != nil {
			*seen = append(*seen, req)
		}
		f, ok := files[string(req.Source)+":"+req.Path]
		if !ok {
			return clientDomain.NewNotFoundError(req.Path + " not found")
		}
		return fn(f.entry, bytes.NewReader(f.data))
	}}})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)
	return srv, root
}

func blobContent(events []*daemonpb.BlobEvent) (*daemonpb.BlobInfo, []byte, *daemonpb.OpFailure) {
	var info *daemonpb.BlobInfo
	var data []byte
	var failure *daemonpb.OpFailure
	for _, ev := range events {
		switch {
		case ev.GetInfo() != nil:
			info = ev.GetInfo()
		case ev.GetFailure() != nil:
			failure = ev.GetFailure()
		default:
			data = append(data, ev.GetData()...)
		}
	}
	return info, data, failure
}

func TestServer_ReadBlobStreamsContent(t *testing.T) {
	big := bytes.Repeat([]byte("0123456789abcdef"), 10000)
	var seen []usecase.BlobRequest
	srv, root := blobServer(t, map[string]blobFile{
		"revision:art/hero.png": {entry: diff.Entry{Path: "art/hero.png", SizeBytes: int64(len(big)), IsBinary: true, Hash: serverDomain.Hash{0xaa}}, data: big},
	}, &seen)

	stream := blobStream()
	require.NoError(t, srv.ReadBlob(&daemonpb.ReadBlobRequest{Root: root, Blob: &daemonpb.BlobRef{Source: "revision", Revision: "main", Path: "art/hero.png"}}, stream))

	require.Equal(t, []usecase.BlobRequest{{Source: usecase.BlobRevision, Revision: "main", Path: "art/hero.png"}}, seen)
	events := stream.all()
	require.NotNil(t, events[0].GetInfo(), "info comes first")
	require.Greater(t, len(events), 3)
	info, data, failure := blobContent(events)
	require.Nil(t, failure)
	require.Equal(t, "image", info.GetKind())
	require.Equal(t, (serverDomain.Hash{0xaa}).String(), info.GetHash())
	require.EqualValues(t, len(big), info.GetSizeBytes())
	require.Equal(t, big, data)
}

func TestServer_ReadBlobWorkingFileHasNoHash(t *testing.T) {
	srv, root := blobServer(t, map[string]blobFile{
		":a.txt": {entry: diff.Entry{Path: "a.txt", SizeBytes: 3}, data: []byte("hi\n")},
	}, nil)
	stream := blobStream()
	require.NoError(t, srv.ReadBlob(&daemonpb.ReadBlobRequest{Root: root, Blob: &daemonpb.BlobRef{Path: "a.txt"}}, stream))
	info, data, _ := blobContent(stream.all())
	require.Empty(t, info.GetHash())
	require.Equal(t, "text", info.GetKind())
	require.Equal(t, "hi\n", string(data))
}

func TestServer_ReadBlobFailures(t *testing.T) {
	srv, root := blobServer(t, nil, nil)

	stream := blobStream()
	require.NoError(t, srv.ReadBlob(&daemonpb.ReadBlobRequest{Root: root, Blob: &daemonpb.BlobRef{Path: "nope"}}, stream))
	_, _, failure := blobContent(stream.all())
	require.EqualValues(t, 404, failure.GetCode())

	stream = blobStream()
	require.NoError(t, srv.ReadBlob(&daemonpb.ReadBlobRequest{Root: newTestClone(t)}, stream))
	_, _, failure = blobContent(stream.all())
	require.Contains(t, failure.GetMessage(), "not watched")
}

type failingReader struct{ sent bool }

func (r *failingReader) Read(p []byte) (int, error) {
	if !r.sent {
		r.sent = true
		return copy(p, "partial"), nil
	}
	return 0, errors.New("disk error")
}

func readerServer(t *testing.T, reader func() io.Reader) (*Server, string) {
	t.Helper()
	root := newTestClone(t)
	srv := newOpServer(t, RepoOps{Diff: &stubDiffRunner{readBlob: func(_ context.Context, _ string, _ usecase.BlobRequest, fn func(diff.Entry, io.Reader) error) error {
		return fn(diff.Entry{Path: "a.bin"}, reader())
	}}})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)
	return srv, root
}

func TestServer_ReadBlobMidStreamErrors(t *testing.T) {
	srv, root := readerServer(t, func() io.Reader { return &failingReader{} })
	req := &daemonpb.ReadBlobRequest{Root: root, Blob: &daemonpb.BlobRef{Path: "a.bin"}}

	stream := blobStream()
	require.NoError(t, srv.ReadBlob(req, stream))
	_, data, failure := blobContent(stream.all())
	require.Equal(t, "partial", string(data))
	require.Contains(t, failure.GetMessage(), "disk error")

	closed := &eventStream[daemonpb.BlobEvent]{ctx: context.Background(), failAt: 1}
	require.Error(t, srv.ReadBlob(req, closed))

	closedOnData := &eventStream[daemonpb.BlobEvent]{ctx: context.Background(), failAt: 2}
	require.Error(t, srv.ReadBlob(req, closedOnData))
}

func TestServer_ReadBlobCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	srv, root := readerServer(t, func() io.Reader {
		cancel()
		return bytes.NewReader([]byte("data"))
	})
	stream := &eventStream[daemonpb.BlobEvent]{ctx: ctx}
	err := srv.ReadBlob(&daemonpb.ReadBlobRequest{Root: root, Blob: &daemonpb.BlobRef{Path: "a.bin"}}, stream)
	require.ErrorIs(t, err, context.Canceled)
}

func pngBytes(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

func solidImage(w, h int, c color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, c.A
	}
	return img
}

func noiseImage(side int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, side, side))
	r := rand.New(rand.NewPCG(1, 2))
	for i := range img.Pix {
		img.Pix[i] = byte(r.UintN(256))
	}
	return img
}

type previewResult struct {
	images  map[string]*daemonpb.ImagePreview
	pngs    map[string][]byte
	diff    *daemonpb.ImageDiffStats
	failure *daemonpb.OpFailure
}

func collectPreview(events []*daemonpb.PreviewEvent) previewResult {
	out := previewResult{images: map[string]*daemonpb.ImagePreview{}, pngs: map[string][]byte{}}
	for _, ev := range events {
		switch {
		case ev.GetImage() != nil:
			out.images[ev.GetImage().GetSide()] = ev.GetImage()
		case ev.GetData() != nil:
			out.pngs[ev.GetData().GetSide()] = append(out.pngs[ev.GetData().GetSide()], ev.GetData().GetPng()...)
		case ev.GetDiff() != nil:
			out.diff = ev.GetDiff()
		case ev.GetFailure() != nil:
			out.failure = ev.GetFailure()
		}
	}
	return out
}

func TestServer_PreviewBothSides(t *testing.T) {
	oldImg := solidImage(40, 20, color.NRGBA{R: 10, A: 255})
	newImg := solidImage(40, 20, color.NRGBA{R: 10, A: 255})
	newImg.SetNRGBA(5, 6, color.NRGBA{G: 200, A: 255})
	srv, root := blobServer(t, map[string]blobFile{
		"snapshot:hero.png": {entry: diff.Entry{Path: "hero.png", SizeBytes: 11}, data: pngBytes(t, oldImg)},
		"working:hero.png":  {entry: diff.Entry{Path: "hero.png", SizeBytes: 12}, data: pngBytes(t, newImg)},
	}, nil)

	stream := previewStream()
	require.NoError(t, srv.Preview(&daemonpb.PreviewRequest{
		Root:    root,
		Old:     &daemonpb.BlobRef{Source: "snapshot", Path: "hero.png"},
		New:     &daemonpb.BlobRef{Source: "working", Path: "hero.png"},
		MaxSize: 10,
	}, stream))

	got := collectPreview(stream.all())
	require.Nil(t, got.failure)
	require.Len(t, got.images, 2)
	newInfo := got.images["new"]
	require.EqualValues(t, 40, newInfo.GetWidth())
	require.EqualValues(t, 10, newInfo.GetPreviewWidth())
	require.EqualValues(t, 5, newInfo.GetPreviewHeight())
	require.Equal(t, "png", newInfo.GetFormat())
	require.EqualValues(t, 12, newInfo.GetSizeBytes())

	decoded, err := png.Decode(bytes.NewReader(got.pngs["old"]))
	require.NoError(t, err)
	require.Equal(t, 10, decoded.Bounds().Dx())

	require.EqualValues(t, 1, got.diff.GetChangedPixels())
	require.EqualValues(t, 800, got.diff.GetTotalPixels())
	require.EqualValues(t, 5, got.diff.GetBoundsX())
	require.EqualValues(t, 6, got.diff.GetBoundsY())
	require.EqualValues(t, 1, got.diff.GetBoundsWidth())
	require.EqualValues(t, 200, got.diff.GetMaxDelta())

	stream = previewStream()
	require.NoError(t, srv.Preview(&daemonpb.PreviewRequest{
		Root:      root,
		Old:       &daemonpb.BlobRef{Source: "snapshot", Path: "hero.png"},
		New:       &daemonpb.BlobRef{Source: "working", Path: "hero.png"},
		Threshold: 250,
	}, stream))
	got = collectPreview(stream.all())
	require.Zero(t, got.diff.GetChangedPixels())
	require.EqualValues(t, 40, got.images["old"].GetPreviewWidth())
}

func TestServer_PreviewSingleSideLargePNG(t *testing.T) {
	srv, root := blobServer(t, map[string]blobFile{
		"revision:noise.png": {entry: diff.Entry{Path: "noise.png"}, data: pngBytes(t, noiseImage(256))},
	}, nil)
	stream := previewStream()
	require.NoError(t, srv.Preview(&daemonpb.PreviewRequest{Root: root, New: &daemonpb.BlobRef{Source: "revision", Revision: "main", Path: "noise.png"}}, stream))

	events := stream.all()
	got := collectPreview(events)
	require.Nil(t, got.diff)
	require.Greater(t, len(events), 2, "large previews are split into chunks")
	decoded, err := png.Decode(bytes.NewReader(got.pngs["new"]))
	require.NoError(t, err)
	require.Equal(t, 256, decoded.Bounds().Dx())
}

func TestServer_PreviewValidation(t *testing.T) {
	srv, root := blobServer(t, nil, nil)
	ref := &daemonpb.BlobRef{Path: "a.png"}
	for _, req := range []*daemonpb.PreviewRequest{
		{Root: root},
		{Root: root, New: ref, MaxSize: -1},
		{Root: root, New: ref, MaxSize: maxPreviewSize + 1},
		{Root: root, New: ref, Threshold: -1},
		{Root: root, New: ref, Threshold: 256},
	} {
		stream := previewStream()
		require.NoError(t, srv.Preview(req, stream))
		require.EqualValues(t, 400, collectPreview(stream.all()).failure.GetCode())
	}

	stream := previewStream()
	require.NoError(t, srv.Preview(&daemonpb.PreviewRequest{Root: newTestClone(t), New: ref}, stream))
	require.Contains(t, collectPreview(stream.all()).failure.GetMessage(), "not watched")
}

func TestServer_PreviewDecodeFailures(t *testing.T) {
	valid := pngBytes(t, solidImage(4, 4, color.NRGBA{A: 255}))
	srv, root := blobServer(t, map[string]blobFile{
		":notes.txt":   {entry: diff.Entry{Path: "notes.txt"}, data: []byte("hello")},
		":fake.png":    {entry: diff.Entry{Path: "fake.png"}, data: []byte("not an image at all")},
		":cut.png":     {entry: diff.Entry{Path: "cut.png"}, data: valid[:40]},
		":big.png":     {entry: diff.Entry{Path: "big.png"}, data: valid},
		":hero.psd":    {entry: diff.Entry{Path: "hero.psd"}, data: []byte("8BPS")},
		":sky.exr":     {entry: diff.Entry{Path: "sky.exr"}, data: fixtureBytes(t, "exr_piz.exr")},
		":missing.png": {},
	}, nil)

	cases := map[string]int32{
		"notes.txt": 415,
		"fake.png":  415,
		"cut.png":   400,
		"hero.psd":  400,
		"sky.exr":   415,
		"gone.png":  404,
	}
	for path, code := range cases {
		stream := previewStream()
		require.NoError(t, srv.Preview(&daemonpb.PreviewRequest{Root: root, New: &daemonpb.BlobRef{Path: path}}, stream))
		require.Equal(t, code, collectPreview(stream.all()).failure.GetCode(), path)
	}
	stream := previewStream()
	require.NoError(t, srv.Preview(&daemonpb.PreviewRequest{Root: root, New: &daemonpb.BlobRef{Path: "sky.exr"}}, stream))
	require.Equal(t, "sky.exr: EXR PIZ compression not supported yet", collectPreview(stream.all()).failure.GetMessage())
	stream = previewStream()
	require.NoError(t, srv.Preview(&daemonpb.PreviewRequest{Root: root, New: &daemonpb.BlobRef{Path: "fake.png"}}, stream))
	require.Equal(t, "fake.png: unsupported image format", collectPreview(stream.all()).failure.GetMessage())

	old := preview.MaxPixels
	preview.MaxPixels = 4
	defer func() { preview.MaxPixels = old }()
	stream = previewStream()
	require.NoError(t, srv.Preview(&daemonpb.PreviewRequest{Root: root, Old: &daemonpb.BlobRef{Path: "big.png"}}, stream))
	require.EqualValues(t, 415, collectPreview(stream.all()).failure.GetCode())
}

func fixtureBytes(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "preview", "testdata", name))
	require.NoError(t, err)
	return data
}

func TestServer_PreviewTexturesAndLayers(t *testing.T) {
	srv, root := blobServer(t, map[string]blobFile{
		":cube.dds":   {entry: diff.Entry{Path: "cube.dds"}, data: fixtureBytes(t, "dds_cube.dds")},
		":sky.exr":    {entry: diff.Entry{Path: "sky.exr"}, data: fixtureBytes(t, "exr_tiled.exr")},
		":layers.psd": {entry: diff.Entry{Path: "layers.psd"}, data: fixtureBytes(t, "psd_layers.psd")},
	}, nil)

	stream := previewStream()
	require.NoError(t, srv.Preview(&daemonpb.PreviewRequest{Root: root, New: &daemonpb.BlobRef{Path: "cube.dds"}, Level: 1, Face: 2}, stream))
	res := collectPreview(stream.all())
	img := res.images["new"]
	require.Equal(t, "R8G8B8A8_UNORM", img.GetEncoding())
	require.EqualValues(t, 2, img.GetLevels())
	require.EqualValues(t, 6, img.GetFaces())
	require.True(t, img.GetCube())
	require.EqualValues(t, 1, img.GetLevel())
	require.EqualValues(t, 2, img.GetFace())
	require.EqualValues(t, 2, img.GetWidth())
	require.False(t, img.GetHdr())

	stream = previewStream()
	require.NoError(t, srv.Preview(&daemonpb.PreviewRequest{Root: root, New: &daemonpb.BlobRef{Path: "sky.exr"}}, stream))
	res = collectPreview(stream.all())
	img = res.images["new"]
	require.True(t, img.GetHdr())
	require.EqualValues(t, 5, img.GetPreviewWidth())
	decoded, err := png.Decode(bytes.NewReader(res.pngs["new"]))
	require.NoError(t, err)
	require.Equal(t, 10, decoded.Bounds().Dx())

	stream = previewStream()
	require.NoError(t, srv.Preview(&daemonpb.PreviewRequest{Root: root, Old: &daemonpb.BlobRef{Path: "layers.psd"}}, stream))
	layers := collectPreview(stream.all()).images["old"].GetLayers()
	require.Len(t, layers, 2)
	require.Equal(t, "Spark", layers[0].GetName())
	require.Equal(t, "layer", layers[0].GetKind())
	require.EqualValues(t, 16, layers[0].GetWidth())
	require.NotEmpty(t, layers[0].GetHash())
}

func TestServer_PreviewSendFailures(t *testing.T) {
	srv, root := blobServer(t, map[string]blobFile{
		":a.png": {entry: diff.Entry{Path: "a.png"}, data: pngBytes(t, solidImage(4, 4, color.NRGBA{A: 255}))},
	}, nil)
	req := &daemonpb.PreviewRequest{Root: root, Old: &daemonpb.BlobRef{Path: "a.png"}, New: &daemonpb.BlobRef{Path: "a.png"}}
	for failAt := 1; failAt <= 5; failAt++ {
		stream := &eventStream[daemonpb.PreviewEvent]{ctx: context.Background(), failAt: failAt}
		require.Error(t, srv.Preview(req, stream), "failAt %d", failAt)
	}
}

func TestSerialDiff_ReadBlobDelegates(t *testing.T) {
	var got usecase.BlobRequest
	serial := SerialDiff(&stubDiffRunner{readBlob: func(_ context.Context, root string, req usecase.BlobRequest, fn func(diff.Entry, io.Reader) error) error {
		got = req
		return fn(diff.Entry{Path: req.Path}, bytes.NewReader(nil))
	}})
	called := false
	err := serial.ReadBlob(context.Background(), "root", usecase.BlobRequest{Path: "a.png"}, func(e diff.Entry, _ io.Reader) error {
		called = e.Path == "a.png"
		return nil
	})
	require.NoError(t, err)
	require.True(t, called)
	require.Equal(t, "a.png", got.Path)
}
