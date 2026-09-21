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

// worker count for direct chunk transfers against the signed HTTP endpoints.
const chunkTransferWorkers = 4

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
	dataByHash := make(map[serverDomain.Hash][]byte, len(chunks))
	for _, chunk := range chunks {
		refs = append(refs, &pb.ChunkRef{Hash: chunk.Hash.String(), SizeBytes: int64(len(chunk.Data))})
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

	confirm, err := client.ConfirmChunkUploads(authedCtx, &pb.ConfirmChunkUploadsRequest{
		Context: &pb.ProjectContext{Org: scope.Org, Project: scope.Project},
		Chunks:  refs,
	})
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
	return forEachChunk(ctx, jobs, func(ctx context.Context, job chunkTransfer) error {
		request, err := http.NewRequestWithContext(ctx, http.MethodPut, c.httpURL(job.url), bytes.NewReader(job.data))
		if err != nil {
			return err
		}
		request.Header.Set("Content-Type", "application/octet-stream")
		res, err := c.http.Do(request)
		if err != nil {
			return err
		}
		defer func() { _ = res.Body.Close() }()
		if res.StatusCode == http.StatusNoContent || res.StatusCode == http.StatusOK {
			return nil
		}
		return chunkHTTPError(res)
	})
}

func (c *Client) getChunks(ctx context.Context, jobs []chunkTransfer, onChunk func(h serverDomain.Hash, data []byte) error) error {
	var (
		mu      sync.Mutex
		sinkErr error
	)
	deliver := func(hash serverDomain.Hash, data []byte) error {
		mu.Lock()
		defer mu.Unlock()
		if sinkErr != nil {
			return sinkErr
		}
		if err := onChunk(hash, data); err != nil {
			sinkErr = err
			return err
		}
		return nil
	}
	return forEachChunk(ctx, jobs, func(ctx context.Context, job chunkTransfer) error {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.httpURL(job.url), nil)
		if err != nil {
			return err
		}
		res, err := c.http.Do(request)
		if err != nil {
			return err
		}
		defer func() { _ = res.Body.Close() }()
		if res.StatusCode != http.StatusOK {
			return chunkHTTPError(res)
		}
		data, err := io.ReadAll(res.Body)
		if err != nil {
			return err
		}
		return deliver(job.hash, data)
	})
}

func forEachChunk(ctx context.Context, jobs []chunkTransfer, fn func(ctx context.Context, job chunkTransfer) error) error {
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
	for i := 0; i < chunkTransferWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range work {
				if err := fn(workCtx, job); err != nil {
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
