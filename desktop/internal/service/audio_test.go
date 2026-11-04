package service

import (
	"encoding/base64"
	"encoding/binary"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func toneWAV(rate int, seconds, amp float64) string {
	n := int(seconds * float64(rate))
	out := []byte("RIFF\x00\x00\x00\x00WAVEfmt ")
	out = binary.LittleEndian.AppendUint32(out, 16)
	out = binary.LittleEndian.AppendUint16(out, 1)
	out = binary.LittleEndian.AppendUint16(out, 1)
	out = binary.LittleEndian.AppendUint32(out, uint32(rate))
	out = binary.LittleEndian.AppendUint32(out, uint32(rate*2))
	out = binary.LittleEndian.AppendUint16(out, 2)
	out = binary.LittleEndian.AppendUint16(out, 16)
	out = append(out, "data"...)
	out = binary.LittleEndian.AppendUint32(out, uint32(2*n))
	for i := 0; i < n; i++ {
		v := amp * math.Sin(2*math.Pi*1000*float64(i)/float64(rate))
		out = binary.LittleEndian.AppendUint16(out, uint16(int16(math.Round(v*32767))))
	}
	return string(out)
}

func TestService_AudioDiff(t *testing.T) {
	svc, d := newModels(t, map[string]string{
		"sfx/hit.wav":     toneWAV(8000, 1, 0.5),
		"sfx/old/hit.wav": toneWAV(8000, 0.5, 0),
		"sfx/bad.wav":     "RIFFnope",
	})
	out, err := svc.AudioDiff(ctx, "/work/game", &ImageRef{Source: "snapshot", Path: "sfx/old/hit.wav"}, &ImageRef{Source: "working", Path: "sfx/hit.wav"})
	require.NoError(t, err)
	n := out.New
	require.NotNil(t, n.Stats)
	assert.Equal(t, "wav", n.Format)
	assert.Equal(t, "PCM 16-bit", n.Stats.Codec)
	assert.Equal(t, 8000, n.Stats.SampleRate)
	assert.Equal(t, int64(8000), n.Stats.Frames)
	require.NotNil(t, n.Stats.PeakDB)
	assert.InDelta(t, -6.02, *n.Stats.PeakDB, 0.01)
	require.NotNil(t, n.Stats.Loudness)
	assert.Len(t, n.Peaks, 1)
	spec, err := base64.StdEncoding.DecodeString(n.Spectrogram)
	require.NoError(t, err)
	assert.Len(t, spec, n.SpecColumns*n.SpecRows)
	wav, err := base64.StdEncoding.DecodeString(n.Playback)
	require.NoError(t, err)
	assert.Equal(t, "RIFF", string(wav[:4]))
	assert.Equal(t, int64(len(toneWAV(8000, 1, 0.5))), n.Size)

	o := out.Old
	require.NotNil(t, o.Stats)
	assert.Nil(t, o.Stats.PeakDB)
	assert.Nil(t, o.Stats.RMSDB)
	assert.Nil(t, o.Stats.Loudness)
	assert.Equal(t, "snapshot", d.reads[0].GetBlob().GetSource())

	out, err = svc.AudioDiff(ctx, "/work/game", nil, &ImageRef{Source: "working", Path: "sfx/bad.wav"})
	require.NoError(t, err)
	assert.Nil(t, out.Old)
	assert.Nil(t, out.New.Stats)
	assert.Contains(t, out.New.Error, "invalid audio")

	d.sizes["sfx/hit.wav"] = maxAudioBytes + 1
	out, err = svc.AudioDiff(ctx, "/work/game", nil, &ImageRef{Source: "working", Path: "sfx/hit.wav"})
	require.NoError(t, err)
	assert.True(t, out.New.TooLarge)
	assert.Equal(t, int64(maxAudioBytes+1), out.New.Size)

	_, err = svc.AudioDiff(ctx, "/work/game", nil, nil)
	assert.Error(t, err)

	d.err = status.Error(codes.Internal, "boom")
	_, err = svc.AudioDiff(ctx, "/work/game", nil, &ImageRef{Source: "working", Path: "sfx/hit.wav"})
	assert.ErrorContains(t, err, "boom")
}

func TestDecibels(t *testing.T) {
	assert.Nil(t, decibels(0))
	assert.InDelta(t, 0, *decibels(1), 1e-9)
	assert.Nil(t, finite(math.NaN()))
	assert.Nil(t, finite(math.Inf(-1)))
}
