package grpc

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTransferLimiter_RespectsLimit(t *testing.T) {
	l := newTransferLimiter(2, maxUploadWorkers)
	ctx := context.Background()
	require.NoError(t, l.acquire(ctx))
	require.NoError(t, l.acquire(ctx))

	acquired := make(chan struct{})
	go func() {
		if err := l.acquire(ctx); err == nil {
			close(acquired)
		}
	}()
	select {
	case <-acquired:
		t.Fatal("third acquire should block while the limit is 2")
	case <-time.After(20 * time.Millisecond):
	}

	l.release()
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("acquire should proceed after a release")
	}
}

func TestTransferLimiter_LowerLimitConverges(t *testing.T) {
	l := newTransferLimiter(4, maxUploadWorkers)
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		require.NoError(t, l.acquire(ctx))
	}
	l.setLimit(2)
	for i := 0; i < 4; i++ {
		l.release()
	}
	require.Equal(t, 2, l.currentLimit())
	require.NoError(t, l.acquire(ctx))
	require.NoError(t, l.acquire(ctx))

	acquired := make(chan struct{})
	go func() {
		if err := l.acquire(ctx); err == nil {
			close(acquired)
		}
	}()
	select {
	case <-acquired:
		t.Fatal("acquire beyond the lowered limit should block")
	case <-time.After(20 * time.Millisecond):
	}
}

func TestTransferLimiter_RaiseLimitTopsUp(t *testing.T) {
	l := newTransferLimiter(2, maxUploadWorkers)
	ctx := context.Background()
	require.NoError(t, l.acquire(ctx))
	require.NoError(t, l.acquire(ctx))
	l.setLimit(4)
	require.NoError(t, l.acquire(ctx))
	require.NoError(t, l.acquire(ctx))
	require.Equal(t, 4, l.currentLimit())
}

func TestTransferLimiter_ContextCancel(t *testing.T) {
	l := newTransferLimiter(1, maxUploadWorkers)
	require.NoError(t, l.acquire(context.Background()))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, l.acquire(ctx), context.Canceled)
}

func TestForEachChunk_RespectsLimit(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{})
	c.SetUploadWorkers(3)

	jobs := make([]chunkTransfer, 50)
	var active, maxActive int32
	var mu sync.Mutex
	err := c.forEachChunk(context.Background(), c.uploads, jobs, func(context.Context, chunkTransfer) (int64, error) {
		current := atomic.AddInt32(&active, 1)
		mu.Lock()
		if current > maxActive {
			maxActive = current
		}
		mu.Unlock()
		time.Sleep(time.Millisecond)
		atomic.AddInt32(&active, -1)
		return 1, nil
	})
	require.NoError(t, err)
	mu.Lock()
	defer mu.Unlock()
	require.Greater(t, maxActive, int32(0))
	require.LessOrEqual(t, maxActive, int32(3))
}

func TestForEachChunk_StopsOnFirstError(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{})
	c.SetUploadWorkers(2)

	wantErr := errors.New("boom")
	var calls int32
	jobs := make([]chunkTransfer, 100)
	err := c.forEachChunk(context.Background(), c.uploads, jobs, func(context.Context, chunkTransfer) (int64, error) {
		if atomic.AddInt32(&calls, 1) == 3 {
			return 0, wantErr
		}
		time.Sleep(5 * time.Millisecond)
		return 1, nil
	})
	require.ErrorIs(t, err, wantErr)
	require.Less(t, atomic.LoadInt32(&calls), int32(len(jobs)))
}

func TestForEachChunk_Empty(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{})
	require.NoError(t, c.forEachChunk(context.Background(), c.uploads, nil, func(context.Context, chunkTransfer) (int64, error) {
		t.Fatal("fn should not be called")
		return 0, nil
	}))
}

func TestNewClient_DefaultTunersEnabled(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{})
	require.NotNil(t, c.uploads.tuner.Load())
	require.NotNil(t, c.downloads.tuner.Load())
	require.Equal(t, defaultUploadWorkers, c.uploads.limiter.currentLimit())
	require.Equal(t, defaultDownloadWorkers, c.downloads.limiter.currentLimit())
	require.NotNil(t, c.http)
}

func TestNewClient_PinnedWorkers(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{}, WithUploadWorkers(7))
	require.Nil(t, c.uploads.tuner.Load())
	require.Equal(t, 7, c.uploads.limiter.currentLimit())
	require.NotNil(t, c.downloads.tuner.Load(), "pinning uploads keeps downloads tuned")

	c = NewClient(NewTransport(), &stubSession{}, WithDownloadWorkers(100))
	require.Nil(t, c.downloads.tuner.Load())
	require.Equal(t, 100, c.downloads.limiter.currentLimit())
	require.NotNil(t, c.uploads.tuner.Load(), "pinning downloads keeps uploads tuned")
}

func TestNewClient_ClampsWorkers(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{}, WithUploadWorkers(1000), WithDownloadWorkers(1000))
	require.Equal(t, maxUploadWorkers, c.uploads.limiter.currentLimit())
	require.Equal(t, maxDownloadWorkers, c.downloads.limiter.currentLimit())

	c = NewClient(NewTransport(), &stubSession{}, WithUploadWorkers(0), WithDownloadWorkers(-3))
	require.Equal(t, defaultUploadWorkers, c.uploads.limiter.currentLimit())
	require.Equal(t, defaultDownloadWorkers, c.downloads.limiter.currentLimit())
}

func TestClient_SetWorkers(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{})
	c.SetUploadWorkers(5)
	require.Nil(t, c.uploads.tuner.Load())
	require.Equal(t, 5, c.uploads.limiter.currentLimit())

	c.SetDownloadWorkers(200)
	require.Nil(t, c.downloads.tuner.Load())
	require.Equal(t, 200, c.downloads.limiter.currentLimit())

	c.uploads.record(1<<30, errors.New("ignored"))
	require.Equal(t, 5, c.uploads.limiter.currentLimit(), "a pinned limit is not tuned")
}

func TestDefaultHTTPClient_PoolsTheLargerMaximum(t *testing.T) {
	transport, ok := defaultHTTPClient().Transport.(*http.Transport)
	require.True(t, ok)
	require.Equal(t, maxDownloadWorkers, transport.MaxConnsPerHost)
	require.Equal(t, maxDownloadWorkers, transport.MaxIdleConnsPerHost)
	require.Equal(t, maxDownloadWorkers, transport.MaxIdleConns)
}

// TestClient_UploadAndDownloadLimitsAreIndependent runs a push and a clone at
// once: each direction fills its own limit, and neither caps the other.
func TestClient_UploadAndDownloadLimitsAreIndependent(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{}, WithUploadWorkers(8), WithDownloadWorkers(40))

	run := func(pool *transferPool, want int32) (int32, error) {
		var active, peak atomic.Int32
		release := make(chan struct{})
		var once sync.Once
		err := c.forEachChunk(context.Background(), pool, make([]chunkTransfer, 200), func(ctx context.Context, _ chunkTransfer) (int64, error) {
			n := active.Add(1)
			defer active.Add(-1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			if n >= want {
				once.Do(func() { close(release) })
			}
			select {
			case <-release:
			case <-time.After(2 * time.Second):
			}
			return 1, nil
		})
		return peak.Load(), err
	}

	var wg sync.WaitGroup
	var upPeak, downPeak int32
	var upErr, downErr error
	wg.Add(2)
	go func() { defer wg.Done(); upPeak, upErr = run(c.uploads, 8) }()
	go func() { defer wg.Done(); downPeak, downErr = run(c.downloads, 40) }()
	wg.Wait()
	require.NoError(t, upErr)
	require.NoError(t, downErr)
	require.Equal(t, int32(8), upPeak)
	require.Equal(t, int32(40), downPeak)
}

func TestClient_WithHTTPClient(t *testing.T) {
	custom := &http.Client{}
	c := NewClient(NewTransport(), &stubSession{}, WithHTTPClient(custom))
	require.Same(t, custom, c.http)

	c = NewClient(NewTransport(), &stubSession{}, WithHTTPClient(nil))
	require.NotNil(t, c.http)
	require.NotSame(t, custom, c.http)
}
