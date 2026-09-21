package server

import (
	"bytes"
	"context"
	"sort"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/usecase"
)

// GetChunkUploadUrls returns signed upload paths for one page of chunk refs.
// The caller needs project write access; chunks already stored come back with
// AlreadyStored set so clients skip the transfer.
func (n *nipaServer) GetChunkUploadUrls(ctx context.Context, req *pb.GetChunkUploadUrlsRequest) (*pb.GetChunkUploadUrlsResponse, error) {
	org, project, err := n.resolveChunkProject(ctx, req.GetContext())
	if err != nil {
		return nil, err
	}
	if err := n.uc.Branch().EnsureProjectAccess(ctx, project.ID, domain.PermissionWrite); err != nil {
		return nil, handleError(err)
	}
	refs, err := toChunkRefs(req.GetChunks())
	if err != nil {
		return nil, handleError(err)
	}
	urls, next, err := n.uc.Chunk().PresignUploadURLs(ctx, org.Slug, project.Slug, refs, int(req.GetPageSize()), req.GetPageToken())
	if err != nil {
		return nil, handleError(err)
	}
	return &pb.GetChunkUploadUrlsResponse{Urls: toPBChunkURLs(urls), NextPageToken: next}, nil
}

// GetChunkDownloadUrls returns signed download paths for one page of chunk
// hashes the caller may read. Hashes outside the visible trees are omitted.
func (n *nipaServer) GetChunkDownloadUrls(ctx context.Context, req *pb.GetChunkDownloadUrlsRequest) (*pb.GetChunkDownloadUrlsResponse, error) {
	org, project, err := n.resolveChunkProject(ctx, req.GetContext())
	if err != nil {
		return nil, err
	}
	allowed, err := n.uc.Branch().VisibleChunks(ctx, project.ID, req.GetCommitIds(), req.GetPaths())
	if err != nil {
		return nil, handleError(err)
	}
	hashes := make([]domain.Hash, 0, len(req.GetHashes()))
	for _, raw := range req.GetHashes() {
		hash, err := domain.ParseHashHex(raw)
		if err != nil {
			return nil, handleError(domain.NewErrorUser("invalid chunk hash"))
		}
		if hashInSet(allowed, hash) {
			hashes = append(hashes, hash)
		}
	}
	urls, next, err := n.uc.Chunk().PresignDownloadURLs(org.Slug, project.Slug, hashes, int(req.GetPageSize()), req.GetPageToken())
	if err != nil {
		return nil, handleError(err)
	}
	return &pb.GetChunkDownloadUrlsResponse{Urls: toPBChunkURLs(urls), NextPageToken: next}, nil
}

// ConfirmChunkUploads verifies uploaded content landed in the store and
// records chunk metadata. Hashes still missing are returned for retry.
func (n *nipaServer) ConfirmChunkUploads(ctx context.Context, req *pb.ConfirmChunkUploadsRequest) (*pb.ConfirmChunkUploadsResponse, error) {
	_, project, err := n.resolveChunkProject(ctx, req.GetContext())
	if err != nil {
		return nil, err
	}
	if err := n.uc.Branch().EnsureProjectAccess(ctx, project.ID, domain.PermissionWrite); err != nil {
		return nil, handleError(err)
	}
	refs, err := toChunkRefs(req.GetChunks())
	if err != nil {
		return nil, handleError(err)
	}
	missing, err := n.uc.Chunk().ConfirmUploads(ctx, refs)
	if err != nil {
		return nil, handleError(err)
	}
	missingHashes := make([]string, 0, len(missing))
	for _, hash := range missing {
		missingHashes = append(missingHashes, hash.String())
	}
	return &pb.ConfirmChunkUploadsResponse{MissingHashes: missingHashes}, nil
}

func (n *nipaServer) resolveChunkProject(ctx context.Context, projectCtx *pb.ProjectContext) (*domain.Organization, *domain.Project, error) {
	if projectCtx == nil {
		return nil, nil, handleError(domain.NewErrorUser("project context is required"))
	}
	org, project, err := n.uc.Common().ResolveBySlug(ctx, projectCtx.GetOrg(), projectCtx.GetProject())
	if err != nil {
		return nil, nil, handleError(err)
	}
	return org, project, nil
}

func toChunkRefs(chunks []*pb.ChunkRef) ([]usecase.ChunkRef, error) {
	refs := make([]usecase.ChunkRef, 0, len(chunks))
	for _, chunk := range chunks {
		hash, err := domain.ParseHashHex(chunk.GetHash())
		if err != nil {
			return nil, domain.NewErrorUser("invalid chunk hash")
		}
		refs = append(refs, usecase.ChunkRef{Hash: hash, SizeBytes: chunk.GetSizeBytes()})
	}
	return refs, nil
}

func toPBChunkURLs(urls []usecase.ChunkURL) []*pb.PresignedChunkUrl {
	out := make([]*pb.PresignedChunkUrl, 0, len(urls))
	for _, u := range urls {
		out = append(out, &pb.PresignedChunkUrl{
			Hash:          u.Hash.String(),
			Url:           u.URL,
			AlreadyStored: u.AlreadyStored,
		})
	}
	return out
}

func hashInSet(hashes []domain.Hash, hash domain.Hash) bool {
	index := sort.Search(len(hashes), func(i int) bool {
		return bytes.Compare(hashes[i][:], hash[:]) >= 0
	})
	return index < len(hashes) && hashes[index] == hash
}
