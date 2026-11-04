package preview

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type wavSpec struct {
	tag        uint16
	channels   int
	rate       int
	bits       int
	width      int
	extensible bool
	pad        bool
}

func sine(rate, channels int, seconds, freq, amp float64) []float64 {
	n := int(seconds * float64(rate))
	out := make([]float64, 0, n*channels)
	for i := 0; i < n; i++ {
		v := amp * math.Sin(2*math.Pi*freq*float64(i)/float64(rate))
		for c := 0; c < channels; c++ {
			out = append(out, v)
		}
	}
	return out
}

func encodeSample(dst []byte, v float64, tag uint16, width int, order binary.AppendByteOrder) []byte {
	if tag == 3 {
		if width == 8 {
			return order.AppendUint64(dst, math.Float64bits(v))
		}
		return order.AppendUint32(dst, math.Float32bits(float32(v)))
	}
	switch width {
	case 1:
		return append(dst, byte(int(math.Round(v*127))+128))
	case 2:
		return order.AppendUint16(dst, uint16(int16(math.Round(v*32767))))
	case 3:
		x := int32(math.Round(v * 8388607))
		if order == binary.AppendByteOrder(binary.BigEndian) {
			return append(dst, byte(x>>16), byte(x>>8), byte(x))
		}
		return append(dst, byte(x), byte(x>>8), byte(x>>16))
	}
	return order.AppendUint32(dst, uint32(int32(math.Round(v*2147483647))))
}

func makeWAV(spec wavSpec, samples []float64) []byte {
	var body []byte
	for _, v := range samples {
		body = encodeSample(body, v, spec.tag, spec.width, binary.LittleEndian)
	}
	align := spec.channels * spec.width
	fmtChunk := binary.LittleEndian.AppendUint16(nil, spec.tag)
	if spec.extensible {
		fmtChunk = binary.LittleEndian.AppendUint16(nil, 0xFFFE)
	}
	fmtChunk = binary.LittleEndian.AppendUint16(fmtChunk, uint16(spec.channels))
	fmtChunk = binary.LittleEndian.AppendUint32(fmtChunk, uint32(spec.rate))
	fmtChunk = binary.LittleEndian.AppendUint32(fmtChunk, uint32(spec.rate*align))
	fmtChunk = binary.LittleEndian.AppendUint16(fmtChunk, uint16(align))
	fmtChunk = binary.LittleEndian.AppendUint16(fmtChunk, uint16(spec.bits))
	if spec.extensible {
		fmtChunk = binary.LittleEndian.AppendUint16(fmtChunk, 22)
		fmtChunk = binary.LittleEndian.AppendUint16(fmtChunk, uint16(spec.bits))
		fmtChunk = binary.LittleEndian.AppendUint32(fmtChunk, 3)
		fmtChunk = binary.LittleEndian.AppendUint16(fmtChunk, spec.tag)
		fmtChunk = append(fmtChunk, make([]byte, 14)...)
	}
	out := []byte("RIFF\x00\x00\x00\x00WAVE")
	out = appendChunk(out, "fmt ", fmtChunk, binary.LittleEndian)
	if spec.pad {
		out = appendChunk(out, "LIST", []byte("odd"), binary.LittleEndian)
	}
	out = appendChunk(out, "data", body, binary.LittleEndian)
	binary.LittleEndian.PutUint32(out[4:], uint32(len(out)-8))
	return out
}

func appendChunk(out []byte, id string, body []byte, order binary.AppendByteOrder) []byte {
	out = append(out, id...)
	out = order.AppendUint32(out, uint32(len(body)))
	out = append(out, body...)
	if len(body)%2 == 1 {
		out = append(out, 0)
	}
	return out
}

func extended(rate float64) []byte {
	exp, mant := 0, rate
	for mant >= 2 {
		mant /= 2
		exp++
	}
	out := binary.BigEndian.AppendUint16(nil, uint16(exp+16383))
	return binary.BigEndian.AppendUint64(out, uint64(mant*math.Ldexp(1, 63)))
}

func makeAIFF(form, compression string, tag uint16, channels, rate, bits int, samples []float64) []byte {
	width := (bits + 7) / 8
	order := binary.AppendByteOrder(binary.BigEndian)
	if compression == "sowt" {
		order = binary.LittleEndian
	}
	sound := make([]byte, 8)
	for _, v := range samples {
		if width == 1 {
			sound = append(sound, byte(int8(math.Round(v*127))))
			continue
		}
		sound = encodeSample(sound, v, tag, width, order)
	}
	comm := binary.BigEndian.AppendUint16(nil, uint16(channels))
	comm = binary.BigEndian.AppendUint32(comm, uint32(len(samples)/channels))
	comm = binary.BigEndian.AppendUint16(comm, uint16(bits))
	comm = append(comm, extended(float64(rate))...)
	if form == "AIFC" {
		comm = append(comm, compression...)
		comm = append(comm, 0, 0)
	}
	out := []byte("FORM\x00\x00\x00\x00" + form)
	out = appendChunk(out, "COMM", comm, binary.BigEndian)
	out = appendChunk(out, "SSND", sound, binary.BigEndian)
	binary.BigEndian.PutUint32(out[4:], uint32(len(out)-8))
	return out
}

func dbfs(v float64) float64 { return 20 * math.Log10(v) }

func TestDecodeAudioLoudnessMatchesBS1770(t *testing.T) {
	for _, rate := range []int{48000, 44100} {
		data := makeWAV(wavSpec{tag: 1, channels: 2, rate: rate, bits: 16, width: 2}, sine(rate, 2, 5, 1000, 0.1))
		a, err := DecodeAudio("tone.wav", data, AudioOptions{})
		require.NoError(t, err)
		s := a.Stats
		assert.Equal(t, "wav", s.Format)
		assert.Equal(t, "PCM 16-bit", s.Codec)
		assert.Equal(t, rate, s.SampleRate)
		assert.Equal(t, 2, s.Channels)
		assert.Equal(t, 16, s.BitDepth)
		assert.Equal(t, int64(5*rate), s.Frames)
		assert.InDelta(t, 5.0, s.Duration, 1e-9)
		assert.InDelta(t, -20, s.Loudness, 0.1, "rate %d", rate)
		assert.InDelta(t, -20, dbfs(s.Peak), 0.01)
		assert.InDelta(t, -23.01, dbfs(s.RMS), 0.01)
		assert.Zero(t, s.Clipped)
		assert.False(t, s.Truncated)
	}
}

func TestDecodeAudioMonoLoudnessAndSurroundWeights(t *testing.T) {
	a, err := DecodeAudio("m.wav", makeWAV(wavSpec{tag: 1, channels: 1, rate: 48000, bits: 16, width: 2}, sine(48000, 1, 3, 1000, 0.1)), AudioOptions{})
	require.NoError(t, err)
	assert.InDelta(t, -23.01, a.Stats.Loudness, 0.1)

	n := 48000 * 3
	samples := make([]float64, 0, n*6)
	tone := sine(48000, 1, 3, 1000, 0.1)
	for i := 0; i < n; i++ {
		samples = append(samples, 0, 0, 0, tone[i], tone[i], 0)
	}
	a, err = DecodeAudio("s.wav", makeWAV(wavSpec{tag: 1, channels: 6, rate: 48000, bits: 16, width: 2}, samples), AudioOptions{})
	require.NoError(t, err)
	assert.InDelta(t, -23.01+10*math.Log10(1.41), a.Stats.Loudness, 0.1)
}

func TestDecodeAudioWaveformAndSpectrogram(t *testing.T) {
	data := makeWAV(wavSpec{tag: 1, channels: 2, rate: 48000, bits: 16, width: 2}, sine(48000, 2, 2, 1000, 0.5))
	a, err := DecodeAudio("tone.wav", data, AudioOptions{Buckets: 100, SpecColumns: 50, SpecRows: 64})
	require.NoError(t, err)
	require.Len(t, a.Peaks, 2)
	require.Len(t, a.Peaks[0], 200)
	for b := 0; b < 100; b++ {
		assert.InDelta(t, -0.5, a.Peaks[1][2*b], 0.01)
		assert.InDelta(t, 0.5, a.Peaks[1][2*b+1], 0.01)
	}
	assert.Equal(t, 50, a.SpecColumns)
	assert.Equal(t, 64, a.SpecRows)
	assert.Len(t, a.Spectrogram, 50*64)
	assert.InDelta(t, 24000, a.SpecMaxHz, 1e-9)
	assert.InDelta(t, 23.4375, a.SpecMinHz, 1e-9)
	col := 25
	best := 0
	for row := 0; row < a.SpecRows; row++ {
		if a.Spectrogram[row*a.SpecColumns+col] > a.Spectrogram[best*a.SpecColumns+col] {
			best = row
		}
	}
	ratio := a.SpecMaxHz / a.SpecMinHz
	lo := a.SpecMinHz * math.Pow(ratio, float64(best)/64)
	hi := a.SpecMinHz * math.Pow(ratio, float64(best+1)/64)
	assert.True(t, lo <= 1030 && hi >= 970, "peak row %d covers %.0f-%.0f Hz", best, lo, hi)
	level := float64(a.Spectrogram[best*a.SpecColumns+col])/255*100 - 100
	assert.InDelta(t, dbfs(0.5), level, 1.5)
	assert.Less(t, int(a.Spectrogram[2*a.SpecColumns+col]), 100)
}

func TestDecodeAudioPlayback(t *testing.T) {
	samples := sine(8000, 2, 1, 440, 0.25)
	data := makeWAV(wavSpec{tag: 1, channels: 2, rate: 8000, bits: 16, width: 2}, samples)

	a, err := DecodeAudio("p.wav", data, AudioOptions{PlaybackBytes: 1 << 20})
	require.NoError(t, err)
	assert.False(t, a.PlaybackTruncated)
	back, err := DecodeAudio("back.wav", a.Playback, AudioOptions{})
	require.NoError(t, err)
	assert.Equal(t, 2, back.Stats.Channels)
	assert.Equal(t, int64(8000), back.Stats.Frames)
	assert.InDelta(t, a.Stats.Peak, back.Stats.Peak, 1e-4)

	a, err = DecodeAudio("p.wav", data, AudioOptions{PlaybackBytes: 20000})
	require.NoError(t, err)
	back, err = DecodeAudio("back.wav", a.Playback, AudioOptions{})
	require.NoError(t, err)
	assert.Equal(t, 1, back.Stats.Channels)
	assert.Equal(t, int64(8000), back.Stats.Frames)
	assert.False(t, a.PlaybackTruncated)

	a, err = DecodeAudio("p.wav", data, AudioOptions{PlaybackBytes: 1044})
	require.NoError(t, err)
	assert.True(t, a.PlaybackTruncated)
	assert.Len(t, a.Playback, 1044)

	a, err = DecodeAudio("p.wav", data, AudioOptions{})
	require.NoError(t, err)
	assert.Nil(t, a.Playback)
}

func TestDecodeAudioWAVEncodings(t *testing.T) {
	samples := sine(8000, 1, 0.5, 300, 0.5)
	samples = append(samples, 1, -1)
	cases := []struct {
		name  string
		spec  wavSpec
		codec string
	}{
		{"u8", wavSpec{tag: 1, bits: 8, width: 1}, "PCM 8-bit"},
		{"s24", wavSpec{tag: 1, bits: 24, width: 3, pad: true}, "PCM 24-bit"},
		{"s32", wavSpec{tag: 1, bits: 32, width: 4}, "PCM 32-bit"},
		{"f32", wavSpec{tag: 3, bits: 32, width: 4}, "Float 32-bit"},
		{"f64", wavSpec{tag: 3, bits: 64, width: 8}, "Float 64-bit"},
		{"ext", wavSpec{tag: 1, bits: 20, width: 3, extensible: true}, "PCM 20-bit"},
		{"zerobits", wavSpec{tag: 1, bits: 0, width: 2}, "PCM 16-bit"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.spec.channels, tc.spec.rate = 1, 8000
			a, err := DecodeAudio("x.WAV", makeWAV(tc.spec, samples), AudioOptions{})
			require.NoError(t, err)
			assert.Equal(t, tc.codec, a.Stats.Codec)
			assert.Equal(t, int64(len(samples)), a.Stats.Frames)
			assert.InDelta(t, 1, a.Stats.Peak, 0.01)
			assert.GreaterOrEqual(t, a.Stats.Clipped, int64(1))
		})
	}
}

func TestDecodeAudioAIFF(t *testing.T) {
	samples := sine(22050, 2, 0.5, 300, 0.5)
	cases := []struct {
		name string
		data []byte
	}{
		{"aiff16", makeAIFF("AIFF", "", 1, 2, 22050, 16, samples)},
		{"aifc-twos24", makeAIFF("AIFC", "twos", 1, 2, 22050, 24, samples)},
		{"aifc-sowt", makeAIFF("AIFC", "sowt", 1, 2, 22050, 16, samples)},
		{"aifc-fl32", makeAIFF("AIFC", "fl32", 3, 2, 22050, 32, samples)},
		{"aifc-fl64", makeAIFF("AIFC", "fl64", 3, 2, 22050, 64, samples)},
		{"aiff8", makeAIFF("AIFF", "", 1, 2, 22050, 8, samples)},
		{"aiff32", makeAIFF("AIFF", "", 1, 2, 22050, 32, samples)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, err := DecodeAudio("x.aiff", tc.data, AudioOptions{})
			require.NoError(t, err)
			assert.Equal(t, "aiff", a.Stats.Format)
			assert.Equal(t, 22050, a.Stats.SampleRate)
			assert.Equal(t, 2, a.Stats.Channels)
			assert.Equal(t, int64(len(samples)/2), a.Stats.Frames)
			assert.InDelta(t, 0.5, a.Stats.Peak, 0.01)
		})
	}
	_, err := DecodeAudio("x.aifc", makeAIFF("AIFC", "ima4", 1, 2, 22050, 16, samples), AudioOptions{})
	assert.ErrorIs(t, err, ErrUnsupported)
	_, err = DecodeAudio("x.aif", []byte("FORM\x00\x00\x00\x04AIFF"), AudioOptions{})
	assert.ErrorIs(t, err, errAudio)
	_, err = DecodeAudio("x.aif", []byte("RIFF"), AudioOptions{})
	assert.ErrorIs(t, err, errAudio)
	assert.InDelta(t, -44100, extendedFloat(append([]byte{0x80 | extended(44100)[0]}, extended(44100)[1:]...)), 1e-9)
}

func TestDecodeAudioFixtures(t *testing.T) {
	cases := []struct {
		file     string
		format   string
		codec    string
		rate     int
		channels int
		bits     int
		minFrame int64
		maxFrame int64
	}{
		{"tone.flac", "flac", "FLAC", 44100, 2, 16, 6144, 6144},
		{"tone.ogg", "ogg", "Vorbis", 22050, 1, 0, 6144, 6144},
		{"tone.mp3", "mp3", "MP3", 22050, 1, 0, 6912, 9000},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", "audio", tc.file))
			require.NoError(t, err)
			a, err := DecodeAudio(tc.file, data, AudioOptions{PlaybackBytes: 1 << 20})
			require.NoError(t, err)
			s := a.Stats
			assert.Equal(t, tc.format, s.Format)
			assert.Equal(t, tc.codec, s.Codec)
			assert.Equal(t, tc.rate, s.SampleRate)
			assert.Equal(t, tc.channels, s.Channels)
			assert.Equal(t, tc.bits, s.BitDepth)
			assert.GreaterOrEqual(t, s.Frames, tc.minFrame)
			assert.LessOrEqual(t, s.Frames, tc.maxFrame)
			assert.InDelta(t, 0.5, s.Peak, 0.05)
			assert.True(t, math.IsInf(s.Loudness, -1), "shorter than one gating block")
			assert.NotEmpty(t, a.Playback)
		})
	}
}

func TestDecodeAudioFLACLength(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "audio", "tone.flac"))
	require.NoError(t, err)
	setSamples := func(n uint64) []byte {
		out := append([]byte(nil), data...)
		out[21] = out[21]&0xf0 | byte(n>>32)&0x0f
		binary.BigEndian.PutUint32(out[22:], uint32(n))
		return out
	}
	for _, n := range []uint64{0, 100, 1 << 35} {
		a, err := DecodeAudio("x.flac", setSamples(n), AudioOptions{Buckets: 64})
		require.NoError(t, err, "nsamples %d", n)
		assert.Equal(t, int64(6144), a.Stats.Frames)
		assert.Len(t, a.Peaks[0], 128)
		assert.InDelta(t, 0.5, a.Peaks[0][127], 0.05)
	}
	_, err = DecodeAudio("x.flac", data[:200], AudioOptions{})
	assert.ErrorIs(t, err, errAudio)
}

func TestDecodeAudioTruncatesLongAudio(t *testing.T) {
	old := MaxAudioFrames
	MaxAudioFrames = 1000
	defer func() { MaxAudioFrames = old }()
	a, err := DecodeAudio("x.wav", makeWAV(wavSpec{tag: 1, channels: 1, rate: 8000, bits: 16, width: 2}, sine(8000, 1, 1, 300, 0.5)), AudioOptions{})
	require.NoError(t, err)
	assert.True(t, a.Stats.Truncated)
	assert.Equal(t, int64(1000), a.Stats.Frames)

	data, err := os.ReadFile(filepath.Join("testdata", "audio", "tone.mp3"))
	require.NoError(t, err)
	frames, err := countFrames(mustOpen(t, "tone.mp3", data))
	require.NoError(t, err)
	assert.LessOrEqual(t, frames, int64(1000+4096))
}

func mustOpen(t *testing.T, name string, data []byte) *audioSource {
	t.Helper()
	_, open, err := audioFormat(name, data)
	require.NoError(t, err)
	src, err := openChecked(open)
	require.NoError(t, err)
	return src
}

func TestDecodeAudioShortAndSilent(t *testing.T) {
	a, err := DecodeAudio("x.wav", makeWAV(wavSpec{tag: 1, channels: 1, rate: 8000, bits: 16, width: 2}, sine(8000, 1, 0.2, 300, 0.5)), AudioOptions{})
	require.NoError(t, err)
	assert.True(t, math.IsInf(a.Stats.Loudness, -1))
	a, err = DecodeAudio("x.wav", makeWAV(wavSpec{tag: 1, channels: 1, rate: 8000, bits: 16, width: 2}, make([]float64, 8000)), AudioOptions{})
	require.NoError(t, err)
	assert.True(t, math.IsInf(a.Stats.Loudness, -1))
	assert.Zero(t, a.Stats.Peak)
	a, err = DecodeAudio("x.wav", makeWAV(wavSpec{tag: 1, channels: 1, rate: 8000, bits: 16, width: 2}, nil), AudioOptions{})
	require.NoError(t, err)
	assert.Zero(t, a.Stats.Frames)
	assert.Zero(t, a.Stats.RMS)
}

func TestDecodeAudioErrors(t *testing.T) {
	wav := func(spec wavSpec) []byte { return makeWAV(spec, sine(8000, max(spec.channels, 1), 0.1, 300, 0.5)) }
	cases := []struct {
		name string
		file string
		data []byte
		want error
	}{
		{"ext", "x.opus", nil, ErrUnsupported},
		{"magic", "x.wav", []byte("RIFX0000WAVE"), errAudio},
		{"nodata", "x.wav", []byte("RIFF\x04\x00\x00\x00WAVE"), errAudio},
		{"adpcm", "x.wav", wav(wavSpec{tag: 2, channels: 1, rate: 8000, bits: 4, width: 1}), ErrUnsupported},
		{"align", "x.wav", wav(wavSpec{tag: 1, channels: 0, rate: 8000, bits: 16, width: 2}), errAudio},
		{"channels", "x.wav", wav(wavSpec{tag: 1, channels: 9, rate: 8000, bits: 16, width: 2}), ErrUnsupported},
		{"rate", "x.wav", wav(wavSpec{tag: 1, channels: 1, rate: 0, bits: 16, width: 2}), errAudio},
		{"flac", "x.flac", []byte("nope"), errAudio},
		{"ogg", "x.ogg", []byte("nope"), errAudio},
		{"mp3", "x.mp3", []byte("nope"), errAudio},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodeAudio(tc.file, tc.data, AudioOptions{})
			assert.ErrorIs(t, err, tc.want)
		})
	}
}

func TestMP3Mono(t *testing.T) {
	frame := []byte{0xff, 0xf3, 0x80, 0xc4}
	assert.True(t, mp3Mono(frame))
	assert.False(t, mp3Mono([]byte{0xff, 0xf3, 0x80, 0x44}))
	tag := []byte{'I', 'D', '3', 4, 0, 0x10, 0, 0, 0, 2, 0xff, 0xf3}
	tag = append(tag, make([]byte, 10)...)
	assert.True(t, mp3Mono(append(tag, frame...)))
	assert.False(t, mp3Mono([]byte{1, 2, 3}))
}

func TestFFTMatchesDFT(t *testing.T) {
	n := 16
	re := make([]float64, n)
	im := make([]float64, n)
	for i := range re {
		re[i] = math.Sin(float64(i)*0.7) + 0.25*float64(i%3)
	}
	wantRe := make([]float64, n)
	wantIm := make([]float64, n)
	for k := 0; k < n; k++ {
		for i := 0; i < n; i++ {
			a := -2 * math.Pi * float64(k*i) / float64(n)
			wantRe[k] += re[i] * math.Cos(a)
			wantIm[k] += re[i] * math.Sin(a)
		}
	}
	fft(re, im)
	for k := 0; k < n; k++ {
		assert.InDelta(t, wantRe[k], re[k], 1e-9)
		assert.InDelta(t, wantIm[k], im[k], 1e-9)
	}
}

func TestPlaybackDownmixesSurroundChannels(t *testing.T) {
	p := newPlayback(8000, 6, 1, 1<<20)
	p.add([]float32{0, 0, 0.5, 0, 0, 0})
	require.Equal(t, 2, p.outCh)
	l, r := downmix([]float32{0, 0, 0.6, 0, 0, 0})
	want := 0.6 * math.Sqrt2 / 2 / (1 + 3*math.Sqrt2/2)
	assert.InDelta(t, want, l, 1e-6)
	assert.InDelta(t, want, r, 1e-6)
	wav, _ := p.wav()
	assert.NotEqual(t, make([]byte, 4), wav[44:48])
}

func TestDownmixKeepsSurroundSides(t *testing.T) {
	l, r := downmix([]float32{0, 0, 0, 0.8, 0})
	assert.InDelta(t, 0.8*0.707/(1+0.707+0.707), l, 1e-3)
	assert.Zero(t, r)
	l, r = downmix([]float32{0, 0, 0, 0, 0.8})
	assert.Zero(t, l)
	assert.Greater(t, r, 0.0)
	l, r = downmix([]float32{1, 1, 1})
	assert.InDelta(t, 1.0, l, 1e-9)
	assert.InDelta(t, 1.0, r, 1e-9)
	l, r = downmix([]float32{0.5, 0.25, 0, 0, 0, 0, 0, 0, 0})
	assert.InDelta(t, 0.5/(1+4*0.707), l, 1e-3)
	assert.Greater(t, r, 0.0)
}
