package service

import (
	"context"
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
