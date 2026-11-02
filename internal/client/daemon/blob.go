package daemon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/png"
	"io"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/diff"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/preview"
)

const (
	blobChunkBytes         = 64 << 10
	defaultPreviewSize     = 2048
	maxPreviewSize         = 8192
	maxPreviewInputBytes   = 512 << 20
	maxPreviewThreshold    = 255
	previewUnsupportedCode = 415
	previewSideOld         = "old"
	previewSideNew         = "new"
)

func blobRequest(ref *daemonpb.BlobRef) usecase.BlobRequest {
	return usecase.BlobRequest{
		Source:   usecase.BlobSource(ref.GetSource()),
		Revision: ref.GetRevision(),
		Path:     ref.GetPath(),
	}
}

func blobInfo(e diff.Entry) *daemonpb.BlobInfo {
	info := &daemonpb.BlobInfo{
		Path:      e.Path,
		SizeBytes: e.SizeBytes,
		IsBinary:  e.IsBinary,
		Kind:      string(preview.Classify(e.Path, e.IsBinary)),
	}
	if e.Hash != (serverDomain.Hash{}) {
		info.Hash = e.Hash.String()
	}
	return info
}

// ReadBlob streams one file version: its info, then its decoded content.
func (s *Server) ReadBlob(req *daemonpb.ReadBlobRequest, stream daemonpb.NipaDaemon_ReadBlobServer) error {
	ctx := stream.Context()
	rp, done, err := s.acquireDiff(ctx, req.GetRoot())
	if err != nil {
		return stream.Send(blobFailureEvent(err))
	}
	defer done()

	err = rp.diff.ReadBlob(ctx, rp.root, blobRequest(req.GetBlob()), func(e diff.Entry, r io.Reader) error {
		if err := stream.Send(&daemonpb.BlobEvent{Event: &daemonpb.BlobEvent_Info{Info: blobInfo(e)}}); err != nil {
			return err
		}
		buf := make([]byte, blobChunkBytes)
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			n, rerr := io.ReadFull(r, buf)
			if n > 0 {
				data := append([]byte(nil), buf[:n]...)
				if err := stream.Send(&daemonpb.BlobEvent{Event: &daemonpb.BlobEvent_Data{Data: data}}); err != nil {
					return err
				}
			}
			if errors.Is(rerr, io.EOF) || errors.Is(rerr, io.ErrUnexpectedEOF) {
				return nil
			}
			if rerr != nil {
				return rerr
			}
		}
	})
	if err == nil {
		return nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return stream.Send(blobFailureEvent(err))
}

type decodedSide struct {
	side  string
	entry diff.Entry
	image *preview.Image
}

// Preview decodes the requested image sides, sends a downscaled PNG for each
// and, when both are present, the pixel diff statistics.
func (s *Server) Preview(req *daemonpb.PreviewRequest, stream daemonpb.NipaDaemon_PreviewServer) error {
	ctx := stream.Context()
	maxSize, threshold, err := previewOptions(req)
	if err != nil {
		return stream.Send(previewFailureEvent(err))
	}
	rp, done, err := s.acquireDiff(ctx, req.GetRoot())
	if err != nil {
		return stream.Send(previewFailureEvent(err))
	}
	defer done()

	var sides []decodedSide
	for _, ref := range []struct {
		side string
		ref  *daemonpb.BlobRef
	}{{previewSideOld, req.GetOld()}, {previewSideNew, req.GetNew()}} {
		if ref.ref == nil {
			continue
		}
		decoded, err := decodePreviewSide(ctx, rp.diff, rp.root, ref.side, ref.ref)
		if err != nil {
			return stream.Send(previewFailureEvent(err))
		}
		sides = append(sides, decoded)
	}

	for _, side := range sides {
		if err := sendPreviewSide(stream, side, maxSize); err != nil {
			return err
		}
	}
	if len(sides) == 2 {
		d := preview.CompareImages(sides[0].image, sides[1].image, threshold)
		return stream.Send(&daemonpb.PreviewEvent{Event: &daemonpb.PreviewEvent_Diff{Diff: imageDiffToPB(d)}})
	}
	return nil
}

func previewOptions(req *daemonpb.PreviewRequest) (int, uint8, error) {
	if req.GetOld() == nil && req.GetNew() == nil {
		return 0, 0, clientDomain.NewUserError("preview needs an old or a new side")
	}
	maxSize := int(req.GetMaxSize())
	switch {
	case maxSize == 0:
		maxSize = defaultPreviewSize
	case maxSize < 0 || maxSize > maxPreviewSize:
		return 0, 0, clientDomain.NewUserError(fmt.Sprintf("max_size must be between 1 and %d", maxPreviewSize))
	}
	threshold := req.GetThreshold()
	if threshold < 0 || threshold > maxPreviewThreshold {
		return 0, 0, clientDomain.NewUserError("threshold must be between 0 and 255")
	}
	return maxSize, uint8(threshold), nil
}

func decodePreviewSide(ctx context.Context, runner DiffRunner, root, side string, ref *daemonpb.BlobRef) (decodedSide, error) {
	out := decodedSide{side: side}
	err := runner.ReadBlob(ctx, root, blobRequest(ref), func(e diff.Entry, r io.Reader) error {
		out.entry = e
		if kind := preview.Classify(e.Path, true); kind != preview.KindImage {
			return unsupportedPreview(fmt.Sprintf("no preview for %s files", kind))
		}
		data, err := io.ReadAll(io.LimitReader(r, maxPreviewInputBytes+1))
		if err != nil {
			return err
		}
		if len(data) > maxPreviewInputBytes {
			return unsupportedPreview(fmt.Sprintf("%s is too large to preview", e.Path))
		}
		img, err := preview.DecodeImage(e.Path, data)
		switch {
		case errors.Is(err, preview.ErrUnsupported):
			return unsupportedPreview(fmt.Sprintf("%s: unsupported image format", e.Path))
		case errors.Is(err, preview.ErrTooLarge):
			return unsupportedPreview(fmt.Sprintf("%s is too large to preview", e.Path))
		case err != nil:
			return clientDomain.NewUserError(err.Error())
		}
		out.image = img
		return nil
	})
	return out, err
}

func sendPreviewSide(stream daemonpb.NipaDaemon_PreviewServer, side decodedSide, maxSize int) error {
	scaled := preview.Downscale(side.image.Pixels, maxSize)
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&buf, scaled); err != nil {
		return err
	}
	info := &daemonpb.ImagePreview{
		Side:          side.side,
		Width:         int32(side.image.Width()),
		Height:        int32(side.image.Height()),
		Format:        side.image.Format,
		BitDepth:      int32(side.image.BitDepth),
		HasAlpha:      side.image.HasAlpha,
		SizeBytes:     side.entry.SizeBytes,
		PreviewWidth:  int32(scaled.Rect.Dx()),
		PreviewHeight: int32(scaled.Rect.Dy()),
	}
	if err := stream.Send(&daemonpb.PreviewEvent{Event: &daemonpb.PreviewEvent_Image{Image: info}}); err != nil {
		return err
	}
	data := buf.Bytes()
	for len(data) > 0 {
		n := min(len(data), blobChunkBytes)
		chunk := &daemonpb.PreviewData{Side: side.side, Png: data[:n]}
		if err := stream.Send(&daemonpb.PreviewEvent{Event: &daemonpb.PreviewEvent_Data{Data: chunk}}); err != nil {
			return err
		}
		data = data[n:]
	}
	return nil
}

func imageDiffToPB(d preview.ImageDiff) *daemonpb.ImageDiffStats {
	return &daemonpb.ImageDiffStats{
		ChangedPixels:   int64(d.ChangedPixels),
		TotalPixels:     int64(d.TotalPixels),
		ChangedRatio:    d.ChangedRatio,
		MaxDelta:        int32(d.MaxDelta),
		BoundsX:         int32(d.Bounds.Min.X),
		BoundsY:         int32(d.Bounds.Min.Y),
		BoundsWidth:     int32(d.Bounds.Dx()),
		BoundsHeight:    int32(d.Bounds.Dy()),
		SizeChanged:     d.SizeChanged,
		FormatChanged:   d.FormatChanged,
		BitDepthChanged: d.BitDepthChanged,
		AlphaChanged:    d.AlphaChanged,
	}
}

func unsupportedPreview(message string) error {
	return &clientDomain.Error{Code: previewUnsupportedCode, Message: message}
}

func blobFailureEvent(err error) *daemonpb.BlobEvent {
	return &daemonpb.BlobEvent{Event: &daemonpb.BlobEvent_Failure{
		Failure: &daemonpb.OpFailure{Code: failureCode(err), Message: err.Error()},
	}}
}

func previewFailureEvent(err error) *daemonpb.PreviewEvent {
	return &daemonpb.PreviewEvent{Event: &daemonpb.PreviewEvent_Failure{
		Failure: &daemonpb.OpFailure{Code: failureCode(err), Message: err.Error()},
	}}
}
