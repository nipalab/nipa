package grpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"go.opentelemetry.io/otel/attribute"

	"github.com/nipalab/nipa/internal/chunkbundle"
	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	pb "github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/obs"
)

// maxBundleResponseBytes bounds the receipts response of one bundle upload
// (about 200 bytes per chunk).
const maxBundleResponseBytes = 8 << 20

// chunkBundle is one bundle upload: the chunks of one presign page that go to
// the page's signed bundle URL in one request.
type chunkBundle struct {
	url     string
	entries []chunkbundle.Entry
	size    int64
	// receipts is filled by a successful PUT; failed is set when the PUT
	// failed in a way another upload attempt may fix. Each bundle owns them
	// and the confirm reads them only after every PUT returned.
	receipts []*pb.ChunkReceipt
	failed   error
}

// splitBundles groups chunks into bundles of at most maxBytes encoded bytes
// and maxCount entries, keeping their order. A chunk larger than maxBytes on
// its own still gets a bundle (the server refuses it if it is really too
// large; the server never signs a limit below its largest chunk).
func splitBundles(url string, chunks []*serverDomain.ChunkData, maxBytes int64, maxCount int) []*chunkBundle {
	if maxCount <= 0 {
		maxCount = len(chunks)
	}
	var bundles []*chunkBundle
	var current *chunkBundle
	for _, chunk := range chunks {
		size := chunkbundle.EncodedSize(len(chunk.Data))
		if current == nil || len(current.entries) >= maxCount || (maxBytes > 0 && current.size+size > maxBytes) {
			current = &chunkBundle{url: url}
			bundles = append(bundles, current)
		}
		current.entries = append(current.entries, chunkbundle.Entry{Hash: chunk.Hash, Data: chunk.Data})
		current.size += size
	}
	return bundles
}

// retryLater marks a transfer failure that runChunks counts against the
// transfer tuner without stopping the other transfers: the caller tries the
// failed work again in its next attempt.
type retryLater struct {
	err error
}

func (e *retryLater) Error() string { return e.err.Error() }

func (e *retryLater) Unwrap() error { return e.err }

// putBundles sends every bundle through the client's transfer pool, one
// limiter slot per bundle. A bundle whose PUT still fails with a retryable
// error after the transfer retries is marked failed (its chunks are tried
// again whole by the next upload attempt); any other failure stops the upload.
func (c *Client) putBundles(ctx context.Context, bundles []*chunkBundle) error {
	if len(bundles) == 0 {
		return nil
	}
	jobs := make([]chunkTransfer, 0, len(bundles))
	for _, bundle := range bundles {
		jobs = append(jobs, chunkTransfer{url: bundle.url, bundle: bundle})
	}
	return c.forEachChunk(ctx, c.uploads, jobs, func(ctx context.Context, job chunkTransfer) (int64, error) {
		return c.putBundle(ctx, job.bundle)
	})
}

func (c *Client) putBundle(ctx context.Context, bundle *chunkBundle) (int64, error) {
	var last retryHint
	err := c.retryTransfer(ctx, c.uploads, func() (retryHint, error) {
		hint, err := c.putBundleOnce(ctx, bundle)
		last = hint
		return hint, err
	})
	if err == nil {
		return bundle.size, nil
	}
	if last.retryable && ctx.Err() == nil {
		bundle.failed = err
		return 0, &retryLater{err: err}
	}
	return 0, err
}

func (c *Client) putBundleOnce(ctx context.Context, bundle *chunkBundle) (hint retryHint, err error) {
	ctx, span := obs.StartSpan(ctx, "chunk.upload.bundle",
		attribute.Int(obs.AttrChunkCount, len(bundle.entries)),
		attribute.Int64(obs.AttrChunkBytes, bundle.size))
	defer func() { obs.FinishSpan(span, err) }()

	target, err := c.transferURL(bundle.url)
	if err != nil {
		return retryHint{}, err
	}
	reqCtx, cancel := context.WithCancel(ctx)
	guard := newStallGuard(c.stallTimeout, cancel)
	defer guard.stop()
	request, err := http.NewRequestWithContext(guard.traceUpload(reqCtx), http.MethodPut, target, nil)
	if err != nil {
		return retryHint{}, err
	}
	guard.setUploadBody(request, bundle.size, func() io.Reader { return chunkbundle.NewEncoder(bundle.entries) })
	request.Header.Set("Content-Type", "application/octet-stream")
	injectTraceContext(ctx, request.Header)
	res, err := c.http.Do(request)
	if err != nil {
		return retryHint{retryable: ctx.Err() == nil}, guard.explain(err)
	}
	defer func() { _ = res.Body.Close() }()
	span.SetAttributes(attribute.Int("http.response.status_code", res.StatusCode))
	if refused, ok := httpMaintenanceError(res); ok {
		return retryHint{}, refused
	}
	if res.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
		return httpRetryHint(res), chunkHTTPError(res)
	}
	receipts, err := decodeBundleReceipts(res.Body)
	if err != nil {
		return retryHint{retryable: ctx.Err() == nil}, err
	}
	bundle.receipts = keepBundleReceipts(bundle, receipts)
	return retryHint{}, nil
}

type bundleReceipt struct {
	Hash    string `json:"hash"`
	Receipt string `json:"receipt"`
}

func decodeBundleReceipts(body io.Reader) ([]bundleReceipt, error) {
	var receipts []bundleReceipt
	if err := json.NewDecoder(io.LimitReader(body, maxBundleResponseBytes)).Decode(&receipts); err != nil {
		return nil, fmt.Errorf("read chunk bundle receipts: %w", err)
	}
	return receipts, nil
}

// keepBundleReceipts keeps the receipts of the bundle's own chunks for the
// confirm. Only a server path (relative URL) can answer with receipts: an
// absolute URL is not the server's, so its answer says nothing about what the
// server verified.
func keepBundleReceipts(bundle *chunkBundle, receipts []bundleReceipt) []*pb.ChunkReceipt {
	if !strings.HasPrefix(bundle.url, "/") {
		return nil
	}
	sent := make(map[string]struct{}, len(bundle.entries))
	for _, entry := range bundle.entries {
		sent[entry.Hash.String()] = struct{}{}
	}
	kept := make([]*pb.ChunkReceipt, 0, len(receipts))
	for _, receipt := range receipts {
		if _, ok := sent[receipt.Hash]; !ok || receipt.Receipt == "" {
			continue
		}
		kept = append(kept, &pb.ChunkReceipt{Hash: receipt.Hash, Receipt: receipt.Receipt})
	}
	return kept
}

// bundleFailure is the error of the last bundle that failed in an attempt, so
// an upload that runs out of attempts reports why.
func bundleFailure(bundles []*chunkBundle) error {
	var failure error
	for _, bundle := range bundles {
		if bundle.failed != nil {
			failure = bundle.failed
		}
	}
	return failure
}

// errUploadIncomplete reports chunks still missing after every attempt, with
// the transfer failure that kept them from the server when there was one.
func errUploadIncomplete(missing int, cause error) error {
	err := domain.NewUserError(fmt.Sprintf("upload incomplete: %d chunk(s) missing on the server", missing))
	if cause == nil {
		return err
	}
	var domainErr *domain.Error
	if errors.As(cause, &domainErr) {
		return domain.NewUserError(fmt.Sprintf("upload incomplete: %d chunk(s) missing on the server: %s", missing, domainErr.Message))
	}
	return domain.NewUserError(fmt.Sprintf("upload incomplete: %d chunk(s) missing on the server: %v", missing, cause))
}
