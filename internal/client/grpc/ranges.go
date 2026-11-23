package grpc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/obs"
)

const (
	// maxRangeGap is the largest run of unwanted bytes between two chunks of
	// one pack that a merged range reads through instead of starting a new
	// request: below it one request is cheaper than two.
	maxRangeGap = 1 << 20
	// maxRangeSpan bounds the bytes one merged range request covers, so a
	// failed request costs at most this much to retry and the ranges of a big
	// pack still spread over the transfer pool.
	maxRangeSpan = 32 << 20
)

// rangePart is one chunk inside a ranged download: its bytes are
// [offset, offset+length) of the object at the transfer's URL.
type rangePart struct {
	hash   serverDomain.Hash
	offset int64
	length int64
}

func (p rangePart) end() int64 {
	return p.offset + p.length
}

// planDownloads turns one window of download jobs into transfers. Loose
// chunks (no length: the whole object is the chunk) stay one transfer each.
// Chunks inside packs are grouped by URL (the server presigns a pack once
// per page), sorted by offset and merged into range transfers while the gap
// between neighbours is at most maxRangeGap and the merged span at most
// maxRangeSpan; a chunk larger than the span is a range of its own. Packs
// keep the order in which they first appear.
func planDownloads(jobs []chunkTransfer) []chunkTransfer {
	planned := make([]chunkTransfer, 0, len(jobs))
	groups := make(map[string][]rangePart)
	var order []string
	for _, job := range jobs {
		if job.length == 0 {
			planned = append(planned, job)
			continue
		}
		if _, ok := groups[job.url]; !ok {
			order = append(order, job.url)
		}
		groups[job.url] = append(groups[job.url], rangePart{hash: job.hash, offset: job.offset, length: job.length})
	}
	for _, url := range order {
		parts := groups[url]
		slices.SortStableFunc(parts, func(a, b rangePart) int {
			switch {
			case a.offset < b.offset:
				return -1
			case a.offset > b.offset:
				return 1
			}
			return 0
		})
		start := 0
		for i := 1; i <= len(parts); i++ {
			if i < len(parts) && canMerge(parts[start], parts[i-1], parts[i]) {
				continue
			}
			planned = append(planned, chunkTransfer{url: url, ranges: parts[start:i:i]})
			start = i
		}
	}
	return planned
}

// canMerge reports whether next may join the range from first to last.
func canMerge(first, last, next rangePart) bool {
	gap := next.offset - last.end()
	return gap >= 0 && gap <= maxRangeGap && next.end()-first.offset <= maxRangeSpan
}

// downloadRange fetches the chunks of one merged range with one ranged GET
// and hands each chunk to the stream (when it wants it) or the sink as soon
// as its bytes arrived, so only one chunk is held in memory. A transfer that
// fails part-way is retried as a unit for the chunks not delivered yet (the
// next request starts at the first of them), so no chunk is delivered twice;
// a failure of the consumer (wrong content, a disk error) is final.
func (c *Client) downloadRange(ctx context.Context, stream *domain.ChunkStream, sink *chunkSink, job chunkTransfer) (int64, error) {
	next := 0
	var total int64
	err := c.retryTransfer(ctx, c.downloads, func() (retryHint, error) {
		n, hint, err := c.fetchRangeOnce(ctx, stream, sink, job.url, job.ranges, &next)
		total += n
		return hint, err
	})
	return total, err
}

func (c *Client) fetchRangeOnce(ctx context.Context, stream *domain.ChunkStream, sink *chunkSink, url string, parts []rangePart, next *int) (n int64, hint retryHint, err error) {
	pending := parts[*next:]
	start, end := pending[0].offset, pending[len(pending)-1].end()
	ctx, span := obs.StartSpan(ctx, "chunk.download.range",
		attribute.Int(obs.AttrChunkCount, len(pending)),
		attribute.Int64("nipa.chunk.range_offset", start),
		attribute.Int64(obs.AttrChunkBytes, end-start))
	defer func() { obs.FinishSpan(span, err) }()

	res, hint, err := c.openRange(ctx, span, url, start, end)
	if err != nil {
		return 0, hint, err
	}
	defer func() { _ = res.Body.Close() }()
	body := &countingReader{r: res.Body}
	// A server that ignores Range answers the whole object: skip to start.
	pos := int64(0)
	if res.StatusCode == http.StatusPartialContent {
		pos = start
	}
	broken := func(err error) (int64, retryHint, error) {
		return body.n, retryHint{retryable: ctx.Err() == nil}, err
	}
	for _, part := range pending {
		if _, err := io.CopyN(io.Discard, body, part.offset-pos); err != nil {
			return broken(fmt.Errorf("download range of chunk %s: %w", part.hash, unexpectedEOF(err)))
		}
		if err := c.deliverPart(stream, sink, body, part); err != nil {
			var transfer *rangeTransferError
			if errors.As(err, &transfer) {
				return broken(transfer.err)
			}
			return body.n, retryHint{}, err
		}
		pos = part.end()
		*next++
	}
	return body.n, retryHint{}, nil
}

// rangeTransferError is a broken response body while one part was read: a
// retry may fix it, unlike a failure of the consumer.
type rangeTransferError struct {
	err error
}

func (e *rangeTransferError) Error() string { return e.err.Error() }

// deliverPart reads exactly one part's bytes from body and hands them over.
func (c *Client) deliverPart(stream *domain.ChunkStream, sink *chunkSink, body io.Reader, part rangePart) error {
	exact := &exactReader{r: body, remaining: part.length}
	if stream != nil && stream.Wants(part.hash) {
		counted := &countingReader{r: exact}
		writeErr := stream.Write(part.hash, counted)
		switch {
		case counted.err != nil:
			return &rangeTransferError{err: fmt.Errorf("download chunk %s: %w", part.hash, counted.err)}
		case writeErr != nil:
			return writeErr
		case exact.remaining != 0:
			// The stream stopped reading early without an error; drain the
			// rest so the next part starts at its offset.
			if _, err := io.Copy(io.Discard, exact); err != nil {
				return &rangeTransferError{err: fmt.Errorf("download chunk %s: %w", part.hash, err)}
			}
		}
		return nil
	}
	data := make([]byte, part.length)
	if _, err := io.ReadFull(exact, data); err != nil {
		return &rangeTransferError{err: fmt.Errorf("download chunk %s: %w", part.hash, unexpectedEOF(err))}
	}
	return sink.deliver(part.hash, data)
}

func unexpectedEOF(err error) error {
	if errors.Is(err, io.EOF) {
		return io.ErrUnexpectedEOF
	}
	return err
}

// exactReader reads exactly remaining bytes from r: an EOF before that is
// io.ErrUnexpectedEOF (a broken transfer, not a short chunk), and it never
// reads past them.
type exactReader struct {
	r         io.Reader
	remaining int64
}

func (e *exactReader) Read(p []byte) (int, error) {
	if e.remaining <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > e.remaining {
		p = p[:e.remaining]
	}
	n, err := e.r.Read(p)
	e.remaining -= int64(n)
	if errors.Is(err, io.EOF) {
		if e.remaining > 0 {
			return n, io.ErrUnexpectedEOF
		}
		return n, io.EOF
	}
	return n, err
}

// openRange sends the GET for bytes [start, end) of the object at url and
// returns the response once it is a 206 (or a 200 for a server that ignores
// Range). The caller closes the body, which fails with errTransferStalled
// when the server stops sending for longer than the stall timeout.
func (c *Client) openRange(ctx context.Context, span trace.Span, url string, start, end int64) (*http.Response, retryHint, error) {
	res, hint, err := c.openGET(ctx, span, url, func(header http.Header) {
		header.Set("Range", "bytes="+strconv.FormatInt(start, 10)+"-"+strconv.FormatInt(end-1, 10))
	})
	if err != nil {
		return nil, hint, err
	}
	fail := func(hint retryHint, err error) (*http.Response, retryHint, error) {
		_ = res.Body.Close()
		return nil, hint, err
	}
	switch res.StatusCode {
	case http.StatusPartialContent:
		if res.ContentLength > end-start {
			return fail(retryHint{}, fmt.Errorf("download range %d-%d: %d bytes answered", start, end-1, res.ContentLength))
		}
	case http.StatusOK:
	default:
		return fail(httpRetryHint(res), chunkHTTPError(res))
	}
	return res, retryHint{}, nil
}

// checkDownloadRange validates the range the server sent for a chunk.
func checkDownloadRange(hash serverDomain.Hash, offset, length int64) error {
	if offset < 0 || length < 0 || (length == 0 && offset != 0) || length > chunker.MaxChunkSize() {
		return domain.NewUserError(fmt.Sprintf("server sent an invalid range %d+%d for chunk %s", offset, length, hash))
	}
	return nil
}
