package preview

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"math"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

type ddsSpec struct {
	w, h, mips, depth int
	pfFlags, bits     uint32
	fourCC            string
	masks             [4]uint32
	caps2             uint32
	dx10              []uint32
	body              []byte
}

func (s ddsSpec) bytes() []byte {
	b := make([]byte, 128)
	copy(b, "DDS ")
	le := binary.LittleEndian
	le.PutUint32(b[4:], 124)
	le.PutUint32(b[12:], uint32(s.h))
	le.PutUint32(b[16:], uint32(s.w))
	le.PutUint32(b[24:], uint32(s.depth))
	le.PutUint32(b[28:], uint32(s.mips))
	le.PutUint32(b[76:], 32)
	le.PutUint32(b[80:], s.pfFlags)
	copy(b[84:88], s.fourCC)
	le.PutUint32(b[88:], s.bits)
	for i, m := range s.masks {
		le.PutUint32(b[92+i*4:], m)
	}
	le.PutUint32(b[112:], s.caps2)
	for _, v := range s.dx10 {
		b = le.AppendUint32(b, v)
	}
	return append(b, s.body...)
}

func dx10(format uint32, w, h int, body []byte) []byte {
	return ddsSpec{w: w, h: h, pfFlags: ddsFlagFourCC, fourCC: "DX10", dx10: []uint32{format, 3, 0, 1, 0}, body: body}.bytes()
}

func le32s(vs ...uint32) []byte {
	var b []byte
	for _, v := range vs {
		b = binary.LittleEndian.AppendUint32(b, v)
	}
	return b
}

func f32s(vs ...float32) []byte {
	var b []byte
	for _, v := range vs {
		b = binary.LittleEndian.AppendUint32(b, math.Float32bits(v))
	}
	return b
}

func halfBytes(vs ...uint16) []byte {
	var b []byte
	for _, v := range vs {
		b = binary.LittleEndian.AppendUint16(b, v)
	}
	return b
}

const half1, half2, halfHalf = 0x3c00, 0x4000, 0x3800

func TestDDSPixelFormats(t *testing.T) {
	for _, tc := range []struct {
		name   string
		file   []byte
		pixel  []uint8
		floats []float32
	}{
		{"R11G11B10", dx10(26, 1, 1, le32s(0x3c0|0x3c0<<11|0x1e0<<22)), nil, []float32{1, 1, 1, 1}},
		{"RGB9E5", dx10(67, 1, 1, le32s(256|128<<9|0<<18|16<<27)), nil, []float32{1, 0.5, 0, 1}},
		{"R10G10B10A2", dx10(24, 1, 1, le32s(0x3ff|0x3ff<<20|3<<30)), []uint8{255, 0, 255, 255}, nil},
		{"RGBA16", dx10(11, 1, 1, halfBytes(0xffff, 0x8000, 0, 0xffff)), []uint8{255, 128, 0, 255}, nil},
		{"R8G8", dx10(49, 1, 1, []byte{10, 20}), []uint8{10, 20, 0, 255}, nil},
		{"R8", dx10(61, 1, 1, []byte{77}), []uint8{77, 77, 77, 255}, nil},
		{"A8", dx10(65, 1, 1, []byte{9}), []uint8{0, 0, 0, 9}, nil},
		{"BGRA", dx10(87, 1, 1, []byte{1, 2, 3, 4}), []uint8{3, 2, 1, 4}, nil},
		{"BGRX", dx10(88, 1, 1, []byte{1, 2, 3, 4}), []uint8{3, 2, 1, 255}, nil},
		{"RGBA32F", dx10(2, 1, 1, f32s(0.5, 2, 0, 1)), nil, []float32{0.5, 2, 0, 1}},
		{"RG16F", dx10(34, 1, 1, halfBytes(half2, halfHalf)), nil, []float32{2, 0.5, 0, 1}},
		{"R32F", dx10(41, 1, 1, f32s(3)), nil, []float32{3, 3, 3, 1}},
		{"R16F", dx10(54, 1, 1, halfBytes(half1)), nil, []float32{1, 1, 1, 1}},
		{"565", ddsSpec{w: 1, h: 1, pfFlags: ddsFlagRGB, bits: 16, masks: [4]uint32{0xf800, 0x7e0, 0x1f, 0}, body: []byte{0x1f, 0xf8}}.bytes(), []uint8{255, 0, 255, 255}, nil},
		{"alpha only", ddsSpec{w: 1, h: 1, pfFlags: ddsFlagAlpha, bits: 8, masks: [4]uint32{0xff, 0, 0, 0xff}, body: []byte{200}}.bytes(), []uint8{0, 0, 0, 200}, nil},
		{"BGR24", ddsSpec{w: 1, h: 1, pfFlags: ddsFlagRGB, bits: 24, masks: [4]uint32{0xff0000, 0xff00, 0xff, 0}, body: []byte{1, 2, 3}}.bytes(), []uint8{3, 2, 1, 255}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			img, err := DecodeImage("a.dds", tc.file)
			require.NoError(t, err)
			if tc.floats != nil {
				require.Equal(t, tc.floats, img.HDR.Pix)
				return
			}
			require.Equal(t, tc.pixel, img.Pixels.Pix)
		})
	}
}

func TestDDSBlockFormatsMatchBlockDecoders(t *testing.T) {
	bc6 := readGzip(t, "bc6h.bin.gz")[:16]
	img, err := DecodeImage("a.dds", dx10(95, 3, 2, bc6))
	require.NoError(t, err)
	var px [16][3]float32
	decodeBC6H(false)(bc6, &px)
	require.Equal(t, []float32{px[5][0], px[5][1], px[5][2], 1}, img.HDR.Pix[(1*3+1)*4:(1*3+1)*4+4])
	require.Equal(t, 16, img.BitDepth)

	bc4 := readGzip(t, "bc4.bin.gz")[:8]
	for _, fourCC := range []string{"ATI1", "BC4U", "BC4S", "ATI2", "DXT3"} {
		body := append(append([]byte{}, bc4...), bc4...)
		img, err := DecodeImage("a.dds", ddsSpec{w: 4, h: 4, pfFlags: ddsFlagFourCC, fourCC: fourCC, body: body}.bytes())
		require.NoError(t, err, fourCC)
		require.Equal(t, fourCC, img.Encoding)
	}
}

func TestDDSLegacyCubeAndErrors(t *testing.T) {
	px := bytes.Repeat([]byte{1, 2, 3, 4}, 3)
	img, err := DecodeImageAt("a.dds", ddsSpec{w: 1, h: 1, pfFlags: ddsFlagRGB | 1, bits: 32, masks: [4]uint32{0xff, 0xff00, 0xff0000, 0xff000000}, caps2: ddsCapsCubemap | 0x400 | 0x800 | 0x1000, body: px}.bytes(), Select{Face: 2})
	require.NoError(t, err)
	require.Equal(t, 3, img.Faces)
	require.False(t, img.Cube)

	cube := ddsSpec{w: 1, h: 1, pfFlags: ddsFlagFourCC, fourCC: "DX10", dx10: []uint32{28, 3, ddsMiscCube, 2, 0}, body: bytes.Repeat([]byte{9, 9, 9, 9}, 12)}
	img, err = DecodeImageAt("a.dds", cube.bytes(), Select{Face: 11})
	require.NoError(t, err)
	require.Equal(t, 12, img.Faces)
	require.True(t, img.Cube)

	bad := func(s ddsSpec) []byte { return s.bytes() }
	for name, tc := range map[string]struct {
		data        []byte
		unsupported bool
	}{
		"magic":       {[]byte("DDS nope"), false},
		"zero width":  {bad(ddsSpec{h: 1, pfFlags: ddsFlagRGB, bits: 32}), false},
		"huge":        {bad(ddsSpec{w: 1 << 20, h: 1, pfFlags: ddsFlagRGB, bits: 32}), false},
		"levels":      {bad(ddsSpec{w: 1, h: 1, mips: 40, pfFlags: ddsFlagRGB, bits: 32}), false},
		"volume":      {bad(ddsSpec{w: 1, h: 1, depth: 2, caps2: ddsCapsVolume, pfFlags: ddsFlagRGB, bits: 32}), true},
		"dx10 volume": {bad(ddsSpec{w: 1, h: 1, pfFlags: ddsFlagFourCC, fourCC: "DX10", dx10: []uint32{28, ddsDimTexture3D, 0, 1, 0}}), true},
		"dx10 format": {bad(ddsSpec{w: 1, h: 1, pfFlags: ddsFlagFourCC, fourCC: "DX10", dx10: []uint32{999, 3, 0, 1, 0}}), true},
		"dx10 short":  {bad(ddsSpec{w: 1, h: 1, pfFlags: ddsFlagFourCC, fourCC: "DX10"}), false},
		"fourcc":      {bad(ddsSpec{w: 1, h: 1, pfFlags: ddsFlagFourCC, fourCC: "ABCD"}), true},
		"no format":   {bad(ddsSpec{w: 1, h: 1}), false},
		"bits":        {bad(ddsSpec{w: 1, h: 1, pfFlags: ddsFlagRGB, bits: 12}), true},
		"truncated":   {bad(ddsSpec{w: 4, h: 4, pfFlags: ddsFlagRGB, bits: 32, body: []byte{1}}), false},
		"arrays":      {bad(ddsSpec{w: 1, h: 1, pfFlags: ddsFlagFourCC, fourCC: "DX10", dx10: []uint32{28, 3, ddsMiscCube, 5000, 0}}), false},
	} {
		_, err := DecodeImage("a.dds", tc.data)
		require.Error(t, err, name)
		require.Equal(t, tc.unsupported, errorsIsUnsupported(err), name)
	}
	_, err = DecodeImage("a.dds", ddsSpec{w: 9000, h: 9000, pfFlags: ddsFlagRGB, bits: 32}.bytes())
	require.ErrorIs(t, err, ErrTooLarge)
}

func errorsIsUnsupported(err error) bool {
	return err != nil && bytes.Contains([]byte(err.Error()), []byte("unsupported"))
}

type ktx2Spec struct {
	vk, w, h, depth, layers, faces, scheme int
	levels                                 [][]byte
	raw                                    []uint64
}

func (s ktx2Spec) bytes() []byte {
	le := binary.LittleEndian
	b := append([]byte{}, ktx2Magic...)
	for _, v := range []int{s.vk, 1, s.w, s.h, s.depth, s.layers, s.faces, len(s.levels), s.scheme} {
		b = le.AppendUint32(b, uint32(v))
	}
	b = append(b, make([]byte, 32)...)
	off := uint64(len(b) + 24*len(s.levels))
	var body []byte
	for i, l := range s.levels {
		raw := uint64(len(l))
		if s.raw != nil {
			raw = s.raw[i]
		}
		b = le.AppendUint64(b, off)
		b = le.AppendUint64(b, uint64(len(l)))
		b = le.AppendUint64(b, raw)
		off += uint64(len(l))
		body = append(body, l...)
	}
	return append(b, body...)
}

func TestKTX2Errors(t *testing.T) {
	ok := ktx2Spec{vk: 37, w: 1, h: 1, faces: 1, levels: [][]byte{{1, 2, 3, 4}}}
	img, err := DecodeImage("a.ktx2", ok.bytes())
	require.NoError(t, err)
	require.Equal(t, []uint8{1, 2, 3, 4}, img.Pixels.Pix)

	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	_, _ = zw.Write([]byte{1, 2, 3, 4})
	_ = zw.Close()
	for name, tc := range map[string]struct {
		spec        ktx2Spec
		data        []byte
		unsupported bool
	}{
		"magic":       {data: []byte("KTX 11 nope")},
		"faces":       {spec: ktx2Spec{vk: 37, w: 1, h: 1, faces: 3, levels: [][]byte{{0}}}},
		"width":       {spec: ktx2Spec{vk: 37, h: 1, levels: [][]byte{{0}}}},
		"depth":       {spec: ktx2Spec{vk: 37, w: 1, h: 1, depth: 2, levels: [][]byte{{0}}}, unsupported: true},
		"scheme":      {spec: ktx2Spec{vk: 37, w: 1, h: 1, scheme: 7, levels: [][]byte{{0}}}, unsupported: true},
		"format":      {spec: ktx2Spec{vk: 1234, w: 1, h: 1, levels: [][]byte{{0}}}, unsupported: true},
		"short level": {spec: ktx2Spec{vk: 37, w: 2, h: 2, levels: [][]byte{{0}}}},
		"zstd":        {spec: ktx2Spec{vk: 37, w: 1, h: 1, scheme: 2, levels: [][]byte{{1, 2, 3}}}},
		"zlib":        {spec: ktx2Spec{vk: 37, w: 1, h: 1, scheme: 3, levels: [][]byte{{1, 2, 3}}}},
		"too big":     {spec: ktx2Spec{vk: 37, w: 1, h: 1, scheme: 3, levels: [][]byte{z.Bytes()}, raw: []uint64{1 << 40}}},
	} {
		data := tc.data
		if data == nil {
			data = tc.spec.bytes()
		}
		_, err := DecodeImage("a.ktx2", data)
		require.Error(t, err, name)
		require.Equal(t, tc.unsupported, errorsIsUnsupported(err), name)
	}
	_, err = DecodeImage("a.ktx2", ok.bytes()[:ktx2HeaderSize+4])
	require.Error(t, err)
	bad := ok.bytes()
	binary.LittleEndian.PutUint64(bad[ktx2HeaderSize:], 1<<40)
	_, err = DecodeImage("a.ktx2", bad)
	require.ErrorIs(t, err, errTruncatedTexture)
	img, err = DecodeImage("a.ktx2", ktx2Spec{vk: 37, w: 1, h: 1, scheme: 3, levels: [][]byte{z.Bytes()}, raw: []uint64{4}}.bytes())
	require.NoError(t, err)
	require.Equal(t, []uint8{1, 2, 3, 4}, img.Pixels.Pix)
}

type exrAttr struct {
	name, kind string
	value      []byte
}

func exrChannelList(chs ...exrChannel) []byte {
	var b []byte
	for _, ch := range chs {
		b = append(b, ch.name...)
		b = append(b, 0)
		b = binary.LittleEndian.AppendUint32(b, uint32(ch.kind))
		b = append(b, 0, 0, 0, 0)
		s := uint32(1)
		if ch.sampling {
			s = 2
		}
		b = binary.LittleEndian.AppendUint32(b, s)
		b = binary.LittleEndian.AppendUint32(b, s)
	}
	return append(b, 0)
}

func box(x0, y0, x1, y1 int32) []byte {
	b := make([]byte, 16)
	for i, v := range []int32{x0, y0, x1, y1} {
		binary.LittleEndian.PutUint32(b[i*4:], uint32(v))
	}
	return b
}

func exrFile(flags uint32, attrs []exrAttr, chunks [][]byte) []byte {
	le := binary.LittleEndian
	b := le.AppendUint32(nil, exrMagic)
	b = le.AppendUint32(b, 2|flags)
	for _, a := range attrs {
		b = append(b, a.name...)
		b = append(b, 0)
		b = append(b, a.kind...)
		b = append(b, 0)
		b = le.AppendUint32(b, uint32(len(a.value)))
		b = append(b, a.value...)
	}
	b = append(b, 0)
	off := uint64(len(b) + 8*len(chunks))
	for _, c := range chunks {
		b = le.AppendUint64(b, off)
		off += uint64(len(c))
	}
	for _, c := range chunks {
		b = append(b, c...)
	}
	return b
}

func exrScanline(y int32, data []byte) []byte {
	b := binary.LittleEndian.AppendUint32(nil, uint32(y))
	b = binary.LittleEndian.AppendUint32(b, uint32(len(data)))
	return append(b, data...)
}

func exrAttrs(compression byte, w, h int32, chs ...exrChannel) []exrAttr {
	return []exrAttr{
		{"channels", "chlist", exrChannelList(chs...)},
		{"compression", "compression", []byte{compression}},
		{"dataWindow", "box2i", box(0, 0, w-1, h-1)},
	}
}

func zlibBytes(b []byte) []byte {
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	_, _ = zw.Write(b)
	_ = zw.Close()
	return buf.Bytes()
}

func pxr24Encode(width int, chs []exrChannel, samples [][]uint32) []byte {
	var out []byte
	for c, ch := range chs {
		planes := map[int]int{exrHalf: 2, exrFloat: 3, exrUint: 4}[ch.kind]
		rows := make([][]byte, planes)
		prev := uint32(0)
		for x := range width {
			v := samples[c][x]
			if ch.kind == exrFloat {
				v &^= 0xff
			}
			d := v - prev
			prev = v
			for p := range planes {
				shift := 24 - 8*p
				if ch.kind == exrHalf {
					shift = 8 - 8*p
				}
				rows[p] = append(rows[p], byte(d>>shift))
			}
		}
		for _, r := range rows {
			out = append(out, r...)
		}
	}
	return out
}

func TestEXRSynthetic(t *testing.T) {
	chs := []exrChannel{{name: "A", kind: exrHalf}, {name: "B", kind: exrFloat}, {name: "G", kind: exrUint}, {name: "R", kind: exrHalf}}
	pattern := [][]uint32{{half1, halfHalf}, {math.Float32bits(4), math.Float32bits(0.25)}, {3, 9}, {half2, 0}}
	samples := make([][]uint32, len(pattern))
	for c, p := range pattern {
		for x := range 32 {
			samples[c] = append(samples[c], p[x%2])
		}
	}
	img, err := DecodeImage("a.exr", exrFile(0, exrAttrs(5, 32, 1, chs...), [][]byte{exrScanline(0, zlibBytes(pxr24Encode(32, chs, samples)))}))
	require.NoError(t, err)
	require.Equal(t, "PXR24", img.Encoding)
	require.Equal(t, []float32{2, 3, 4, 1, 0, 9, 0.25, 0.5}, img.HDR.Pix[:8])
	require.Equal(t, img.HDR.Pix[:8], img.HDR.Pix[120:])

	raw := bytes.Repeat([]byte{0x3c}, 32)
	raw = append(raw, bytes.Repeat([]byte{0x40}, 64)...)
	raw = append(raw, bytes.Repeat([]byte{0x01}, 64)...)
	raw = append(raw, bytes.Repeat([]byte{0x3c}, 32)...)
	img, err = DecodeImage("a.exr", exrFile(0, exrAttrs(1, 16, 1, chs...), [][]byte{exrScanline(0, exrRLEEncode(exrPredict(raw)))}))
	require.NoError(t, err)
	h := halfToFloat(0x3c3c)
	require.Equal(t, []float32{h, float32(0x01010101), math.Float32frombits(0x40404040), h}, img.HDR.Pix[:4])
	require.Equal(t, img.HDR.Pix[:4], img.HDR.Pix[60:])

	gray, err := DecodeImage("a.exr", exrFile(0, exrAttrs(0, 1, 1, exrChannel{name: "depth.Z", kind: exrFloat}), [][]byte{exrScanline(0, f32s(2))}))
	require.NoError(t, err)
	require.Equal(t, []float32{2, 2, 2, 1}, gray.HDR.Pix)

	window := exrAttrs(0, 1, 1, exrChannel{name: "R", kind: exrHalf})
	tiles := append(append([]exrAttr{}, window...), exrAttr{"tiles", "tiledesc", append(le32s(1, 1), 0)})
	for name, tc := range map[string]struct {
		data        []byte
		unsupported bool
	}{
		"magic":        {data: []byte{1, 2, 3, 4, 5, 6, 7, 8}},
		"version":      {data: binary.LittleEndian.AppendUint32(binary.LittleEndian.AppendUint32(nil, exrMagic), 1)},
		"multipart":    {data: exrFile(exrFlagMultipart, window, nil), unsupported: true},
		"unterminated": {data: append(binary.LittleEndian.AppendUint32(binary.LittleEndian.AppendUint32(nil, exrMagic), 2), 'x')},
		"no type":      {data: append(binary.LittleEndian.AppendUint32(binary.LittleEndian.AppendUint32(nil, exrMagic), 2), 'x', 0)},
		"no size":      {data: append(binary.LittleEndian.AppendUint32(binary.LittleEndian.AppendUint32(nil, exrMagic), 2), 'x', 0, 'y', 0)},
		"attr size":    {data: append(binary.LittleEndian.AppendUint32(binary.LittleEndian.AppendUint32(nil, exrMagic), 2), 'x', 0, 'y', 0, 9, 0, 0, 0)},
		"missing":      {data: exrFile(0, window[:2], nil)},
		"compression":  {data: exrFile(0, []exrAttr{{"compression", "compression", []byte{0, 0}}}, nil)},
		"window":       {data: exrFile(0, []exrAttr{{"dataWindow", "box2i", []byte{0}}}, nil)},
		"tiles attr":   {data: exrFile(0, []exrAttr{{"tiles", "tiledesc", []byte{0}}}, nil)},
		"tiled":        {data: exrFile(exrFlagTiled, window, nil)},
		"chlist":       {data: exrFile(0, []exrAttr{{"channels", "chlist", []byte{'R', 0, 1}}}, nil)},
		"type":         {data: exrFile(0, []exrAttr{{"channels", "chlist", exrChannelList(exrChannel{name: "R", kind: 7})}}, nil)},
		"sampling":     {data: exrFile(0, exrAttrs(0, 1, 1, exrChannel{name: "R", kind: exrHalf, sampling: true}), nil), unsupported: true},
		"b44":          {data: exrFile(0, exrAttrs(6, 1, 1, exrChannel{name: "R", kind: exrHalf}), nil), unsupported: true},
		"unknown":      {data: exrFile(0, exrAttrs(42, 1, 1, exrChannel{name: "R", kind: exrHalf}), nil), unsupported: true},
		"offsets":      {data: exrFile(0, window, nil)},
		"chunk y":      {data: exrFile(0, window, [][]byte{exrScanline(5, halfBytes(half1))})},
		"chunk short":  {data: exrFile(0, window, [][]byte{{0, 0}})},
		"data short":   {data: exrFile(0, window, [][]byte{exrScanline(0, []byte{1})})},
		"zip":          {data: exrFile(0, exrAttrs(3, 1, 1, exrChannel{name: "R", kind: exrHalf}), [][]byte{exrScanline(0, []byte{1})})},
		"zip body":     {data: exrFile(0, exrAttrs(3, 64, 1, exrChannel{name: "R", kind: exrHalf}), [][]byte{exrScanline(0, append(zlibBytes([]byte{1, 2, 3, 4, 5}), 0))})},
		"pxr24":        {data: exrFile(0, exrAttrs(5, 64, 1, exrChannel{name: "R", kind: exrHalf}), [][]byte{exrScanline(0, zlibBytes([]byte{1}))})},
		"rle":          {data: exrFile(0, exrAttrs(1, 64, 1, exrChannel{name: "R", kind: exrHalf}), [][]byte{exrScanline(0, []byte{1})})},
		"tile coords":  {data: exrFile(exrFlagTiled, tiles, [][]byte{append(le32s(3, 0, 0, 0, 2), 0, 0)})},
		"tile short":   {data: exrFile(exrFlagTiled, tiles, [][]byte{{0}})},
	} {
		_, err := DecodeImage("a.exr", tc.data)
		require.Error(t, err, name)
		require.Equal(t, tc.unsupported, errorsIsUnsupported(err), name)
	}
	_, err = DecodeImage("a.exr", exrFile(0, exrAttrs(0, 9000, 9000, exrChannel{name: "R", kind: exrHalf}), nil))
	require.ErrorIs(t, err, ErrTooLarge)
	repeat := []byte{3, 7}
	require.Equal(t, []byte{7, 7, 7, 7}, exrRLE(append(repeat, 0x7f), 4))
}

func TestRadianceErrors(t *testing.T) {
	head := "#?RADIANCE\nFORMAT=32-bit_rle_rgbe\n\n"
	for name, data := range map[string]string{
		"magic":       "P6",
		"header":      "#?RADIANCE",
		"format":      "#?RADIANCE\nFORMAT=32-bit_rle_xyze\n\n-Y 1 +X 1\n",
		"no res":      head,
		"orientation": head + "+X 1 -Y 1\n",
		"number":      head + "-Y a +X 1\n",
		"size":        head + "-Y 0 +X 1\n",
		"flat":        head + "-Y 1 +X 2\n\x01\x02",
		"run":         head + "-Y 1 +X 2\n\x05\x05\x05\x80\x01\x01\x01\x09",
		"rle width":   head + "-Y 1 +X 8\n\x02\x02\x00\x09",
		"rle short":   head + "-Y 1 +X 8\n\x02\x02\x00\x08",
		"rle run":     head + "-Y 1 +X 8\n\x02\x02\x00\x08\xff\x01",
		"rle zero":    head + "-Y 1 +X 8\n\x02\x02\x00\x08\x00",
		"rle literal": head + "-Y 1 +X 8\n\x02\x02\x00\x08\x08\x01",
		"rle end":     head + "-Y 1 +X 8\n\x02\x02\x00\x08\x88",
	} {
		_, err := DecodeImage("a.hdr", []byte(data))
		require.Error(t, err, name)
	}
	rle := head + "-Y 1 +X 8\n\x02\x02\x00\x08"
	for _, v := range []byte{64, 32, 0, 129} {
		rle += string([]byte{0x80 + 8, v})
	}
	img, err := DecodeImage("a.hdr", []byte(rle))
	require.NoError(t, err)
	require.InDelta(t, 64.5/128, img.HDR.Pix[0], 1e-6)
}

type psdLayerSpec struct {
	name     string
	unicode  string
	divider  int
	hidden   bool
	channels int
}

type psdSpec struct {
	version, channels, depth, mode, w, h int
	colorData                            []byte
	layers                               []psdLayerSpec
	mergedAlpha                          bool
	lr16                                 bool
	compression                          int
	planes                               [][]byte
}

func be16(v int) []byte { return binary.BigEndian.AppendUint16(nil, uint16(v)) }
func be32(v int) []byte { return binary.BigEndian.AppendUint32(nil, uint32(v)) }

func (s psdSpec) length(n int) []byte {
	if s.version == 2 {
		return binary.BigEndian.AppendUint64(nil, uint64(n))
	}
	return be32(n)
}

func (s psdSpec) layerInfo() []byte {
	count := len(s.layers)
	if s.mergedAlpha {
		count = -count
	}
	b := be16(count)
	var data []byte
	for _, l := range s.layers {
		b = append(b, be32(0)...)
		b = append(b, be32(0)...)
		b = append(b, be32(1)...)
		b = append(b, be32(2)...)
		b = append(b, be16(l.channels)...)
		for c := range l.channels {
			b = append(b, be16(c)...)
			b = append(b, s.length(4)...)
			data = append(data, 0, 0, byte(c), byte(len(l.name)))
		}
		flags := byte(0)
		if l.hidden {
			flags = psdLayerHidden
		}
		b = append(b, "8BIMnorm"...)
		b = append(b, 200, 0, flags, 0)
		var extra []byte
		extra = append(extra, be32(0)...)
		extra = append(extra, be32(0)...)
		name := append([]byte{byte(len(l.name))}, l.name...)
		for len(name)%4 != 0 {
			name = append(name, 0)
		}
		extra = append(extra, name...)
		if l.unicode != "" {
			u := be32(utf8.RuneCountInString(l.unicode) + 1)
			for _, r := range l.unicode {
				u = append(u, be16(int(r))...)
			}
			u = append(u, 0, 0)
			extra = append(extra, "8BIMluni"...)
			extra = append(extra, be32(len(u))...)
			extra = append(extra, u...)
		}
		if l.divider != 0 {
			extra = append(extra, "8BIMlsct"...)
			extra = append(extra, be32(4)...)
			extra = append(extra, be32(l.divider)...)
		}
		b = append(b, be32(len(extra))...)
		b = append(b, extra...)
	}
	return append(b, data...)
}

func (s psdSpec) bytes() []byte {
	b := []byte("8BPS")
	b = append(b, be16(max(s.version, 1))...)
	b = append(b, make([]byte, 6)...)
	b = append(b, be16(s.channels)...)
	b = append(b, be32(s.h)...)
	b = append(b, be32(s.w)...)
	b = append(b, be16(s.depth)...)
	b = append(b, be16(s.mode)...)
	b = append(b, be32(len(s.colorData))...)
	b = append(b, s.colorData...)
	b = append(b, be32(0)...)
	var section []byte
	if len(s.layers) > 0 {
		info := s.layerInfo()
		if s.lr16 {
			section = append(section, s.length(0)...)
			section = append(section, be32(0)...)
			section = append(section, "8BIMLr16"...)
			section = append(section, s.length(len(info))...)
			section = append(section, info...)
		} else {
			section = append(section, s.length(len(info))...)
			section = append(section, info...)
		}
	}
	b = append(b, s.length(len(section))...)
	b = append(b, section...)
	b = append(b, be16(s.compression)...)
	if s.compression == psdCompressRLE {
		var rows []byte
		for _, p := range s.planes {
			rowBytes := len(p) / s.h
			for y := range s.h {
				row := append([]byte{byte(rowBytes - 1)}, p[y*rowBytes:(y+1)*rowBytes]...)
				if s.version == 2 {
					b = append(b, be32(len(row))...)
				} else {
					b = append(b, be16(len(row))...)
				}
				rows = append(rows, row...)
			}
		}
		return append(b, rows...)
	}
	for _, p := range s.planes {
		b = append(b, p...)
	}
	return b
}

func TestPSDSynthetic(t *testing.T) {
	groups := psdSpec{
		channels: 4, depth: 8, mode: psdModeRGB, w: 2, h: 1, mergedAlpha: true,
		planes: [][]byte{{10, 20}, {30, 40}, {50, 60}, {255, 0}},
		layers: []psdLayerSpec{
			{name: "</Layer group>", divider: psdGroupEnd},
			{name: "Body", channels: 3},
			{name: "Eyes", unicode: "Yeux é", channels: 2, hidden: true},
			{name: "Character", divider: psdGroupOpen},
			{name: "Background", channels: 1},
		},
	}
	for _, lr16 := range []bool{false, true} {
		groups.lr16 = lr16
		for _, version := range []int{1, 2} {
			groups.version = version
			for _, compression := range []int{psdCompressRaw, psdCompressRLE} {
				groups.compression = compression
				img, err := DecodeImage("a.psd", groups.bytes())
				require.NoError(t, err)
				require.Equal(t, []uint8{10, 30, 50, 255, 20, 40, 60, 0}, img.Pixels.Pix)
				require.True(t, img.HasAlpha)
				names := []string{}
				for _, l := range img.Layers {
					names = append(names, l.Name)
				}
				require.Equal(t, []string{"Background", "Character", "Yeux é", "Body"}, names)
				require.Equal(t, "group", img.Layers[1].Kind)
				require.Equal(t, 1, img.Layers[2].Depth)
				require.False(t, img.Layers[2].Visible)
				require.Equal(t, 200, img.Layers[3].Opacity)
				require.Equal(t, "norm", img.Layers[3].Blend)
				require.Equal(t, 2, img.Layers[3].Width)
				require.Empty(t, img.Layers[1].Hash)
				require.NotEqual(t, img.Layers[2].Hash, img.Layers[3].Hash)
			}
		}
	}

	palette := make([]byte, 768)
	palette[1], palette[256+1], palette[512+1] = 9, 8, 7
	img, err := DecodeImage("a.psd", psdSpec{channels: 1, depth: 8, mode: psdModeIndexed, w: 1, h: 1, colorData: palette, planes: [][]byte{{1}}}.bytes())
	require.NoError(t, err)
	require.Equal(t, []uint8{9, 8, 7, 255}, img.Pixels.Pix)
	require.Equal(t, "Indexed", img.Encoding)

	img, err = DecodeImage("a.psd", psdSpec{channels: 1, depth: 1, mode: psdModeBitmap, w: 9, h: 1, planes: [][]byte{{0x80, 0x80}}}.bytes())
	require.NoError(t, err)
	require.Equal(t, uint8(0), img.Pixels.Pix[0])
	require.Equal(t, uint8(255), img.Pixels.Pix[4])
	require.Equal(t, uint8(0), img.Pixels.Pix[8*4])

	float := func(vs ...float32) []byte {
		var b []byte
		for _, v := range vs {
			b = binary.BigEndian.AppendUint32(b, math.Float32bits(v))
		}
		return b
	}
	img, err = DecodeImage("a.psd", psdSpec{channels: 4, depth: 32, mode: psdModeRGB, w: 1, h: 1, mergedAlpha: true, layers: []psdLayerSpec{{name: "x"}},
		planes: [][]byte{float(2), float(0.5), float(0), float(0.25)}}.bytes())
	require.NoError(t, err)
	require.Equal(t, []float32{2, 0.5, 0, 0.25}, img.HDR.Pix)
	img, err = DecodeImage("a.psd", psdSpec{channels: 1, depth: 32, mode: psdModeGray, w: 1, h: 1, planes: [][]byte{float(3)}}.bytes())
	require.NoError(t, err)
	require.Equal(t, []float32{3, 3, 3, 1}, img.HDR.Pix)

	for _, mode := range []int{psdModeMulti, psdModeDuotone, 42} {
		require.NotEmpty(t, psdModeName(mode))
	}
	for name, tc := range map[string]struct {
		data        []byte
		unsupported bool
	}{
		"signature":    {data: []byte("8BPX")},
		"version":      {data: append([]byte("8BPS"), 0, 3)},
		"channels":     {data: psdSpec{depth: 8, w: 1, h: 1}.bytes()},
		"depth":        {data: psdSpec{channels: 1, depth: 7, w: 1, h: 1}.bytes()},
		"size":         {data: psdSpec{channels: 1, depth: 8}.bytes()},
		"lab":          {data: psdSpec{channels: 3, depth: 8, mode: 9, w: 1, h: 1, planes: [][]byte{{1}, {1}, {1}}}.bytes(), unsupported: true},
		"cmyk float":   {data: psdSpec{channels: 4, depth: 32, mode: psdModeCMYK, w: 1, h: 1, planes: [][]byte{float(1), float(1), float(1), float(1)}}.bytes(), unsupported: true},
		"zip":          {data: psdSpec{channels: 1, depth: 8, mode: psdModeGray, w: 1, h: 1, compression: 2}.bytes(), unsupported: true},
		"palette":      {data: psdSpec{channels: 1, depth: 8, mode: psdModeIndexed, w: 1, h: 1, planes: [][]byte{{1}}}.bytes()},
		"planes":       {data: psdSpec{channels: 1, depth: 8, mode: psdModeRGB, w: 1, h: 1, planes: [][]byte{{1}}}.bytes()},
		"float planes": {data: psdSpec{channels: 1, depth: 32, mode: psdModeRGB, w: 1, h: 1, planes: [][]byte{float(1)}}.bytes()},
		"truncated":    {data: psdSpec{channels: 3, depth: 8, mode: psdModeRGB, w: 4, h: 4, planes: [][]byte{{1}}}.bytes()},
		"rle":          {data: psdSpec{channels: 1, depth: 8, mode: psdModeGray, w: 2, h: 1, compression: 1, planes: [][]byte{{1}}}.bytes()[:44]},
	} {
		_, err := DecodeImage("a.psd", tc.data)
		require.Error(t, err, name)
		require.Equal(t, tc.unsupported, errorsIsUnsupported(err), name)
	}

	broken := groups
	broken.version, broken.lr16, broken.compression = 1, false, psdCompressRaw
	data := broken.bytes()
	at := bytes.Index(data, []byte("8BIMnorm"))
	data[at] = 'X'
	img, err = DecodeImage("a.psd", data)
	require.NoError(t, err)
	require.Empty(t, img.Layers)

	for _, b := range [][]byte{{0x81}, {0x02, 1}, {0x80}} {
		_, err := unpackBits(nil, b, 2)
		require.Error(t, err)
	}
	out, err := unpackBits(nil, []byte{0x80, 0xff, 7}, 2)
	require.NoError(t, err)
	require.Equal(t, []byte{7, 7}, out)
	_, ok := psdUnicodeName([]byte{0, 0, 0, 9})
	require.False(t, ok)
	_, ok = psdUnicodeName(nil)
	require.False(t, ok)
}

func TestFloatHelpers(t *testing.T) {
	require.Equal(t, float32(0), halfToFloat(0))
	require.True(t, math.Signbit(float64(halfToFloat(0x8000))))
	require.Equal(t, float32(1.0/(1<<24)), halfToFloat(1))
	require.Equal(t, float32(-1.0/(1<<24)), halfToFloat(0x8001))
	require.True(t, math.IsInf(float64(halfToFloat(0x7c00)), 1))
	require.True(t, math.IsNaN(float64(halfToFloat(0x7e00))))
	require.Equal(t, float32(-2), halfToFloat(0xc000))
	require.Equal(t, float32(0.25/16384), unpackFloat(16, 6))
	require.True(t, math.IsInf(float64(unpackFloat(31<<6, 6)), 1))

	f := newFloatImage(4, 2)
	for i := range f.Pix {
		f.Pix[i] = float32(i % 4)
	}
	f.Pix[0] = float32(math.NaN())
	require.Same(t, f, DownscaleFloat(f, 0))
	require.Same(t, f, DownscaleFloat(f, 4))
	small := DownscaleFloat(f, 2)
	require.Equal(t, 2, small.Width)
	require.Equal(t, 1, small.Height)
	require.Equal(t, []float32{0, 1, 2, 3}, small.Pix[4:8])
	require.Equal(t, float32(0), small.Pix[0])
	tall := DownscaleFloat(newFloatImage(1, 4), 2)
	require.Equal(t, 1, tall.Width)
	require.Equal(t, 2, tall.Height)

	hdr := newFloatImage(4, 1)
	copy(hdr.Pix, []float32{0, 0, 0, 1, 2, 1, 0.5, 0.5, 0, 0, 0, 1, float32(math.Inf(1)), 1, 1, 1})
	hdr.Pix[8] = math.MaxFloat32
	packed := PackHDR(hdr)
	require.Equal(t, 8, packed.Rect.Dx())
	require.Equal(t, []uint8{0, 0, 0, 255}, packed.Pix[0:4])
	require.Equal(t, []uint8{128, 64, 32, 255}, packed.Pix[4:8])
	require.Equal(t, []uint8{130, 128, 0, 255}, packed.Pix[(4+1)*4:(4+1)*4+4])
	require.Equal(t, []uint8{255, 255, 255, 255}, packed.Pix[8:12])
	require.Equal(t, []uint8{0, 0, 0, 255}, packed.Pix[12:16])
	require.ErrorIs(t, checkFloatSize(8192, 8192), ErrTooLarge)
	require.NoError(t, checkFloatSize(8192, 4096))
	opaque := newFloatImage(1, 1)
	opaque.Pix[3] = 1
	require.False(t, opaque.hasAlpha())
}

func exrPredict(raw []byte) []byte {
	inter := make([]byte, len(raw))
	for i := range raw {
		if i%2 == 0 {
			inter[i/2] = raw[i]
		} else {
			inter[(len(raw)+1)/2+i/2] = raw[i]
		}
	}
	out := make([]byte, len(inter))
	out[0] = inter[0]
	for i := 1; i < len(inter); i++ {
		out[i] = inter[i] - inter[i-1] + 128
	}
	return out
}

func exrRLEEncode(b []byte) []byte {
	var out []byte
	for len(b) > 0 {
		n := 1
		for n < len(b) && n < 128 && b[n] == b[0] {
			n++
		}
		out = append(out, byte(n-1), b[0])
		b = b[n:]
	}
	return out
}
