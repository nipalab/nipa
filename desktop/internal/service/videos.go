package service

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/preview"
)

const maxVideoBytes = 4 << 30

type VideoStats struct {
	Container  string  `json:"container"`
	Duration   float64 `json:"duration"`
	Width      int     `json:"width"`
	Height     int     `json:"height"`
	VideoCodec string  `json:"videoCodec"`
	FrameRate  float64 `json:"frameRate"`
	AudioCodec string  `json:"audioCodec"`
	SampleRate int     `json:"sampleRate"`
	Channels   int     `json:"channels"`
	Bitrate    int64   `json:"bitrate"`
	Tracks     int     `json:"tracks"`
}

// VideoSide is one version of a video: its container stats and the URL the
// webview streams it from (Media is the token OpenMedia takes).
type VideoSide struct {
	Path       string      `json:"path"`
	Format     string      `json:"format"`
	Size       int64       `json:"size"`
	Stats      *VideoStats `json:"stats"`
	StatsError string      `json:"statsError,omitempty"`
	URL        string      `json:"url"`
	Media      string      `json:"media"`
	TooLarge   bool        `json:"tooLarge,omitempty"`
}

type VideoCompare struct {
	Old *VideoSide `json:"old"`
	New *VideoSide `json:"new"`
}

// VideoDiff prepares the old and/or new version of a video for the webview,
// which plays them through MediaMiddleware, with container stats read in Go.
// A working-copy side is served in place; stored sides are copied to temp
// files, both at once.
func (s *Service) VideoDiff(ctx context.Context, root string, old, new *ImageRef) (*VideoCompare, error) {
	if old == nil && new == nil {
		return nil, errors.New("nothing to compare")
	}
	client, err := s.client(ctx, root)
	if err != nil {
		return nil, err
	}
	// The first failure cancels the other side's copy.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	refs := [2]*ImageRef{old, new}
	var sides [2]*VideoSide
	var errs [2]error
	var wg sync.WaitGroup
	for i, ref := range refs {
		if ref == nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sides[i], errs[i] = s.loadVideo(ctx, client, root, ref)
			if errs[i] != nil {
				cancel()
			}
		}()
	}
	wg.Wait()
	if err := firstVideoError(errs); err != nil {
		for _, side := range sides {
			if side != nil && side.Media != "" {
				s.media.drop(side.Media)
			}
		}
		return nil, err
	}
	return &VideoCompare{Old: sides[0], New: sides[1]}, nil
}

// firstVideoError prefers the error that caused the cancel over the
// context.Canceled it then produced on the other side.
func firstVideoError(errs [2]error) error {
	var canceled error
	for _, err := range errs {
		switch {
		case err == nil:
		case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
			canceled = err
		default:
			return err
		}
	}
	if canceled != nil {
		return canceled
	}
	return nil
}

func (s *Service) loadVideo(ctx context.Context, client daemonpb.NipaDaemonClient, root string, ref *ImageRef) (*VideoSide, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	side := &VideoSide{Path: ref.Path, Format: modelFormat(ref.Path)}
	// A working-copy file is already on disk: serve it where it is. When it
	// cannot be served in place (missing, outside the clone) the daemon read
	// below reports why.
	if isWorking(ref) {
		if full, size, err := resolveWorkingFile(root, ref.Path); err == nil {
			f, err := os.Open(full)
			if err == nil {
				side.Size = size
				probeVideo(side, f)
				_ = f.Close()
				side.Media = s.media.register(full, ref.Path)
				side.URL = mediaURL(side.Media, ref.Path)
				return side, nil
			}
		}
	}
	f, err := s.media.create(ref.Path)
	if err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = f.Close()
			_ = os.Remove(f.Name())
		}
	}()
	info, err := s.copyBlob(ctx, client, root, blobRef(ref), maxVideoBytes, f)
	if errors.Is(err, errBlobTooLarge) {
		side.Size, side.TooLarge, side.StatsError = info.GetSizeBytes(), true, "the video is too large to preview"
		return side, nil
	}
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	side.Size = st.Size()
	probeVideo(side, f)
	if err := f.Close(); err != nil {
		return nil, err
	}
	keep = true
	side.Media = s.media.commit(f, ref.Path)
	side.URL = mediaURL(side.Media, ref.Path)
	return side, nil
}

func probeVideo(side *VideoSide, f *os.File) {
	stats, err := preview.ProbeVideo(side.Path, f, side.Size)
	if err != nil {
		side.StatsError = strings.TrimPrefix(err.Error(), "preview: ")
		return
	}
	side.Stats = &VideoStats{
		Container: stats.Container, Duration: stats.Duration, Width: stats.Width, Height: stats.Height,
		VideoCodec: stats.VideoCodec, FrameRate: stats.FrameRate, AudioCodec: stats.AudioCodec,
		SampleRate: stats.SampleRate, Channels: stats.Channels, Bitrate: stats.Bitrate, Tracks: stats.Tracks,
	}
}
