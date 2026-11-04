package service

import (
	"context"
	"encoding/base64"
	"errors"
	"math"
	"strings"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/preview"
)

const (
	maxAudioBytes  = 256 << 20
	playbackBudget = 48 << 20
)

type AudioStats struct {
	Codec      string   `json:"codec"`
	SampleRate int      `json:"sampleRate"`
	Channels   int      `json:"channels"`
	BitDepth   int      `json:"bitDepth"`
	Frames     int64    `json:"frames"`
	Duration   float64  `json:"duration"`
	PeakDB     *float64 `json:"peakDb"` // nil for digital silence
	RMSDB      *float64 `json:"rmsDb"`
	Loudness   *float64 `json:"loudness"` // integrated LUFS, nil when too short or silent
	Clipped    int64    `json:"clipped"`
	Truncated  bool     `json:"truncated"`
}

// AudioSide is one analysed version of an audio file. Peaks holds min/max
// pairs per bucket and channel, Spectrogram is base64 bytes, SpecRows rows of
// SpecColumns (row 0 lowest), Playback a base64 16-bit WAV.
type AudioSide struct {
	Path              string      `json:"path"`
	Format            string      `json:"format"`
	Size              int64       `json:"size"`
	Stats             *AudioStats `json:"stats"`
	Error             string      `json:"error,omitempty"`
	TooLarge          bool        `json:"tooLarge,omitempty"`
	Peaks             [][]float32 `json:"peaks"`
	Spectrogram       string      `json:"spectrogram"`
	SpecColumns       int         `json:"specColumns"`
	SpecRows          int         `json:"specRows"`
	SpecMinHz         float64     `json:"specMinHz"`
	SpecMaxHz         float64     `json:"specMaxHz"`
	Playback          string      `json:"playback"`
	PlaybackTruncated bool        `json:"playbackTruncated,omitempty"`
}

type AudioCompare struct {
	Old *AudioSide `json:"old"`
	New *AudioSide `json:"new"`
}

// AudioDiff decodes the old and/or new version of an audio file into
// waveforms, spectrograms, loudness stats and playable WAV copies.
func (s *Service) AudioDiff(ctx context.Context, root string, old, new *ImageRef) (*AudioCompare, error) {
	if old == nil && new == nil {
		return nil, errors.New("nothing to compare")
	}
	client, err := s.client(ctx, root)
	if err != nil {
		return nil, err
	}
	out := &AudioCompare{}
	for _, side := range []struct {
		ref  *ImageRef
		dest **AudioSide
	}{{old, &out.Old}, {new, &out.New}} {
		if side.ref == nil {
			continue
		}
		a, err := s.loadAudio(ctx, client, root, side.ref)
		if err != nil {
			return nil, err
		}
		*side.dest = a
	}
	return out, nil
}

func (s *Service) loadAudio(ctx context.Context, client daemonpb.NipaDaemonClient, root string, ref *ImageRef) (*AudioSide, error) {
	side := &AudioSide{Path: ref.Path, Format: modelFormat(ref.Path), Peaks: [][]float32{}}
	info, data, err := s.readBlob(ctx, client, root, blobRef(ref), maxAudioBytes)
	if errors.Is(err, errBlobTooLarge) {
		side.Size, side.TooLarge, side.Error = info.GetSizeBytes(), true, "the file is too large to read"
		return side, nil
	}
	if err != nil {
		return nil, err
	}
	side.Size = int64(len(data))
	a, err := preview.DecodeAudio(ref.Path, data, preview.AudioOptions{PlaybackBytes: playbackBudget})
	if err != nil {
		side.Error = strings.TrimPrefix(err.Error(), "preview: ")
		return side, nil
	}
	st := a.Stats
	side.Format = st.Format
	side.Stats = &AudioStats{
		Codec: st.Codec, SampleRate: st.SampleRate, Channels: st.Channels, BitDepth: st.BitDepth,
		Frames: st.Frames, Duration: st.Duration, PeakDB: decibels(st.Peak), RMSDB: decibels(st.RMS),
		Loudness: finite(st.Loudness), Clipped: st.Clipped, Truncated: st.Truncated,
	}
	side.Peaks = a.Peaks
	side.Spectrogram = base64.StdEncoding.EncodeToString(a.Spectrogram)
	side.SpecColumns, side.SpecRows, side.SpecMinHz, side.SpecMaxHz = a.SpecColumns, a.SpecRows, a.SpecMinHz, a.SpecMaxHz
	side.Playback = base64.StdEncoding.EncodeToString(a.Playback)
	side.PlaybackTruncated = a.PlaybackTruncated
	return side, nil
}

func decibels(linear float64) *float64 {
	if linear <= 0 {
		return nil
	}
	return finite(20 * math.Log10(linear))
}

func finite(v float64) *float64 {
	if math.IsInf(v, 0) || math.IsNaN(v) {
		return nil
	}
	return &v
}
