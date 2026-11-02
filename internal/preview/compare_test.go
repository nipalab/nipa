package preview

import (
	"image"
	"image/color"
	"testing"

	"github.com/stretchr/testify/assert"
)

func wrap(img *image.NRGBA) *Image {
	return &Image{Pixels: img, Format: "png", BitDepth: 8, HasAlpha: hasAlpha(img)}
}

func TestCompareImagesIdentical(t *testing.T) {
	a := wrap(sampleNRGBA(4, 3))
	d := CompareImages(a, wrap(sampleNRGBA(4, 3)), 0)
	assert.Zero(t, d.ChangedPixels)
	assert.Equal(t, 12, d.TotalPixels)
	assert.Zero(t, d.ChangedRatio)
	assert.True(t, d.Bounds.Empty())
	assert.False(t, d.SizeChanged || d.FormatChanged || d.BitDepthChanged || d.AlphaChanged)
}

func TestCompareImagesChangedRegion(t *testing.T) {
	oldImg := sampleNRGBA(4, 4)
	newImg := sampleNRGBA(4, 4)
	newImg.SetNRGBA(1, 2, color.NRGBA{R: 1, G: 2, B: 3, A: 255})
	newImg.SetNRGBA(3, 3, color.NRGBA{R: 40 * 3, G: 60 * 3, B: 95, A: 255})

	d := CompareImages(wrap(oldImg), wrap(newImg), 0)
	assert.Equal(t, 2, d.ChangedPixels)
	assert.Equal(t, image.Rect(1, 2, 4, 4), d.Bounds)
	assert.InDelta(t, 2.0/16, d.ChangedRatio, 1e-9)
	assert.Equal(t, uint8(118), d.MaxDelta)

	d = CompareImages(wrap(oldImg), wrap(newImg), 5)
	assert.Equal(t, 1, d.ChangedPixels)
	assert.Equal(t, image.Rect(1, 2, 2, 3), d.Bounds)
}

func TestCompareImagesTransparentPixelsEqual(t *testing.T) {
	oldImg := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	newImg := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	newImg.SetNRGBA(0, 0, color.NRGBA{R: 200})
	d := CompareImages(wrap(oldImg), wrap(newImg), 0)
	assert.Zero(t, d.ChangedPixels)
}

func TestCompareImagesMetadataChanges(t *testing.T) {
	oldImg := wrap(sampleNRGBA(4, 4))
	newImg := wrap(sampleNRGBA(8, 4))
	newImg.Format = "tga"
	newImg.BitDepth = 16
	newImg.HasAlpha = true

	d := CompareImages(oldImg, newImg, 0)
	assert.True(t, d.SizeChanged)
	assert.True(t, d.FormatChanged)
	assert.True(t, d.BitDepthChanged)
	assert.True(t, d.AlphaChanged)
	assert.Zero(t, d.TotalPixels)
	assert.Equal(t, 8, d.NewWidth)
	assert.Equal(t, 4, d.OldWidth)
}
