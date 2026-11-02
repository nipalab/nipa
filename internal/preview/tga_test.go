package preview

import (
	"encoding/binary"
	"image/color"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecodeTGAFixturesMatchReference(t *testing.T) {
	cases := []struct {
		fixture string
		ref     string
		alpha   bool
	}{
		{"ref.tga", "ref.png", true},
		{"ref_rle.tga", "ref.png", true},
		{"ref32.tga", "ref.png", true},
		{"ref32_rle.tga", "ref.png", true},
		{"ref24.tga", "ref24.png", false},
		{"ref24_rle.tga", "ref24.png", false},
		{"ref_gray.tga", "ref_gray.png", false},
	}
	for _, c := range cases {
		ref, err := DecodeImage(c.ref, readFixture(t, c.ref))
		require.NoError(t, err, c.ref)
		img, err := DecodeImage(c.fixture, readFixture(t, c.fixture))
		require.NoError(t, err, c.fixture)

		assert.Equal(t, "tga", img.Format, c.fixture)
		assert.Equal(t, c.alpha, img.HasAlpha, c.fixture)
		d := CompareImages(ref, img, 0)
		assert.False(t, d.SizeChanged, c.fixture)
		assert.Zero(t, d.ChangedPixels, c.fixture)
	}
}

type tgaBuilder struct {
	imageType  byte
	cmType     byte
	cmFirst    uint16
	cmLength   uint16
	cmDepth    byte
	width      uint16
	height     uint16
	depth      byte
	descriptor byte
	id         []byte
	palette    []byte
	pixels     []byte
}

func (b tgaBuilder) bytes() []byte {
	h := make([]byte, tgaHeaderSize)
	h[0] = byte(len(b.id))
	h[1] = b.cmType
	h[2] = b.imageType
	binary.LittleEndian.PutUint16(h[3:], b.cmFirst)
	binary.LittleEndian.PutUint16(h[5:], b.cmLength)
	h[7] = b.cmDepth
	binary.LittleEndian.PutUint16(h[12:], b.width)
	binary.LittleEndian.PutUint16(h[14:], b.height)
	h[16] = b.depth
	h[17] = b.descriptor
	out := append(h, b.id...)
	out = append(out, b.palette...)
	return append(out, b.pixels...)
}

func TestDecodeTGAOrientation(t *testing.T) {
	pixels := []byte{
		0, 0, 255, // red, stored first
		0, 255, 0, // green
	}
	bottomUp := tgaBuilder{imageType: tgaTrueColor, width: 1, height: 2, depth: 24, pixels: pixels}
	img, err := DecodeImage("a.tga", bottomUp.bytes())
	require.NoError(t, err)
	assert.Equal(t, color.NRGBA{G: 255, A: 255}, img.Pixels.NRGBAAt(0, 0))
	assert.Equal(t, color.NRGBA{R: 255, A: 255}, img.Pixels.NRGBAAt(0, 1))

	rightToLeft := tgaBuilder{imageType: tgaTrueColor, width: 2, height: 1, depth: 24, descriptor: 0x30, id: []byte("hi"), pixels: pixels}
	img, err = DecodeImage("a.tga", rightToLeft.bytes())
	require.NoError(t, err)
	assert.Equal(t, color.NRGBA{G: 255, A: 255}, img.Pixels.NRGBAAt(0, 0))
	assert.Equal(t, color.NRGBA{R: 255, A: 255}, img.Pixels.NRGBAAt(1, 0))
}

func TestDecodeTGASixteenBit(t *testing.T) {
	px := make([]byte, 4)
	binary.LittleEndian.PutUint16(px, 0x7c00)
	binary.LittleEndian.PutUint16(px[2:], 0x801f)

	withAlpha := tgaBuilder{imageType: tgaTrueColor, width: 2, height: 1, depth: 16, descriptor: 0x21, pixels: px}
	img, err := DecodeImage("a.tga", withAlpha.bytes())
	require.NoError(t, err)
	assert.Equal(t, color.NRGBA{R: 255, A: 0}, img.Pixels.NRGBAAt(0, 0))
	assert.Equal(t, color.NRGBA{B: 255, A: 255}, img.Pixels.NRGBAAt(1, 0))

	noAlpha := withAlpha
	noAlpha.descriptor = 0x20
	img, err = DecodeImage("a.tga", noAlpha.bytes())
	require.NoError(t, err)
	assert.False(t, img.HasAlpha)

	gray16 := tgaBuilder{imageType: tgaGray, width: 1, height: 1, depth: 16, descriptor: 0x20, pixels: []byte{100, 50}}
	img, err = DecodeImage("a.tga", gray16.bytes())
	require.NoError(t, err)
	assert.Equal(t, color.NRGBA{R: 100, G: 100, B: 100, A: 50}, img.Pixels.NRGBAAt(0, 0))
}

func TestDecodeTGAPaletteOutOfRange(t *testing.T) {
	b := tgaBuilder{
		imageType: tgaColorMapped, cmType: 1, cmFirst: 1, cmLength: 1, cmDepth: 24,
		width: 2, height: 1, depth: 8, descriptor: 0x20,
		palette: []byte{0, 0, 255},
		pixels:  []byte{1, 9},
	}
	img, err := DecodeImage("a.tga", b.bytes())
	require.NoError(t, err)
	assert.Equal(t, color.NRGBA{R: 255, A: 255}, img.Pixels.NRGBAAt(0, 0))
	assert.Equal(t, color.NRGBA{}, img.Pixels.NRGBAAt(1, 0))
}

func TestDecodeTGARejectsBadInput(t *testing.T) {
	valid := tgaBuilder{imageType: tgaTrueColor, width: 2, height: 1, depth: 24, pixels: make([]byte, 6)}
	rle := tgaBuilder{imageType: tgaRLETrueColor, width: 4, height: 1, depth: 24}
	mapped := tgaBuilder{imageType: tgaColorMapped, cmType: 1, cmLength: 4, cmDepth: 24, width: 1, height: 1, depth: 8}

	unsupported := []tgaBuilder{
		{imageType: 0, width: 1, height: 1, depth: 24},
		{imageType: tgaTrueColor, width: 1, height: 1, depth: 8},
		{imageType: tgaGray, width: 1, height: 1, depth: 24},
		{imageType: tgaColorMapped, cmType: 0, width: 1, height: 1, depth: 8},
		{imageType: tgaColorMapped, cmType: 1, cmDepth: 8, width: 1, height: 1, depth: 8},
	}
	for _, b := range unsupported {
		_, err := DecodeImage("a.tga", b.bytes())
		assert.ErrorIs(t, err, ErrUnsupported, "type %d depth %d", b.imageType, b.depth)
	}

	withID := valid
	withID.id = []byte("abcd")
	truncated := [][]byte{
		valid.bytes()[:10],
		withID.bytes()[:tgaHeaderSize+2],
		valid.bytes()[:tgaHeaderSize+3],
		mapped.bytes(),
		rle.bytes(),
		append(rle.bytes(), 0x83, 1),
		append(rle.bytes(), 0x01, 1, 2, 3),
	}
	for i, data := range truncated {
		_, err := DecodeImage("a.tga", data)
		assert.ErrorIs(t, err, errTGATruncated, "case %d", i)
	}

	empty := tgaBuilder{imageType: tgaTrueColor, width: 0, height: 1, depth: 24}
	_, err := DecodeImage("a.tga", empty.bytes())
	assert.Error(t, err)

	huge := tgaBuilder{imageType: tgaTrueColor, width: 65535, height: 65535, depth: 24}
	_, err = DecodeImage("a.tga", huge.bytes())
	assert.ErrorIs(t, err, ErrTooLarge)
}

func TestDecodeTGASkipsUnusedColorMap(t *testing.T) {
	b := tgaBuilder{
		imageType: tgaTrueColor, cmType: 1, cmLength: 2, cmDepth: 8,
		width: 1, height: 1, depth: 24, descriptor: 0x20,
		palette: []byte{1, 2},
		pixels:  []byte{0, 0, 255},
	}
	img, err := DecodeImage("a.tga", b.bytes())
	require.NoError(t, err)
	assert.Equal(t, color.NRGBA{R: 255, A: 255}, img.Pixels.NRGBAAt(0, 0))
}

func TestDecodeTGARLEOverrunIsTrimmed(t *testing.T) {
	b := tgaBuilder{imageType: tgaRLEGray, width: 3, height: 1, depth: 8, descriptor: 0x20, pixels: []byte{0x84, 7}}
	img, err := DecodeImage("a.tga", b.bytes())
	require.NoError(t, err)
	assert.Equal(t, color.NRGBA{R: 7, G: 7, B: 7, A: 255}, img.Pixels.NRGBAAt(2, 0))
}
