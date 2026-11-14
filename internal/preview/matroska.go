package preview

import (
	"encoding/binary"
	"errors"
	"io"
	"math"
)

const (
	ebmlHeaderID     = 0x1A45DFA3
	ebmlDocType      = 0x4282
	mkvSegment       = 0x18538067
	mkvInfo          = 0x1549A966
	mkvTimecodeScale = 0x2AD7B1
	mkvDuration      = 0x4489
	mkvTracks        = 0x1654AE6B
	mkvTrackEntry    = 0xAE
	mkvTrackType     = 0x83
	mkvCodecID       = 0x86
	mkvDefaultDur    = 0x23E383
	mkvVideo         = 0xE0
	mkvAudio         = 0xE1
	mkvPixelWidth    = 0xB0
	mkvPixelHeight   = 0xBA
	mkvSampling      = 0xB5
	mkvChannels      = 0x9F
	mkvCluster       = 0x1F43B675
	unknownSize      = -1
)

type ebmlReader struct {
	r        io.ReaderAt
	elements int
}

func (e *ebmlReader) vint(off int64, keepMarker bool) (int64, int, error) {
	var first [1]byte
	if _, err := e.r.ReadAt(first[:], off); err != nil {
		return 0, 0, err
	}
	width := 1
	for mask := byte(0x80); width <= 8 && first[0]&mask == 0; mask >>= 1 {
		width++
	}
	if width > 8 {
		return 0, 0, errors.New("preview: bad EBML number")
	}
	buf := make([]byte, width)
	if _, err := e.r.ReadAt(buf, off); err != nil {
		return 0, 0, err
	}
	if !keepMarker {
		buf[0] &= byte(0xFF >> width)
	}
	var v uint64
	allOnes := true
	for i, b := range buf {
		v = v<<8 | uint64(b)
		limit := byte(0xFF)
		if i == 0 {
			limit = byte(0xFF >> width)
		}
		if b != limit {
			allOnes = false
		}
	}
	if !keepMarker && allOnes {
		return unknownSize, width, nil
	}
	if v > math.MaxInt64 {
		return 0, 0, errors.New("preview: EBML number overflows")
	}
	return int64(v), width, nil
}

type ebmlElement struct {
	id    int64
	start int64
	end   int64
}

// children calls fn for every element in [start, end) until fn returns false.
func (e *ebmlReader) children(start, end int64, fn func(ebmlElement) (bool, error)) error {
	for off := start; off < end; {
		e.elements++
		if e.elements > maxVideoBoxes {
			return errVideoLimit
		}
		id, idWidth, err := e.vint(off, true)
		if err != nil {
			return nil
		}
		size, sizeWidth, err := e.vint(off+int64(idWidth), false)
		if err != nil {
			return nil
		}
		body := off + int64(idWidth+sizeWidth)
		elEnd := end
		if size != unknownSize {
			if size > end-body {
				elEnd = end
			} else {
				elEnd = body + size
			}
		}
		more, err := fn(ebmlElement{id: id, start: body, end: elEnd})
		if err != nil || !more {
			return err
		}
		if size == unknownSize && id != mkvSegment {
			return nil
		}
		off = elEnd
	}
	return nil
}

func (e *ebmlReader) bytes(el ebmlElement, limit int) []byte {
	n := min(el.end-el.start, int64(limit))
	if n <= 0 {
		return nil
	}
	buf := make([]byte, n)
	if _, err := e.r.ReadAt(buf, el.start); err != nil && !errors.Is(err, io.EOF) {
		return nil
	}
	return buf
}

func (e *ebmlReader) uint(el ebmlElement) uint64 {
	var v uint64
	for _, b := range e.bytes(el, 8) {
		v = v<<8 | uint64(b)
	}
	return v
}

func (e *ebmlReader) float(el ebmlElement) float64 {
	b := e.bytes(el, 8)
	switch len(b) {
	case 4:
		return float64(math.Float32frombits(binary.BigEndian.Uint32(b)))
	case 8:
		return math.Float64frombits(binary.BigEndian.Uint64(b))
	}
	return 0
}

type mkvTrack struct {
	kind     uint64
	codec    string
	width    int
	height   int
	frameNS  uint64
	rate     float64
	channels int
}

func probeMatroska(r io.ReaderAt, size int64) (*VideoStats, error) {
	e := &ebmlReader{r: r}
	st := &VideoStats{Container: "mkv"}
	var segment *ebmlElement
	err := e.children(0, size, func(el ebmlElement) (bool, error) {
		switch el.id {
		case ebmlHeaderID:
			return true, e.children(el.start, el.end, func(c ebmlElement) (bool, error) {
				if c.id == ebmlDocType && string(e.bytes(c, 16)) == "webm" {
					st.Container = "webm"
				}
				return true, nil
			})
		case mkvSegment:
			segment = &el
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	if segment == nil {
		return nil, errors.New("preview: no Matroska segment")
	}
	scale := uint64(1000000)
	var duration float64
	var tracks []*mkvTrack
	err = e.children(segment.start, segment.end, func(el ebmlElement) (bool, error) {
		switch el.id {
		case mkvInfo:
			return true, e.children(el.start, el.end, func(c ebmlElement) (bool, error) {
				switch c.id {
				case mkvTimecodeScale:
					if v := e.uint(c); v > 0 {
						scale = v
					}
				case mkvDuration:
					duration = e.float(c)
				}
				return true, nil
			})
		case mkvTracks:
			return true, e.children(el.start, el.end, func(c ebmlElement) (bool, error) {
				if c.id != mkvTrackEntry {
					return true, nil
				}
				t := &mkvTrack{}
				tracks = append(tracks, t)
				return true, e.trackEntry(c, t)
			})
		case mkvCluster:
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	if duration > 0 && !math.IsInf(duration, 0) && !math.IsNaN(duration) {
		st.Duration = duration * float64(scale) / 1e9
	}
	st.Tracks = len(tracks)
	for _, t := range tracks {
		switch {
		case t.kind == 1 && st.VideoCodecID == "":
			st.VideoCodecID, st.VideoCodec = t.codec, codecName(t.codec)
			st.Width, st.Height = t.width, t.height
			if t.frameNS > 0 {
				st.FrameRate = 1e9 / float64(t.frameNS)
			}
		case t.kind == 2 && st.AudioCodecID == "":
			st.AudioCodecID, st.AudioCodec = t.codec, codecName(t.codec)
			st.SampleRate, st.Channels = int(t.rate), t.channels
		}
	}
	return st, nil
}

func (e *ebmlReader) trackEntry(el ebmlElement, t *mkvTrack) error {
	return e.children(el.start, el.end, func(c ebmlElement) (bool, error) {
		switch c.id {
		case mkvTrackType:
			t.kind = e.uint(c)
		case mkvCodecID:
			t.codec = string(e.bytes(c, 64))
		case mkvDefaultDur:
			t.frameNS = e.uint(c)
		case mkvVideo:
			return true, e.children(c.start, c.end, func(v ebmlElement) (bool, error) {
				switch v.id {
				case mkvPixelWidth:
					t.width = int(min(e.uint(v), 1<<20))
				case mkvPixelHeight:
					t.height = int(min(e.uint(v), 1<<20))
				}
				return true, nil
			})
		case mkvAudio:
			return true, e.children(c.start, c.end, func(a ebmlElement) (bool, error) {
				switch a.id {
				case mkvSampling:
					if f := e.float(a); f > 0 && f < 1e7 {
						t.rate = f
					}
				case mkvChannels:
					t.channels = int(min(e.uint(a), 1024))
				}
				return true, nil
			})
		}
		return true, nil
	})
}
