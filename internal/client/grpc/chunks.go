package grpc

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"

	"github.com/nipalab/nipa/internal/chunkproof"
	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	pb "github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/obs"
)

// maxUploadAttempts bounds how often chunks the server could not confirm are
// presigned and sent again, e.g. after it deleted garbage planted on their key.
const maxUploadAttempts = 3

// UploadChunks stores chunk content on the server. Chunks the project is
// already linked to are skipped. For the rest, the client first proves it holds
// the bytes (the server links content it already has, with no transfer) and
// only sends what is not stored yet to the signed upload URLs. Every upload is
// confirmed with a fresh proof so the server records chunk metadata and links
// the project.
func (c *Client) UploadChunks(ctx context.Context, scope domain.ChunkScope, chunks []*serverDomain.ChunkData, onChunk ...func(ch *serverDomain.ChunkData)) (int, int, error) {
	if len(chunks) == 0 {
		return 0, 0, nil
	}
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return 0, 0, err
	}
	authedCtx, err := c.authedContext(ctx)
	if err != nil {
		return 0, 0, err
	}

	uploaded := map[serverDomain.Hash]struct{}{}
	skipped := map[serverDomain.Hash]struct{}{}
	remaining := chunks
	for attempt := 0; attempt < maxUploadAttempts && len(remaining) > 0; attempt++ {
		remaining, err = c.uploadAttempt(authedCtx, client, scope, remaining, uploaded, skipped)
		if err != nil {
			return 0, 0, err
		}
	}
	if len(remaining) > 0 {
		return 0, 0, domain.NewUserError(fmt.Sprintf("upload incomplete: %d chunk(s) missing on the server", len(remaining)))
	}

	if len(onChunk) > 0 && onChunk[0] != nil {
		for _, chunk := range chunks {
			onChunk[0](chunk)
		}
	}
	return len(uploaded), len(skipped), nil
}

// uploadAttempt presigns, claims, uploads and confirms the given chunks once.
// It returns the chunks that still need another attempt.
func (c *Client) uploadAttempt(ctx context.Context, client pb.NipaServiceClient, scope domain.ChunkScope, chunks []*serverDomain.ChunkData, uploaded, skipped map[serverDomain.Hash]struct{}) ([]*serverDomain.ChunkData, error) {
	refs := make([]*pb.ChunkRef, 0, len(chunks))
	dataByHash := make(map[serverDomain.Hash]*serverDomain.ChunkData, len(chunks))
	for _, chunk := range chunks {
		refs = append(refs, &pb.ChunkRef{Hash: chunk.Hash.String(), SizeBytes: int64(len(chunk.Data))})
		dataByHash[chunk.Hash] = chunk
	}

	retry := map[serverDomain.Hash]struct{}{}
	var uploadedProofs []*pb.ChunkPossessionProof
	confirmHashes := make([]string, 0, len(chunks))
	pageToken := ""
	for {
		res, err := client.GetChunkUploadUrls(ctx, &pb.GetChunkUploadUrlsRequest{
			Context:   &pb.ProjectContext{Org: scope.Org, Project: scope.Project},
			Chunks:    refs,
			PageSize:  int32(len(refs)),
			PageToken: pageToken,
		})
		if err != nil {
			return nil, toDomainError(err)
		}

		var pending []chunkTransfer
		var pendingProofs []*pb.ChunkPossessionProof
		for _, u := range res.GetUrls() {
			hash, err := decodeHash(u.GetHash())
			if err != nil {
				return nil, err
			}
			chunk, ok := dataByHash[hash]
			if !ok {
				return nil, domain.NewUserError(fmt.Sprintf("server requested unknown chunk %s", hash))
			}
			confirmHashes = append(confirmHashes, hash.String())
			if u.GetAlreadyStored() {
				skipped[hash] = struct{}{}
				continue
			}
			pending = append(pending, chunkTransfer{hash: hash, url: u.GetUrl(), headers: u.GetHeaders(), data: chunk.Data})
			pendingProofs = append(pendingProofs, &pb.ChunkPossessionProof{
				Hash:      hash.String(),
				Challenge: u.GetProofChallenge(),
				Proof:     chunkproof.Proof(u.GetProofChallenge(), chunk.Data),
			})
		}

		notProven, err := c.confirmChunks(ctx, client, scope, hashStrings(pending), pendingProofs)
		if err != nil {
			return nil, err
		}
		jobs := make([]chunkTransfer, 0, len(notProven))
		for i, job := range pending {
			if _, absent := notProven[job.hash.String()]; !absent {
				skipped[job.hash] = struct{}{}
				continue
			}
			if job.url == "" {
				retry[job.hash] = struct{}{}
				continue
			}
			jobs = append(jobs, job)
			uploadedProofs = append(uploadedProofs, pendingProofs[i])
		}

		if err := c.putChunks(ctx, jobs); err != nil {
			return nil, err
		}
		for _, job := range jobs {
			uploaded[job.hash] = struct{}{}
			delete(skipped, job.hash)
		}

		pageToken = res.GetNextPageToken()
		if pageToken == "" {
			break
		}
	}

	notConfirmed, err := c.confirmChunks(ctx, client, scope, confirmHashes, uploadedProofs)
	if err != nil {
		return nil, err
	}
	var remaining []*serverDomain.ChunkData
	for _, chunk := range chunks {
		_, deferred := retry[chunk.Hash]
		_, failed := notConfirmed[chunk.Hash.String()]
		if deferred || failed {
			delete(uploaded, chunk.Hash)
			delete(skipped, chunk.Hash)
			remaining = append(remaining, chunk)
		}
	}
	return remaining, nil
}

// confirmChunks sends hashes with their possession proofs and returns the set
// of hashes the server could not confirm. Confirmations are serialized so
// concurrent upload windows never stack SQLite metadata writes.
func (c *Client) confirmChunks(ctx context.Context, client pb.NipaServiceClient, scope domain.ChunkScope, hashes []string, proofs []*pb.ChunkPossessionProof) (map[string]struct{}, error) {
	if len(hashes) == 0 {
		return nil, nil
	}
	c.confirmMu.Lock()
	res, err := client.ConfirmChunkUploads(ctx, &pb.ConfirmChunkUploadsRequest{
		Context: &pb.ProjectContext{Org: scope.Org, Project: scope.Project},
		Hashes:  hashes,
		Proofs:  proofs,
	})
	c.confirmMu.Unlock()
	if err != nil {
		return nil, toDomainError(err)
	}
	missing := make(map[string]struct{}, len(res.GetMissingHashes()))
	for _, hash := range res.GetMissingHashes() {
		missing[hash] = struct{}{}
	}
	return missing, nil
}

func hashStrings(jobs []chunkTransfer) []string {
	hashes := make([]string, 0, len(jobs))
	for _, job := range jobs {
		hashes = append(hashes, job.hash.String())
	}
	return hashes
}

// DownloadChunks fetches chunk content through signed download URLs. Callers
// receive each chunk through onChunk and are responsible for hash verification.
func (c *Client) DownloadChunks(ctx context.Context, scope domain.ChunkScope, hashes []serverDomain.Hash, onChunk func(h serverDomain.Hash, data []byte) error) error {
	if len(hashes) == 0 {
		return nil
	}
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return err
	}
	authedCtx, err := c.authedContext(ctx)
	if err != nil {
		return err
	}

	rawHashes := make([]string, 0, len(hashes))
	for _, hash := range hashes {
		rawHashes = append(rawHashes, hash.String())
	}

	jobs := make([]chunkTransfer, 0, len(hashes))
	found := make(map[serverDomain.Hash]struct{}, len(hashes))
	pageToken := ""
	for {
		res, err := client.GetChunkDownloadUrls(authedCtx, &pb.GetChunkDownloadUrlsRequest{
			Context:   &pb.ProjectContext{Org: scope.Org, Project: scope.Project},
			CommitIds: scope.CommitIDs,
			Paths:     scope.Paths,
			Hashes:    rawHashes,
			PageSize:  int32(len(rawHashes)),
			PageToken: pageToken,
		})
		if err != nil {
			return toDomainError(err)
		}
		for _, u := range res.GetUrls() {
			hash, err := decodeHash(u.GetHash())
			if err != nil {
				return err
			}
			found[hash] = struct{}{}
			jobs = append(jobs, chunkTransfer{hash: hash, url: u.GetUrl()})
		}
		pageToken = res.GetNextPageToken()
		if pageToken == "" {
			break
		}
	}

	for _, hash := range hashes {
		if _, ok := found[hash]; !ok {
			return domain.NewNotFoundError(fmt.Sprintf("chunk %s not found", hash))
		}
	}

	return c.getChunks(authedCtx, jobs, onChunk)
}

type chunkTransfer struct {
	hash    serverDomain.Hash
	url     string
	headers map[string]string
	data    []byte
}

func (c *Client) putChunks(ctx context.Context, jobs []chunkTransfer) error {
	return c.forEachChunk(ctx, jobs, c.putChunk)
}

// putChunk sends one chunk with a PUT. A direct backend's create-only target
// answers 412 when the content was stored in the meantime; that is not a
// failure, the confirmation that follows decides whether the bytes match.
func (c *Client) putChunk(ctx context.Context, job chunkTransfer) (size int64, err error) {
	ctx, span := obs.StartSpan(ctx, "chunk.upload.put",
		attribute.String(obs.AttrChunkHash, job.hash.String()),
		attribute.Int(obs.AttrChunkSize, len(job.data)))
	defer func() { obs.FinishSpan(span, err) }()

	request, err := http.NewRequestWithContext(ctx, http.MethodPut, c.httpURL(job.url), bytes.NewReader(job.data))
	if err != nil {
		return 0, err
	}
	for name, value := range job.headers {
		request.Header.Set(name, value)
	}
	injectTraceContext(ctx, request.Header)
	res, err := c.http.Do(request)
	if err != nil {
		return 0, err
	}
	defer func() { _ = res.Body.Close() }()
	span.SetAttributes(attribute.Int("http.response.status_code", res.StatusCode))
	switch res.StatusCode {
	case http.StatusNoContent, http.StatusOK, http.StatusPreconditionFailed:
		return int64(len(job.data)), nil
	}
	return int64(len(job.data)), chunkHTTPError(res)
}

// chunkSink serializes chunk delivery to the caller's callback and remembers
// the first failure so no further chunks are delivered after it.
type chunkSink struct {
	mu  sync.Mutex
	err error
	fn  func(h serverDomain.Hash, data []byte) error
}

func (s *chunkSink) deliver(hash serverDomain.Hash, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	if err := s.fn(hash, data); err != nil {
		s.err = err
		return err
	}
	return nil
}

func (c *Client) getChunks(ctx context.Context, jobs []chunkTransfer, onChunk func(h serverDomain.Hash, data []byte) error) error {
	sink := &chunkSink{fn: onChunk}
	return c.forEachChunk(ctx, jobs, func(ctx context.Context, job chunkTransfer) (size int64, err error) {
		ctx, span := obs.StartSpan(ctx, "chunk.download.get",
			attribute.String(obs.AttrChunkHash, job.hash.String()))
		defer func() { obs.FinishSpan(span, err) }()

		request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.httpURL(job.url), nil)
		if err != nil {
			return 0, err
		}
		injectTraceContext(ctx, request.Header)
		res, err := c.http.Do(request)
		if err != nil {
			return 0, err
		}
		defer func() { _ = res.Body.Close() }()
		span.SetAttributes(attribute.Int("http.response.status_code", res.StatusCode))
		if res.StatusCode != http.StatusOK {
			return 0, chunkHTTPError(res)
		}
		data, err := io.ReadAll(res.Body)
		if err != nil {
			return 0, err
		}
		span.SetAttributes(attribute.Int(obs.AttrChunkSize, len(data)))
		if err := sink.deliver(job.hash, data); err != nil {
			return int64(len(data)), err
		}
		return int64(len(data)), nil
	})
}

// injectTraceContext propagates the current span to the receiving HTTP
// endpoint; a no-op when tracing is disabled.
func injectTraceContext(ctx context.Context, header http.Header) {
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(header))
}

// forEachChunk runs fn for every job with the client's adaptive transfer limit
// and feeds the observed throughput back into the concurrency tuner.
func (c *Client) forEachChunk(ctx context.Context, jobs []chunkTransfer, fn func(ctx context.Context, job chunkTransfer) (int64, error)) error {
	if len(jobs) == 0 {
		return nil
	}
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	work := make(chan chunkTransfer)
	var (
		wg       sync.WaitGroup
		once     sync.Once
		firstErr error
	)
	for i := 0; i < maxUploadWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range work {
				if err := c.limiter.acquire(workCtx); err != nil {
					once.Do(func() {
						firstErr = err
						cancel()
					})
					return
				}
				bytes, err := fn(workCtx, job)
				c.limiter.release()
				c.recordTransfer(bytes, err)
				if err != nil {
					once.Do(func() {
						firstErr = err
						cancel()
					})
					return
				}
			}
		}()
	}
	for _, job := range jobs {
		select {
		case work <- job:
		case <-workCtx.Done():
		}
	}
	close(work)
	wg.Wait()
	return firstErr
}

func (c *Client) httpURL(path string) string {
	if strings.Contains(path, "://") {
		return path
	}
	base := c.transport.url
	if base == "" {
		return path
	}
	// The transport address keeps the scheme when the client connected over
	// TLS; a bare host falls back to cleartext, matching Connect.
	if !strings.Contains(base, "://") {
		base = "http://" + base
	}
	return strings.TrimRight(base, "/") + path
}

func chunkHTTPError(res *http.Response) error {
	if res.StatusCode == http.StatusNotFound {
		return domain.NewNotFoundError("chunk not found on the server")
	}
	if res.StatusCode == http.StatusForbidden || res.StatusCode == http.StatusUnauthorized {
		return domain.NewUserError("chunk transfer URL is invalid or expired")
	}
	return domain.NewUserError(fmt.Sprintf("chunk transfer failed with status %d", res.StatusCode))
}
