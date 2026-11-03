package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
)

type previewDaemon struct {
	*opsDaemon
	events []*daemonpb.PreviewEvent
	err    error
	last   *daemonpb.PreviewRequest
}

func (d *previewDaemon) Preview(_ context.Context, req *daemonpb.PreviewRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[daemonpb.PreviewEvent], error) {
	d.last = req
	if d.err != nil {
		return nil, d.err
	}
	return &listStream[daemonpb.PreviewEvent]{items: d.events}, nil
}

func newPreview(t *testing.T) (*Service, *previewDaemon) {
	t.Helper()
	d := &previewDaemon{opsDaemon: &opsDaemon{fakeDaemon: &fakeDaemon{watchErr: map[string]error{}, changes: make(chan *daemonpb.RepoChange, 4)}}}
	svc := New(func(context.Context) (Session, error) { return &opsSession{client: d}, nil }, &memStore{}, nil)
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	return svc, d
}

func previewEvents(evs ...any) []*daemonpb.PreviewEvent {
	out := make([]*daemonpb.PreviewEvent, 0, len(evs))
	for _, ev := range evs {
		switch v := ev.(type) {
		case *daemonpb.ImagePreview:
			out = append(out, &daemonpb.PreviewEvent{Event: &daemonpb.PreviewEvent_Image{Image: v}})
		case *daemonpb.PreviewData:
			out = append(out, &daemonpb.PreviewEvent{Event: &daemonpb.PreviewEvent_Data{Data: v}})
		case *daemonpb.ImageDiffStats:
			out = append(out, &daemonpb.PreviewEvent{Event: &daemonpb.PreviewEvent_Diff{Diff: v}})
		case *daemonpb.OpFailure:
			out = append(out, &daemonpb.PreviewEvent{Event: &daemonpb.PreviewEvent_Failure{Failure: v}})
		}
	}
	return out
}

func TestService_ImageDiffCollectsBothSides(t *testing.T) {
	svc, d := newPreview(t)
	d.events = previewEvents(
		&daemonpb.ImagePreview{Side: "old", Width: 4, Height: 2, Format: "png", BitDepth: 8, SizeBytes: 90, PreviewWidth: 4, PreviewHeight: 2},
		&daemonpb.PreviewData{Side: "old", Png: []byte("ab")},
		&daemonpb.PreviewData{Side: "old", Png: []byte("c")},
		&daemonpb.ImagePreview{Side: "new", Width: 8, Height: 4, Format: "tga", BitDepth: 8, HasAlpha: true, SizeBytes: 120, PreviewWidth: 8, PreviewHeight: 4},
		&daemonpb.PreviewData{Side: "new", Png: []byte("xyz")},
		&daemonpb.PreviewData{Side: "stray", Png: []byte("ignored")},
		&daemonpb.ImageDiffStats{ChangedPixels: 3, TotalPixels: 32, ChangedRatio: 0.09375, MaxDelta: 255, BoundsX: 1, BoundsY: 2, BoundsWidth: 3, BoundsHeight: 1, SizeChanged: true, FormatChanged: true, AlphaChanged: true},
	)
	out, err := svc.ImageDiff(ctx, "/work/game", &ImageRef{Source: "snapshot", Path: "a.png"}, &ImageRef{Source: "working", Path: "a.tga"}, 1024)
	require.NoError(t, err)
	require.Equal(t, "snapshot", d.last.GetOld().GetSource())
	require.Equal(t, "a.tga", d.last.GetNew().GetPath())
	require.EqualValues(t, 1024, d.last.GetMaxSize())
	require.Equal(t, &ImageInfo{Width: 4, Height: 2, Format: "png", BitDepth: 8, Size: 90, PreviewWidth: 4, PreviewHeight: 2, Src: "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("abc"))}, out.Old)
	require.Equal(t, "data:image/png;base64,"+base64.StdEncoding.EncodeToString([]byte("xyz")), out.New.Src)
	require.True(t, out.New.HasAlpha)
	require.Equal(t, &ImageStats{ChangedPixels: 3, TotalPixels: 32, ChangedRatio: 0.09375, MaxDelta: 255, BoundsX: 1, BoundsY: 2, BoundsWidth: 3, BoundsHeight: 1, SizeChanged: true, FormatChanged: true, AlphaChanged: true}, out.Stats)
}

func TestService_ImageDiffOneSide(t *testing.T) {
	svc, d := newPreview(t)
	d.events = previewEvents(&daemonpb.ImagePreview{Side: "new", Width: 1, Height: 1, Format: "png"}, &daemonpb.PreviewData{Side: "new", Png: []byte("p")})
	out, err := svc.ImageDiff(ctx, "/work/game", nil, &ImageRef{Source: "working", Path: "a.png"}, 0)
	require.NoError(t, err)
	require.Nil(t, d.last.GetOld())
	require.Nil(t, out.Old)
	require.Nil(t, out.Stats)
	require.NotEmpty(t, out.New.Src)
}

func TestService_ImageDiffFailures(t *testing.T) {
	svc, d := newPreview(t)
	_, err := svc.ImageDiff(ctx, "/work/game", nil, nil, 0)
	require.EqualError(t, err, "nothing to compare")

	ref := &ImageRef{Source: "working", Path: "a.dds"}
	d.events = previewEvents(&daemonpb.OpFailure{Code: 415, Message: "a.dds: unsupported image format"})
	out, err := svc.ImageDiff(ctx, "/work/game", nil, ref, 0)
	require.NoError(t, err)
	require.Equal(t, &ImageCompare{Unsupported: "a.dds: unsupported image format"}, out)

	d.events = previewEvents(&daemonpb.OpFailure{Code: 404, Message: "not found"})
	_, err = svc.ImageDiff(ctx, "/work/game", nil, ref, 0)
	require.EqualError(t, err, "not found")

	d.err = status.Error(codes.Internal, "no stream")
	_, err = svc.ImageDiff(ctx, "/work/game", nil, ref, 0)
	require.EqualError(t, err, "no stream")

	d.err = nil
	d.watchErr["/work/lost"] = status.Error(codes.InvalidArgument, "not a clone")
	_, err = svc.ImageDiff(ctx, "/work/lost", nil, ref, 0)
	require.EqualError(t, err, "not a clone")
}

type erroringPreview struct {
	*previewDaemon
}

func (e *erroringPreview) Preview(context.Context, *daemonpb.PreviewRequest, ...grpc.CallOption) (grpc.ServerStreamingClient[daemonpb.PreviewEvent], error) {
	return &listStream[daemonpb.PreviewEvent]{err: status.Error(codes.Unavailable, "daemon gone")}, nil
}

func TestService_ImageDiffStreamError(t *testing.T) {
	_, inner := newPreview(t)
	d := &erroringPreview{previewDaemon: inner}
	svc := New(func(context.Context) (Session, error) { return &opsSession{client: d}, nil }, &memStore{}, nil)
	defer func() { _ = svc.ServiceShutdown() }()
	_, err := svc.ImageDiff(ctx, "/work/game", nil, &ImageRef{Source: "working", Path: "a.png"}, 0)
	require.EqualError(t, err, "daemon gone")
}

func encodePNG(t *testing.T, w, h int, fill color.NRGBA, mark *image.Point) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetNRGBA(x, y, fill)
		}
	}
	if mark != nil {
		img.SetNRGBA(mark.X, mark.Y, color.NRGBA{B: 255, A: 255})
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

func TestImageDiffWithRealDaemon(t *testing.T) {
	endpoint := startRealDaemon(t)
	red := color.NRGBA{R: 255, A: 255}
	root := seedClone(t, "hero.png", string(encodePNG(t, 4, 4, red, nil)))
	require.NoError(t, os.WriteFile(filepath.Join(root, "hero.png"), encodePNG(t, 4, 4, red, &image.Point{X: 2, Y: 1}), 0o644))

	svc := newRealService(endpoint)
	defer func() { _ = svc.ServiceShutdown() }()
	out, err := svc.ImageDiff(ctx, root, &ImageRef{Source: "snapshot", Path: "hero.png"}, &ImageRef{Source: "working", Path: "hero.png"}, 0)
	require.NoError(t, err)
	require.Equal(t, 4, out.Old.Width)
	require.Equal(t, "png", out.New.Format)
	require.True(t, strings.HasPrefix(out.New.Src, "data:image/png;base64,"))
	require.EqualValues(t, 1, out.Stats.ChangedPixels)
	require.Equal(t, 2, out.Stats.BoundsX)

	require.NoError(t, os.WriteFile(filepath.Join(root, "notes.txt"), []byte("hi"), 0o644))
	out, err = svc.ImageDiff(ctx, root, nil, &ImageRef{Source: "working", Path: "notes.txt"}, 0)
	require.NoError(t, err)
	require.NotEmpty(t, out.Unsupported)
}
