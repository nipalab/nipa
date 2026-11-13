package grpc

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

func throttleOnly(throttles int) retryPolicy {
	return retryPolicy{attempts: 1, backoff: time.Millisecond, throttleAttempts: throttles, maxThrottleWait: 5 * time.Millisecond}
}

func TestUploadChunks_RetriesThrottledPut(t *testing.T) {
	h := testChunkHash(0x21)
	srv := newChunkTestServer()
	srv.failPuts[h.String()] = 3
	srv.failPutStatus = http.StatusTooManyRequests
	srv.retryAfter = "1"
	c := newDownloadClient(t, srv, throttleOnly(5))
	limit := c.limiter.currentLimit()

	uploaded, _, err := c.UploadChunks(context.Background(), domain.ChunkScope{Org: "org", Project: "proj"}, []*serverDomain.ChunkData{
		{Hash: h, Data: []byte("throttled payload")},
	})
	require.NoError(t, err)
	require.Equal(t, 1, uploaded)
	require.Equal(t, 4, srv.putAttemptsFor(h.String()))
	data, ok := srv.storedChunk(h.String())
	require.True(t, ok)
	require.Equal(t, []byte("throttled payload"), data, "the body is sent again in full")
	require.Less(t, c.limiter.currentLimit(), limit, "throttling lowers the transfer concurrency")
}

func TestUploadChunks_RetriesTransientPut(t *testing.T) {
	h := testChunkHash(0x22)
	srv := newChunkTestServer()
	srv.failPuts[h.String()] = 2
	srv.failPutStatus = http.StatusServiceUnavailable
	c := newDownloadClient(t, srv, fastRetry(3))

	_, _, err := c.UploadChunks(context.Background(), domain.ChunkScope{Org: "org", Project: "proj"}, []*serverDomain.ChunkData{
		{Hash: h, Data: []byte("x")},
	})
	require.NoError(t, err)
	require.Equal(t, 3, srv.putAttemptsFor(h.String()))
}

func TestUploadChunks_ThrottleBudgetExhausted(t *testing.T) {
	h := testChunkHash(0x23)
	srv := newChunkTestServer()
	srv.failPuts[h.String()] = 100
	srv.failPutStatus = http.StatusTooManyRequests
	c := newDownloadClient(t, srv, throttleOnly(2))

	_, _, err := c.UploadChunks(context.Background(), domain.ChunkScope{Org: "org", Project: "proj"}, []*serverDomain.ChunkData{
		{Hash: h, Data: []byte("x")},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "429")
	require.Equal(t, 3, srv.putAttemptsFor(h.String()))
}

func TestUploadChunks_DoesNotRetryForbiddenPut(t *testing.T) {
	h := testChunkHash(0x24)
	srv := newChunkTestServer()
	srv.failPuts[h.String()] = 100
	srv.failPutStatus = http.StatusForbidden
	c := newDownloadClient(t, srv, fastRetry(5))

	_, _, err := c.UploadChunks(context.Background(), domain.ChunkScope{Org: "org", Project: "proj"}, []*serverDomain.ChunkData{
		{Hash: h, Data: []byte("x")},
	})
	require.Error(t, err)
	require.Equal(t, 1, srv.putAttemptsFor(h.String()))
}

func TestDownloadChunks_ThrottleUsesSeparateBudget(t *testing.T) {
	h := testChunkHash(0x25)
	srv := newChunkTestServer()
	srv.put(h.String(), []byte("payload"))
	srv.failGets[h.String()] = 6
	srv.failGetStatus = http.StatusTooManyRequests
	srv.retryAfter = "1"
	c := newDownloadClient(t, srv, throttleOnly(6))

	got := newDeliveries()
	require.NoError(t, c.DownloadChunks(context.Background(), downloadScope(), []serverDomain.Hash{h}, got.sink))
	require.Equal(t, 7, srv.attemptsFor(h.String()))
	require.Equal(t, 1, got.seen[h])
}

func TestRetryPolicy_ThrottleDelay(t *testing.T) {
	p := retryPolicy{backoff: 100 * time.Millisecond, maxThrottleWait: time.Second}
	for i := 0; i < 50; i++ {
		d := p.throttleDelay(0, 400*time.Millisecond)
		require.GreaterOrEqual(t, d, 400*time.Millisecond)
		require.LessOrEqual(t, d, 600*time.Millisecond)

		d = p.throttleDelay(1, 0)
		require.GreaterOrEqual(t, d, 200*time.Millisecond)
		require.LessOrEqual(t, d, 300*time.Millisecond)

		d = p.throttleDelay(0, time.Minute)
		require.GreaterOrEqual(t, d, time.Second)
		require.LessOrEqual(t, d, 1500*time.Millisecond)

		d = p.throttleDelay(60, 0)
		require.LessOrEqual(t, d, 1500*time.Millisecond)
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for value, want := range map[string]time.Duration{
		"":                              0,
		"3":                             3 * time.Second,
		" 12 ":                          12 * time.Second,
		"0":                             0,
		"-4":                            0,
		"soon":                          0,
		"Thu, 01 Jan 2026 12:00:05 GMT": 5 * time.Second,
		"Thu, 01 Jan 2026 11:59:00 GMT": 0,
	} {
		require.Equal(t, want, parseRetryAfter(value, now), "value %q", value)
	}
}

func TestRateLimitWait(t *testing.T) {
	wait, ok := rateLimitWait(status.Error(codes.ResourceExhausted, "rate limit exceeded, retry in 7s"))
	require.True(t, ok)
	require.Equal(t, 7*time.Second, wait)

	wait, ok = rateLimitWait(status.Error(codes.ResourceExhausted, "rate limit exceeded"))
	require.True(t, ok)
	require.Zero(t, wait)

	_, ok = rateLimitWait(status.Error(codes.ResourceExhausted, "storage quota exceeded"))
	require.False(t, ok, "a quota failure is not throttling")
	_, ok = rateLimitWait(status.Error(codes.Unavailable, "rate limit exceeded, retry in 1s"))
	require.False(t, ok)
	_, ok = rateLimitWait(errors.New("rate limit exceeded"))
	require.False(t, ok)
	_, ok = rateLimitWait(nil)
	require.False(t, ok)
}

func interceptorCalls(t *testing.T, policy retryPolicy, results ...error) (int, error) {
	t.Helper()
	return interceptorMethodCalls(t, "/greet.NipaService/GetChunkUploadUrls", policy, results...)
}

func interceptorMethodCalls(t *testing.T, method string, policy retryPolicy, results ...error) (int, error) {
	t.Helper()
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	c.retry = policy
	calls := 0
	invoker := func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
		err := results[min(calls, len(results)-1)]
		calls++
		return err
	}
	err := c.unaryAuthInterceptor()(context.Background(), method, nil, nil, nil, invoker)
	return calls, err
}

func TestUnaryInterceptor_RetriesRateLimitedCalls(t *testing.T) {
	limited := status.Error(codes.ResourceExhausted, "rate limit exceeded, retry in 1s")
	calls, err := interceptorCalls(t, throttleOnly(5), limited, limited, nil)
	require.NoError(t, err)
	require.Equal(t, 3, calls)
}

func TestUnaryInterceptor_GivesUpAfterThrottleBudget(t *testing.T) {
	limited := status.Error(codes.ResourceExhausted, "rate limit exceeded, retry in 1s")
	calls, err := interceptorCalls(t, throttleOnly(2), limited)
	require.Equal(t, codes.ResourceExhausted, status.Code(err))
	require.Equal(t, 3, calls)
}

func TestUnaryInterceptor_DoesNotRetryQuota(t *testing.T) {
	quota := status.Error(codes.ResourceExhausted, "storage quota exceeded")
	calls, err := interceptorCalls(t, throttleOnly(5), quota, nil)
	require.Equal(t, codes.ResourceExhausted, status.Code(err))
	require.Equal(t, 1, calls)
}

func TestUnaryInterceptor_ThrottleWaitHonoursCancel(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	c.retry = retryPolicy{attempts: 1, backoff: time.Minute, throttleAttempts: 5, maxThrottleWait: time.Minute}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	invoker := func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error {
		return status.Error(codes.ResourceExhausted, "rate limit exceeded, retry in 30s")
	}
	start := time.Now()
	err := c.unaryAuthInterceptor()(ctx, "/greet.NipaService/GetChunkUploadUrls", nil, nil, nil, invoker)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(start), 5*time.Second)
}

func TestConcurrencyTuner_ThrottleHalvesLimit(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	c.limiter.setLimit(32)
	c.recordTransfer(0, errThrottled)
	require.Equal(t, 16, c.limiter.currentLimit())
}

func (s *chunkTestServer) putAttemptsFor(hash string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.putAttempts[hash]
}

func TestUnaryInterceptor_RetriesThrottledTokenRefresh(t *testing.T) {
	limited := status.Error(codes.ResourceExhausted, "rate limit exceeded, retry in 1s")
	calls, err := interceptorMethodCalls(t, "/greet.NipaService/LoginWithRefreshToken", throttleOnly(5), limited, nil)
	require.NoError(t, err)
	require.Equal(t, 2, calls)
}

func TestUnaryInterceptor_DoesNotRetryThrottledCredentialLogins(t *testing.T) {
	limited := status.Error(codes.ResourceExhausted, "rate limit exceeded, retry in 1s")
	for _, method := range []string{
		"/greet.NipaService/LoginWithUsernamePassword",
		"/greet.NipaService/BeginLoginSSHKey",
		"/greet.NipaService/CompleteLoginSSHKey",
		"/greet.NipaService/CompleteLoginMFA",
	} {
		calls, err := interceptorMethodCalls(t, method, throttleOnly(5), limited, nil)
		require.Equal(t, codes.ResourceExhausted, status.Code(err), method)
		require.Equal(t, 1, calls, method)
	}
}
