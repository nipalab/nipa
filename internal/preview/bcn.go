package preview

import (
	"encoding/binary"
	"image"
	"math"
)

type ldrBlock func(b []byte, out *[16][4]uint8)

type hdrBlock func(b []byte, out *[16][3]float32)

// decodeBlocks expands 4x4 block-compressed data into dst, clipping the
// blocks that hang over the right and bottom edges.
func decodeBlocks(src []byte, w, h, size int, dst *image.NRGBA, fn ldrBlock) {
	bw := (w + 3) / 4
	var px [16][4]uint8
	for by := range (h + 3) / 4 {
		for bx := range bw {
			off := (by*bw + bx) * size
			fn(src[off:off+size], &px)
			for i := range 16 {
				x, y := bx*4+i%4, by*4+i/4
				if x >= w || y >= h {
					continue
				}
				o := y*dst.Stride + x*4
				copy(dst.Pix[o:o+4], px[i][:])
			}
		}
	}
}

func decodeBlocksHDR(src []byte, w, h, size int, dst *FloatImage, fn hdrBlock) {
	bw := (w + 3) / 4
	var px [16][3]float32
	for by := range (h + 3) / 4 {
		for bx := range bw {
			off := (by*bw + bx) * size
			fn(src[off:off+size], &px)
			for i := range 16 {
				x, y := bx*4+i%4, by*4+i/4
				if x >= w || y >= h {
					continue
				}
				o := (y*w + x) * 4
				copy(dst.Pix[o:o+3], px[i][:])
				dst.Pix[o+3] = 1
			}
		}
	}
}

func expand565(c uint16) [3]int {
	r, g, b := int(c>>11&31), int(c>>5&63), int(c&31)
	return [3]int{r<<3 | r>>2, g<<2 | g>>4, b<<3 | b>>2}
}

// bc1Colors decodes the colour half shared by BC1, BC2 and BC3. BC2/BC3 always
// use the four-colour palette; BC1 switches to three colours plus transparent
// black when the first endpoint is not greater than the second.
func bc1Colors(b []byte, out *[16][4]uint8, alwaysFour bool) {
	c0, c1 := binary.LittleEndian.Uint16(b), binary.LittleEndian.Uint16(b[2:])
	e0, e1 := expand565(c0), expand565(c1)
	var pal [4][4]uint8
	for c := range 3 {
		pal[0][c], pal[1][c] = uint8(e0[c]), uint8(e1[c])
		if c0 > c1 || alwaysFour {
			pal[2][c] = uint8((2*e0[c] + e1[c] + 1) / 3)
			pal[3][c] = uint8((e0[c] + 2*e1[c] + 1) / 3)
		} else {
			pal[2][c] = uint8((e0[c] + e1[c] + 1) / 2)
		}
	}
	pal[0][3], pal[1][3], pal[2][3] = 0xff, 0xff, 0xff
	if c0 > c1 || alwaysFour {
		pal[3][3] = 0xff
	}
	idx := binary.LittleEndian.Uint32(b[4:])
	for i := range 16 {
		out[i] = pal[idx>>(2*i)&3]
	}
}

func decodeBC1(b []byte, out *[16][4]uint8) { bc1Colors(b, out, false) }

func decodeBC2(b []byte, out *[16][4]uint8) {
	bc1Colors(b[8:], out, true)
	alpha := binary.LittleEndian.Uint64(b)
	for i := range 16 {
		out[i][3] = uint8(alpha>>(4*i)&0xf) * 17
	}
}

func decodeBC3(b []byte, out *[16][4]uint8) {
	bc1Colors(b[8:], out, true)
	var a [16]uint8
	bc4Values(b, false, &a)
	for i := range 16 {
		out[i][3] = a[i]
	}
}

// bc4Values decodes one BC4 channel block (also the BC3 alpha and both BC5
// channels). Signed blocks are remapped from [-1, 1] to [0, 255].
func bc4Values(b []byte, signed bool, out *[16]uint8) {
	var e0, e1 int
	if signed {
		e0, e1 = max(int(int8(b[0])), -127), max(int(int8(b[1])), -127)
	} else {
		e0, e1 = int(b[0]), int(b[1])
	}
	lo, hi := 0, 255
	if signed {
		lo, hi = -127, 127
	}
	var pal [8]int
	pal[0], pal[1] = e0, e1
	if e0 > e1 {
		for i := 1; i < 7; i++ {
			pal[i+1] = roundDiv((7-i)*e0+i*e1, 7)
		}
	} else {
		for i := 1; i < 5; i++ {
			pal[i+1] = roundDiv((5-i)*e0+i*e1, 5)
		}
		pal[6], pal[7] = lo, hi
	}
	var bits uint64
	for i := 2; i < 8; i++ {
		bits |= uint64(b[i]) << (8 * (i - 2))
	}
	for i := range 16 {
		v := pal[bits>>(3*i)&7]
		if signed {
			v = roundDiv((v+127)*255, 254)
		}
		out[i] = uint8(v)
	}
}

func roundDiv(n, d int) int {
	if n < 0 {
		return -((-n + d/2) / d)
	}
	return (n + d/2) / d
}

func decodeBC4(signed bool) ldrBlock {
	return func(b []byte, out *[16][4]uint8) {
		var v [16]uint8
		bc4Values(b, signed, &v)
		for i := range 16 {
			out[i] = [4]uint8{v[i], v[i], v[i], 0xff}
		}
	}
}

// decodeBC5 shows a two-channel block as a normal map: red and green carry X
// and Y, blue is the reconstructed Z.
func decodeBC5(signed bool) ldrBlock {
	return func(b []byte, out *[16][4]uint8) {
		var r, g [16]uint8
		bc4Values(b, signed, &r)
		bc4Values(b[8:], signed, &g)
		for i := range 16 {
			x, y := float64(r[i])/127.5-1, float64(g[i])/127.5-1
			z := math.Sqrt(max(0, 1-x*x-y*y))
			out[i] = [4]uint8{r[i], g[i], uint8(math.Round((z + 1) * 127.5)), 0xff}
		}
	}
}
