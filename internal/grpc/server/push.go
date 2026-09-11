package server

import (
	"context"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
)

func (n *nipaServer) Push(ctx context.Context, req *pb.PushRequest) (*pb.PushResponse, error) {
	if req.Message == "" {
		return nil, handleError(domain.NewErrorUser("commit message is required"))
	}
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}

	files := make([]*domain.PushFile, 0, len(req.Files))
	for _, pf := range req.Files {
		fileHash, err := domain.ParseHashHex(pf.FileHash)
		if err != nil {
			return nil, handleError(domain.NewErrorUser("invalid file hash for " + pf.Path))
		}
		chunkHashes := make([]domain.Hash, 0, len(pf.ChunkHashes))
		for _, h := range pf.ChunkHashes {
			ch, err := domain.ParseHashHex(h)
			if err != nil {
				return nil, handleError(domain.NewErrorUser("invalid chunk hash for " + pf.Path))
			}
			chunkHashes = append(chunkHashes, ch)
		}
		files = append(files, &domain.PushFile{
			Path:        pf.Path,
			Mode:        pbFileModeToInt(pf.Mode),
			SizeBytes:   pf.SizeBytes,
			IsBinary:    pf.IsBinary,
			FileHash:    fileHash,
			ChunkHashes: chunkHashes,
		})
	}

	result, err := n.uc.Push().Push(ctx, project.ID, req.Branch, req.BaseTreeHash, req.Message, files, req.RemovedFiles)
	if err != nil {
		return nil, handleError(err)
	}
	return &pb.PushResponse{
		CommitId:   result.CommitID.Base36(),
		CommitHash: result.CommitHash.String(),
		TreeHash:   result.TreeHash.String(),
	}, nil
}

func pbFileModeToInt(m pb.FileMode) int {
	switch m {
	case pb.FileMode_FILE_MODE_READ_ONLY:
		return 0o444
	case pb.FileMode_FILE_MODE_EXECUTABLE:
		return 0o755
	case pb.FileMode_FILE_MODE_READ_WRITE, pb.FileMode_FILE_MODE_UNSPECIFIED:
		return 0o644
	default:
		return 0o644
	}
}
