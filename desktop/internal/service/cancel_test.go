package service

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// Every blob-backed viewer stops once the frontend cancels its call: a read
// in progress aborts, a read that finished after the cancel is not decoded,
// and the other side is never requested. Video copies both sides at once,
// so both may have started; neither leaves a media file behind.
func TestService_ViewersStopOnCancel(t *testing.T) {
	viewers := map[string]func(ctx context.Context, svc *Service, old, new *ImageRef) error{
		"audio": func(ctx context.Context, svc *Service, old, new *ImageRef) error {
			_, err := svc.AudioDiff(ctx, "/work/game", old, new)
			return err
		},
		"video": func(ctx context.Context, svc *Service, old, new *ImageRef) error {
			_, err := svc.VideoDiff(ctx, "/work/game", old, new)
			return err
		},
		"model": func(ctx context.Context, svc *Service, old, new *ImageRef) error {
			_, err := svc.ModelDiff(ctx, "/work/game", old, new)
			return err
		},
		"scene": func(ctx context.Context, svc *Service, old, new *ImageRef) error {
			_, err := svc.SceneDiff(ctx, "/work/game", old, new)
			return err
		},
		"table": func(ctx context.Context, svc *Service, old, new *ImageRef) error {
			_, err := svc.TableDiff(ctx, "/work/game", old, new, "")
			return err
		},
		"font": func(ctx context.Context, svc *Service, old, new *ImageRef) error {
			_, err := svc.FontDiff(ctx, "/work/game", old, new)
			return err
		},
	}
	for name, run := range viewers {
		for _, when := range []string{"during read", "after read"} {
			t.Run(name+" "+when, func(t *testing.T) {
				svc, d := newModels(t, map[string]string{"a.bin": "old bytes", "b.bin": "new bytes"})
				callCtx, cancel := context.WithCancel(ctx)
				defer cancel()
				// Bind the session before cancelling, so only the reads see it.
				_, err := svc.client(callCtx, "/work/game")
				require.NoError(t, err)
				if when == "during read" {
					d.onRead = func(string) { cancel() }
				} else {
					d.onDrained = func(string) { cancel() }
				}
				err = run(callCtx, svc, &ImageRef{Path: "a.bin"}, &ImageRef{Path: "b.bin"})
				require.ErrorIs(t, err, context.Canceled)
				if name == "video" {
					require.LessOrEqual(t, len(d.reads), 2)
				} else {
					require.Len(t, d.reads, 1, "the second side is never read")
				}
				require.Empty(t, svc.media.files, "a cancelled call publishes no media")
			})
		}
	}
}

// A cancel that lands while a viewer publishes its media files (after the
// last read, so no read reports it) still fails the call and drops every
// file it published: the frontend discards a cancelled result and would
// never release them. Each case cancels on the nth publish.
func TestService_ViewersDropMediaCancelledWhilePublishing(t *testing.T) {
	files := map[string]string{
		"a.gltf": heroGLTF, "hero.bin": "B", "tex/skin.png": "P",
		"hit.wav": toneWAV(8000, 0.1, 0.5), "clip.mp4": "not really a video",
	}
	cases := []struct {
		name  string
		nth   int
		run   func(ctx context.Context, svc *Service) error
		files int
	}{
		{"model main file", 1, func(ctx context.Context, svc *Service) error {
			_, err := svc.ModelDiff(ctx, "/w", nil, &ImageRef{Source: "snapshot", Path: "a.gltf"})
			return err
		}, 1},
		{"model texture", 2, func(ctx context.Context, svc *Service) error {
			_, err := svc.ModelDiff(ctx, "/w", nil, &ImageRef{Source: "snapshot", Path: "a.gltf"})
			return err
		}, 2},
		{"model last file", 3, func(ctx context.Context, svc *Service) error {
			_, err := svc.ModelDiff(ctx, "/w", nil, &ImageRef{Source: "snapshot", Path: "a.gltf"})
			return err
		}, 3},
		{"audio", 1, func(ctx context.Context, svc *Service) error {
			_, err := svc.AudioDiff(ctx, "/w", nil, &ImageRef{Source: "snapshot", Path: "hit.wav"})
			return err
		}, 1},
		{"audio second side", 2, func(ctx context.Context, svc *Service) error {
			_, err := svc.AudioDiff(ctx, "/w", &ImageRef{Source: "snapshot", Path: "hit.wav"}, &ImageRef{Source: "snapshot", Path: "hit.wav"})
			return err
		}, 2},
		{"video", 1, func(ctx context.Context, svc *Service) error {
			_, err := svc.VideoDiff(ctx, "/w", nil, &ImageRef{Source: "snapshot", Path: "clip.mp4"})
			return err
		}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := newModels(t, files)
			callCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			_, err := svc.client(callCtx, "/w")
			require.NoError(t, err)
			published := 0
			svc.media.onAdd = func(string) {
				published++
				if published == tc.nth {
					cancel()
				}
			}
			require.ErrorIs(t, tc.run(callCtx, svc), context.Canceled)
			require.Equal(t, tc.files, published, "publishing stops at the cancel")
			require.Empty(t, svc.media.files, "a cancelled call publishes no media")
			entries, err := os.ReadDir(svc.media.dir)
			require.NoError(t, err)
			require.Empty(t, entries, "the written files are removed")
		})
	}
}
