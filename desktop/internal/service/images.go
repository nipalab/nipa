package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
)

const previewUnsupported = 415

// ImageRef names one version of an image: the working file, the last synced
// snapshot or a revision.
type ImageRef struct {
	Source   string `json:"source"` // working, snapshot, revision
	Revision string `json:"revision,omitempty"`
	Path     string `json:"path"`
}

type ImageInfo struct {
	Width         int    `json:"width"`
	Height        int    `json:"height"`
	Format        string `json:"format"`
	BitDepth      int    `json:"bitDepth"`
	HasAlpha      bool   `json:"hasAlpha"`
	Size          int64  `json:"size"`
	PreviewWidth  int    `json:"previewWidth"`
	PreviewHeight int    `json:"previewHeight"`
	Src           string `json:"src"` // data:image/png;base64 URL of the downscaled preview
}

type ImageStats struct {
	ChangedPixels   int64   `json:"changedPixels"`
	TotalPixels     int64   `json:"totalPixels"`
	ChangedRatio    float64 `json:"changedRatio"`
	MaxDelta        int     `json:"maxDelta"`
	BoundsX         int     `json:"boundsX"`
	BoundsY         int     `json:"boundsY"`
	BoundsWidth     int     `json:"boundsWidth"`
	BoundsHeight    int     `json:"boundsHeight"`
	SizeChanged     bool    `json:"sizeChanged"`
	FormatChanged   bool    `json:"formatChanged"`
	BitDepthChanged bool    `json:"bitDepthChanged"`
	AlphaChanged    bool    `json:"alphaChanged"`
}

type ImageCompare struct {
	Old   *ImageInfo  `json:"old"`
	New   *ImageInfo  `json:"new"`
	Stats *ImageStats `json:"stats"`
	// Unsupported explains why no preview could be made (unknown format, too
	// large); the frontend then falls back to the metadata view.
	Unsupported string `json:"unsupported,omitempty"`
}

// ImageDiff decodes the old and/or new version of an image into previews no
// larger than maxSize pixels, plus pixel-diff statistics when both exist.
func (s *Service) ImageDiff(ctx context.Context, root string, old, new *ImageRef, maxSize int) (*ImageCompare, error) {
	if old == nil && new == nil {
		return nil, errors.New("nothing to compare")
	}
	client, err := s.client(ctx, root)
	if err != nil {
		return nil, err
	}
	stream, err := client.Preview(ctx, &daemonpb.PreviewRequest{
		Root:    root,
		Old:     blobRef(old),
		New:     blobRef(new),
		MaxSize: int32(maxSize),
	})
	if err != nil {
		s.dropIfDisconnected(err)
		return nil, cleanError(err)
	}
	out := &ImageCompare{}
	pngs := map[string]*bytes.Buffer{}
	for {
		ev, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			s.dropIfDisconnected(err)
			return nil, cleanError(err)
		}
		switch {
		case ev.GetFailure() != nil:
			if ev.GetFailure().GetCode() == previewUnsupported {
				return &ImageCompare{Unsupported: ev.GetFailure().GetMessage()}, nil
			}
			return nil, errors.New(ev.GetFailure().GetMessage())
		case ev.GetImage() != nil:
			img := ev.GetImage()
			info := toImageInfo(img)
			if img.GetSide() == "old" {
				out.Old = info
			} else {
				out.New = info
			}
			pngs[img.GetSide()] = &bytes.Buffer{}
		case ev.GetData() != nil:
			if buf := pngs[ev.GetData().GetSide()]; buf != nil {
				buf.Write(ev.GetData().GetPng())
			}
		case ev.GetDiff() != nil:
			out.Stats = toImageStats(ev.GetDiff())
		}
	}
	for side, info := range map[string]*ImageInfo{"old": out.Old, "new": out.New} {
		if info != nil {
			info.Src = "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngs[side].Bytes())
		}
	}
	return out, nil
}

func blobRef(ref *ImageRef) *daemonpb.BlobRef {
	if ref == nil {
		return nil
	}
	return &daemonpb.BlobRef{Source: ref.Source, Revision: ref.Revision, Path: ref.Path}
}

func toImageInfo(img *daemonpb.ImagePreview) *ImageInfo {
	return &ImageInfo{
		Width:         int(img.GetWidth()),
		Height:        int(img.GetHeight()),
		Format:        img.GetFormat(),
		BitDepth:      int(img.GetBitDepth()),
		HasAlpha:      img.GetHasAlpha(),
		Size:          img.GetSizeBytes(),
		PreviewWidth:  int(img.GetPreviewWidth()),
		PreviewHeight: int(img.GetPreviewHeight()),
	}
}

func toImageStats(d *daemonpb.ImageDiffStats) *ImageStats {
	return &ImageStats{
		ChangedPixels:   d.GetChangedPixels(),
		TotalPixels:     d.GetTotalPixels(),
		ChangedRatio:    d.GetChangedRatio(),
		MaxDelta:        int(d.GetMaxDelta()),
		BoundsX:         int(d.GetBoundsX()),
		BoundsY:         int(d.GetBoundsY()),
		BoundsWidth:     int(d.GetBoundsWidth()),
		BoundsHeight:    int(d.GetBoundsHeight()),
		SizeChanged:     d.GetSizeChanged(),
		FormatChanged:   d.GetFormatChanged(),
		BitDepthChanged: d.GetBitDepthChanged(),
		AlphaChanged:    d.GetAlphaChanged(),
	}
}
