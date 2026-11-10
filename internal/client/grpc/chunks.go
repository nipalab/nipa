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
	domain.NotifyVerify(ctx, len(hashes))
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

// downloadWindow is how many chunks are presigned per round of download URLs.
// URLs are requested a window ahead of the transfers instead of all at once, so
// a long clone never holds URLs that outlive their lifetime, and it matches the
// server's default page size so no extra requests are made.
const downloadWindow = 1000

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

	type urlWindow struct {
		jobs []chunkTransfer
		err  error
	}
	sink := &chunkSink{fn: onChunk}
	return c.runChunks(authedCtx, func(ctx context.Context, emit func(chunkTransfer) bool) error {
		fetch := func(start int) <-chan urlWindow {
			out := make(chan urlWindow, 1)
			end := min(start+downloadWindow, len(hashes))
			go func() {
				jobs, err := c.downloadURLs(ctx, client, scope, hashes[start:end])
				out <- urlWindow{jobs: jobs, err: err}
			}()
			return out
		}
		next := fetch(0)
		for start := 0; start < len(hashes); start += downloadWindow {
			window := <-next
			if window.err != nil {
				return window.err
			}
			if start+downloadWindow < len(hashes) {
				next = fetch(start + downloadWindow)
			}
			for _, job := range window.jobs {
				if !emit(job) {
					return nil
				}
			}
		}
		return nil
	}, func(ctx context.Context, job chunkTransfer) (int64, error) {
		return c.downloadChunk(ctx, sink, job)
	})
}

// downloadURLs presigns one window of hashes, following the server's pages, and
// fails when the server does not know one of them.
func (c *Client) downloadURLs(ctx context.Context, client pb.NipaServiceClient, scope domain.ChunkScope, hashes []serverDomain.Hash) ([]chunkTransfer, error) {
	rawHashes := make([]string, 0, len(hashes))
	for _, hash := range hashes {
		rawHashes = append(rawHashes, hash.String())
	}

	jobs := make([]chunkTransfer, 0, len(hashes))
	found := make(map[serverDomain.Hash]struct{}, len(hashes))
	pageToken := ""
	for {
		res, err := client.GetChunkDownloadUrls(ctx, &pb.GetChunkDownloadUrlsRequest{
			Context:   &pb.ProjectContext{Org: scope.Org, Project: scope.Project},
			CommitIds: scope.CommitIDs,
			Paths:     scope.Paths,
			Hashes:    rawHashes,
			PageSize:  int32(len(rawHashes)),
			PageToken: pageToken,
		})
		if err != nil {
			return nil, toDomainError(err)
		}
		for _, u := range res.GetUrls() {
			hash, err := decodeHash(u.GetHash())
			if err != nil {
				return nil, err
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
			return nil, domain.NewNotFoundError(fmt.Sprintf("chunk %s not found", hash))
		}
	}
	return jobs, nil
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

// putChunk sends one chunk with a PUT, retrying transient failures and
// throttling. A direct backend's create-only target answers 412 when the
// content was stored in the meantime; that is not a failure, the confirmation
// that follows decides whether the bytes match.
func (c *Client) putChunk(ctx context.Context, job chunkTransfer) (int64, error) {
	err := c.retryTransfer(ctx, func() (retryHint, error) {
		return c.putChunkOnce(ctx, job)
	})
	return int64(len(job.data)), err
}

func (c *Client) putChunkOnce(ctx context.Context, job chunkTransfer) (hint retryHint, err error) {
	ctx, span := obs.StartSpan(ctx, "chunk.upload.put",
		attribute.String(obs.AttrChunkHash, job.hash.String()),
		attribute.Int(obs.AttrChunkSize, len(job.data)))
	defer func() { obs.FinishSpan(span, err) }()

	request, err := http.NewRequestWithContext(ctx, http.MethodPut, c.httpURL(job.url), bytes.NewReader(job.data))
	if err != nil {
		return retryHint{}, err
	}
	for name, value := range job.headers {
		request.Header.Set(name, value)
	}
	injectTraceContext(ctx, request.Header)
	res, err := c.http.Do(request)
	if err != nil {
		return retryHint{retryable: ctx.Err() == nil}, err
	}
	defer func() { _ = res.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
	span.SetAttributes(attribute.Int("http.response.status_code", res.StatusCode))
	switch res.StatusCode {
	case http.StatusNoContent, http.StatusOK, http.StatusPreconditionFailed:
		return retryHint{}, nil
	}
	return httpRetryHint(res), chunkHTTPError(res)
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
	return c.forEachChunk(ctx, jobs, func(ctx context.Context, job chunkTransfer) (int64, error) {
		return c.downloadChunk(ctx, sink, job)
	})
}

// downloadChunk fetches one chunk and hands it to the sink. Transient failures
// (network errors, truncated bodies, 5xx, 408) and throttling (429) are retried;
// anything else, such as an expired or unknown URL, fails right away. The sink
// only sees a chunk after a complete, successful read, so a retry can never
// deliver one twice.
func (c *Client) downloadChunk(ctx context.Context, sink *chunkSink, job chunkTransfer) (int64, error) {
	var data []byte
	err := c.retryTransfer(ctx, func() (retryHint, error) {
		var (
			hint retryHint
			err  error
		)
		data, hint, err = c.fetchChunk(ctx, job)
		return hint, err
	})
	if err != nil {
		return 0, err
	}
	return int64(len(data)), sink.deliver(job.hash, data)
}

// fetchChunk downloads one chunk once; the hint says whether trying again
// could succeed. A cancelled context never is.
func (c *Client) fetchChunk(ctx context.Context, job chunkTransfer) (data []byte, hint retryHint, err error) {
	ctx, span := obs.StartSpan(ctx, "chunk.download.get",
		attribute.String(obs.AttrChunkHash, job.hash.String()))
	defer func() { obs.FinishSpan(span, err) }()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.httpURL(job.url), nil)
	if err != nil {
		return nil, retryHint{}, err
	}
	injectTraceContext(ctx, request.Header)
	res, err := c.http.Do(request)
	if err != nil {
		return nil, retryHint{retryable: ctx.Err() == nil}, err
	}
	defer func() { _ = res.Body.Close() }()
	span.SetAttributes(attribute.Int("http.response.status_code", res.StatusCode))
	if res.StatusCode != http.StatusOK {
		return nil, httpRetryHint(res), chunkHTTPError(res)
	}
	data, err = io.ReadAll(res.Body)
	if err != nil {
		return nil, retryHint{retryable: ctx.Err() == nil}, err
	}
	span.SetAttributes(attribute.Int(obs.AttrChunkSize, len(data)))
	return data, retryHint{}, nil
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
	return c.runChunks(ctx, func(_ context.Context, emit func(chunkTransfer) bool) error {
		for _, job := range jobs {
			if !emit(job) {
				return nil
			}
		}
		return nil
	}, fn)
}

// runChunks runs fn for every job the feed emits on a pool of workers. The feed
// returns when it has no more jobs or when emit reports false because the
// transfer was cancelled; a feed error stops the transfer like a failed job.
func (c *Client) runChunks(ctx context.Context, feed func(ctx context.Context, emit func(chunkTransfer) bool) error, fn func(ctx context.Context, job chunkTransfer) (int64, error)) error {
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	work := make(chan chunkTransfer)
	var (
		wg       sync.WaitGroup
		once     sync.Once
		firstErr error
	)
	fail := func(err error) {
		once.Do(func() {
			firstErr = err
			cancel()
		})
	}
	for i := 0; i < maxUploadWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range work {
				if err := c.limiter.acquire(workCtx); err != nil {
					fail(err)
					return
				}
				bytes, err := fn(workCtx, job)
				c.limiter.release()
				c.recordTransfer(bytes, err)
				if err != nil {
					fail(err)
					return
				}
			}
		}()
	}
	feedErr := feed(workCtx, func(job chunkTransfer) bool {
		select {
		case work <- job:
			return true
		case <-workCtx.Done():
			return false
		}
	})
	if feedErr != nil {
		fail(feedErr)
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
