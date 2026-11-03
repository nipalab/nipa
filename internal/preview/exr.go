package preview

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
)

const (
	exrMagic         = 20000630
	exrFlagTiled     = 0x200
	exrFlagDeep      = 0x800
	exrFlagMultipart = 0x1000
	exrUint          = 0
	exrHalf          = 1
	exrFloat         = 2
)

var exrCompressions = []string{"NONE", "RLE", "ZIPS", "ZIP", "PIZ", "PXR24", "B44", "B44A", "DWAA", "DWAB", "HTJ2K256", "HTJ2K32"}

const exrMaxBlockBytes = 256 << 20

var exrLinesPerBlock = map[int]int{0: 1, 1: 1, 2: 1, 3: 16, 5: 16}

var (
	errEXRHeader    = errors.New("preview: invalid exr header")
	errEXRTruncated = errors.New("preview: truncated exr data")
)

type exrChannel struct {
	name     string
	kind     int
	size     int
	sampling bool
}

type exrHeader struct {
	channels                  []exrChannel
	compression               int
	minX, minY, width, height int
	tiled                     bool
	tileWidth, tileHeight     int
	offsetTable               int
}

func decodeEXR(data []byte) (*Image, error) {
	h, err := parseEXRHeader(data)
	if err != nil {
		return nil, err
	}
	if _, ok := exrLinesPerBlock[h.compression]; !ok {
		name := fmt.Sprint(h.compression)
		if h.compression < len(exrCompressions) {
			name = exrCompressions[h.compression]
		}
		return nil, fmt.Errorf("%w: EXR %s compression", ErrUnsupported, name)
	}
	if err := checkFloatSize(h.width, h.height); err != nil {
		return nil, err
	}
	pick, depth, err := h.displayChannels()
	if err != nil {
		return nil, err
	}
	if err := h.plausible(len(data)); err != nil {
		return nil, err
	}
	img := newFloatImage(h.width, h.height)
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 1
	}
	if h.tiled {
		err = h.readTiles(data, img, pick)
	} else {
		err = h.readScanlines(data, img, pick)
	}
	if err != nil {
		return nil, err
	}
	out := &Image{Format: "exr", Encoding: exrCompressions[h.compression], BitDepth: depth}
	out.setHDR(img)
	return out, nil
}

func parseEXRHeader(data []byte) (*exrHeader, error) {
	if len(data) < 8 || binary.LittleEndian.Uint32(data) != exrMagic {
		return nil, errEXRHeader
	}
	flags := binary.LittleEndian.Uint32(data[4:])
	if flags&0xff != 2 {
		return nil, errEXRHeader
	}
	if flags&(exrFlagDeep|exrFlagMultipart) != 0 {
		return nil, fmt.Errorf("%w: multi-part or deep EXR", ErrUnsupported)
	}
	h := &exrHeader{tiled: flags&exrFlagTiled != 0, compression: -1}
	pos := 8
	cstring := func() (string, error) {
		end := bytes.IndexByte(data[pos:], 0)
		if end < 0 {
			return "", errEXRHeader
		}
		s := string(data[pos : pos+end])
		pos += end + 1
		return s, nil
	}
	haveWindow := false
	for {
		name, err := cstring()
		if err != nil {
			return nil, err
		}
		if name == "" {
			break
		}
		if _, err := cstring(); err != nil {
			return nil, err
		}
		if pos+4 > len(data) {
			return nil, errEXRHeader
		}
		size := int(binary.LittleEndian.Uint32(data[pos:]))
		pos += 4
		if size < 0 || size > len(data)-pos {
			return nil, errEXRHeader
		}
		value := data[pos : pos+size]
		pos += size
		switch name {
		case "channels":
			if h.channels, err = parseEXRChannels(value); err != nil {
				return nil, err
			}
		case "compression":
			if len(value) != 1 {
				return nil, errEXRHeader
			}
			h.compression = int(value[0])
		case "dataWindow":
			if len(value) != 16 {
				return nil, errEXRHeader
			}
			i32 := func(o int) int { return int(int32(binary.LittleEndian.Uint32(value[o:]))) }
			h.minX, h.minY = i32(0), i32(4)
			h.width, h.height = i32(8)-h.minX+1, i32(12)-h.minY+1
			haveWindow = true
		case "tiles":
			if len(value) != 9 {
				return nil, errEXRHeader
			}
			h.tileWidth = int(binary.LittleEndian.Uint32(value))
			h.tileHeight = int(binary.LittleEndian.Uint32(value[4:]))
		}
	}
	if len(h.channels) == 0 || h.compression < 0 || !haveWindow || (h.tiled && (h.tileWidth <= 0 || h.tileHeight <= 0)) {
		return nil, errEXRHeader
	}
	h.offsetTable = pos
	return h, nil
}

func parseEXRChannels(v []byte) ([]exrChannel, error) {
	var out []exrChannel
	for len(v) > 0 && v[0] != 0 {
		end := bytes.IndexByte(v, 0)
		if end < 0 || len(v) < end+17 {
			return nil, errEXRHeader
		}
		ch := exrChannel{name: string(v[:end]), kind: int(binary.LittleEndian.Uint32(v[end+1:]))}
		xs, ys := binary.LittleEndian.Uint32(v[end+9:]), binary.LittleEndian.Uint32(v[end+13:])
		ch.sampling = xs != 1 || ys != 1
		switch ch.kind {
		case exrHalf:
			ch.size = 2
		case exrUint, exrFloat:
			ch.size = 4
		default:
			return nil, errEXRHeader
		}
		out = append(out, ch)
		v = v[end+17:]
	}
	return out, nil
}

// displayChannels maps the image's channels onto RGBA: the unnamed layer
// when it has colour or luminance, otherwise the first named layer with
// colour; a lone channel is shown as gray. It returns, per channel, the
// RGBA slot it feeds (-1 to skip; 4 marks gray).
func (h *exrHeader) displayChannels() ([]int, int, error) {
	layers := map[string]map[string]int{}
	for i, ch := range h.channels {
		layer, base := "", ch.name
		if dot := strings.LastIndexByte(ch.name, '.'); dot >= 0 {
			layer, base = ch.name[:dot], ch.name[dot+1:]
		}
		if layers[layer] == nil {
			layers[layer] = map[string]int{}
		}
		layers[layer][strings.ToUpper(base)] = i
	}
	names := make([]string, 0, len(layers))
	for name := range layers {
		names = append(names, name)
	}
	sort.Strings(names)
	chosen := ""
	found := false
	for _, name := range names {
		l := layers[name]
		_, r := l["R"]
		_, y := l["Y"]
		if r || (y && name == "") {
			chosen, found = name, true
			break
		}
	}
	pick := make([]int, len(h.channels))
	for i := range pick {
		pick[i] = -1
	}
	if !found {
		pick[0] = 4
	} else {
		l := layers[chosen]
		slots := map[string]int{"R": 0, "G": 1, "B": 2, "A": 3}
		if _, r := l["R"]; !r {
			slots = map[string]int{"Y": 4, "A": 3}
		}
		for base, slot := range slots {
			if i, ok := l[base]; ok {
				pick[i] = slot
			}
		}
	}
	depth := 0
	for i, slot := range pick {
		if slot < 0 {
			continue
		}
		if h.channels[i].sampling {
			return nil, 0, fmt.Errorf("%w: subsampled EXR channels", ErrUnsupported)
		}
		depth = max(depth, h.channels[i].size*8)
	}
	return pick, depth, nil
}

// plausible rejects files too short to hold the offset table and one chunk
// header per block, before the image is allocated.
func (h *exrHeader) plausible(size int) error {
	blocks := (h.height + exrLinesPerBlock[h.compression] - 1) / exrLinesPerBlock[h.compression]
	if h.tiled {
		blocks = ((h.width + h.tileWidth - 1) / h.tileWidth) * ((h.height + h.tileHeight - 1) / h.tileHeight)
	}
	if blocks > (size-h.offsetTable)/16 {
		return errEXRTruncated
	}
	return nil
}

func (h *exrHeader) lineBytes(width int) int {
	n := 0
	for _, ch := range h.channels {
		n += ch.size * width
	}
	return n
}

func (h *exrHeader) offset(data []byte, i int) (int, error) {
	at := h.offsetTable + i*8
	if at+8 > len(data) {
		return 0, errEXRTruncated
	}
	off := binary.LittleEndian.Uint64(data[at:])
	if off > uint64(len(data)) {
		return 0, errEXRTruncated
	}
	return int(off), nil
}

func (h *exrHeader) readScanlines(data []byte, img *FloatImage, pick []int) error {
	lpb := exrLinesPerBlock[h.compression]
	blocks := (h.height + lpb - 1) / lpb
	for b := range blocks {
		off, err := h.offset(data, b)
		if err != nil {
			return err
		}
		if off+8 > len(data) {
			return errEXRTruncated
		}
		y := int(int32(binary.LittleEndian.Uint32(data[off:]))) - h.minY
		size := int(binary.LittleEndian.Uint32(data[off+4:]))
		if y < 0 || y >= h.height || size < 0 || size > len(data)-off-8 {
			return errEXRTruncated
		}
		lines := min(lpb, h.height-y)
		raw, err := h.uncompress(data[off+8:off+8+size], h.width, lines)
		if err != nil {
			return err
		}
		h.place(raw, img, pick, 0, y, h.width, lines)
	}
	return nil
}

func (h *exrHeader) readTiles(data []byte, img *FloatImage, pick []int) error {
	tilesX := (h.width + h.tileWidth - 1) / h.tileWidth
	tilesY := (h.height + h.tileHeight - 1) / h.tileHeight
	for i := range tilesX * tilesY {
		off, err := h.offset(data, i)
		if err != nil {
			return err
		}
		if off+20 > len(data) {
			return errEXRTruncated
		}
		i32 := func(o int) int { return int(int32(binary.LittleEndian.Uint32(data[off+o:]))) }
		tx, ty, lx, ly, size := i32(0), i32(4), i32(8), i32(12), i32(16)
		if lx != 0 || ly != 0 || tx < 0 || ty < 0 || tx >= tilesX || ty >= tilesY || size < 0 || size > len(data)-off-20 {
			return errEXRTruncated
		}
		x0, y0 := tx*h.tileWidth, ty*h.tileHeight
		w, lines := min(h.tileWidth, h.width-x0), min(h.tileHeight, h.height-y0)
		raw, err := h.uncompress(data[off+20:off+20+size], w, lines)
		if err != nil {
			return err
		}
		h.place(raw, img, pick, x0, y0, w, lines)
	}
	return nil
}

func (h *exrHeader) place(raw []byte, img *FloatImage, pick []int, x0, y0, w, lines int) {
	pos := 0
	for line := range lines {
		row := (y0 + line) * img.Width
		for ci, ch := range h.channels {
			slot := pick[ci]
			for x := range w {
				if slot >= 0 {
					v := exrSample(raw[pos:], ch.kind)
					px := img.Pix[(row+x0+x)*4:]
					if slot == 4 {
						px[0], px[1], px[2] = v, v, v
					} else {
						px[slot] = v
					}
				}
				pos += ch.size
			}
		}
	}
}

func exrSample(b []byte, kind int) float32 {
	switch kind {
	case exrHalf:
		return halfToFloat(binary.LittleEndian.Uint16(b))
	case exrFloat:
		return math.Float32frombits(binary.LittleEndian.Uint32(b))
	}
	return float32(binary.LittleEndian.Uint32(b))
}

func (h *exrHeader) uncompress(src []byte, width, lines int) ([]byte, error) {
	want := h.lineBytes(width) * lines
	if want > exrMaxBlockBytes {
		return nil, ErrTooLarge
	}
	if len(src) >= want || h.compression == 0 {
		if len(src) < want {
			return nil, errEXRTruncated
		}
		return src[:want], nil
	}
	switch h.compression {
	case 1:
		return exrUnpredict(exrRLE(src, want), want)
	case 2, 3:
		raw, err := exrInflate(src, want)
		if err != nil {
			return nil, err
		}
		return exrUnpredict(raw, want)
	}
	return h.pxr24(src, width, lines, want)
}

func exrInflate(src []byte, limit int) ([]byte, error) {
	zr, err := zlib.NewReader(bytes.NewReader(src))
	if err != nil {
		return nil, fmt.Errorf("preview: exr zlib: %w", err)
	}
	out, err := io.ReadAll(io.LimitReader(zr, int64(limit)))
	if err != nil {
		return nil, fmt.Errorf("preview: exr zlib: %w", err)
	}
	return out, nil
}

func exrRLE(src []byte, limit int) []byte {
	out := make([]byte, 0, min(limit, 2*len(src)))
	for len(src) > 0 && len(out) < limit {
		n := int(int8(src[0]))
		src = src[1:]
		if n < 0 {
			n = min(-n, len(src))
			out = append(out, src[:n]...)
			src = src[n:]
			continue
		}
		if len(src) == 0 {
			break
		}
		for range min(n+1, limit-len(out)) {
			out = append(out, src[0])
		}
		src = src[1:]
	}
	return out
}

func exrUnpredict(t []byte, want int) ([]byte, error) {
	if len(t) != want {
		return nil, errEXRTruncated
	}
	for i := 1; i < len(t); i++ {
		t[i] = t[i-1] + t[i] - 128
	}
	out := make([]byte, len(t))
	half := (len(t) + 1) / 2
	for i := range out {
		if i%2 == 0 {
			out[i] = t[i/2]
		} else {
			out[i] = t[half+i/2]
		}
	}
	return out, nil
}

// pxr24 undoes the PXR24 byte-plane transposition and delta coding. Float
// channels keep 24 bits of their 32, which is what makes the codec lossy.
func (h *exrHeader) pxr24(src []byte, width, lines, want int) ([]byte, error) {
	planes := 0
	for _, ch := range h.channels {
		switch ch.kind {
		case exrHalf:
			planes += 2
		case exrFloat:
			planes += 3
		default:
			planes += 4
		}
	}
	tmp, err := exrInflate(src, planes*width*lines)
	if err != nil {
		return nil, err
	}
	if len(tmp) != planes*width*lines {
		return nil, errEXRTruncated
	}
	out := make([]byte, 0, want)
	pos := 0
	for range lines {
		for _, ch := range h.channels {
			var pixel uint32
			switch ch.kind {
			case exrHalf:
				p0, p1 := tmp[pos:pos+width], tmp[pos+width:pos+2*width]
				pos += 2 * width
				for x := range width {
					pixel += uint32(p0[x])<<8 | uint32(p1[x])
					out = binary.LittleEndian.AppendUint16(out, uint16(pixel))
				}
			case exrFloat:
				p0, p1, p2 := tmp[pos:pos+width], tmp[pos+width:pos+2*width], tmp[pos+2*width:pos+3*width]
				pos += 3 * width
				for x := range width {
					pixel += uint32(p0[x])<<24 | uint32(p1[x])<<16 | uint32(p2[x])<<8
					out = binary.LittleEndian.AppendUint32(out, pixel)
				}
			default:
				p := [4][]byte{tmp[pos : pos+width], tmp[pos+width : pos+2*width], tmp[pos+2*width : pos+3*width], tmp[pos+3*width : pos+4*width]}
				pos += 4 * width
				for x := range width {
					pixel += uint32(p[0][x])<<24 | uint32(p[1][x])<<16 | uint32(p[2][x])<<8 | uint32(p[3][x])
					out = binary.LittleEndian.AppendUint32(out, pixel)
				}
			}
		}
	}
	return out, nil
}
