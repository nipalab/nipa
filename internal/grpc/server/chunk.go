package server

import (
	"io"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
)

// UploadChunks accepts a client streaming chunk content. Each chunk is
// verified by BLAKE3 hash server-side and stored idempotently; the response
// reports how many chunks were newly stored versus already present.
func (n *nipaServer) UploadChunks(stream pb.NipaService_UploadChunksServer) error {
	var uploaded, skipped int32
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		hash, err := domain.ParseHashHex(req.Hash)
		if err != nil {
			return handleError(domain.NewErrorUser("invalid chunk hash"))
		}
		isNew, err := n.uc.Chunk().Upload(stream.Context(), hash, req.Data)
		if err != nil {
			return handleError(err)
		}
		if isNew {
			uploaded++
		} else {
			skipped++
		}
	}
	return stream.SendAndClose(&pb.UploadChunksResponse{
		Uploaded: uploaded,
		Skipped:  skipped,
	})
}

// DownloadChunks streams back the stored content for each requested hash.
// Missing content aborts the stream with a NotFound error.
func (n *nipaServer) DownloadChunks(stream pb.NipaService_DownloadChunksServer) error {
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		hash, err := domain.ParseHashHex(req.Hash)
		if err != nil {
			return handleError(domain.NewErrorUser("invalid chunk hash"))
		}
		data, err := n.uc.Chunk().Download(stream.Context(), hash)
		if err != nil {
			return handleError(err)
		}
		if err := stream.Send(&pb.DownloadChunk{Hash: req.Hash, Data: data}); err != nil {
			return err
		}
	}
}
