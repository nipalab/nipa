package preview

import "encoding/binary"

type bitReader struct {
	lo, hi uint64
	pos    uint
}

func newBitReader(b []byte) *bitReader {
	return &bitReader{lo: binary.LittleEndian.Uint64(b), hi: binary.LittleEndian.Uint64(b[8:])}
}

func (r *bitReader) read(n uint) int {
	if n == 0 {
		return 0
	}
	var v uint64
	switch {
	case r.pos >= 64:
		v = r.hi >> (r.pos - 64)
	case r.pos+n <= 64:
		v = r.lo >> r.pos
	default:
		v = r.lo>>r.pos | r.hi<<(64-r.pos)
	}
	r.pos += n
	return int(v & (1<<n - 1))
}

func (r *bitReader) readReversed(n uint) int {
	v := 0
	for i := range n {
		v |= r.read(1) << (n - 1 - i)
	}
	return v
}

var bptcWeights = [5][]int{
	2: {0, 21, 43, 64},
	3: {0, 9, 18, 27, 37, 46, 55, 64},
	4: {0, 4, 9, 13, 17, 21, 26, 30, 34, 38, 43, 47, 51, 55, 60, 64},
}

func bptcInterp(e0, e1, w int) int { return ((64-w)*e0 + w*e1 + 32) >> 6 }

func bptcSubset(subsets, partition, i int) int {
	switch subsets {
	case 2:
		return int(bptcPartition2[partition][i])
	case 3:
		return int(bptcPartition3[partition][i])
	}
	return 0
}

func bptcIsAnchor(subsets, partition, i int) bool {
	if i == 0 {
		return true
	}
	switch subsets {
	case 2:
		return int(bptcAnchor2[partition][1]) == i
	case 3:
		return int(bptcAnchor3[partition][1]) == i || int(bptcAnchor3[partition][2]) == i
	}
	return false
}

type bc7Mode struct {
	subsets, partitionBits, rotationBits, selectorBits int
	colorBits, alphaBits                               int
	endpointPBits, sharedPBits                         bool
	indexBits, index2Bits                              int
}

var bc7Modes = [8]bc7Mode{
	{subsets: 3, partitionBits: 4, colorBits: 4, endpointPBits: true, indexBits: 3},
	{subsets: 2, partitionBits: 6, colorBits: 6, sharedPBits: true, indexBits: 3},
	{subsets: 3, partitionBits: 6, colorBits: 5, indexBits: 2},
	{subsets: 2, partitionBits: 6, colorBits: 7, endpointPBits: true, indexBits: 2},
	{subsets: 1, rotationBits: 2, selectorBits: 1, colorBits: 5, alphaBits: 6, indexBits: 2, index2Bits: 3},
	{subsets: 1, rotationBits: 2, colorBits: 7, alphaBits: 8, indexBits: 2, index2Bits: 2},
	{subsets: 1, colorBits: 7, alphaBits: 7, endpointPBits: true, indexBits: 4},
	{subsets: 2, partitionBits: 6, colorBits: 5, alphaBits: 5, endpointPBits: true, indexBits: 2},
}

func decodeBC7(b []byte, out *[16][4]uint8) {
	mode := 0
	for mode < 8 && b[0]&(1<<mode) == 0 {
		mode++
	}
	if mode == 8 {
		*out = [16][4]uint8{}
		return
	}
	m := bc7Modes[mode]
	r := newBitReader(b)
	r.read(uint(mode + 1))
	partition := r.read(uint(m.partitionBits))
	rotation := r.read(uint(m.rotationBits))
	selector := r.read(uint(m.selectorBits))

	var ep [3][2][4]int
	channels := 3
	if m.alphaBits > 0 {
		channels = 4
	}
	for c := range channels {
		bits := m.colorBits
		if c == 3 {
			bits = m.alphaBits
		}
		for s := range m.subsets {
			for e := range 2 {
				ep[s][e][c] = r.read(uint(bits))
			}
		}
	}
	colorBits, alphaBits := m.colorBits, m.alphaBits
	if m.endpointPBits || m.sharedPBits {
		for s := range m.subsets {
			var p [2]int
			p[0] = r.read(1)
			if m.endpointPBits {
				p[1] = r.read(1)
			} else {
				p[1] = p[0]
			}
			for e := range 2 {
				for c := range channels {
					ep[s][e][c] = ep[s][e][c]<<1 | p[e]
				}
			}
		}
		colorBits++
		if alphaBits > 0 {
			alphaBits++
		}
	}
	for s := range m.subsets {
		for e := range 2 {
			for c := range 4 {
				switch {
				case c < 3:
					ep[s][e][c] = expandBits(ep[s][e][c], colorBits)
				case alphaBits > 0:
					ep[s][e][c] = expandBits(ep[s][e][c], alphaBits)
				default:
					ep[s][e][c] = 255
				}
			}
		}
	}

	var idx, idx2 [16]int
	for i := range 16 {
		bits := m.indexBits
		if bptcIsAnchor(m.subsets, partition, i) {
			bits--
		}
		idx[i] = r.read(uint(bits))
	}
	if m.index2Bits > 0 {
		for i := range 16 {
			bits := m.index2Bits
			if i == 0 {
				bits--
			}
			idx2[i] = r.read(uint(bits))
		}
	}

	for i := range 16 {
		s := bptcSubset(m.subsets, partition, i)
		colorW := bptcWeights[m.indexBits][idx[i]]
		alphaW := colorW
		if m.index2Bits > 0 {
			alphaW = bptcWeights[m.index2Bits][idx2[i]]
			if selector == 1 {
				colorW, alphaW = alphaW, colorW
			}
		}
		var px [4]int
		for c := range 4 {
			w := colorW
			if c == 3 {
				w = alphaW
			}
			px[c] = bptcInterp(ep[s][0][c], ep[s][1][c], w)
		}
		if rotation > 0 {
			px[3], px[rotation-1] = px[rotation-1], px[3]
		}
		out[i] = [4]uint8{uint8(px[0]), uint8(px[1]), uint8(px[2]), uint8(px[3])}
	}
}

func expandBits(v, bits int) int {
	v <<= 8 - bits
	return v | v>>bits
}

type bc6hField struct {
	ep, ch, shift, bits uint8
	reversed            bool
}

type bc6hMode struct {
	code        uint8
	partitioned bool
	bits        [4]uint8
	fields      []bc6hField
}

func signExtend(v, bits int) int {
	shift := 32 - bits
	return int(int32(uint32(v)<<shift) >> shift)
}

func bc6hUnquantize(v, bits int, signed bool) int {
	if !signed {
		switch {
		case bits >= 15:
			return v
		case v == 0:
			return 0
		case v == 1<<bits-1:
			return 0xffff
		}
		return (v<<16 + 0x8000) >> bits
	}
	if bits >= 16 {
		return v
	}
	neg := v < 0
	if neg {
		v = -v
	}
	switch {
	case v == 0:
	case v >= 1<<(bits-1)-1:
		v = 0x7fff
	default:
		v = (v<<15 + 0x4000) >> (bits - 1)
	}
	if neg {
		return -v
	}
	return v
}

func bc6hFinish(v int, signed bool) uint16 {
	if !signed {
		return uint16(v * 31 >> 6)
	}
	if v < 0 {
		return 0x8000 | uint16(-v*31>>5)
	}
	return uint16(v * 31 >> 5)
}

func decodeBC6H(signed bool) hdrBlock {
	return func(b []byte, out *[16][3]float32) {
		r := newBitReader(b)
		code := r.read(2)
		if code > 1 {
			code |= r.read(3) << 2
		}
		var m *bc6hMode
		for i := range bc6hModes {
			if int(bc6hModes[i].code) == code {
				m = &bc6hModes[i]
			}
		}
		if m == nil {
			*out = [16][3]float32{}
			return
		}
		var ep [4][3]int
		for _, f := range m.fields {
			v := 0
			if f.reversed {
				v = r.readReversed(uint(f.bits))
			} else {
				v = r.read(uint(f.bits))
			}
			ep[f.ep][f.ch] |= v << f.shift
		}
		partition, endpoints, indexBits := 0, 2, 4
		if m.partitioned {
			partition, endpoints, indexBits = r.read(5), 4, 3
		}
		base := int(m.bits[0])
		transformed := m.bits[1] != m.bits[0]
		for c := range 3 {
			if signed {
				ep[0][c] = signExtend(ep[0][c], base)
			}
			for e := 1; e < endpoints; e++ {
				if transformed || signed {
					ep[e][c] = signExtend(ep[e][c], int(m.bits[c+1]))
				}
				if transformed {
					ep[e][c] = (ep[e][c] + ep[0][c]) & (1<<base - 1)
					if signed {
						ep[e][c] = signExtend(ep[e][c], base)
					}
				}
			}
			for e := range endpoints {
				ep[e][c] = bc6hUnquantize(ep[e][c], base, signed)
			}
		}
		subsets := 1
		if m.partitioned {
			subsets = 2
		}
		for i := range 16 {
			bits := indexBits
			if bptcIsAnchor(subsets, partition, i) {
				bits--
			}
			w := bptcWeights[indexBits][r.read(uint(bits))]
			s := bptcSubset(subsets, partition, i) * 2
			for c := range 3 {
				out[i][c] = halfToFloat(bc6hFinish(bptcInterp(ep[s][c], ep[s+1][c], w), signed))
			}
		}
	}
}
