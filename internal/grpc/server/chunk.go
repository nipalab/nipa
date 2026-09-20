package server

import (
	"bytes"
	"context"
	"io"
	"sort"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
)

// UploadChunks accepts a client streaming chunk content. The first message
// must carry the project context and the caller needs write access; each chunk
// is verified by BLAKE3 hash server-side and stored idempotently.
func (n *nipaServer) UploadChunks(stream pb.NipaService_UploadChunksServer) error {
	var (
		uploaded, skipped int32
		authorized        bool
	)
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if !authorized {
			project, err := n.resolveChunkProject(stream.Context(), req.GetContext())
			if err != nil {
				return err
			}
			if err := n.uc.Branch().EnsureProjectAccess(stream.Context(), project.ID, domain.PermissionWrite); err != nil {
				return handleError(err)
			}
			authorized = true
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

// DownloadChunks streams back the stored content for each requested hash. The
// first message must carry the project context and the commit scope; a hash is
// only served when it belongs to a file the caller may read. Missing or
// invisible content aborts the stream with a NotFound error.
func (n *nipaServer) DownloadChunks(stream pb.NipaService_DownloadChunksServer) error {
	var (
		allowed     []domain.Hash
		initialized bool
	)
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if !initialized {
			project, err := n.resolveChunkProject(stream.Context(), req.GetContext())
			if err != nil {
				return err
			}
			allowed, err = n.uc.Branch().VisibleChunks(stream.Context(), project.ID, req.GetCommitIds(), req.GetPaths())
			if err != nil {
				return handleError(err)
			}
			initialized = true
		}
		hash, err := domain.ParseHashHex(req.Hash)
		if err != nil {
			return handleError(domain.NewErrorUser("invalid chunk hash"))
		}
		if !hashInSet(allowed, hash) {
			return handleError(domain.NewErrorNotFound("chunk not found"))
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

func (n *nipaServer) resolveChunkProject(ctx context.Context, projectCtx *pb.ProjectContext) (*domain.Project, error) {
	if projectCtx == nil {
		return nil, handleError(domain.NewErrorUser("project context is required"))
	}
	_, project, err := n.uc.Common().ResolveBySlug(ctx, projectCtx.GetOrg(), projectCtx.GetProject())
	if err != nil {
		return nil, handleError(err)
	}
	return project, nil
}

func hashInSet(hashes []domain.Hash, hash domain.Hash) bool {
	index := sort.Search(len(hashes), func(i int) bool {
		return bytes.Compare(hashes[i][:], hash[:]) >= 0
	})
	return index < len(hashes) && hashes[index] == hash
}
