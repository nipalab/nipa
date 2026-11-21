package grpc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/chunkproof"
	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	pb "github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/obs"
)

// maxUploadAttempts bounds how often chunks the server could not confirm are
// presigned and sent again, e.g. after it deleted garbage planted on their key.
const maxUploadAttempts = 3

// uploadBatch bounds the chunks one presign and confirm round carries, so a
// window of many small chunks stays far below the gRPC message size limit.
const uploadBatch = 1000

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
	for start := 0; start < len(chunks); start += uploadBatch {
		remaining := chunks[start:min(start+uploadBatch, len(chunks))]
		for attempt := 0; attempt < maxUploadAttempts && len(remaining) > 0; attempt++ {
			remaining, err = c.uploadAttempt(authedCtx, client, scope, remaining, uploaded, skipped)
			if err != nil {
				return 0, 0, err
			}
		}
		if len(remaining) > 0 {
			return 0, 0, domain.NewUserError(fmt.Sprintf("upload incomplete: %d chunk(s) missing on the server", len(remaining)))
		}
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
	return c.DownloadChunksStream(ctx, scope, hashes, nil, onChunk)
}

// DownloadChunksStream is DownloadChunks with an optional stream: chunks the
// stream wants are copied from the response body straight into stream.Write
// and never buffered, the rest reach onChunk as before. Write verifies the
// content itself and may run concurrently for different chunks.
func (c *Client) DownloadChunksStream(ctx context.Context, scope domain.ChunkScope, hashes []serverDomain.Hash, stream *domain.ChunkStream, onChunk func(h serverDomain.Hash, data []byte) error) error {
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
		if stream != nil && stream.Wants(job.hash) {
			return c.streamChunk(ctx, stream, job)
		}
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

	target, err := c.transferURL(job.url)
	if err != nil {
		return retryHint{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, target, bytes.NewReader(job.data))
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
	span.SetAttributes(attribute.Int("http.response.status_code", res.StatusCode))
	if refused, ok := httpMaintenanceError(res); ok {
		return retryHint{}, refused
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
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

// streamChunk fetches one chunk and copies its body into the stream. An
// attempt whose body could not be read to the end is retried like a buffered
// download; a failure of the stream itself (wrong content, a disk error) is
// final.
func (c *Client) streamChunk(ctx context.Context, stream *domain.ChunkStream, job chunkTransfer) (int64, error) {
	var n int64
	err := c.retryTransfer(ctx, func() (retryHint, error) {
		var (
			hint retryHint
			err  error
		)
		n, hint, err = c.streamChunkOnce(ctx, stream, job)
		return hint, err
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}

func (c *Client) streamChunkOnce(ctx context.Context, stream *domain.ChunkStream, job chunkTransfer) (n int64, hint retryHint, err error) {
	ctx, span := obs.StartSpan(ctx, "chunk.download.get",
		attribute.String(obs.AttrChunkHash, job.hash.String()))
	defer func() { obs.FinishSpan(span, err) }()

	res, hint, err := c.openChunk(ctx, span, job)
	if err != nil {
		return 0, hint, err
	}
	defer func() { _ = res.Body.Close() }()
	limit := chunker.MaxChunkSize()
	body := &countingReader{r: io.LimitReader(res.Body, limit+1)}
	writeErr := stream.Write(job.hash, body)
	span.SetAttributes(attribute.Int64(obs.AttrChunkSize, body.n))
	switch {
	case body.n > limit:
		return 0, retryHint{}, oversizedChunkError(job.hash, limit)
	case body.err != nil:
		// The transfer broke, whatever the stream made of the partial body.
		return 0, retryHint{retryable: ctx.Err() == nil}, body.err
	case writeErr != nil:
		return 0, retryHint{}, writeErr
	}
	return body.n, retryHint{}, nil
}

// countingReader counts the bytes read and keeps the first read error other
// than EOF, which tells a broken transfer from a failure of the consumer.
type countingReader struct {
	r   io.Reader
	n   int64
	err error
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	r.n += int64(n)
	if err != nil && !errors.Is(err, io.EOF) && r.err == nil {
		r.err = err
	}
	return n, err
}

// openChunk sends the GET for one chunk and returns the response once it is a
// 200 whose declared length fits a chunk. The caller closes the body, which
// fails with errTransferStalled when the server stops sending for longer than
// the client's stall timeout.
func (c *Client) openChunk(ctx context.Context, span trace.Span, job chunkTransfer) (*http.Response, retryHint, error) {
	target, err := c.transferURL(job.url)
	if err != nil {
		return nil, retryHint{}, err
	}
	reqCtx, cancel := context.WithCancel(ctx)
	guard := newStallGuard(c.stallTimeout, cancel)
	request, err := http.NewRequestWithContext(reqCtx, http.MethodGet, target, nil)
	if err != nil {
		guard.stop()
		return nil, retryHint{}, err
	}
	injectTraceContext(ctx, request.Header)
	res, err := c.http.Do(request)
	if err != nil {
		err = guard.explain(err)
		guard.stop()
		return nil, retryHint{retryable: ctx.Err() == nil}, err
	}
	guard.body = res.Body
	res.Body = guard
	span.SetAttributes(attribute.Int("http.response.status_code", res.StatusCode))
	fail := func(hint retryHint, err error) (*http.Response, retryHint, error) {
		_ = res.Body.Close()
		return nil, hint, err
	}
	if refused, ok := httpMaintenanceError(res); ok {
		return fail(retryHint{}, refused)
	}
	if res.StatusCode != http.StatusOK {
		return fail(httpRetryHint(res), chunkHTTPError(res))
	}
	if res.ContentLength > chunker.MaxChunkSize() {
		return fail(retryHint{}, oversizedChunkError(job.hash, chunker.MaxChunkSize()))
	}
	return res, retryHint{}, nil
}

// fetchChunk downloads one chunk once; the hint says whether trying again
// could succeed. A cancelled context never is.
func (c *Client) fetchChunk(ctx context.Context, job chunkTransfer) (data []byte, hint retryHint, err error) {
	ctx, span := obs.StartSpan(ctx, "chunk.download.get",
		attribute.String(obs.AttrChunkHash, job.hash.String()))
	defer func() { obs.FinishSpan(span, err) }()

	res, hint, err := c.openChunk(ctx, span, job)
	if err != nil {
		return nil, hint, err
	}
	defer func() { _ = res.Body.Close() }()
	limit := chunker.MaxChunkSize()
	data, err = readChunkBody(res.Body, res.ContentLength, limit)
	if errors.Is(err, errChunkTooLarge) {
		return nil, retryHint{}, oversizedChunkError(job.hash, limit)
	}
	if err != nil {
		return nil, retryHint{retryable: ctx.Err() == nil}, err
	}
	span.SetAttributes(attribute.Int(obs.AttrChunkSize, len(data)))
	return data, retryHint{}, nil
}

var errChunkTooLarge = errors.New("chunk body exceeds the maximum chunk size")

// oversizedChunkError is final: no chunk any profile produces is that large,
// so a retry would only download the same oversized body again.
func oversizedChunkError(hash serverDomain.Hash, limit int64) error {
	return fmt.Errorf("download chunk %s: %w of %d bytes", hash, errChunkTooLarge, limit)
}

// readChunkBody reads at most limit bytes of a chunk body. A known length
// (already checked against limit) sizes the buffer up front, with one spare
// byte so the read that reports EOF does not grow it; a body past limit fails
// with errChunkTooLarge after reading only limit+1 bytes.
func readChunkBody(body io.Reader, contentLength, limit int64) ([]byte, error) {
	size := int64(512)
	if contentLength >= 0 {
		size = contentLength + 1
	}
	buf := make([]byte, 0, size)
	r := io.LimitReader(body, limit+1)
	for {
		if len(buf) == cap(buf) {
			buf = append(buf, 0)[:len(buf)]
		}
		n, err := r.Read(buf[len(buf):cap(buf)])
		buf = buf[:len(buf)+n]
		if int64(len(buf)) > limit {
			return nil, errChunkTooLarge
		}
		if errors.Is(err, io.EOF) {
			return buf, nil
		}
		if err != nil {
			return nil, err
		}
	}
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

// allowInsecureChunkURLsEnv lets a TLS session use cleartext chunk URLs, for
// deployments whose object storage is only reachable over http.
const allowInsecureChunkURLsEnv = "NIPA_ALLOW_INSECURE_CHUNK_URLS"

func insecureChunkURLsAllowed() bool {
	allowed, _ := strconv.ParseBool(os.Getenv(allowInsecureChunkURLsEnv))
	return allowed
}

func isScheme(rawURL, scheme string) bool {
	return len(rawURL) > len(scheme)+2 && strings.EqualFold(rawURL[:len(scheme)+3], scheme+"://")
}

// transferURL resolves a chunk transfer URL from the server. A session that
// talks TLS to the server does not follow it to a cleartext URL: the content
// would cross the network unencrypted although the user asked for https.
func (c *Client) transferURL(raw string) (string, error) {
	target := c.httpURL(raw)
	if isScheme(c.transport.url, "https") && isScheme(target, "http") && !insecureChunkURLsAllowed() {
		return "", errInsecureChunkURL()
	}
	return target, nil
}

func errInsecureChunkURL() error {
	return domain.NewUserError("the server sent a cleartext (http) chunk transfer URL on a TLS session; refusing to use it").
		WithHint("file content would be sent or received unencrypted", "ask the server administrator to serve chunk storage over https, or set "+allowInsecureChunkURLsEnv+"=1 to allow it")
}

// maxTransferRedirects bounds the redirects one chunk transfer follows.
const maxTransferRedirects = 3

// checkTransferRedirect stops redirect chains and https to http downgrades.
func checkTransferRedirect(req *http.Request, via []*http.Request) error {
	if len(via) > maxTransferRedirects {
		return fmt.Errorf("chunk transfer stopped after %d redirects", maxTransferRedirects)
	}
	if via[0].URL.Scheme == "https" && req.URL.Scheme != "https" && !insecureChunkURLsAllowed() {
		return errInsecureChunkURL()
	}
	return nil
}

var errTransferStalled = errors.New("chunk transfer stalled")

// stallGuard cancels a transfer that makes no progress for timeout: nothing
// arrives before the response, or between two reads of its body.
type stallGuard struct {
	body    io.ReadCloser
	timer   *time.Timer
	timeout time.Duration
	cancel  context.CancelFunc
	stalled atomic.Bool
}

func newStallGuard(timeout time.Duration, cancel context.CancelFunc) *stallGuard {
	g := &stallGuard{timeout: timeout, cancel: cancel}
	if timeout > 0 {
		g.timer = time.AfterFunc(timeout, func() {
			g.stalled.Store(true)
			cancel()
		})
	}
	return g
}

func (g *stallGuard) explain(err error) error {
	if err != nil && g.stalled.Load() {
		return fmt.Errorf("%w: no data for %s", errTransferStalled, g.timeout)
	}
	return err
}

func (g *stallGuard) stop() {
	if g.timer != nil {
		g.timer.Stop()
	}
	g.cancel()
}

func (g *stallGuard) Read(p []byte) (int, error) {
	n, err := g.body.Read(p)
	if n > 0 && g.timer != nil {
		g.timer.Reset(g.timeout)
	}
	if err != nil && !errors.Is(err, io.EOF) {
		err = g.explain(err)
	}
	return n, err
}

func (g *stallGuard) Close() error {
	g.stop()
	return g.body.Close()
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
