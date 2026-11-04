package preview

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"strings"

	"github.com/hajimehoshi/go-mp3"
	"github.com/jfreymuth/oggvorbis"
	"github.com/mewkiz/flac"
)

var errAudio = errors.New("preview: invalid audio")

type audioSource struct {
	rate     int
	channels int
	bitDepth int
	codec    string
	frames   int64
	clip     float32
	read     func(buf []float32) (int, error)
}

func intFullScale(bits int) float32 {
	return float32(1 - math.Ldexp(1, -(bits-1)))
}

type audioOpener func() (*audioSource, error)

func audioFormat(name string, data []byte) (string, audioOpener, error) {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".wav", ".wave":
		return "wav", func() (*audioSource, error) { return openWAV(data) }, nil
	case ".aif", ".aiff", ".aifc":
		return "aiff", func() (*audioSource, error) { return openAIFF(data) }, nil
	case ".flac":
		return "flac", func() (*audioSource, error) { return openFLAC(data) }, nil
	case ".ogg", ".oga":
		return "ogg", func() (*audioSource, error) { return openVorbis(data) }, nil
	case ".mp3":
		return "mp3", func() (*audioSource, error) { return openMP3(data) }, nil
	}
	return "", nil, fmt.Errorf("%w: %s audio", ErrUnsupported, ext)
}

type sampleDecoder func([]byte) float32

func pcmSource(body []byte, channels, width int, decode sampleDecoder) func([]float32) (int, error) {
	pos := 0
	return func(buf []float32) (int, error) {
		n := 0
		for n+channels <= len(buf) && pos+channels*width <= len(body) {
			for c := 0; c < channels; c++ {
				buf[n] = decode(body[pos : pos+width])
				n++
				pos += width
			}
		}
		if n == 0 {
			return 0, io.EOF
		}
		return n, nil
	}
}

func intDecoder(width int, order binary.ByteOrder, unsigned8 bool) sampleDecoder {
	switch width {
	case 1:
		if unsigned8 {
			return func(b []byte) float32 { return (float32(b[0]) - 128) / 128 }
		}
		return func(b []byte) float32 { return float32(int8(b[0])) / 128 }
	case 2:
		return func(b []byte) float32 { return float32(int16(order.Uint16(b))) / 32768 }
	case 3:
		return func(b []byte) float32 {
			var v int32
			if order == binary.BigEndian {
				v = int32(b[0])<<24 | int32(b[1])<<16 | int32(b[2])<<8
			} else {
				v = int32(b[2])<<24 | int32(b[1])<<16 | int32(b[0])<<8
			}
			return float32(v>>8) / 8388608
		}
	}
	return func(b []byte) float32 { return float32(float64(int32(order.Uint32(b))) / 2147483648) }
}

func floatDecoder(width int, order binary.ByteOrder) sampleDecoder {
	if width == 8 {
		return func(b []byte) float32 { return float32(math.Float64frombits(order.Uint64(b))) }
	}
	return func(b []byte) float32 { return math.Float32frombits(order.Uint32(b)) }
}

func openWAV(data []byte) (*audioSource, error) {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, fmt.Errorf("%w: not a WAV file", errAudio)
	}
	var format []byte
	var body []byte
	haveData := false
	for off := 12; off+8 <= len(data); {
		size := int64(binary.LittleEndian.Uint32(data[off+4:]))
		end := min(int64(off)+8+size, int64(len(data)))
		chunk := data[off+8 : end]
		switch string(data[off : off+4]) {
		case "fmt ":
			format = chunk
		case "data":
			body, haveData = chunk, true
		}
		if haveData && format != nil {
			break
		}
		off = int(end + size&1)
	}
	if len(format) < 16 || !haveData {
		return nil, fmt.Errorf("%w: WAV without format or data", errAudio)
	}
	tag := binary.LittleEndian.Uint16(format)
	channels := int(binary.LittleEndian.Uint16(format[2:]))
	rate := int(binary.LittleEndian.Uint32(format[4:]))
	align := int(binary.LittleEndian.Uint16(format[12:]))
	bits := int(binary.LittleEndian.Uint16(format[14:]))
	if tag == 0xFFFE && len(format) >= 26 {
		tag = binary.LittleEndian.Uint16(format[24:])
	}
	if channels <= 0 || align <= 0 || align%channels != 0 {
		return nil, fmt.Errorf("%w: WAV block layout", errAudio)
	}
	width := align / channels
	if bits == 0 || bits > width*8 {
		bits = width * 8
	}
	var decode sampleDecoder
	codec := fmt.Sprintf("PCM %d-bit", bits)
	clip := intFullScale(width * 8)
	switch {
	case tag == 1 && width <= 4:
		decode = intDecoder(width, binary.LittleEndian, true)
	case tag == 3 && (width == 4 || width == 8):
		decode = floatDecoder(width, binary.LittleEndian)
		codec = fmt.Sprintf("Float %d-bit", bits)
		clip = 1
	default:
		return nil, fmt.Errorf("%w: WAV encoding 0x%04x/%d-bit", ErrUnsupported, tag, width*8)
	}
	return &audioSource{rate: rate, channels: channels, bitDepth: bits, codec: codec, frames: int64(len(body) / align), clip: clip, read: pcmSource(body, channels, width, decode)}, nil
}

func extendedFloat(b []byte) float64 {
	exp := int(binary.BigEndian.Uint16(b)&0x7fff) - 16383 - 63
	v := math.Ldexp(float64(binary.BigEndian.Uint64(b[2:])), exp)
	if b[0]&0x80 != 0 {
		return -v
	}
	return v
}

func openAIFF(data []byte) (*audioSource, error) {
	if len(data) < 12 || string(data[:4]) != "FORM" || (string(data[8:12]) != "AIFF" && string(data[8:12]) != "AIFC") {
		return nil, fmt.Errorf("%w: not an AIFF file", errAudio)
	}
	var comm, sound []byte
	for off := 12; off+8 <= len(data); {
		size := int64(binary.BigEndian.Uint32(data[off+4:]))
		end := min(int64(off)+8+size, int64(len(data)))
		chunk := data[off+8 : end]
		switch string(data[off : off+4]) {
		case "COMM":
			comm = chunk
		case "SSND":
			sound = chunk
		}
		off = int(end + size&1)
	}
	if len(comm) < 18 || len(sound) < 8 {
		return nil, fmt.Errorf("%w: AIFF without COMM or SSND", errAudio)
	}
	channels := int(int16(binary.BigEndian.Uint16(comm)))
	frames := int64(binary.BigEndian.Uint32(comm[2:]))
	bits := int(int16(binary.BigEndian.Uint16(comm[6:])))
	rate := extendedFloat(comm[8:18])
	compression := "NONE"
	if string(data[8:12]) == "AIFC" && len(comm) >= 22 {
		compression = string(comm[18:22])
	}
	if channels <= 0 || bits <= 0 || bits > 64 || !(rate >= 1 && rate <= 1e7) {
		return nil, fmt.Errorf("%w: AIFF format", errAudio)
	}
	width := (bits + 7) / 8
	var decode sampleDecoder
	codec := fmt.Sprintf("PCM %d-bit", bits)
	clip := intFullScale(width * 8)
	switch {
	case (compression == "NONE" || compression == "twos") && width <= 4:
		decode = intDecoder(width, binary.BigEndian, false)
	case compression == "sowt" && width <= 4:
		decode = intDecoder(width, binary.LittleEndian, false)
	case (compression == "fl32" || compression == "FL32") && width == 4, (compression == "fl64" || compression == "FL64") && width == 8:
		decode = floatDecoder(width, binary.BigEndian)
		codec = fmt.Sprintf("Float %d-bit", bits)
		clip = 1
	default:
		return nil, fmt.Errorf("%w: AIFF compression %q", ErrUnsupported, compression)
	}
	offset := int64(binary.BigEndian.Uint32(sound))
	body := sound[min(8+offset, int64(len(sound))):]
	frames = min(frames, int64(len(body)/(width*channels)))
	return &audioSource{rate: int(math.Round(rate)), channels: channels, bitDepth: bits, codec: codec, frames: frames, clip: clip, read: pcmSource(body[:frames*int64(width*channels)], channels, width, decode)}, nil
}

func openFLAC(data []byte) (*audioSource, error) {
	stream, err := flac.New(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errAudio, err)
	}
	info := stream.Info
	channels := int(info.NChannels)
	var pending []float32
	read := func(buf []float32) (int, error) {
		for len(pending) == 0 {
			f, err := stream.ParseNext()
			if err != nil {
				return 0, err
			}
			bps := int(f.BitsPerSample)
			if bps == 0 {
				bps = int(info.BitsPerSample)
			}
			if len(f.Subframes) != channels || bps <= 0 || bps > 32 {
				return 0, fmt.Errorf("%w: FLAC frame layout", errAudio)
			}
			scale := float32(math.Ldexp(1, bps-1))
			n := int(f.BlockSize)
			for _, sub := range f.Subframes {
				n = min(n, len(sub.Samples))
			}
			pending = make([]float32, 0, n*channels)
			for i := 0; i < n; i++ {
				for _, sub := range f.Subframes {
					pending = append(pending, float32(sub.Samples[i])/scale)
				}
			}
		}
		n := min(len(buf)/channels*channels, len(pending))
		copy(buf, pending[:n])
		pending = pending[n:]
		return n, nil
	}
	return &audioSource{rate: int(info.SampleRate), channels: channels, bitDepth: int(info.BitsPerSample), codec: "FLAC", frames: int64(min(info.NSamples, math.MaxInt64)), clip: intFullScale(int(info.BitsPerSample)), read: read}, nil
}

func openVorbis(data []byte) (*audioSource, error) {
	r, err := oggvorbis.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errAudio, err)
	}
	channels := r.Channels()
	read := func(buf []float32) (int, error) {
		n, err := r.Read(buf[:len(buf)/max(channels, 1)*max(channels, 1)])
		if n > 0 {
			return n, nil
		}
		if err == nil {
			err = io.EOF
		}
		return 0, err
	}
	return &audioSource{rate: r.SampleRate(), channels: channels, codec: "Vorbis", frames: r.Length(), clip: 1, read: read}, nil
}

func openMP3(data []byte) (*audioSource, error) {
	d, err := mp3.NewDecoder(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errAudio, err)
	}
	channels := 2
	if mp3Mono(data) {
		channels = 1
	}
	raw := make([]byte, 16384)
	read := func(buf []float32) (int, error) {
		want := min(len(buf)/channels, len(raw)/4) * 4
		n, err := io.ReadFull(d, raw[:want])
		frames := n / 4
		if frames == 0 {
			if err == nil || errors.Is(err, io.ErrUnexpectedEOF) {
				err = io.EOF
			}
			return 0, err
		}
		for i := 0; i < frames; i++ {
			l := float32(int16(binary.LittleEndian.Uint16(raw[4*i:]))) / 32768
			if channels == 1 {
				buf[i] = l
				continue
			}
			buf[2*i] = l
			buf[2*i+1] = float32(int16(binary.LittleEndian.Uint16(raw[4*i+2:]))) / 32768
		}
		return frames * channels, nil
	}
	return &audioSource{rate: d.SampleRate(), channels: channels, codec: "MP3", frames: d.Length() / 4, clip: intFullScale(16), read: read}, nil
}

func mp3Mono(data []byte) bool {
	i := 0
	if len(data) >= 10 && string(data[:3]) == "ID3" {
		size := int(data[6]&0x7f)<<21 | int(data[7]&0x7f)<<14 | int(data[8]&0x7f)<<7 | int(data[9]&0x7f)
		i = 10 + size
		if data[5]&0x10 != 0 {
			i += 10
		}
	}
	for limit := min(len(data)-3, i+65536); i < limit; i++ {
		h := data[i:]
		if h[0] == 0xff && h[1]&0xe0 == 0xe0 && (h[1]>>1)&3 != 0 && h[2]>>4 != 15 && (h[2]>>2)&3 != 3 {
			return h[3]>>6 == 3
		}
	}
	return false
}
