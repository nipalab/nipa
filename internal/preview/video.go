package preview

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

// VideoStats describes a video container without decoding any frame.
type VideoStats struct {
	Container    string  `json:"container"`
	Duration     float64 `json:"duration"`
	Width        int     `json:"width"`
	Height       int     `json:"height"`
	VideoCodec   string  `json:"videoCodec"`
	VideoCodecID string  `json:"videoCodecId"`
	FrameRate    float64 `json:"frameRate"`
	AudioCodec   string  `json:"audioCodec"`
	AudioCodecID string  `json:"audioCodecId"`
	SampleRate   int     `json:"sampleRate"`
	Channels     int     `json:"channels"`
	Bitrate      int64   `json:"bitrate"`
	Tracks       int     `json:"tracks"`
}

const (
	maxVideoBoxes    = 20000
	maxVideoDepth    = 12
	maxVideoLeafRead = 1 << 20
)

var errVideoLimit = errors.New("preview: video container exceeds parser limits")

// ProbeVideo reads the container headers of an MP4/MOV or WebM/MKV file:
// duration, resolution, codecs, frame rate and overall bitrate. Other
// containers are ErrUnsupported.
func ProbeVideo(name string, r io.ReaderAt, size int64) (*VideoStats, error) {
	var head [12]byte
	n, _ := r.ReadAt(head[:], 0)
	var st *VideoStats
	var err error
	switch {
	case n >= 4 && binary.BigEndian.Uint32(head[:4]) == ebmlHeaderID:
		st, err = probeMatroska(r, size)
	case n >= 8 && isBMFF(head[4:8]):
		st, err = probeBMFF(name, r, size)
	default:
		return nil, fmt.Errorf("%w: %s video", ErrUnsupported, strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), "."))
	}
	if err != nil {
		return nil, err
	}
	if st.Duration > 0 {
		st.Bitrate = int64(float64(size) * 8 / st.Duration)
	}
	return st, nil
}

func isBMFF(t []byte) bool {
	switch string(t) {
	case "ftyp", "moov", "mdat", "free", "skip", "wide", "pnot":
		return true
	}
	return false
}

var codecNames = map[string]string{
	"avc1": "H.264", "avc3": "H.264", "hvc1": "H.265", "hev1": "H.265", "av01": "AV1",
	"vp08": "VP8", "vp09": "VP9", "mp4v": "MPEG-4 Part 2", "jpeg": "Motion JPEG", "mjpa": "Motion JPEG",
	"apcn": "ProRes 422", "apch": "ProRes 422 HQ", "apcs": "ProRes 422 LT", "apco": "ProRes 422 Proxy",
	"ap4h": "ProRes 4444", "ap4x": "ProRes 4444 XQ", "dvh1": "Dolby Vision", "dvhe": "Dolby Vision",
	"mp4a": "AAC", "opus": "Opus", "ac-3": "AC-3", "ec-3": "E-AC-3", "alac": "ALAC", "flac": "FLAC",
	"lpcm": "PCM", "sowt": "PCM", "twos": "PCM", "in24": "PCM", "in32": "PCM", "fl32": "PCM", "fl64": "PCM",
	".mp3": "MP3",

	"V_MPEG4/ISO/AVC": "H.264", "V_MPEGH/ISO/HEVC": "H.265", "V_AV1": "AV1", "V_VP8": "VP8", "V_VP9": "VP9",
	"V_MJPEG": "Motion JPEG", "V_PRORES": "ProRes", "V_THEORA": "Theora", "V_MPEG4/ISO/ASP": "MPEG-4 Part 2",
	"A_OPUS": "Opus", "A_VORBIS": "Vorbis", "A_AAC": "AAC", "A_MPEG/L3": "MP3", "A_FLAC": "FLAC",
	"A_AC3": "AC-3", "A_EAC3": "E-AC-3", "A_PCM/INT/LIT": "PCM", "A_PCM/FLOAT/IEEE": "PCM",
}

func codecName(id string) string {
	if name, ok := codecNames[strings.ToLower(id)]; ok {
		return name
	}
	if name, ok := codecNames[id]; ok {
		return name
	}
	if strings.HasPrefix(id, "A_AAC") {
		return "AAC"
	}
	return strings.TrimSpace(id)
}

type bmffTrack struct {
	handler   string
	codec     string
	width     int
	height    int
	timescale uint32
	duration  uint64
	samples   uint64
	rate      int
	channels  int
}

type bmffProbe struct {
	r      io.ReaderAt
	boxes  int
	brand  string
	scale  uint32
	length uint64
	tracks []*bmffTrack
}

func probeBMFF(name string, r io.ReaderAt, size int64) (*VideoStats, error) {
	p := &bmffProbe{r: r}
	if err := p.walk(0, size, 0, nil); err != nil {
		return nil, err
	}
	if p.scale == 0 && len(p.tracks) == 0 {
		return nil, errors.New("preview: no movie header in video")
	}
	st := &VideoStats{Container: "mp4", Tracks: len(p.tracks)}
	if p.brand == "qt  " || strings.EqualFold(filepath.Ext(name), ".mov") {
		st.Container = "mov"
	}
	if p.scale > 0 {
		st.Duration = float64(p.length) / float64(p.scale)
	}
	for _, t := range p.tracks {
		switch t.handler {
		case "vide":
			if st.VideoCodecID != "" {
				continue
			}
			st.VideoCodecID, st.VideoCodec = t.codec, codecName(t.codec)
			st.Width, st.Height = t.width, t.height
			if t.timescale > 0 && t.duration > 0 && t.samples > 0 {
				st.FrameRate = float64(t.samples) / (float64(t.duration) / float64(t.timescale))
			}
			if st.Duration == 0 && t.timescale > 0 {
				st.Duration = float64(t.duration) / float64(t.timescale)
			}
		case "soun":
			if st.AudioCodecID != "" {
				continue
			}
			st.AudioCodecID, st.AudioCodec = t.codec, codecName(t.codec)
			st.SampleRate, st.Channels = t.rate, t.channels
			if st.SampleRate == 0 {
				st.SampleRate = int(t.timescale)
			}
		}
	}
	return st, nil
}

func (p *bmffProbe) walk(start, end int64, depth int, track *bmffTrack) error {
	if depth > maxVideoDepth {
		return errVideoLimit
	}
	for off := start; off+8 <= end; {
		p.boxes++
		if p.boxes > maxVideoBoxes {
			return errVideoLimit
		}
		var hdr [16]byte
		if _, err := p.r.ReadAt(hdr[:8], off); err != nil {
			return nil
		}
		size := int64(binary.BigEndian.Uint32(hdr[:4]))
		typ := string(hdr[4:8])
		body := off + 8
		switch size {
		case 0:
			size = end - off
		case 1:
			if _, err := p.r.ReadAt(hdr[8:16], off+8); err != nil {
				return nil
			}
			large := binary.BigEndian.Uint64(hdr[8:16])
			if large > uint64(end-off) {
				return nil
			}
			size, body = int64(large), off+16
		}
		if size < body-off || off+size > end {
			return nil
		}
		next := off + size
		if err := p.box(typ, body, next, depth, track); err != nil {
			return err
		}
		off = next
	}
	return nil
}

func (p *bmffProbe) leaf(start, end int64, limit int) []byte {
	n := min(end-start, int64(limit))
	if n <= 0 {
		return nil
	}
	buf := make([]byte, n)
	if _, err := p.r.ReadAt(buf, start); err != nil && !errors.Is(err, io.EOF) {
		return nil
	}
	return buf
}

func (p *bmffProbe) box(typ string, start, end int64, depth int, track *bmffTrack) error {
	switch typ {
	case "moov", "mdia", "minf", "stbl", "edts":
		return p.walk(start, end, depth+1, track)
	case "trak":
		t := &bmffTrack{}
		p.tracks = append(p.tracks, t)
		return p.walk(start, end, depth+1, t)
	case "ftyp":
		if b := p.leaf(start, end, 4); len(b) == 4 {
			p.brand = string(b)
		}
	case "mvhd":
		if scale, dur, ok := timeHeader(p.leaf(start, end, 32)); ok {
			p.scale, p.length = scale, dur
		}
	case "mdhd":
		if track != nil {
			if scale, dur, ok := timeHeader(p.leaf(start, end, 32)); ok {
				track.timescale, track.duration = scale, dur
			}
		}
	case "tkhd":
		if track != nil {
			track.width, track.height = trackSize(p.leaf(start, end, 96))
		}
	case "hdlr":
		if b := p.leaf(start, end, 12); track != nil && len(b) == 12 {
			track.handler = string(b[8:12])
		}
	case "stsd":
		if track != nil {
			parseSampleDescription(p.leaf(start, end, 64), track)
		}
	case "stts":
		if track != nil {
			track.samples = sampleCount(p.leaf(start, end, maxVideoLeafRead))
		}
	}
	return nil
}

func timeHeader(b []byte) (uint32, uint64, bool) {
	if len(b) < 4 {
		return 0, 0, false
	}
	if b[0] == 1 {
		if len(b) < 32 {
			return 0, 0, false
		}
		return binary.BigEndian.Uint32(b[20:24]), binary.BigEndian.Uint64(b[24:32]), true
	}
	if len(b) < 20 {
		return 0, 0, false
	}
	return binary.BigEndian.Uint32(b[12:16]), uint64(binary.BigEndian.Uint32(b[16:20])), true
}

func trackSize(b []byte) (int, int) {
	off := 76
	if len(b) > 0 && b[0] == 1 {
		off = 88
	}
	if len(b) < off+8 {
		return 0, 0
	}
	return int(binary.BigEndian.Uint32(b[off:off+4]) >> 16), int(binary.BigEndian.Uint32(b[off+4:off+8]) >> 16)
}

func parseSampleDescription(b []byte, t *bmffTrack) {
	if len(b) < 16 || binary.BigEndian.Uint32(b[4:8]) == 0 {
		return
	}
	entry := b[8:]
	t.codec = string(entry[4:8])
	if len(entry) >= 36 && t.handler == "soun" {
		t.channels = int(binary.BigEndian.Uint16(entry[24:26]))
		t.rate = int(binary.BigEndian.Uint32(entry[32:36]) >> 16)
	}
}

func sampleCount(b []byte) uint64 {
	if len(b) < 8 {
		return 0
	}
	n := int(binary.BigEndian.Uint32(b[4:8]))
	var total uint64
	for i := 0; i < n && 8+i*8+8 <= len(b); i++ {
		total += uint64(binary.BigEndian.Uint32(b[8+i*8 : 12+i*8]))
	}
	return total
}
