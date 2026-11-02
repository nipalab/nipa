package grpc

import (
	"context"
	"encoding/binary"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

func downloadScope() domain.ChunkScope {
	return domain.ChunkScope{Org: "org", Project: "proj", CommitIDs: []string{"1"}}
}

func newDownloadClient(t *testing.T, srv *chunkTestServer, policy retryPolicy) *Client {
	t.Helper()
	addr := startChunkTestServer(t, srv)
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	c.retry = policy
	require.NoError(t, c.Connect(context.Background(), addr))
	return c
}

type deliveries struct {
	mu   sync.Mutex
	seen map[serverDomain.Hash]int
	data map[serverDomain.Hash][]byte
}

func newDeliveries() *deliveries {
	return &deliveries{seen: map[serverDomain.Hash]int{}, data: map[serverDomain.Hash][]byte{}}
}

func (d *deliveries) sink(h serverDomain.Hash, data []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.seen[h]++
	d.data[h] = data
	return nil
}

func fastRetry(attempts int) retryPolicy {
	return retryPolicy{attempts: attempts, backoff: time.Millisecond}
}

func TestDownloadChunks_RetriesTransientStatuses(t *testing.T) {
	for _, status := range []int{
		http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable,
		http.StatusTooManyRequests, http.StatusRequestTimeout,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			h := testChunkHash(0x01)
			srv := newChunkTestServer()
			srv.put(h.String(), []byte("payload"))
			srv.failGets[h.String()] = 2
			srv.failGetStatus = status
			c := newDownloadClient(t, srv, fastRetry(3))

			got := newDeliveries()
			require.NoError(t, c.DownloadChunks(context.Background(), downloadScope(), []serverDomain.Hash{h}, got.sink))
			require.Equal(t, 3, srv.attemptsFor(h.String()), "two failures and the successful attempt")
			require.Equal(t, 1, got.seen[h], "a retried chunk is delivered once")
			require.Equal(t, []byte("payload"), got.data[h])
		})
	}
}

func TestDownloadChunks_RetriesTruncatedBody(t *testing.T) {
	h := testChunkHash(0x02)
	srv := newChunkTestServer()
	srv.put(h.String(), []byte("a complete chunk body"))
	srv.truncateGets[h.String()] = 1
	c := newDownloadClient(t, srv, fastRetry(3))

	got := newDeliveries()
	require.NoError(t, c.DownloadChunks(context.Background(), downloadScope(), []serverDomain.Hash{h}, got.sink))
	require.Equal(t, 2, srv.attemptsFor(h.String()))
	require.Equal(t, 1, got.seen[h])
	require.Equal(t, []byte("a complete chunk body"), got.data[h], "the partial first read is never delivered")
}

func TestDownloadChunks_GivesUpAfterAttempts(t *testing.T) {
	h := testChunkHash(0x03)
	srv := newChunkTestServer()
	srv.put(h.String(), []byte("x"))
	srv.failGets[h.String()] = 100
	srv.failGetStatus = http.StatusServiceUnavailable
	c := newDownloadClient(t, srv, fastRetry(4))

	got := newDeliveries()
	err := c.DownloadChunks(context.Background(), downloadScope(), []serverDomain.Hash{h}, got.sink)
	require.Error(t, err)
	require.Equal(t, 4, srv.attemptsFor(h.String()))
	require.Empty(t, got.seen)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 400, domErr.Code)
}

func TestDownloadChunks_DoesNotRetryClientErrors(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusUnauthorized, http.StatusNotFound, http.StatusBadRequest} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			h := testChunkHash(0x04)
			srv := newChunkTestServer()
			srv.put(h.String(), []byte("x"))
			srv.failGets[h.String()] = 100
			srv.failGetStatus = status
			c := newDownloadClient(t, srv, fastRetry(5))

			err := c.DownloadChunks(context.Background(), downloadScope(), []serverDomain.Hash{h}, newDeliveries().sink)
			require.Error(t, err)
			require.Equal(t, 1, srv.attemptsFor(h.String()), "an expired or unknown URL is not worth repeating")
		})
	}
}

func TestDownloadChunks_RetryStopsWhenContextIsCancelled(t *testing.T) {
	h := testChunkHash(0x05)
	srv := newChunkTestServer()
	srv.put(h.String(), []byte("x"))
	srv.failGets[h.String()] = 100
	srv.failGetStatus = http.StatusServiceUnavailable
	c := newDownloadClient(t, srv, retryPolicy{attempts: 5, backoff: 30 * time.Second})

	ctx, cancel := context.WithCancel(context.Background())
	failed := make(chan struct{})
	srv.hookBeforeGet = func() {
		select {
		case <-failed:
		default:
			close(failed)
		}
	}
	go func() {
		<-failed
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := c.DownloadChunks(ctx, downloadScope(), []serverDomain.Hash{h}, newDeliveries().sink)
	require.Error(t, err)
	require.Less(t, time.Since(start), 5*time.Second, "cancelling must interrupt the backoff sleep")
	require.Equal(t, 1, srv.attemptsFor(h.String()))
}

func TestDownloadChunks_SinkErrorIsNotRetried(t *testing.T) {
	h := testChunkHash(0x06)
	srv := newChunkTestServer()
	srv.put(h.String(), []byte("x"))
	c := newDownloadClient(t, srv, fastRetry(5))

	sinkErr := fmt.Errorf("disk full")
	err := c.DownloadChunks(context.Background(), downloadScope(), []serverDomain.Hash{h}, func(serverDomain.Hash, []byte) error {
		return sinkErr
	})
	require.ErrorIs(t, err, sinkErr)
	require.Equal(t, 1, srv.attemptsFor(h.String()))
}

func windowHashes(n int) []serverDomain.Hash {
	hashes := make([]serverDomain.Hash, 0, n)
	for i := 0; i < n; i++ {
		var h serverDomain.Hash
		binary.BigEndian.PutUint32(h[:4], uint32(i+1))
		hashes = append(hashes, h)
	}
	return hashes
}

func TestDownloadChunks_RequestsURLsInWindows(t *testing.T) {
	n := 2*downloadWindow + 7
	srv := newChunkTestServer()
	hashes := windowHashes(n)
	for _, h := range hashes {
		srv.put(h.String(), h[:8])
	}
	c := newDownloadClient(t, srv, fastRetry(3))

	got := newDeliveries()
	require.NoError(t, c.DownloadChunks(context.Background(), downloadScope(), hashes, got.sink))
	require.Len(t, got.seen, n)
	for _, h := range hashes {
		require.Equal(t, 1, got.seen[h])
		require.Equal(t, h[:8], got.data[h])
	}
	require.Equal(t, []int{downloadWindow, downloadWindow, 7}, srv.downloadCalls())
}

func TestDownloadChunks_FetchesNextWindowWhileTransferring(t *testing.T) {
	n := downloadWindow + 3
	srv := newChunkTestServer()
	hashes := windowHashes(n)
	for _, h := range hashes {
		srv.put(h.String(), []byte("x"))
	}

	var once sync.Once
	prefetched := make(chan bool, 1)
	srv.hookBeforeGet = func() {
		once.Do(func() {
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				if len(srv.downloadCalls()) >= 2 {
					prefetched <- true
					return
				}
				time.Sleep(5 * time.Millisecond)
			}
			prefetched <- false
		})
	}
	c := newDownloadClient(t, srv, fastRetry(3))

	require.NoError(t, c.DownloadChunks(context.Background(), downloadScope(), hashes, newDeliveries().sink))
	require.True(t, <-prefetched, "the second window's URLs must be requested before the first window finishes")
}

func TestDownloadChunks_UnknownChunkInLaterWindow(t *testing.T) {
	n := downloadWindow + 5
	srv := newChunkTestServer()
	hashes := windowHashes(n)
	for _, h := range hashes[:n-1] {
		srv.put(h.String(), []byte("x"))
	}
	c := newDownloadClient(t, srv, fastRetry(3))

	err := c.DownloadChunks(context.Background(), downloadScope(), hashes, newDeliveries().sink)
	require.Error(t, err)
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)
	require.Contains(t, err.Error(), hashes[n-1].String())
}

func TestDownloadChunks_PresignFailureInLaterWindowStopsTransfer(t *testing.T) {
	n := 2 * downloadWindow
	srv := newChunkTestServer()
	hashes := windowHashes(n)
	for _, h := range hashes {
		srv.put(h.String(), []byte("x"))
	}
	srv.downloadErrAfter = 1
	c := newDownloadClient(t, srv, fastRetry(3))

	done := make(chan error, 1)
	go func() {
		done <- c.DownloadChunks(context.Background(), downloadScope(), hashes, newDeliveries().sink)
	}()
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(20 * time.Second):
		t.Fatal("a failed presign request must not hang the transfer")
	}
}

func TestRunChunks_FeedErrorStopsWorkers(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	boom := fmt.Errorf("feed failed")

	var mu sync.Mutex
	ran := 0
	err := c.runChunks(context.Background(), func(_ context.Context, emit func(chunkTransfer) bool) error {
		for i := 0; i < 3; i++ {
			if !emit(chunkTransfer{hash: testChunkHash(byte(i + 1))}) {
				return nil
			}
		}
		return boom
	}, func(context.Context, chunkTransfer) (int64, error) {
		mu.Lock()
		ran++
		mu.Unlock()
		return 1, nil
	})
	require.ErrorIs(t, err, boom)
	mu.Lock()
	defer mu.Unlock()
	require.LessOrEqual(t, ran, 3)
}

func TestRetryPolicy_DelayGrowsWithJitter(t *testing.T) {
	p := retryPolicy{attempts: 3, backoff: 100 * time.Millisecond}
	for attempt := 0; attempt < 3; attempt++ {
		base := p.backoff << attempt
		for i := 0; i < 50; i++ {
			d := p.delay(attempt)
			require.GreaterOrEqual(t, d, base)
			require.LessOrEqual(t, d, base+base/4)
		}
	}
}

func TestSleepContext(t *testing.T) {
	require.NoError(t, sleepContext(context.Background(), time.Millisecond))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, sleepContext(ctx, time.Minute), context.Canceled)
}

func TestRetryableStatus(t *testing.T) {
	for status, want := range map[int]bool{
		200: false, 301: false, 400: false, 401: false, 403: false, 404: false,
		408: true, 429: true, 500: true, 502: true, 503: true, 504: true,
	} {
		require.Equal(t, want, retryableStatus(status), "status %d", status)
	}
}
