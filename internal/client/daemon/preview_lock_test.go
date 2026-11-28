package daemon

import (
	"bytes"
	"context"
	"image/color"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/diff"
	"github.com/nipalab/nipa/internal/preview"
)

func stubDecoders(t *testing.T, img func(string, []byte, preview.Select) (*preview.Image, error), thumb func(string, []byte) (*preview.Image, error)) {
	t.Helper()
	oldImg, oldThumb := decodeImageAt, decodeThumbnail
	if img != nil {
		decodeImageAt = img
	}
	if thumb != nil {
		decodeThumbnail = thumb
	}
	t.Cleanup(func() { decodeImageAt, decodeThumbnail = oldImg, oldThumb })
}

// serialBlobServer serves files through a SerialDiff-wrapped runner, like the
// real daemon wiring, and returns the serial runner so tests can contend for
// its lock.
func serialBlobServer(t *testing.T, files map[string][]byte) (*Server, string, DiffRunner) {
	t.Helper()
	serial := SerialDiff(&stubDiffRunner{readBlob: func(_ context.Context, _ string, req usecase.BlobRequest, fn func(diff.Entry, io.Reader) error) error {
		return fn(diff.Entry{Path: req.Path}, bytes.NewReader(files[req.Path]))
	}})
	root := newTestClone(t)
	srv := newOpServer(t, RepoOps{Diff: serial})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)
	return srv, root, serial
}

func TestServer_PreviewDecodesOutsideDiffLock(t *testing.T) {
	data := pngBytes(t, solidImage(4, 4, color.NRGBA{A: 255}))
	srv, root, serial := serialBlobServer(t, map[string][]byte{"a.png": data, "b.blend": data})

	// The decoder only returns once another caller got through the serial
	// lock; if decoding ran inside ReadBlob this would never happen.
	lockFree := func() error {
		done := make(chan error, 1)
		go func() {
			done <- serial.ReadBlob(context.Background(), root, usecase.BlobRequest{Path: "a.png"}, func(diff.Entry, io.Reader) error { return nil })
		}()
		select {
		case err := <-done:
			return err
		case <-time.After(5 * time.Second):
			t.Error("decode ran while the diff lock was held")
			return nil
		}
	}
	stubDecoders(t,
		func(path string, b []byte, sel preview.Select) (*preview.Image, error) {
			require.NoError(t, lockFree())
			return preview.DecodeImageAt(path, b, sel)
		},
		func(string, []byte) (*preview.Image, error) {
			require.NoError(t, lockFree())
			return preview.DecodeImageAt("x.png", data, preview.Select{})
		})

	stream := previewStream()
	require.NoError(t, srv.Preview(&daemonpb.PreviewRequest{
		Root: root,
		Old:  &daemonpb.BlobRef{Path: "a.png"},
		New:  &daemonpb.BlobRef{Path: "b.blend"},
	}, stream))
	got := collectPreview(stream.all())
	require.Nil(t, got.failure)
	require.Len(t, got.images, 2)
	require.True(t, got.images["new"].GetThumbnail())
	require.NotNil(t, got.diff)
}

func TestServer_PreviewCancelledStopsBeforeSecondDecode(t *testing.T) {
	data := pngBytes(t, solidImage(4, 4, color.NRGBA{A: 255}))
	srv, root, _ := serialBlobServer(t, map[string][]byte{"a.png": data, "b.png": data})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var decoded []string
	stubDecoders(t, func(path string, b []byte, sel preview.Select) (*preview.Image, error) {
		decoded = append(decoded, path)
		cancel()
		return preview.DecodeImageAt(path, b, sel)
	}, nil)

	stream := &eventStream[daemonpb.PreviewEvent]{ctx: ctx}
	err := srv.Preview(&daemonpb.PreviewRequest{
		Root: root,
		Old:  &daemonpb.BlobRef{Path: "a.png"},
		New:  &daemonpb.BlobRef{Path: "b.png"},
	}, stream)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, []string{"a.png"}, decoded)
	require.Empty(t, stream.all(), "nothing is sent once cancelled")
}

func TestServer_PreviewCancelledAfterReadSkipsDecode(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root := newTestClone(t)
	srv := newOpServer(t, RepoOps{Diff: SerialDiff(&stubDiffRunner{readBlob: func(_ context.Context, _ string, req usecase.BlobRequest, fn func(diff.Entry, io.Reader) error) error {
		err := fn(diff.Entry{Path: req.Path}, bytes.NewReader([]byte("png")))
		cancel()
		return err
	}})})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)
	stubDecoders(t, func(string, []byte, preview.Select) (*preview.Image, error) {
		t.Error("decode must not run after cancellation")
		return nil, nil
	}, nil)

	err = srv.Preview(&daemonpb.PreviewRequest{Root: root, New: &daemonpb.BlobRef{Path: "a.png"}}, &eventStream[daemonpb.PreviewEvent]{ctx: ctx})
	require.ErrorIs(t, err, context.Canceled)
}

func TestServer_PreviewCancelledBeforeCompare(t *testing.T) {
	data := pngBytes(t, solidImage(4, 4, color.NRGBA{A: 255}))
	srv, root, _ := serialBlobServer(t, map[string][]byte{"a.png": data, "b.png": data})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := &cancelOnSend{eventStream: eventStream[daemonpb.PreviewEvent]{ctx: ctx}, cancel: cancel, after: 1}
	err := srv.Preview(&daemonpb.PreviewRequest{
		Root: root,
		Old:  &daemonpb.BlobRef{Path: "a.png"},
		New:  &daemonpb.BlobRef{Path: "b.png"},
	}, stream)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, collectPreview(stream.all()).diff)
}

// cancelOnSend cancels its context once `after` events were sent.
type cancelOnSend struct {
	eventStream[daemonpb.PreviewEvent]
	cancel func()
	after  int
}

func (s *cancelOnSend) Send(ev *daemonpb.PreviewEvent) error {
	err := s.eventStream.Send(ev)
	if len(s.all()) >= s.after {
		s.cancel()
	}
	return err
}
