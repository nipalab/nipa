package preview

import "image"

type ImageDiff struct {
	OldWidth, OldHeight int
	NewWidth, NewHeight int
	SizeChanged         bool
	FormatChanged       bool
	BitDepthChanged     bool
	AlphaChanged        bool
	ChangedPixels       int
	TotalPixels         int
	ChangedRatio        float64
	MaxDelta            uint8
	Bounds              image.Rectangle
}

// CompareImages counts pixels whose largest channel delta exceeds threshold.
// Fully transparent pixels compare equal whatever their color. Images of
// different sizes report no pixel statistics.
func CompareImages(oldImg, newImg *Image, threshold uint8) ImageDiff {
	d := ImageDiff{
		OldWidth: oldImg.Width(), OldHeight: oldImg.Height(),
		NewWidth: newImg.Width(), NewHeight: newImg.Height(),
		FormatChanged:   oldImg.Format != newImg.Format,
		BitDepthChanged: oldImg.BitDepth != newImg.BitDepth,
		AlphaChanged:    oldImg.HasAlpha != newImg.HasAlpha,
	}
	if d.OldWidth != d.NewWidth || d.OldHeight != d.NewHeight {
		d.SizeChanged = true
		return d
	}
	a, b := oldImg.Pixels, newImg.Pixels
	d.TotalPixels = d.NewWidth * d.NewHeight
	for y := 0; y < d.NewHeight; y++ {
		ra := a.Pix[y*a.Stride : y*a.Stride+d.NewWidth*4]
		rb := b.Pix[y*b.Stride : y*b.Stride+d.NewWidth*4]
		for x := 0; x < d.NewWidth; x++ {
			delta := pixelDelta(ra[x*4:x*4+4], rb[x*4:x*4+4])
			if delta > d.MaxDelta {
				d.MaxDelta = delta
			}
			if delta <= threshold {
				continue
			}
			d.ChangedPixels++
			d.Bounds = d.Bounds.Union(image.Rect(x, y, x+1, y+1))
		}
	}
	d.ChangedRatio = float64(d.ChangedPixels) / float64(d.TotalPixels)
	return d
}

func pixelDelta(p, q []byte) uint8 {
	if p[3] == 0 && q[3] == 0 {
		return 0
	}
	var m uint8
	for i := range 4 {
		v := p[i] - q[i]
		if q[i] > p[i] {
			v = q[i] - p[i]
		}
		m = max(m, v)
	}
	return m
}
