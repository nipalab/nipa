package server

import (
	"bytes"
	"context"
	"fmt"
	"sort"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/chunkproof"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/usecase"
)

// checkChunkProtocol refuses a client that speaks an older chunk transfer
// protocol (chunkproof.Protocol): it would send possession proofs this server
// no longer reads, expect global dedup or PUT single chunks to a route this
// server no longer has.
func checkChunkProtocol(version uint32) error {
	if version >= chunkproof.Protocol {
		return nil
	}
	return status.Error(codes.FailedPrecondition, fmt.Sprintf(
		"this server speaks chunk protocol %d and the client %d; upgrade nipa to push and clone", chunkproof.Protocol, version))
}

// GetChunkUploadUrls decides one page of chunk refs: chunks the project
// already holds come back with AlreadyStored set so clients skip the
// transfer; the others get a direct presigned URL each, or, when the
// project's store takes uploads through the server, one signed bundle URL for
// the page. The caller needs project write access.
func (n *nipaServer) GetChunkUploadUrls(ctx context.Context, req *pb.GetChunkUploadUrlsRequest) (*pb.GetChunkUploadUrlsResponse, error) {
	if err := checkChunkProtocol(req.GetChunkProtocol()); err != nil {
		return nil, err
	}
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
	presign, err := n.uc.Chunk().PresignUploadURLs(ctx, org, project, refs, int(req.GetPageSize()), req.GetPageToken())
	if err != nil {
		return nil, handleError(err)
	}
	return &pb.GetChunkUploadUrlsResponse{
		Urls:            toPBChunkURLs(presign.URLs),
		NextPageToken:   presign.NextPageToken,
		ChunkProtocol:   chunkproof.Protocol,
		BundleUrl:       presign.BundleURL,
		BundleMaxBytes:  presign.BundleMaxBytes,
		BundleMaxChunks: int32(presign.BundleMaxChunks),
	}, nil
}

// GetChunkDownloadUrls returns signed download paths for one page of chunk
// hashes the caller may read. Hashes outside the visible trees are omitted.
func (n *nipaServer) GetChunkDownloadUrls(ctx context.Context, req *pb.GetChunkDownloadUrlsRequest) (*pb.GetChunkDownloadUrlsResponse, error) {
	if err := checkChunkProtocol(req.GetChunkProtocol()); err != nil {
		return nil, err
	}
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
	urls, next, err := n.uc.Chunk().PresignDownloadURLs(ctx, org, project, hashes, int(req.GetPageSize()), req.GetPageToken())
	if err != nil {
		return nil, handleError(err)
	}
	return &pb.GetChunkDownloadUrlsResponse{Urls: toPBChunkURLs(urls), NextPageToken: next, ChunkProtocol: chunkproof.Protocol}, nil
}

// ConfirmChunkUploads verifies uploaded content landed in the project's store
// and records the project's chunk rows. A valid receipt (bundle upload)
// stands in for the read-back; other chunks are read back and hashed. The
// recorded size is measured server-side; hashes still missing are returned
// for retry.
func (n *nipaServer) ConfirmChunkUploads(ctx context.Context, req *pb.ConfirmChunkUploadsRequest) (*pb.ConfirmChunkUploadsResponse, error) {
	if err := checkChunkProtocol(req.GetChunkProtocol()); err != nil {
		return nil, err
	}
	org, project, err := n.resolveChunkProject(ctx, req.GetContext())
	if err != nil {
		return nil, err
	}
	if err := n.uc.Branch().EnsureProjectAccess(ctx, project.ID, domain.PermissionWrite); err != nil {
		return nil, handleError(err)
	}
	hashes, err := toChunkHashes(req.GetHashes())
	if err != nil {
		return nil, handleError(err)
	}
	receipts, err := toChunkReceipts(req.GetReceipts())
	if err != nil {
		return nil, handleError(err)
	}
	missing, err := n.uc.Chunk().ConfirmUploads(ctx, org, project, hashes, receipts)
	if err != nil {
		return nil, handleError(err)
	}
	missingHashes := make([]string, 0, len(missing))
	for _, hash := range missing {
		missingHashes = append(missingHashes, hash.String())
	}
	return &pb.ConfirmChunkUploadsResponse{MissingHashes: missingHashes, ChunkProtocol: chunkproof.Protocol}, nil
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

func toChunkHashes(raw []string) ([]domain.Hash, error) {
	hashes := make([]domain.Hash, 0, len(raw))
	for _, item := range raw {
		hash, err := domain.ParseHashHex(item)
		if err != nil {
			return nil, domain.NewErrorUser("invalid chunk hash")
		}
		hashes = append(hashes, hash)
	}
	return hashes, nil
}

func toChunkReceipts(raw []*pb.ChunkReceipt) ([]usecase.ChunkReceipt, error) {
	receipts := make([]usecase.ChunkReceipt, 0, len(raw))
	for _, item := range raw {
		hash, err := domain.ParseHashHex(item.GetHash())
		if err != nil {
			return nil, domain.NewErrorUser("invalid chunk hash")
		}
		receipts = append(receipts, usecase.ChunkReceipt{Hash: hash, Receipt: item.GetReceipt()})
	}
	return receipts, nil
}

func toPBChunkURLs(urls []usecase.ChunkURL) []*pb.PresignedChunkUrl {
	out := make([]*pb.PresignedChunkUrl, 0, len(urls))
	for _, u := range urls {
		out = append(out, &pb.PresignedChunkUrl{
			Hash:          u.Hash.String(),
			Url:           u.URL,
			AlreadyStored: u.AlreadyStored,
			Headers:       u.Headers,
			Offset:        u.Offset,
			Length:        u.Length,
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
