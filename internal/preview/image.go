package preview

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"path/filepath"
	"strings"

	xdraw "golang.org/x/image/draw"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
)

// MaxPixels bounds decoded images (8192x8192) so hostile headers cannot
// allocate unbounded memory.
var MaxPixels = 8192 * 8192

var (
	ErrUnsupported = errors.New("preview: unsupported format")
	ErrTooLarge    = errors.New("preview: image too large")
)

// Image is one decoded surface. Pixels is always 8-bit sRGB for display and
// comparison; HDR additionally keeps the linear samples of float sources.
// Levels and Faces describe the whole texture the surface was taken from.
type Image struct {
	Pixels   *image.NRGBA
	HDR      *FloatImage
	Format   string
	Encoding string
	BitDepth int
	HasAlpha bool
	Levels   int
	Faces    int
	Cube     bool
	Level    int
	Face     int
	Layers   []Layer
}

func (i *Image) setHDR(f *FloatImage) {
	i.HDR = f
	i.Pixels = f.display()
	i.HasAlpha = f.hasAlpha()
}

func (i *Image) Width() int  { return i.Pixels.Rect.Dx() }
func (i *Image) Height() int { return i.Pixels.Rect.Dy() }

// DecodeImage decodes the first surface of an image; see DecodeImageAt.
func DecodeImage(name string, data []byte) (*Image, error) {
	return DecodeImageAt(name, data, Select{})
}

// DecodeImageAt decodes PNG, JPEG, GIF, BMP, TIFF, WebP and TGA content and
// the game formats DDS, KTX2, EXR, Radiance HDR and PSD/PSB. Formats without
// a reliable magic number are chosen by extension; sel picks the mip level
// and cube face or array layer of textures.
func DecodeImageAt(name string, data []byte, sel Select) (*Image, error) {
	img, err := decodeByExtension(name, data, sel)
	if err != nil || img == nil {
		return img, err
	}
	img.Levels, img.Faces = max(img.Levels, 1), max(img.Faces, 1)
	return img, nil
}

func decodeByExtension(name string, data []byte, sel Select) (*Image, error) {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".tga":
		return decodeTGA(data)
	case ".dds":
		return decodeDDS(data, sel)
	case ".ktx2":
		return decodeKTX2(data, sel)
	case ".exr":
		return decodeEXR(data)
	case ".hdr":
		return decodeRadiance(data)
	case ".psd", ".psb":
		return decodePSD(data)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		if errors.Is(err, image.ErrFormat) {
			return nil, ErrUnsupported
		}
		return nil, fmt.Errorf("preview: decode %s config: %w", name, err)
	}
	if err := checkSize(cfg.Width, cfg.Height); err != nil {
		return nil, err
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("preview: decode %s: %w", name, err)
	}
	pixels := toNRGBA(src)
	return &Image{
		Pixels:   pixels,
		Format:   format,
		BitDepth: bitDepth(cfg.ColorModel),
		HasAlpha: hasAlpha(pixels),
	}, nil
}

// checkFloatSize applies half the pixel budget to float sources, which cost
// 16 bytes per pixel while decoding.
func checkFloatSize(w, h int) error {
	if err := checkSize(w, h); err != nil {
		return err
	}
	if w > MaxPixels/2/h {
		return ErrTooLarge
	}
	return nil
}

func checkSize(w, h int) error {
	if w <= 0 || h <= 0 {
		return fmt.Errorf("preview: invalid dimensions %dx%d", w, h)
	}
	if w > MaxPixels/h {
		return ErrTooLarge
	}
	return nil
}

func toNRGBA(src image.Image) *image.NRGBA {
	if n, ok := src.(*image.NRGBA); ok && n.Rect.Min == (image.Point{}) {
		return n
	}
	b := src.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Rect, src, b.Min, draw.Src)
	return dst
}

func bitDepth(model color.Model) int {
	switch model {
	case color.RGBA64Model, color.NRGBA64Model, color.Gray16Model, color.Alpha16Model:
		return 16
	}
	return 8
}

func hasAlpha(img *image.NRGBA) bool {
	for i := 3; i < len(img.Pix); i += 4 {
		if img.Pix[i] != 0xff {
			return true
		}
	}
	return false
}

// Downscale fits img inside a maxSide x maxSide box, keeping the aspect ratio.
// Images already within the box are returned unchanged.
func Downscale(img *image.NRGBA, maxSide int) *image.NRGBA {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	if maxSide <= 0 || (w <= maxSide && h <= maxSide) {
		return img
	}
	nw, nh := maxSide, maxSide
	if w >= h {
		nh = max(1, h*maxSide/w)
	} else {
		nw = max(1, w*maxSide/h)
	}
	dst := image.NewNRGBA(image.Rect(0, 0, nw, nh))
	xdraw.BiLinear.Scale(dst, dst.Rect, img, img.Rect, xdraw.Src, nil)
	return dst
}
