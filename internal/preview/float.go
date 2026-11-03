package preview

import (
	"image"
	"math"
)

// FloatImage holds linear, non-premultiplied RGBA samples of an HDR source.
type FloatImage struct {
	Width, Height int
	Pix           []float32
}

func newFloatImage(w, h int) *FloatImage {
	return &FloatImage{Width: w, Height: h, Pix: make([]float32, w*h*4)}
}

func (f *FloatImage) hasAlpha() bool {
	for i := 3; i < len(f.Pix); i += 4 {
		if f.Pix[i] < 1 {
			return true
		}
	}
	return false
}

func halfToFloat(h uint16) float32 {
	sign := uint32(h>>15) << 31
	exp := uint32(h>>10) & 0x1f
	mant := uint32(h & 0x3ff)
	switch {
	case exp == 0 && mant == 0:
		return math.Float32frombits(sign)
	case exp == 0:
		v := float32(mant) / (1 << 24)
		if sign != 0 {
			return -v
		}
		return v
	case exp == 31:
		return math.Float32frombits(sign | 0x7f800000 | mant<<13)
	}
	return math.Float32frombits(sign | (exp+112)<<23 | mant<<13)
}

const srgbSteps = 4096

var srgbTable = func() [srgbSteps + 1]uint8 {
	var t [srgbSteps + 1]uint8
	for i := range t {
		v := float64(i) / srgbSteps
		if v <= 0.0031308 {
			v *= 12.92
		} else {
			v = 1.055*math.Pow(v, 1/2.4) - 0.055
		}
		t[i] = uint8(math.Round(v * 255))
	}
	return t
}()

func clamp01(v float32) float32 {
	switch {
	case v > 1:
		return 1
	case v > 0:
		return v
	}
	return 0
}

func linearToSRGB(v float32) uint8 {
	return srgbTable[int(clamp01(v)*srgbSteps+0.5)]
}

// display maps the linear samples to 8-bit sRGB, clamping at 1.0. It is the
// image used for pixel statistics and viewers without exposure control.
func (f *FloatImage) display() *image.NRGBA {
	dst := image.NewNRGBA(image.Rect(0, 0, f.Width, f.Height))
	for i := 0; i < len(f.Pix); i += 4 {
		dst.Pix[i] = linearToSRGB(f.Pix[i])
		dst.Pix[i+1] = linearToSRGB(f.Pix[i+1])
		dst.Pix[i+2] = linearToSRGB(f.Pix[i+2])
		dst.Pix[i+3] = uint8(clamp01(f.Pix[i+3])*255 + 0.5)
	}
	return dst
}

// DownscaleFloat box-filters f into a maxSide x maxSide box, keeping the
// aspect ratio; images already within the box are returned unchanged.
func DownscaleFloat(f *FloatImage, maxSide int) *FloatImage {
	w, h := f.Width, f.Height
	if maxSide <= 0 || (w <= maxSide && h <= maxSide) {
		return f
	}
	nw, nh := maxSide, maxSide
	if w >= h {
		nh = max(1, h*maxSide/w)
	} else {
		nw = max(1, w*maxSide/h)
	}
	dst := newFloatImage(nw, nh)
	for y := range nh {
		y0, y1 := y*h/nh, max(y*h/nh+1, (y+1)*h/nh)
		for x := range nw {
			x0, x1 := x*w/nw, max(x*w/nw+1, (x+1)*w/nw)
			var sum [4]float64
			for sy := y0; sy < y1; sy++ {
				row := f.Pix[(sy*w+x0)*4 : (sy*w+x1)*4]
				for i := 0; i < len(row); i += 4 {
					for c := range 4 {
						if v := row[i+c]; !math.IsNaN(float64(v)) && !math.IsInf(float64(v), 0) {
							sum[c] += float64(v)
						}
					}
				}
			}
			n := float64((y1 - y0) * (x1 - x0))
			o := (y*nw + x) * 4
			for c := range 4 {
				dst.Pix[o+c] = float32(sum[c] / n)
			}
		}
	}
	return dst
}

// PackHDR stores f as an opaque 8-bit image twice as wide: the left half holds
// shared-exponent (RGBE) mantissas, the right half the exponent in red and
// the alpha in green. Opaque pixels survive a browser canvas untouched, so
// the frontend can rebuild the floats and apply its own exposure.
func PackHDR(f *FloatImage) *image.NRGBA {
	dst := image.NewNRGBA(image.Rect(0, 0, f.Width*2, f.Height))
	for y := range f.Height {
		for x := range f.Width {
			i := (y*f.Width + x) * 4
			r, g, b := max(f.Pix[i], 0), max(f.Pix[i+1], 0), max(f.Pix[i+2], 0)
			m := max(r, g, b)
			left := y*dst.Stride + x*4
			right := y*dst.Stride + (f.Width+x)*4
			dst.Pix[left+3], dst.Pix[right+3] = 0xff, 0xff
			dst.Pix[right+1] = uint8(clamp01(f.Pix[i+3])*255 + 0.5)
			if !(m >= 1e-32) || math.IsInf(float64(m), 0) {
				continue
			}
			frac, exp := math.Frexp(float64(m))
			if exp > 127 {
				dst.Pix[left], dst.Pix[left+1], dst.Pix[left+2], dst.Pix[right] = 0xff, 0xff, 0xff, 0xff
				continue
			}
			scale := frac * 256 / float64(m)
			dst.Pix[left] = uint8(min(255, float64(r)*scale))
			dst.Pix[left+1] = uint8(min(255, float64(g)*scale))
			dst.Pix[left+2] = uint8(min(255, float64(b)*scale))
			dst.Pix[right] = uint8(exp + 128)
		}
	}
	return dst
}
