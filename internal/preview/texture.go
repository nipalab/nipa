package preview

import (
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"math"
)

// maxTextureSide rejects absurd container dimensions before any size math.
const maxTextureSide = 1 << 16

// Select picks one surface of a texture with mipmaps, cube faces or array
// layers. Out-of-range values are clamped to the last surface.
type Select struct {
	Level int
	Face  int
}

type pixelFormat struct {
	name  string
	block int
	bpp   int
	depth int
	alpha bool
	ldr   func(src []byte, w, h int, dst *image.NRGBA)
	hdr   func(src []byte, w, h int, dst *FloatImage)
}

func (f pixelFormat) surfaceSize(w, h int) int {
	if f.block > 0 {
		return ((w + 3) / 4) * ((h + 3) / 4) * f.block
	}
	return w * h * f.bpp
}

func blockFormat(name string, size int, alpha bool, fn ldrBlock) pixelFormat {
	return pixelFormat{name: name, block: size, depth: 8, alpha: alpha, ldr: func(src []byte, w, h int, dst *image.NRGBA) {
		decodeBlocks(src, w, h, size, dst, fn)
	}}
}

func bc6hFormat(name string, signed bool) pixelFormat {
	fn := decodeBC6H(signed)
	return pixelFormat{name: name, block: 16, depth: 16, hdr: func(src []byte, w, h int, dst *FloatImage) {
		decodeBlocksHDR(src, w, h, 16, dst, fn)
	}}
}

// channelFormat describes uncompressed 8-bit layouts by the byte offset of
// each RGBA channel; -1 means absent (zero colour, opaque alpha). gray copies
// the first channel into green and blue.
func channelFormat(name string, bpp int, offsets [4]int, gray bool) pixelFormat {
	return pixelFormat{name: name, bpp: bpp, depth: 8, alpha: offsets[3] >= 0, ldr: func(src []byte, w, h int, dst *image.NRGBA) {
		for i := range w * h {
			s, d := src[i*bpp:], dst.Pix[(i/w)*dst.Stride+(i%w)*4:]
			for c := range 4 {
				switch {
				case offsets[c] >= 0:
					d[c] = s[offsets[c]]
				case c == 3:
					d[c] = 0xff
				case gray:
					d[c] = s[offsets[0]]
				default:
					d[c] = 0
				}
			}
		}
	}}
}

// floatFormat describes uncompressed float layouts: channels samples of 2
// (half) or 4 (float) bytes per pixel. One channel is shown as gray.
func floatFormat(name string, channels, size int) pixelFormat {
	return pixelFormat{name: name, bpp: channels * size, depth: size * 8, alpha: channels == 4, hdr: func(src []byte, w, h int, dst *FloatImage) {
		for i := range w * h {
			px := dst.Pix[i*4 : i*4+4]
			px[3] = 1
			for c := range channels {
				o := (i*channels + c) * size
				var v float32
				if size == 2 {
					v = halfToFloat(binary.LittleEndian.Uint16(src[o:]))
				} else {
					v = math.Float32frombits(binary.LittleEndian.Uint32(src[o:]))
				}
				px[c] = v
			}
			if channels == 1 {
				px[1], px[2] = px[0], px[0]
			}
		}
	}}
}

func unpackFloat(v uint32, mantBits uint) float32 {
	exp := v >> mantBits
	mant := v & (1<<mantBits - 1)
	switch exp {
	case 0:
		return float32(mant) / float32(uint32(1)<<mantBits) / (1 << 14)
	case 31:
		return float32(math.Inf(1))
	}
	return float32(math.Ldexp(1+float64(mant)/float64(uint32(1)<<mantBits), int(exp)-15))
}

var r11g11b10Float = pixelFormat{name: "R11G11B10_FLOAT", bpp: 4, depth: 11, hdr: func(src []byte, w, h int, dst *FloatImage) {
	for i := range w * h {
		v := binary.LittleEndian.Uint32(src[i*4:])
		px := dst.Pix[i*4 : i*4+4]
		px[0], px[1], px[2], px[3] = unpackFloat(v&0x7ff, 6), unpackFloat(v>>11&0x7ff, 6), unpackFloat(v>>22, 5), 1
	}
}}

var rgb9e5Float = pixelFormat{name: "RGB9E5_SHAREDEXP", bpp: 4, depth: 9, hdr: func(src []byte, w, h int, dst *FloatImage) {
	for i := range w * h {
		v := binary.LittleEndian.Uint32(src[i*4:])
		scale := math.Ldexp(1, int(v>>27)-15-9)
		px := dst.Pix[i*4 : i*4+4]
		px[0], px[1], px[2], px[3] = float32(float64(v&0x1ff)*scale), float32(float64(v>>9&0x1ff)*scale), float32(float64(v>>18&0x1ff)*scale), 1
	}
}}

var rgb10a2 = pixelFormat{name: "R10G10B10A2_UNORM", bpp: 4, depth: 10, alpha: true, ldr: func(src []byte, w, h int, dst *image.NRGBA) {
	for i := range w * h {
		v := binary.LittleEndian.Uint32(src[i*4:])
		d := dst.Pix[(i/w)*dst.Stride+(i%w)*4:]
		d[0], d[1], d[2], d[3] = uint8((v&0x3ff)>>2), uint8((v>>10&0x3ff)>>2), uint8((v>>20&0x3ff)>>2), uint8(v>>30)*85
	}
}}

var rgba16 = pixelFormat{name: "R16G16B16A16_UNORM", bpp: 8, depth: 16, alpha: true, ldr: func(src []byte, w, h int, dst *image.NRGBA) {
	for i := range w * h {
		d := dst.Pix[(i/w)*dst.Stride+(i%w)*4:]
		for c := range 4 {
			d[c] = src[i*8+c*2+1]
		}
	}
}}

// maskFormat decodes legacy DDS layouts described by bit masks.
func maskFormat(bits int, masks [4]uint32, luminance bool) (pixelFormat, error) {
	if bits%8 != 0 || bits < 8 || bits > 32 {
		return pixelFormat{}, fmt.Errorf("%w: %d-bit pixels", ErrUnsupported, bits)
	}
	bpp := bits / 8
	for c, m := range masks {
		if bits < 32 && m>>bits != 0 {
			masks[c] = 0
		}
	}
	if luminance && masks[0] == 0 {
		masks[0] = 0xff
	}
	name := fmt.Sprintf("%d-bit RGB", bits)
	if luminance {
		name = fmt.Sprintf("%d-bit luminance", bits)
	}
	return pixelFormat{name: name, bpp: bpp, depth: 8, alpha: masks[3] != 0, ldr: func(src []byte, w, h int, dst *image.NRGBA) {
		for i := range w * h {
			var v uint32
			for b := range bpp {
				v |= uint32(src[i*bpp+b]) << (8 * b)
			}
			d := dst.Pix[(i/w)*dst.Stride+(i%w)*4:]
			for c := range 4 {
				d[c] = maskValue(v, masks[c])
			}
			if masks[3] == 0 {
				d[3] = 0xff
			}
			if luminance {
				d[1], d[2] = d[0], d[0]
			}
		}
	}}, nil
}

func maskValue(v, mask uint32) uint8 {
	if mask == 0 {
		return 0
	}
	shift := 0
	for mask>>shift&1 == 0 {
		shift++
	}
	m := mask >> shift
	return uint8((uint64(v>>shift&m)*255 + uint64(m)/2) / uint64(m))
}

// texture is a container-agnostic view of a texture file: its pixel format,
// dimensions and a lookup for the raw bytes of one surface.
type texture struct {
	container     string
	format        pixelFormat
	width, height int
	levels, faces int
	cube          bool
	surface       func(level, face int) ([]byte, error)
}

func mipSize(v, level int) int { return max(1, v>>level) }

func clampIndex(v, n int) int { return min(max(v, 0), max(n-1, 0)) }

var errTruncatedTexture = errors.New("preview: truncated texture data")

func (t *texture) decode(sel Select) (*Image, error) {
	level, face := clampIndex(sel.Level, t.levels), clampIndex(sel.Face, t.faces)
	w, h := mipSize(t.width, level), mipSize(t.height, level)
	check := checkSize
	if t.format.hdr != nil {
		check = checkFloatSize
	}
	if err := check(w, h); err != nil {
		return nil, err
	}
	data, err := t.surface(level, face)
	if err != nil {
		return nil, err
	}
	if len(data) < t.format.surfaceSize(w, h) {
		return nil, errTruncatedTexture
	}
	img := &Image{
		Format: t.container, Encoding: t.format.name, BitDepth: t.format.depth,
		Levels: t.levels, Faces: t.faces, Cube: t.cube, Level: level, Face: face,
	}
	if t.format.hdr != nil {
		f := newFloatImage(w, h)
		t.format.hdr(data, w, h, f)
		img.setHDR(f)
		return img, nil
	}
	img.Pixels = image.NewNRGBA(image.Rect(0, 0, w, h))
	t.format.ldr(data, w, h, img.Pixels)
	img.HasAlpha = t.format.alpha && hasAlpha(img.Pixels)
	return img, nil
}
