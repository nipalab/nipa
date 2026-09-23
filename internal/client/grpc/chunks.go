package grpc

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	pb "github.com/nipalab/nipa/internal/grpc/pb"
)

// UploadChunks sends chunk content to the signed upload URLs returned by the
// server. Chunks the server already stores are reported as skipped. After the
// transfer the uploads are confirmed so the server records chunk metadata.
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

	refs := make([]*pb.ChunkRef, 0, len(chunks))
	rawHashes := make([]string, 0, len(chunks))
	dataByHash := make(map[serverDomain.Hash][]byte, len(chunks))
	for _, chunk := range chunks {
		refs = append(refs, &pb.ChunkRef{Hash: chunk.Hash.String(), SizeBytes: int64(len(chunk.Data))})
		rawHashes = append(rawHashes, chunk.Hash.String())
		dataByHash[chunk.Hash] = chunk.Data
	}

	var uploaded, skipped int
	pageToken := ""
	for {
		res, err := client.GetChunkUploadUrls(authedCtx, &pb.GetChunkUploadUrlsRequest{
			Context:   &pb.ProjectContext{Org: scope.Org, Project: scope.Project},
			Chunks:    refs,
			PageSize:  int32(len(refs)),
			PageToken: pageToken,
		})
		if err != nil {
			return 0, 0, toDomainError(err)
		}

		jobs := make([]chunkTransfer, 0, len(res.GetUrls()))
		for _, u := range res.GetUrls() {
			hash, err := decodeHash(u.GetHash())
			if err != nil {
				return 0, 0, err
			}
			if u.GetAlreadyStored() {
				skipped++
				continue
			}
			data, ok := dataByHash[hash]
			if !ok {
				return 0, 0, domain.NewUserError(fmt.Sprintf("server requested unknown chunk %s", hash))
			}
			jobs = append(jobs, chunkTransfer{hash: hash, url: u.GetUrl(), data: data})
		}

		if err := c.putChunks(authedCtx, jobs); err != nil {
			return 0, 0, err
		}
		uploaded += len(jobs)

		pageToken = res.GetNextPageToken()
		if pageToken == "" {
			break
		}
	}

	c.confirmMu.Lock()
	confirm, err := client.ConfirmChunkUploads(authedCtx, &pb.ConfirmChunkUploadsRequest{
		Context: &pb.ProjectContext{Org: scope.Org, Project: scope.Project},
		Hashes:  rawHashes,
	})
	c.confirmMu.Unlock()
	if err != nil {
		return 0, 0, toDomainError(err)
	}
	if missing := confirm.GetMissingHashes(); len(missing) > 0 {
		return 0, 0, domain.NewUserError(fmt.Sprintf("upload incomplete: %d chunk(s) missing on the server", len(missing)))
	}

	if len(onChunk) > 0 && onChunk[0] != nil {
		for _, chunk := range chunks {
			onChunk[0](chunk)
		}
	}
	return uploaded, skipped, nil
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
	hash serverDomain.Hash
	url  string
	data []byte
}

func (c *Client) putChunks(ctx context.Context, jobs []chunkTransfer) error {
	return c.forEachChunk(ctx, jobs, func(ctx context.Context, job chunkTransfer) (int64, error) {
		request, err := http.NewRequestWithContext(ctx, http.MethodPut, c.httpURL(job.url), bytes.NewReader(job.data))
		if err != nil {
			return 0, err
		}
		request.Header.Set("Content-Type", "application/octet-stream")
		res, err := c.http.Do(request)
		if err != nil {
			return 0, err
		}
		defer func() { _ = res.Body.Close() }()
		if res.StatusCode == http.StatusNoContent || res.StatusCode == http.StatusOK {
			return int64(len(job.data)), nil
		}
		return int64(len(job.data)), chunkHTTPError(res)
	})
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
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.httpURL(job.url), nil)
		if err != nil {
			return 0, err
		}
		res, err := c.http.Do(request)
		if err != nil {
			return 0, err
		}
		defer func() { _ = res.Body.Close() }()
		if res.StatusCode != http.StatusOK {
			return 0, chunkHTTPError(res)
		}
		data, err := io.ReadAll(res.Body)
		if err != nil {
			return 0, err
		}
		if err := sink.deliver(job.hash, data); err != nil {
			return int64(len(data)), err
		}
		return int64(len(data)), nil
	})
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
	if !strings.Contains(base, "://") {
		// TODO: check later, it should be https
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
