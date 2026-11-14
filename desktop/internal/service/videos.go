package service

import (
	"context"
	"errors"
	"os"
	"strings"

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

// VideoDiff copies the old and/or new version of a video to a temp file the
// webview plays through MediaMiddleware, with container stats read in Go.
func (s *Service) VideoDiff(ctx context.Context, root string, old, new *ImageRef) (*VideoCompare, error) {
	if old == nil && new == nil {
		return nil, errors.New("nothing to compare")
	}
	client, err := s.client(ctx, root)
	if err != nil {
		return nil, err
	}
	out := &VideoCompare{}
	for _, side := range []struct {
		ref  *ImageRef
		dest **VideoSide
	}{{old, &out.Old}, {new, &out.New}} {
		if side.ref == nil {
			continue
		}
		v, err := s.loadVideo(ctx, client, root, side.ref)
		if err != nil {
			return nil, err
		}
		*side.dest = v
	}
	return out, nil
}

func (s *Service) loadVideo(ctx context.Context, client daemonpb.NipaDaemonClient, root string, ref *ImageRef) (*VideoSide, error) {
	side := &VideoSide{Path: ref.Path, Format: modelFormat(ref.Path)}
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
	if stats, err := preview.ProbeVideo(ref.Path, f, side.Size); err != nil {
		side.StatsError = strings.TrimPrefix(err.Error(), "preview: ")
	} else {
		side.Stats = &VideoStats{
			Container: stats.Container, Duration: stats.Duration, Width: stats.Width, Height: stats.Height,
			VideoCodec: stats.VideoCodec, FrameRate: stats.FrameRate, AudioCodec: stats.AudioCodec,
			SampleRate: stats.SampleRate, Channels: stats.Channels, Bitrate: stats.Bitrate, Tracks: stats.Tracks,
		}
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	keep = true
	side.Media = s.media.commit(f, ref.Path)
	side.URL = mediaURL(side.Media, ref.Path)
	return side, nil
}
