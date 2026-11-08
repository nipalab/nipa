package assetdiff

import (
	"math"

	"github.com/nipalab/nipa/internal/preview"
)

type AudioResult struct {
	Old   *AudioSide  `json:"old,omitempty"`
	New   *AudioSide  `json:"new,omitempty"`
	Delta *AudioDelta `json:"delta,omitempty"`
}

// AudioSide holds levels in dB; a nil level is silence or too short to
// measure.
type AudioSide struct {
	Format     string   `json:"format"`
	Codec      string   `json:"codec"`
	SampleRate int      `json:"sample_rate"`
	Channels   int      `json:"channels"`
	BitDepth   int      `json:"bit_depth"`
	Frames     int64    `json:"frames"`
	Duration   float64  `json:"duration_seconds"`
	PeakDB     *float64 `json:"peak_db"`
	RMSDB      *float64 `json:"rms_db"`
	Loudness   *float64 `json:"loudness_lufs"`
	Clipped    int64    `json:"clipped_samples"`
	Truncated  bool     `json:"truncated,omitempty"`
}

type AudioDelta struct {
	Duration      float64  `json:"duration_seconds"`
	PeakDB        *float64 `json:"peak_db,omitempty"`
	Loudness      *float64 `json:"loudness_lufs,omitempty"`
	Clipped       int64    `json:"clipped_samples"`
	FormatChanged bool     `json:"format_changed"`
}

var minimalAudio = preview.AudioOptions{Buckets: 1, SpecColumns: 1, SpecRows: 1}

func compareAudio(sides [2]side) (*AudioResult, error) {
	out := &AudioResult{}
	for i, s := range sides {
		if !s.present {
			continue
		}
		a, err := preview.DecodeAudio(s.ref.Path, s.data, minimalAudio)
		if err != nil {
			return nil, err
		}
		st := a.Stats
		as := &AudioSide{
			Format: st.Format, Codec: st.Codec, SampleRate: st.SampleRate, Channels: st.Channels, BitDepth: st.BitDepth,
			Frames: st.Frames, Duration: st.Duration, PeakDB: decibels(st.Peak), RMSDB: decibels(st.RMS),
			Loudness: finite(st.Loudness), Clipped: st.Clipped, Truncated: st.Truncated,
		}
		if i == 0 {
			out.Old = as
		} else {
			out.New = as
		}
	}
	if out.Old == nil || out.New == nil {
		return out, nil
	}
	o, n := out.Old, out.New
	out.Delta = &AudioDelta{
		Duration: n.Duration - o.Duration, PeakDB: sub(n.PeakDB, o.PeakDB), Loudness: sub(n.Loudness, o.Loudness),
		Clipped:       n.Clipped - o.Clipped,
		FormatChanged: o.Format != n.Format || o.Codec != n.Codec || o.SampleRate != n.SampleRate || o.Channels != n.Channels || o.BitDepth != n.BitDepth,
	}
	return out, nil
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

func sub(a, b *float64) *float64 {
	if a == nil || b == nil {
		return nil
	}
	v := *a - *b
	return &v
}
