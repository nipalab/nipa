package assetdiff

import (
	"bytes"
	"image/png"

	"github.com/nipalab/nipa/internal/preview"
)

type ImageResult struct {
	Old     *ImageSide    `json:"old,omitempty"`
	New     *ImageSide    `json:"new,omitempty"`
	Compare *ImageCompare `json:"compare,omitempty"`
}

// ImageSide describes one version. ThumbnailPNG (base64 in JSON) is only
// filled when Options.Images is set.
type ImageSide struct {
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	Format       string `json:"format"`
	BitDepth     int    `json:"bit_depth"`
	HasAlpha     bool   `json:"has_alpha"`
	ThumbnailPNG []byte `json:"thumbnail_png,omitempty"`
}

type ImageCompare struct {
	SizeChanged     bool    `json:"size_changed"`
	FormatChanged   bool    `json:"format_changed"`
	BitDepthChanged bool    `json:"bit_depth_changed"`
	AlphaChanged    bool    `json:"alpha_changed"`
	ChangedPixels   int     `json:"changed_pixels"`
	TotalPixels     int     `json:"total_pixels"`
	ChangedRatio    float64 `json:"changed_ratio"`
	MaxDelta        uint8   `json:"max_delta"`
	Bounds          *Rect   `json:"bounds,omitempty"`
}

type Rect struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

func compareImage(sides [2]side, opts Options) (*ImageResult, error) {
	var imgs [2]*preview.Image
	out := &ImageResult{}
	for i, s := range sides {
		if !s.present {
			continue
		}
		img, err := preview.DecodeImage(s.ref.Path, s.data)
		if err != nil {
			return nil, err
		}
		imgs[i] = img
		is := &ImageSide{Width: img.Width(), Height: img.Height(), Format: img.Format, BitDepth: img.BitDepth, HasAlpha: img.HasAlpha}
		if opts.Images {
			var buf bytes.Buffer
			if err := png.Encode(&buf, preview.Downscale(img.Pixels, opts.ThumbSize)); err != nil {
				return nil, err
			}
			is.ThumbnailPNG = buf.Bytes()
		}
		if i == 0 {
			out.Old = is
		} else {
			out.New = is
		}
	}
	if imgs[0] == nil || imgs[1] == nil {
		return out, nil
	}
	d := preview.CompareImages(imgs[0], imgs[1], opts.Threshold)
	out.Compare = &ImageCompare{
		SizeChanged: d.SizeChanged, FormatChanged: d.FormatChanged, BitDepthChanged: d.BitDepthChanged, AlphaChanged: d.AlphaChanged,
		ChangedPixels: d.ChangedPixels, TotalPixels: d.TotalPixels, ChangedRatio: d.ChangedRatio, MaxDelta: d.MaxDelta,
	}
	if !d.Bounds.Empty() {
		out.Compare.Bounds = &Rect{X: d.Bounds.Min.X, Y: d.Bounds.Min.Y, W: d.Bounds.Dx(), H: d.Bounds.Dy()}
	}
	return out, nil
}
