package grpc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	pb "github.com/nipalab/nipa/internal/grpc/pb"
)

func part(b byte, offset, length int64) chunkTransfer {
	return chunkTransfer{hash: testChunkHash(b), url: "pack", offset: offset, length: length}
}

func TestPlanDownloads_MergesNeighbours(t *testing.T) {
	loose := chunkTransfer{hash: testChunkHash(0x09), url: "loose"}
	jobs := []chunkTransfer{
		part(3, 300, 100), // out of order
		loose,
		part(1, 4, 100),
		part(2, 104, 100),                // adjacent
		part(4, 400+maxRangeGap, 1),      // gap of exactly maxRangeGap: merged
		part(5, 401+2*maxRangeGap+1, 10), // gap past maxRangeGap: new range
		{hash: testChunkHash(0x06), url: "other", offset: 4, length: 10},
	}
	planned := planDownloads(jobs)
	require.Len(t, planned, 4)
	require.Equal(t, loose, planned[0], "a loose chunk stays a whole-object transfer")
	require.Equal(t, "pack", planned[1].url)
	require.Equal(t, []rangePart{
		{hash: testChunkHash(1), offset: 4, length: 100},
		{hash: testChunkHash(2), offset: 104, length: 100},
		{hash: testChunkHash(3), offset: 300, length: 100},
		{hash: testChunkHash(4), offset: 400 + maxRangeGap, length: 1},
	}, planned[1].ranges)
	require.Equal(t, []rangePart{{hash: testChunkHash(5), offset: 401 + 2*maxRangeGap + 1, length: 10}}, planned[2].ranges)
	require.Equal(t, "other", planned[3].url)
	require.Len(t, planned[3].ranges, 1)
}

func TestPlanDownloads_SpanLimit(t *testing.T) {
	const size = 8 << 20
	var jobs []chunkTransfer
	for i := 0; i < 9; i++ {
		jobs = append(jobs, part(byte(i), int64(4+i*size), size))
	}
	jobs = append(jobs, part(0x20, int64(4+9*size), maxRangeSpan+1)) // larger than a span: alone
	planned := planDownloads(jobs)
	require.Len(t, planned, 4)
	require.Len(t, planned[0].ranges, 4, "4 x 8 MiB fill one 32 MiB span")
	require.Len(t, planned[1].ranges, 4)
	require.Len(t, planned[2].ranges, 1)
	require.Len(t, planned[3].ranges, 1)
	for _, transfer := range planned[:3] {
		first, last := transfer.ranges[0], transfer.ranges[len(transfer.ranges)-1]
		require.LessOrEqual(t, last.end()-first.offset, int64(maxRangeSpan))
	}
}

func TestPlanDownloads_OverlapStartsANewRange(t *testing.T) {
	planned := planDownloads([]chunkTransfer{part(1, 4, 100), part(2, 50, 100)})
	require.Len(t, planned, 2)
}

func TestCheckDownloadRange(t *testing.T) {
	h := testChunkHash(1)
	require.NoError(t, checkDownloadRange(h, 0, 0))
	require.NoError(t, checkDownloadRange(h, 4, 100))
	for _, bad := range [][2]int64{{-1, 10}, {4, -1}, {4, 0}, {4, chunker.MaxChunkSize() + 1}} {
		require.Error(t, checkDownloadRange(h, bad[0], bad[1]), bad)
	}
}

// packServer serves pack objects with Range support and logs every request's
// Range header.
type packServer struct {
	*httptest.Server
	mu       sync.Mutex
	objects  map[string][]byte
	ranges   []string
	truncate int // answers this many requests with half a body
	fail     int // answers this many requests with failStatus
	status   int
	noRange  bool // ignores Range, like a server without range support
}

func newPackServer(t *testing.T) *packServer {
	s := &packServer{objects: map[string][]byte{}, status: http.StatusBadGateway}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

func (s *packServer) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.ranges = append(s.ranges, r.Header.Get("Range"))
	data, ok := s.objects[r.URL.Path]
	fail := s.fail > 0
	if fail {
		s.fail--
	}
	truncate := s.truncate > 0
	if truncate {
		s.truncate--
	}
	noRange := s.noRange
	s.mu.Unlock()
	switch {
	case !ok:
		http.NotFound(w, r)
		return
	case fail:
		w.WriteHeader(s.status)
		return
	}
	if noRange {
		r.Header.Del("Range")
	}
	if truncate {
		var start, end int64
		_, _ = fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end)
		body := data[start : end+1]
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(data)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(body[:len(body)/2])
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		if hj, ok := w.(http.Hijacker); ok {
			conn, _, err := hj.Hijack()
			if err == nil {
				_ = conn.Close()
			}
		}
		return
	}
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
}

func (s *packServer) requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.ranges...)
}

// packOf lays contents out like a pack (4-byte header, then the chunks back
// to back, gapBefore[i] unwanted bytes before chunk i) and registers each
// chunk's range with the chunk server.
func packOf(srv *chunkTestServer, packs *packServer, path string, gapBefore []int, contents ...[]byte) []serverDomain.Hash {
	object := []byte("NPK1")
	hashes := make([]serverDomain.Hash, 0, len(contents))
	if srv.packed == nil {
		srv.packed = map[string]*pb.PresignedChunkUrl{}
	}
	for i, data := range contents {
		if i < len(gapBefore) {
			object = append(object, bytes.Repeat([]byte{'-'}, gapBefore[i])...)
		}
		hash := chunker.Sum(data)
		srv.packed[hash.String()] = &pb.PresignedChunkUrl{Hash: hash.String(), Url: packs.URL + path, Offset: int64(len(object)), Length: int64(len(data))}
		object = append(object, data...)
		hashes = append(hashes, hash)
	}
	packs.mu.Lock()
	packs.objects[path] = object
	packs.mu.Unlock()
	return hashes
}

func TestDownloadChunksStream_PackRanges(t *testing.T) {
	srv := newChunkTestServer()
	packs := newPackServer(t)
	a, b, c := []byte("first packed chunk"), []byte("second packed chunk"), []byte("third packed chunk")
	hashes := packOf(srv, packs, "/packs/1", []int{0, 0, maxRangeGap + 1}, a, b, c)
	loose := testChunkHash(0x50)
	srv.put(loose.String(), []byte("a loose chunk"))
	c2 := newDownloadClient(t, srv, fastRetry(3))

	rec := newStreamRecorder(hashes[1])
	got := newDeliveries()
	all := []serverDomain.Hash{hashes[2], loose, hashes[0], hashes[1]}
	require.NoError(t, c2.DownloadChunksStream(context.Background(), downloadScope(), all, rec.stream(), got.sink))

	require.Equal(t, a, got.data[hashes[0]])
	require.Equal(t, b, rec.data[hashes[1]], "a wanted chunk is streamed out of the range")
	require.Equal(t, c, got.data[hashes[2]])
	require.Equal(t, []byte("a loose chunk"), got.data[loose])
	require.Equal(t, 1, srv.attemptsFor(loose.String()), "a loose chunk is fetched whole, as before")
	for _, h := range hashes {
		require.LessOrEqual(t, got.seen[h]+rec.attempts[h], 1, "every chunk is handed over once")
	}
	requests := packs.requests()
	require.Len(t, requests, 2, "neighbours merge; the chunk past the gap gets its own range")
	end := 4 + len(a) + len(b) - 1
	require.Contains(t, requests, fmt.Sprintf("bytes=4-%d", end))
}

func TestDownloadChunksStream_RetriesTheUndeliveredPartOfARange(t *testing.T) {
	srv := newChunkTestServer()
	packs := newPackServer(t)
	contents := [][]byte{bytes.Repeat([]byte("a"), 1000), bytes.Repeat([]byte("b"), 1000), bytes.Repeat([]byte("c"), 1000), bytes.Repeat([]byte("d"), 1000)}
	hashes := packOf(srv, packs, "/packs/2", nil, contents...)
	packs.truncate = 1
	c := newDownloadClient(t, srv, fastRetry(3))

	got := newDeliveries()
	require.NoError(t, c.DownloadChunksStream(context.Background(), downloadScope(), hashes, nil, got.sink))
	for i, h := range hashes {
		require.Equal(t, contents[i], got.data[h])
		require.Equal(t, 1, got.seen[h], "no chunk is delivered twice")
	}
	requests := packs.requests()
	require.Len(t, requests, 2)
	require.Equal(t, "bytes=4-4003", requests[0])
	// Half the span (2000 bytes) arrived: chunks a and b were delivered, so
	// the retry starts at c.
	require.Equal(t, "bytes=2004-4003", requests[1])
}

func TestDownloadChunksStream_RangeRetriesAndFailures(t *testing.T) {
	t.Run("transient status is retried", func(t *testing.T) {
		srv := newChunkTestServer()
		packs := newPackServer(t)
		hashes := packOf(srv, packs, "/packs/3", nil, []byte("x1"), []byte("x2"))
		packs.fail = 2
		c := newDownloadClient(t, srv, fastRetry(3))
		got := newDeliveries()
		require.NoError(t, c.DownloadChunksStream(context.Background(), downloadScope(), hashes, nil, got.sink))
		require.Len(t, packs.requests(), 3)
		require.Equal(t, []byte("x2"), got.data[hashes[1]])
	})
	t.Run("a missing pack is final", func(t *testing.T) {
		srv := newChunkTestServer()
		packs := newPackServer(t)
		hashes := packOf(srv, packs, "/packs/4", nil, []byte("y1"))
		srv.packed[hashes[0].String()].Url = packs.URL + "/packs/missing"
		c := newDownloadClient(t, srv, fastRetry(3))
		err := c.DownloadChunksStream(context.Background(), downloadScope(), hashes, nil, newDeliveries().sink)
		require.Error(t, err)
		require.Len(t, packs.requests(), 1)
	})
	t.Run("a server without Range support still works", func(t *testing.T) {
		srv := newChunkTestServer()
		packs := newPackServer(t)
		hashes := packOf(srv, packs, "/packs/5", []int{0, 10}, []byte("z1"), []byte("z2"))
		packs.noRange = true
		c := newDownloadClient(t, srv, fastRetry(3))
		got := newDeliveries()
		require.NoError(t, c.DownloadChunksStream(context.Background(), downloadScope(), hashes, nil, got.sink))
		require.Equal(t, []byte("z1"), got.data[hashes[0]])
		require.Equal(t, []byte("z2"), got.data[hashes[1]])
	})
	t.Run("a consumer failure is final", func(t *testing.T) {
		srv := newChunkTestServer()
		packs := newPackServer(t)
		hashes := packOf(srv, packs, "/packs/6", nil, []byte("w1"), []byte("w2"))
		c := newDownloadClient(t, srv, fastRetry(3))
		rec := newStreamRecorder(hashes...)
		rec.writeErr = errors.New("disk full")
		err := c.DownloadChunksStream(context.Background(), downloadScope(), hashes, rec.stream(), newDeliveries().sink)
		require.ErrorContains(t, err, "disk full")
		require.Len(t, packs.requests(), 1)
		sinkErr := errors.New("sink failed")
		err = c.DownloadChunksStream(context.Background(), downloadScope(), hashes, nil, func(serverDomain.Hash, []byte) error { return sinkErr })
		require.ErrorIs(t, err, sinkErr)
	})
	t.Run("an invalid range from the server is refused", func(t *testing.T) {
		srv := newChunkTestServer()
		packs := newPackServer(t)
		hashes := packOf(srv, packs, "/packs/7", nil, []byte("v1"))
		srv.packed[hashes[0].String()].Offset = -1
		c := newDownloadClient(t, srv, fastRetry(3))
		err := c.DownloadChunksStream(context.Background(), downloadScope(), hashes, nil, newDeliveries().sink)
		require.ErrorContains(t, err, "invalid range")
		require.Empty(t, packs.requests())
	})
}

func TestExactReader(t *testing.T) {
	r := &exactReader{r: strings.NewReader("abc"), remaining: 5}
	buf := make([]byte, 10)
	n, err := r.Read(buf)
	require.Equal(t, 3, n)
	require.NoError(t, err)
	_, err = r.Read(buf)
	require.ErrorIs(t, err, io.ErrUnexpectedEOF, "an EOF before the length is a broken transfer")

	r = &exactReader{r: strings.NewReader("abcdef"), remaining: 2}
	n, err = r.Read(buf)
	require.Equal(t, 2, n)
	require.NoError(t, err)
	_, err = r.Read(buf)
	require.ErrorIs(t, err, io.EOF, "it never reads past its length")
	require.Equal(t, io.ErrUnexpectedEOF, unexpectedEOF(io.EOF))
	other := errors.New("other")
	require.Equal(t, other, unexpectedEOF(other))
}
