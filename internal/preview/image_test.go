package preview

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/image/bmp"
	"golang.org/x/image/tiff"
)

func sampleNRGBA(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 40), G: uint8(y * 60), B: 90, A: 0xff})
		}
	}
	return img
}

func encode(t *testing.T, fn func(*bytes.Buffer) error) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, fn(&buf))
	return buf.Bytes()
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return data
}

func TestDecodeImageFormats(t *testing.T) {
	src := sampleNRGBA(5, 3)
	cases := []struct {
		name   string
		data   []byte
		format string
	}{
		{"a.png", encode(t, func(b *bytes.Buffer) error { return png.Encode(b, src) }), "png"},
		{"a.jpg", encode(t, func(b *bytes.Buffer) error { return jpeg.Encode(b, src, nil) }), "jpeg"},
		{"a.gif", encode(t, func(b *bytes.Buffer) error { return gif.Encode(b, src, nil) }), "gif"},
		{"a.bmp", encode(t, func(b *bytes.Buffer) error { return bmp.Encode(b, src) }), "bmp"},
		{"a.tiff", encode(t, func(b *bytes.Buffer) error { return tiff.Encode(b, src, nil) }), "tiff"},
	}
	for _, c := range cases {
		img, err := DecodeImage(c.name, c.data)
		require.NoError(t, err, c.name)
		assert.Equal(t, c.format, img.Format, c.name)
		assert.Equal(t, 5, img.Width(), c.name)
		assert.Equal(t, 3, img.Height(), c.name)
		assert.Equal(t, 8, img.BitDepth, c.name)
		assert.False(t, img.HasAlpha, c.name)
	}
}

func TestDecodeImageWebPMatchesPNG(t *testing.T) {
	ref, err := DecodeImage("ref.png", readFixture(t, "ref.png"))
	require.NoError(t, err)
	webp, err := DecodeImage("ref.webp", readFixture(t, "ref.webp"))
	require.NoError(t, err)

	assert.Equal(t, "webp", webp.Format)
	assert.True(t, webp.HasAlpha)
	d := CompareImages(ref, webp, 0)
	assert.Zero(t, d.ChangedPixels)
}

func TestDecodeImageSixteenBit(t *testing.T) {
	src := image.NewNRGBA64(image.Rect(0, 0, 2, 2))
	src.SetNRGBA64(1, 1, color.NRGBA64{R: 0xffff, A: 0x8000})
	img, err := DecodeImage("deep.png", encode(t, func(b *bytes.Buffer) error { return png.Encode(b, src) }))
	require.NoError(t, err)
	assert.Equal(t, 16, img.BitDepth)
	assert.True(t, img.HasAlpha)
}

func TestDecodeImageErrors(t *testing.T) {
	_, err := DecodeImage("x.bin", []byte("definitely not an image"))
	assert.ErrorIs(t, err, ErrUnsupported)

	full := encode(t, func(b *bytes.Buffer) error { return png.Encode(b, sampleNRGBA(5, 3)) })
	_, err = DecodeImage("cut.png", full[:40])
	assert.Error(t, err)

	_, err = DecodeImage("cut.png", full[:20])
	assert.Error(t, err)

	old := MaxPixels
	MaxPixels = 10
	defer func() { MaxPixels = old }()
	_, err = DecodeImage("big.png", full)
	assert.ErrorIs(t, err, ErrTooLarge)
}

func TestCheckSizeRejectsEmpty(t *testing.T) {
	assert.Error(t, checkSize(0, 4))
	assert.Error(t, checkSize(4, 0))
	assert.NoError(t, checkSize(4, 4))
}

func TestToNRGBAOffsetsSubImage(t *testing.T) {
	src := sampleNRGBA(4, 4)
	sub := src.SubImage(image.Rect(1, 1, 3, 3))
	out := toNRGBA(sub)
	assert.Equal(t, image.Rect(0, 0, 2, 2), out.Rect)
	assert.Equal(t, src.NRGBAAt(1, 1), out.NRGBAAt(0, 0))

	assert.Same(t, src, toNRGBA(src))
}

func TestDownscale(t *testing.T) {
	small := sampleNRGBA(4, 2)
	assert.Same(t, small, Downscale(small, 8))
	assert.Same(t, small, Downscale(small, 0))

	wide := Downscale(sampleNRGBA(100, 50), 10)
	assert.Equal(t, image.Rect(0, 0, 10, 5), wide.Rect)

	tall := Downscale(sampleNRGBA(20, 200), 10)
	assert.Equal(t, image.Rect(0, 0, 1, 10), tall.Rect)

	thin := Downscale(sampleNRGBA(1000, 1), 10)
	assert.Equal(t, image.Rect(0, 0, 10, 1), thin.Rect)
}
