package preview

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/fnv"
	"image"
	"math"
	"unicode/utf16"
)

// Layer describes one Photoshop layer or group, top-most first; Depth is the
// group nesting. Hash fingerprints the layer's pixel data so a changed layer
// can be told from an untouched one.
type Layer struct {
	Name    string
	Kind    string
	Depth   int
	Visible bool
	Opacity int
	Blend   string
	X, Y    int
	Width   int
	Height  int
	Hash    string
}

const (
	psdModeBitmap    = 0
	psdModeGray      = 1
	psdModeIndexed   = 2
	psdModeRGB       = 3
	psdModeCMYK      = 4
	psdModeMulti     = 7
	psdModeDuotone   = 8
	psdCompressRaw   = 0
	psdCompressRLE   = 1
	psdLayerHidden   = 0x2
	psdGroupOpen     = 1
	psdGroupClosed   = 2
	psdGroupEnd      = 3
	psdMaxChannels   = 56
	psdDecodedPlanes = 5
)

var (
	errPSDHeader    = errors.New("preview: invalid psd header")
	errPSDTruncated = errors.New("preview: truncated psd data")
)

type psdReader struct {
	data []byte
	pos  int
	big  bool
}

func (r *psdReader) need(n int) error {
	if n < 0 || n > len(r.data)-r.pos {
		return errPSDTruncated
	}
	return nil
}

func (r *psdReader) bytes(n int) ([]byte, error) {
	if err := r.need(n); err != nil {
		return nil, err
	}
	b := r.data[r.pos : r.pos+n]
	r.pos += n
	return b, nil
}

func (r *psdReader) u16() (int, error) {
	b, err := r.bytes(2)
	if err != nil {
		return 0, err
	}
	return int(binary.BigEndian.Uint16(b)), nil
}

func (r *psdReader) u32() (int, error) {
	b, err := r.bytes(4)
	if err != nil {
		return 0, err
	}
	return int(binary.BigEndian.Uint32(b)), nil
}

func (r *psdReader) length(wide bool) (int, error) {
	if !wide {
		return r.u32()
	}
	b, err := r.bytes(8)
	if err != nil {
		return 0, err
	}
	v := binary.BigEndian.Uint64(b)
	if v > uint64(len(r.data)) {
		return 0, errPSDTruncated
	}
	return int(v), nil
}

func (r *psdReader) section(wide bool) (*psdReader, error) {
	n, err := r.length(wide)
	if err != nil {
		return nil, err
	}
	b, err := r.bytes(n)
	if err != nil {
		return nil, err
	}
	return &psdReader{data: b, big: r.big}, nil
}

type psdHeader struct {
	channels, height, width, depth, mode int
}

func decodePSD(data []byte) (*Image, error) {
	r := &psdReader{data: data}
	sig, err := r.bytes(4)
	if err != nil || string(sig) != "8BPS" {
		return nil, errPSDHeader
	}
	version, _ := r.u16()
	if version != 1 && version != 2 {
		return nil, errPSDHeader
	}
	r.big = version == 2
	if _, err := r.bytes(6); err != nil {
		return nil, err
	}
	var h psdHeader
	if h.channels, err = r.u16(); err != nil {
		return nil, err
	}
	if h.height, err = r.u32(); err != nil {
		return nil, err
	}
	if h.width, err = r.u32(); err != nil {
		return nil, err
	}
	if h.depth, err = r.u16(); err != nil {
		return nil, err
	}
	if h.mode, err = r.u16(); err != nil {
		return nil, err
	}
	if h.channels < 1 || h.channels > psdMaxChannels || (h.depth != 1 && h.depth != 8 && h.depth != 16 && h.depth != 32) {
		return nil, errPSDHeader
	}
	if err := checkSize(h.width, h.height); err != nil {
		return nil, err
	}
	colorData, err := r.section(false)
	if err != nil {
		return nil, err
	}
	if _, err := r.section(false); err != nil {
		return nil, err
	}
	layerSection, err := r.section(r.big)
	if err != nil {
		return nil, err
	}
	layers, mergedAlpha := parsePSDLayers(layerSection)

	planes, err := readPSDPlanes(r, h)
	if err != nil {
		return nil, err
	}
	format := "psd"
	if r.big {
		format = "psb"
	}
	img := &Image{Format: format, Encoding: psdModeName(h.mode), BitDepth: h.depth, Layers: layers}
	if h.depth == 32 {
		if err := checkFloatSize(h.width, h.height); err != nil {
			return nil, err
		}
		f, err := psdFloatComposite(h, planes, mergedAlpha)
		if err != nil {
			return nil, err
		}
		img.setHDR(f)
		return img, nil
	}
	if img.Pixels, err = psdComposite(h, planes, colorData.data, mergedAlpha); err != nil {
		return nil, err
	}
	img.HasAlpha = hasAlpha(img.Pixels)
	return img, nil
}

func psdModeName(mode int) string {
	switch mode {
	case psdModeBitmap:
		return "Bitmap"
	case psdModeGray:
		return "Grayscale"
	case psdModeIndexed:
		return "Indexed"
	case psdModeRGB:
		return "RGB"
	case psdModeCMYK:
		return "CMYK"
	case psdModeMulti:
		return "Multichannel"
	case psdModeDuotone:
		return "Duotone"
	}
	return fmt.Sprintf("mode %d", mode)
}

func (h psdHeader) rowBytes() int {
	if h.depth == 1 {
		return (h.width + 7) / 8
	}
	return h.width * h.depth / 8
}

// readPSDPlanes reads the merged image's first channels (colour plus the
// merged transparency); further spot and alpha channels are skipped.
func readPSDPlanes(r *psdReader, h psdHeader) ([][]byte, error) {
	compression, err := r.u16()
	if err != nil {
		return nil, err
	}
	n := min(h.channels, psdDecodedPlanes)
	rowBytes := h.rowBytes()
	planes := make([][]byte, n)
	switch compression {
	case psdCompressRaw:
		for c := range n {
			if planes[c], err = r.bytes(rowBytes * h.height); err != nil {
				return nil, err
			}
		}
	case psdCompressRLE:
		counts := make([]int, h.channels*h.height)
		for i := range counts {
			if r.big {
				counts[i], err = r.u32()
			} else {
				counts[i], err = r.u16()
			}
			if err != nil {
				return nil, err
			}
		}
		for c := range n {
			var plane []byte
			for y := range h.height {
				src, err := r.bytes(counts[c*h.height+y])
				if err != nil {
					return nil, err
				}
				if plane, err = unpackBits(plane, src, rowBytes); err != nil {
					return nil, err
				}
			}
			planes[c] = plane
		}
	default:
		return nil, fmt.Errorf("%w: PSD image compression %d", ErrUnsupported, compression)
	}
	return planes, nil
}

func unpackBits(dst, src []byte, want int) ([]byte, error) {
	start := len(dst)
	for len(src) > 0 && len(dst)-start < want {
		n := int(int8(src[0]))
		src = src[1:]
		switch {
		case n >= 0:
			if n+1 > len(src) {
				return nil, errPSDTruncated
			}
			dst = append(dst, src[:n+1]...)
			src = src[n+1:]
		case n > -128:
			if len(src) == 0 {
				return nil, errPSDTruncated
			}
			for range 1 - n {
				dst = append(dst, src[0])
			}
			src = src[1:]
		}
	}
	if len(dst)-start < want {
		return nil, errPSDTruncated
	}
	return dst[:start+want], nil
}

func psdSample(plane []byte, i, depth int) uint8 {
	switch depth {
	case 1:
		if plane[i/8]&(0x80>>(i%8)) != 0 {
			return 0
		}
		return 0xff
	case 16:
		return plane[i*2]
	}
	return plane[i]
}

func psdComposite(h psdHeader, planes [][]byte, colorData []byte, mergedAlpha bool) (*image.NRGBA, error) {
	dst := image.NewNRGBA(image.Rect(0, 0, h.width, h.height))
	colors := 1
	switch h.mode {
	case psdModeRGB:
		colors = 3
	case psdModeCMYK:
		colors = 4
	case psdModeIndexed:
		if len(colorData) < 768 {
			return nil, errPSDHeader
		}
	case psdModeGray, psdModeBitmap, psdModeDuotone, psdModeMulti:
	default:
		return nil, fmt.Errorf("%w: PSD %s colour mode", ErrUnsupported, psdModeName(h.mode))
	}
	if len(planes) < colors {
		return nil, errPSDHeader
	}
	alpha := -1
	if mergedAlpha && len(planes) > colors && h.mode != psdModeBitmap && h.mode != psdModeIndexed {
		alpha = colors
	}
	rowBytes := h.rowBytes()
	for y := range h.height {
		for x := range h.width {
			i := y*h.width + x
			if h.depth == 1 {
				i = y*rowBytes*8 + x
			}
			px := dst.Pix[y*dst.Stride+x*4:]
			s := func(c int) uint8 { return psdSample(planes[c], i, h.depth) }
			switch h.mode {
			case psdModeRGB:
				px[0], px[1], px[2] = s(0), s(1), s(2)
			case psdModeCMYK:
				k := int(s(3))
				px[0], px[1], px[2] = uint8(int(s(0))*k/255), uint8(int(s(1))*k/255), uint8(int(s(2))*k/255)
			case psdModeIndexed:
				v := int(s(0))
				px[0], px[1], px[2] = colorData[v], colorData[256+v], colorData[512+v]
			default:
				v := s(0)
				px[0], px[1], px[2] = v, v, v
			}
			px[3] = 0xff
			if alpha >= 0 {
				px[3] = s(alpha)
			}
		}
	}
	return dst, nil
}

func psdFloatComposite(h psdHeader, planes [][]byte, mergedAlpha bool) (*FloatImage, error) {
	colors := 1
	switch h.mode {
	case psdModeRGB:
		colors = 3
	case psdModeGray:
	default:
		return nil, fmt.Errorf("%w: 32-bit PSD %s", ErrUnsupported, psdModeName(h.mode))
	}
	if len(planes) < colors {
		return nil, errPSDHeader
	}
	f := newFloatImage(h.width, h.height)
	sample := func(c, i int) float32 { return math.Float32frombits(binary.BigEndian.Uint32(planes[c][i*4:])) }
	for i := range h.width * h.height {
		px := f.Pix[i*4:]
		for c := range 3 {
			px[c] = sample(min(c, colors-1), i)
		}
		px[3] = 1
		if mergedAlpha && len(planes) > colors {
			px[3] = sample(colors, i)
		}
	}
	return f, nil
}

var psdWideKeys = map[string]bool{
	"LMsk": true, "Lr16": true, "Lr32": true, "Layr": true, "Mt16": true, "Mt32": true,
	"Mtrn": true, "Alph": true, "FMsk": true, "lnk2": true, "FEid": true, "FXid": true, "PxSD": true,
}

// parsePSDLayers reads the layer records; it never fails the decode, since
// the composite is still worth showing when the layer data is unusual. The
// boolean reports whether the merged image carries a transparency channel.
func parsePSDLayers(r *psdReader) ([]Layer, bool) {
	if len(r.data) == 0 {
		return nil, false
	}
	info, err := r.section(r.big)
	if err != nil {
		return nil, false
	}
	if len(info.data) > 0 {
		return readPSDLayerInfo(info)
	}
	if _, err := r.section(false); err != nil {
		return nil, false
	}
	for r.need(12) == nil {
		sig, _ := r.bytes(4)
		key, _ := r.bytes(4)
		if string(sig) != "8BIM" && string(sig) != "8B64" {
			return nil, false
		}
		block, err := r.section(r.big && psdWideKeys[string(key)])
		if err != nil {
			return nil, false
		}
		if k := string(key); k == "Lr16" || k == "Lr32" || k == "Layr" {
			return readPSDLayerInfo(block)
		}
		r.pos += r.pos % 2
	}
	return nil, false
}

type psdRecord struct {
	layer    Layer
	divider  int
	channels []int
}

func readPSDLayerInfo(r *psdReader) ([]Layer, bool) {
	raw, err := r.u16()
	if err != nil {
		return nil, false
	}
	count := int(int16(raw))
	mergedAlpha := count < 0
	if count < 0 {
		count = -count
	}
	records := make([]psdRecord, 0, count)
	for range count {
		rec, err := readPSDRecord(r)
		if err != nil {
			return nil, mergedAlpha
		}
		records = append(records, rec)
	}
	for i := range records {
		h := fnv.New64a()
		for _, n := range records[i].channels {
			b, err := r.bytes(n)
			if err != nil {
				return nil, mergedAlpha
			}
			h.Write(b)
		}
		records[i].layer.Hash = hex.EncodeToString(h.Sum(nil))
	}
	out := make([]Layer, 0, len(records))
	depth := 0
	for i := len(records) - 1; i >= 0; i-- {
		rec := records[i]
		switch rec.divider {
		case psdGroupEnd:
			depth = max(depth-1, 0)
			continue
		case psdGroupOpen, psdGroupClosed:
			rec.layer.Kind, rec.layer.Depth = "group", depth
			rec.layer.X, rec.layer.Y, rec.layer.Width, rec.layer.Height, rec.layer.Hash = 0, 0, 0, 0, ""
			out = append(out, rec.layer)
			depth++
			continue
		}
		rec.layer.Kind, rec.layer.Depth = "layer", depth
		out = append(out, rec.layer)
	}
	return out, mergedAlpha
}

func readPSDRecord(r *psdReader) (psdRecord, error) {
	var rec psdRecord
	var rect [4]int
	for i := range rect {
		v, err := r.u32()
		if err != nil {
			return rec, err
		}
		rect[i] = int(int32(uint32(v)))
	}
	rec.layer.Y, rec.layer.X = rect[0], rect[1]
	rec.layer.Height, rec.layer.Width = max(rect[2]-rect[0], 0), max(rect[3]-rect[1], 0)
	n, err := r.u16()
	if err != nil {
		return rec, err
	}
	if n > psdMaxChannels {
		return rec, errPSDHeader
	}
	for range n {
		if _, err := r.u16(); err != nil {
			return rec, err
		}
		size, err := r.length(r.big)
		if err != nil {
			return rec, err
		}
		rec.channels = append(rec.channels, size)
	}
	head, err := r.bytes(12)
	if err != nil {
		return rec, err
	}
	if string(head[:4]) != "8BIM" {
		return rec, errPSDHeader
	}
	rec.layer.Blend = string(head[4:8])
	rec.layer.Opacity = int(head[8])
	rec.layer.Visible = head[10]&psdLayerHidden == 0
	extra, err := r.section(false)
	if err != nil {
		return rec, err
	}
	if _, err := extra.section(false); err != nil {
		return rec, err
	}
	if _, err := extra.section(false); err != nil {
		return rec, err
	}
	nameLen, err := extra.bytes(1)
	if err != nil {
		return rec, err
	}
	name, err := extra.bytes(int(nameLen[0]))
	if err != nil {
		return rec, err
	}
	rec.layer.Name = string(name)
	extra.pos += (4 - (1+len(name))%4) % 4
	for extra.need(12) == nil {
		sig, _ := extra.bytes(4)
		key, _ := extra.bytes(4)
		if string(sig) != "8BIM" && string(sig) != "8B64" {
			break
		}
		block, err := extra.section(r.big && psdWideKeys[string(key)])
		if err != nil {
			break
		}
		switch string(key) {
		case "luni":
			if name, ok := psdUnicodeName(block.data); ok {
				rec.layer.Name = name
			}
		case "lsct", "lsdk":
			if len(block.data) >= 4 {
				rec.divider = int(binary.BigEndian.Uint32(block.data))
			}
		}
	}
	return rec, nil
}

func psdUnicodeName(b []byte) (string, bool) {
	if len(b) < 4 {
		return "", false
	}
	n := int(binary.BigEndian.Uint32(b))
	if n > (len(b)-4)/2 {
		return "", false
	}
	units := make([]uint16, n)
	for i := range units {
		units[i] = binary.BigEndian.Uint16(b[4+i*2:])
	}
	for len(units) > 0 && units[len(units)-1] == 0 {
		units = units[:len(units)-1]
	}
	return string(utf16.Decode(units)), true
}
