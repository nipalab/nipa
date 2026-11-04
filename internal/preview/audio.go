package preview

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
)

// MaxAudioFrames bounds how many frames DecodeAudio analyses; longer audio is
// cut there and marked Truncated.
var MaxAudioFrames int64 = 48000 * 60 * 60

const (
	maxAudioChannels = 8
	specWindow       = 2048
	specFloorDB      = -100.0
)

// AudioOptions sizes the waveform, the spectrogram and the playback copy.
// PlaybackBytes 0 skips the playback copy.
type AudioOptions struct {
	Buckets       int
	SpecColumns   int
	SpecRows      int
	PlaybackBytes int
}

type AudioStats struct {
	Format     string
	Codec      string
	SampleRate int
	Channels   int
	BitDepth   int // 0 for lossy codecs
	Frames     int64
	Duration   float64
	Peak       float64 // linear sample peak over all channels
	RMS        float64 // linear, over all channels
	Loudness   float64 // integrated LUFS (ITU-R BS.1770), -Inf when nothing passes the gates
	Clipped    int64   // samples at the format's full scale or beyond
	Truncated  bool
}

// Audio is the analysed form of an audio file. Peaks holds, per channel, a
// min and max pair per bucket; Spectrogram is SpecRows rows of SpecColumns
// bytes (row 0 is SpecMinHz, log spaced up to SpecMaxHz, 0 = specFloorDB,
// 255 = 0 dBFS); Playback is a 16-bit PCM WAV of at most two channels.
type Audio struct {
	Stats             AudioStats
	Peaks             [][]float32
	Spectrogram       []byte
	SpecColumns       int
	SpecRows          int
	SpecMinHz         float64
	SpecMaxHz         float64
	Playback          []byte
	PlaybackTruncated bool
}

// DecodeAudio decodes WAV, AIFF, FLAC, Ogg Vorbis or MP3 bytes, picked by
// the file extension, and analyses them.
func DecodeAudio(name string, data []byte, opts AudioOptions) (out *Audio, err error) {
	format, open, err := audioFormat(name, data)
	if err != nil {
		return nil, err
	}
	defer func() {
		if r := recover(); r != nil {
			out, err = nil, fmt.Errorf("%w: corrupt %s data", errAudio, format)
		}
	}()
	opts = audioDefaults(opts)
	src, err := openChecked(open)
	if err != nil {
		return nil, err
	}
	total := src.frames
	if total <= 0 {
		if total, err = countFrames(src); err != nil {
			return nil, err
		}
		if src, err = openChecked(open); err != nil {
			return nil, err
		}
	}
	total = min(total, MaxAudioFrames)
	a, frames, err := analyze(src, total, opts)
	if err != nil {
		return nil, err
	}
	if frames > 0 && !a.Stats.Truncated && math.Abs(float64(frames-total)) > float64(total)/100 {
		if src, err = openChecked(open); err != nil {
			return nil, err
		}
		if a, _, err = analyze(src, frames, opts); err != nil {
			return nil, err
		}
	}
	a.Stats.Format = format
	return a, nil
}

func audioDefaults(o AudioOptions) AudioOptions {
	if o.Buckets <= 0 {
		o.Buckets = 2048
	}
	if o.SpecColumns <= 0 {
		o.SpecColumns = 1024
	}
	if o.SpecRows <= 0 {
		o.SpecRows = 128
	}
	return o
}

func openChecked(open audioOpener) (*audioSource, error) {
	src, err := open()
	if err != nil {
		return nil, err
	}
	if src.channels <= 0 || src.channels > maxAudioChannels {
		return nil, fmt.Errorf("%w: %d channels", ErrUnsupported, src.channels)
	}
	if src.rate <= 0 || src.rate > 768000 {
		return nil, fmt.Errorf("%w: sample rate %d", errAudio, src.rate)
	}
	return src, nil
}

func countFrames(src *audioSource) (int64, error) {
	buf := make([]float32, 4096*src.channels)
	var frames int64
	for frames <= MaxAudioFrames {
		n, err := src.read(buf)
		frames += int64(n / src.channels)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return 0, fmt.Errorf("%w: %v", errAudio, err)
		}
	}
	return frames, nil
}

func analyze(src *audioSource, total int64, opts AudioOptions) (*Audio, int64, error) {
	ch := src.channels
	total = max(total, 1)
	buckets := int(min(int64(opts.Buckets), total))
	peaks := make([][]float32, ch)
	for c := range peaks {
		peaks[c] = make([]float32, 2*buckets)
	}
	touched := make([]bool, buckets)
	loud := newLoudness(src.rate, ch)
	spec := newSpectrogram(src.rate, total, int(min(int64(opts.SpecColumns), max(1, total/64))), opts.SpecRows)
	play := newPlayback(src.rate, ch, total, opts.PlaybackBytes)
	stats := AudioStats{Codec: src.codec, SampleRate: src.rate, Channels: ch, BitDepth: src.bitDepth, Loudness: math.Inf(-1)}
	var sumSq float64
	var frame int64
	buf := make([]float32, 4096*ch)
	for {
		n, err := src.read(buf)
		for i := 0; i+ch <= n; i += ch {
			if frame >= MaxAudioFrames {
				stats.Truncated = true
				break
			}
			s := buf[i : i+ch]
			b := int(min(frame*int64(buckets)/total, int64(buckets-1)))
			var mono float64
			for c, v := range s {
				if !touched[b] || v < peaks[c][2*b] {
					peaks[c][2*b] = v
				}
				if !touched[b] || v > peaks[c][2*b+1] {
					peaks[c][2*b+1] = v
				}
				x := float64(v)
				a := math.Abs(x)
				stats.Peak = max(stats.Peak, a)
				if a >= float64(src.clip) {
					stats.Clipped++
				}
				sumSq += x * x
				mono += x
			}
			touched[b] = true
			loud.add(s)
			spec.push(mono / float64(ch))
			play.add(s)
			frame++
		}
		if stats.Truncated || errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, 0, fmt.Errorf("%w: %v", errAudio, err)
		}
	}
	spec.finish(frame)
	stats.Frames = frame
	stats.Duration = float64(frame) / float64(src.rate)
	if frame > 0 {
		stats.RMS = math.Sqrt(sumSq / float64(frame*int64(ch)))
	}
	stats.Loudness = loud.integrated()
	wav, cut := play.wav()
	return &Audio{
		Stats:             stats,
		Peaks:             peaks,
		Spectrogram:       spec.out,
		SpecColumns:       spec.cols,
		SpecRows:          spec.rows,
		SpecMinHz:         spec.minHz,
		SpecMaxHz:         spec.maxHz,
		Playback:          wav,
		PlaybackTruncated: cut,
	}, frame, nil
}

type biquad struct{ b0, b1, b2, a1, a2, x1, x2, y1, y2 float64 }

func (q *biquad) step(x float64) float64 {
	y := q.b0*x + q.b1*q.x1 + q.b2*q.x2 - q.a1*q.y1 - q.a2*q.y2
	q.x2, q.x1, q.y2, q.y1 = q.x1, x, q.y1, y
	return y
}

// kWeighting builds the BS.1770 pre-filter and RLB high-pass for any rate,
// using the analog prototypes from libebur128.
func kWeighting(rate float64) (biquad, biquad) {
	k := math.Tan(math.Pi * 1681.974450955533 / rate)
	q := 0.7071752369554196
	vh := math.Pow(10, 3.999843853973347/20)
	vb := math.Pow(vh, 0.4996667741545416)
	a0 := 1 + k/q + k*k
	shelf := biquad{
		b0: (vh + vb*k/q + k*k) / a0, b1: 2 * (k*k - vh) / a0, b2: (vh - vb*k/q + k*k) / a0,
		a1: 2 * (k*k - 1) / a0, a2: (1 - k/q + k*k) / a0,
	}
	k = math.Tan(math.Pi * 38.13547087602444 / rate)
	q = 0.5003270373238773
	a0 = 1 + k/q + k*k
	high := biquad{b0: 1, b1: -2, b2: 1, a1: 2 * (k*k - 1) / a0, a2: (1 - k/q + k*k) / a0}
	return shelf, high
}

type loudness struct {
	filters [][2]biquad
	weights []float64
	stepLen int
	fill    int
	acc     float64
	steps   []float64
}

func newLoudness(rate, channels int) *loudness {
	l := &loudness{stepLen: max(1, int(math.Round(float64(rate)/10)))}
	for c := 0; c < channels; c++ {
		shelf, high := kWeighting(float64(rate))
		l.filters = append(l.filters, [2]biquad{shelf, high})
		w := 1.0
		if channels == 6 && c == 3 {
			w = 0
		} else if channels == 6 && c >= 4 {
			w = 1.41
		}
		l.weights = append(l.weights, w)
	}
	return l
}

func (l *loudness) add(s []float32) {
	for c, v := range s {
		f := &l.filters[c]
		y := f[1].step(f[0].step(float64(v)))
		l.acc += l.weights[c] * y * y
	}
	l.fill++
	if l.fill == l.stepLen {
		l.steps = append(l.steps, l.acc)
		l.fill, l.acc = 0, 0
	}
}

func blockLoudness(e float64) float64 { return -0.691 + 10*math.Log10(e) }

func (l *loudness) integrated() float64 {
	var blocks []float64
	for i := 0; i+4 <= len(l.steps); i++ {
		e := (l.steps[i] + l.steps[i+1] + l.steps[i+2] + l.steps[i+3]) / float64(4*l.stepLen)
		if blockLoudness(e) > -70 {
			blocks = append(blocks, e)
		}
	}
	if len(blocks) == 0 {
		return math.Inf(-1)
	}
	var sum float64
	for _, e := range blocks {
		sum += e
	}
	gate := blockLoudness(sum/float64(len(blocks))) - 10
	sum = 0
	n := 0
	for _, e := range blocks {
		if blockLoudness(e) > gate {
			sum += e
			n++
		}
	}
	return blockLoudness(sum / float64(n))
}

type spectrogram struct {
	cols, rows   int
	total        int64
	minHz, maxHz float64
	ring         []float64
	pos          int64
	next         int
	window       []float64
	windowSum    float64
	re, im       []float64
	lo, hi       []int
	out          []byte
}

func newSpectrogram(rate int, total int64, cols, rows int) *spectrogram {
	s := &spectrogram{cols: cols, rows: rows, total: total, ring: make([]float64, specWindow), window: make([]float64, specWindow),
		re: make([]float64, specWindow), im: make([]float64, specWindow), lo: make([]int, rows), hi: make([]int, rows), out: make([]byte, cols*rows)}
	for i := range s.window {
		s.window[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(specWindow))
		s.windowSum += s.window[i]
	}
	r := float64(rate)
	s.maxHz = r / 2
	s.minHz = max(r/specWindow, min(20, s.maxHz/10))
	for row := 0; row < rows; row++ {
		f0 := s.minHz * math.Pow(s.maxHz/s.minHz, float64(row)/float64(rows))
		f1 := s.minHz * math.Pow(s.maxHz/s.minHz, float64(row+1)/float64(rows))
		lo := min(int(f0*specWindow/r), specWindow/2)
		hi := min(max(int(f1*specWindow/r), lo+1), specWindow/2+1)
		s.lo[row], s.hi[row] = lo, hi
	}
	return s
}

func (s *spectrogram) start(col int) int64 {
	return (2*int64(col)+1)*s.total/(2*int64(s.cols)) - specWindow/2
}

func (s *spectrogram) push(x float64) {
	s.ring[s.pos%specWindow] = x
	s.pos++
	for s.next < s.cols && s.start(s.next)+specWindow <= s.pos {
		s.column(s.next)
		s.next++
	}
}

func (s *spectrogram) finish(frames int64) {
	for s.next < s.cols && s.start(s.next)+specWindow <= frames+specWindow {
		s.push(0)
	}
}

func (s *spectrogram) column(col int) {
	start := s.start(col)
	for i := range s.re {
		k := start + int64(i)
		v := 0.0
		if k >= 0 && k < s.pos {
			v = s.ring[k%specWindow]
		}
		s.re[i], s.im[i] = v*s.window[i], 0
	}
	fft(s.re, s.im)
	for row := 0; row < s.rows; row++ {
		power := 0.0
		for b := s.lo[row]; b < s.hi[row]; b++ {
			power = max(power, s.re[b]*s.re[b]+s.im[b]*s.im[b])
		}
		db := 20 * math.Log10(2*math.Sqrt(power)/s.windowSum)
		level := (db - specFloorDB) / -specFloorDB * 255
		s.out[row*s.cols+col] = byte(math.Round(max(0, min(255, level))))
	}
}

func fft(re, im []float64) {
	n := len(re)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			re[i], re[j] = re[j], re[i]
			im[i], im[j] = im[j], im[i]
		}
	}
	for size := 2; size <= n; size <<= 1 {
		angle := -2 * math.Pi / float64(size)
		wr, wi := math.Cos(angle), math.Sin(angle)
		for start := 0; start < n; start += size {
			cr, ci := 1.0, 0.0
			for k := 0; k < size/2; k++ {
				a, b := start+k, start+k+size/2
				tr := re[b]*cr - im[b]*ci
				ti := re[b]*ci + im[b]*cr
				re[b], im[b] = re[a]-tr, im[a]-ti
				re[a], im[a] = re[a]+tr, im[a]+ti
				cr, ci = cr*wr-ci*wi, cr*wi+ci*wr
			}
		}
	}
}

type playback struct {
	rate, channels, outCh int
	limit                 int64
	frames                int64
	pcm                   []byte
	cut                   bool
}

func newPlayback(rate, channels int, total int64, budget int) *playback {
	p := &playback{rate: rate, channels: channels, outCh: min(channels, 2)}
	if budget <= 44 {
		return p
	}
	if p.outCh == 2 && total*4+44 > int64(budget) {
		p.outCh = 1
	}
	p.limit = (int64(budget) - 44) / int64(2*p.outCh)
	return p
}

func pcm16(v float64) uint16 {
	return uint16(int16(math.Round(max(-1, min(1, v)) * 32767)))
}

const (
	sideLeft  = 0
	sideRight = 1
	sideBoth  = 2
)

var channelSides = map[int][]uint8{
	3: {sideLeft, sideRight, sideBoth},
	4: {sideLeft, sideRight, sideLeft, sideRight},
	5: {sideLeft, sideRight, sideBoth, sideLeft, sideRight},
	6: {sideLeft, sideRight, sideBoth, sideBoth, sideLeft, sideRight},
	7: {sideLeft, sideRight, sideBoth, sideBoth, sideBoth, sideLeft, sideRight},
	8: {sideLeft, sideRight, sideBoth, sideBoth, sideLeft, sideRight, sideLeft, sideRight},
}

func downmix(s []float32) (float64, float64) {
	if len(s) == 2 {
		return float64(s[0]), float64(s[1])
	}
	sides := channelSides[len(s)]
	var sum, weight [2]float64
	for i, v := range s {
		side, w := uint8(i&1), 1.0
		if sides != nil {
			side = sides[i]
		}
		if i >= 2 {
			w = math.Sqrt2 / 2
		}
		for k := 0; k < 2; k++ {
			if side == uint8(k) || side == sideBoth {
				sum[k] += w * float64(v)
				weight[k] += w
			}
		}
	}
	return sum[0] / weight[0], sum[1] / weight[1]
}

func (p *playback) add(s []float32) {
	if p.limit == 0 {
		return
	}
	if p.frames >= p.limit {
		p.cut = true
		return
	}
	p.frames++
	if p.outCh == 2 {
		left, right := downmix(s)
		p.pcm = binary.LittleEndian.AppendUint16(p.pcm, pcm16(left))
		p.pcm = binary.LittleEndian.AppendUint16(p.pcm, pcm16(right))
		return
	}
	var sum float64
	for _, v := range s {
		sum += float64(v)
	}
	p.pcm = binary.LittleEndian.AppendUint16(p.pcm, pcm16(sum/float64(len(s))))
}

func (p *playback) wav() ([]byte, bool) {
	if p.limit == 0 {
		return nil, false
	}
	h := make([]byte, 0, 44+len(p.pcm))
	h = append(h, "RIFF"...)
	h = binary.LittleEndian.AppendUint32(h, uint32(36+len(p.pcm)))
	h = append(h, "WAVEfmt "...)
	h = binary.LittleEndian.AppendUint32(h, 16)
	h = binary.LittleEndian.AppendUint16(h, 1)
	h = binary.LittleEndian.AppendUint16(h, uint16(p.outCh))
	h = binary.LittleEndian.AppendUint32(h, uint32(p.rate))
	h = binary.LittleEndian.AppendUint32(h, uint32(p.rate*p.outCh*2))
	h = binary.LittleEndian.AppendUint16(h, uint16(p.outCh*2))
	h = binary.LittleEndian.AppendUint16(h, 16)
	h = append(h, "data"...)
	h = binary.LittleEndian.AppendUint32(h, uint32(len(p.pcm)))
	return append(h, p.pcm...), p.cut
}
