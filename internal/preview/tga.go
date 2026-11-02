package preview

import (
	"encoding/binary"
	"errors"
	"image"
)

const tgaHeaderSize = 18

const (
	tgaColorMapped    = 1
	tgaTrueColor      = 2
	tgaGray           = 3
	tgaRLEColorMapped = 9
	tgaRLETrueColor   = 10
	tgaRLEGray        = 11
)

var errTGATruncated = errors.New("preview: truncated tga")

type tgaHeader struct {
	idLength     int
	colorMapType byte
	imageType    byte
	cmFirst      int
	cmLength     int
	cmDepth      int
	width        int
	height       int
	depth        int
	descriptor   byte
}

func parseTGAHeader(data []byte) (tgaHeader, error) {
	if len(data) < tgaHeaderSize {
		return tgaHeader{}, errTGATruncated
	}
	h := tgaHeader{
		idLength:     int(data[0]),
		colorMapType: data[1],
		imageType:    data[2],
		cmFirst:      int(binary.LittleEndian.Uint16(data[3:])),
		cmLength:     int(binary.LittleEndian.Uint16(data[5:])),
		cmDepth:      int(data[7]),
		width:        int(binary.LittleEndian.Uint16(data[12:])),
		height:       int(binary.LittleEndian.Uint16(data[14:])),
		depth:        int(data[16]),
		descriptor:   data[17],
	}
	switch h.imageType {
	case tgaColorMapped, tgaRLEColorMapped:
		if h.colorMapType != 1 || h.depth != 8 {
			return h, ErrUnsupported
		}
		if !validTGADepth(h.cmDepth) {
			return h, ErrUnsupported
		}
	case tgaTrueColor, tgaRLETrueColor:
		if !validTGADepth(h.depth) {
			return h, ErrUnsupported
		}
	case tgaGray, tgaRLEGray:
		if h.depth != 8 && h.depth != 16 {
			return h, ErrUnsupported
		}
	default:
		return h, ErrUnsupported
	}
	return h, checkSize(h.width, h.height)
}

func validTGADepth(depth int) bool {
	return depth == 15 || depth == 16 || depth == 24 || depth == 32
}

func decodeTGA(data []byte) (*Image, error) {
	h, err := parseTGAHeader(data)
	if err != nil {
		return nil, err
	}
	pos := tgaHeaderSize + h.idLength
	if pos > len(data) {
		return nil, errTGATruncated
	}
	alphaBits := int(h.descriptor & 0x0f)

	var palette [][4]byte
	if h.colorMapType == 1 {
		entry := (h.cmDepth + 7) / 8
		size := h.cmLength * entry
		if pos+size > len(data) {
			return nil, errTGATruncated
		}
		if h.imageType == tgaColorMapped || h.imageType == tgaRLEColorMapped {
			palette = make([][4]byte, h.cmLength)
			for i := range palette {
				palette[i] = tgaColor(data[pos+i*entry:], h.cmDepth, alphaBits)
			}
		}
		pos += size
	}

	bpp := (h.depth + 7) / 8
	raw, err := tgaPixels(data[pos:], h, bpp)
	if err != nil {
		return nil, err
	}

	img := image.NewNRGBA(image.Rect(0, 0, h.width, h.height))
	topDown := h.descriptor&0x20 != 0
	rightToLeft := h.descriptor&0x10 != 0
	for i := 0; i < h.width*h.height; i++ {
		x, y := i%h.width, i/h.width
		if !topDown {
			y = h.height - 1 - y
		}
		if rightToLeft {
			x = h.width - 1 - x
		}
		px := raw[i*bpp : i*bpp+bpp]
		var c [4]byte
		switch h.imageType {
		case tgaColorMapped, tgaRLEColorMapped:
			idx := int(px[0]) - h.cmFirst
			if idx >= 0 && idx < len(palette) {
				c = palette[idx]
			}
		case tgaGray, tgaRLEGray:
			c = [4]byte{px[0], px[0], px[0], 0xff}
			if bpp == 2 {
				c[3] = px[1]
			}
		default:
			c = tgaColor(px, h.depth, alphaBits)
		}
		copy(img.Pix[y*img.Stride+x*4:], c[:])
	}

	return &Image{Pixels: img, Format: "tga", BitDepth: 8, HasAlpha: hasAlpha(img)}, nil
}

func tgaPixels(data []byte, h tgaHeader, bpp int) ([]byte, error) {
	total := h.width * h.height * bpp
	rle := h.imageType >= tgaRLEColorMapped
	if !rle {
		if len(data) < total {
			return nil, errTGATruncated
		}
		return data[:total], nil
	}
	out := make([]byte, 0, total)
	pos := 0
	for len(out) < total {
		if pos >= len(data) {
			return nil, errTGATruncated
		}
		packet := data[pos]
		pos++
		count := int(packet&0x7f) + 1
		if packet&0x80 != 0 {
			if pos+bpp > len(data) {
				return nil, errTGATruncated
			}
			for range count {
				out = append(out, data[pos:pos+bpp]...)
			}
			pos += bpp
		} else {
			n := count * bpp
			if pos+n > len(data) {
				return nil, errTGATruncated
			}
			out = append(out, data[pos:pos+n]...)
			pos += n
		}
	}
	if len(out) > total {
		out = out[:total]
	}
	return out, nil
}

func tgaColor(px []byte, depth, alphaBits int) [4]byte {
	switch depth {
	case 15, 16:
		v := binary.LittleEndian.Uint16(px)
		c := [4]byte{expand5(v >> 10), expand5(v >> 5), expand5(v), 0xff}
		if depth == 16 && alphaBits > 0 && v&0x8000 == 0 {
			c[3] = 0
		}
		return c
	case 24:
		return [4]byte{px[2], px[1], px[0], 0xff}
	default:
		return [4]byte{px[2], px[1], px[0], px[3]}
	}
}

func expand5(v uint16) byte {
	v &= 0x1f
	return byte(v<<3 | v>>2)
}
