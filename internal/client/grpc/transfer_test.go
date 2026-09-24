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
	l := newTransferLimiter(2)
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
	l := newTransferLimiter(4)
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
	l := newTransferLimiter(2)
	ctx := context.Background()
	require.NoError(t, l.acquire(ctx))
	require.NoError(t, l.acquire(ctx))
	l.setLimit(4)
	require.NoError(t, l.acquire(ctx))
	require.NoError(t, l.acquire(ctx))
	require.Equal(t, 4, l.currentLimit())
}

func TestTransferLimiter_ContextCancel(t *testing.T) {
	l := newTransferLimiter(1)
	require.NoError(t, l.acquire(context.Background()))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, l.acquire(ctx), context.Canceled)
}

func TestConcurrencyTuner_IncreaseOnImprovement(t *testing.T) {
	tun := newConcurrencyTuner(4, 64, 16)
	base := time.Unix(0, 0)

	require.Equal(t, 16, tun.observe(16, 8<<20, base, nil))
	limit := tun.observe(16, 8<<20, base.Add(time.Second), nil)
	require.Equal(t, 18, limit)
}

func TestConcurrencyTuner_DecreaseOnRegression(t *testing.T) {
	tun := newConcurrencyTuner(4, 64, 16)
	base := time.Unix(0, 0)

	tun.observe(16, 8<<20, base, nil)
	tun.observe(16, 8<<20, base.Add(time.Second), nil)

	require.Equal(t, 18, tun.observe(18, 8<<20, base.Add(2*time.Second), nil))
	require.Equal(t, 14, tun.observe(18, 8<<20, base.Add(5*time.Second), nil))
}

func TestConcurrencyTuner_HalvesOnError(t *testing.T) {
	tun := newConcurrencyTuner(4, 64, 16)
	require.Equal(t, 8, tun.observe(16, 1<<20, time.Unix(0, 0), errors.New("boom")))
}

func TestConcurrencyTuner_Clamps(t *testing.T) {
	tun := newConcurrencyTuner(4, 64, 16)
	require.Equal(t, 4, tun.observe(4, 1<<20, time.Unix(0, 0), errors.New("boom")))
	require.Equal(t, 64, tun.increase(64))
}

func TestConcurrencyTuner_WindowBelowThreshold(t *testing.T) {
	tun := newConcurrencyTuner(4, 64, 16)
	require.Equal(t, 16, tun.observe(16, 1<<20, time.Unix(0, 0), nil))
	require.Equal(t, 16, tun.observe(16, 1<<20, time.Unix(0, 1), nil))
}

func TestForEachChunk_RespectsLimit(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{})
	c.SetUploadWorkers(3)

	jobs := make([]chunkTransfer, 50)
	var active, maxActive int32
	var mu sync.Mutex
	err := c.forEachChunk(context.Background(), jobs, func(context.Context, chunkTransfer) (int64, error) {
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
	err := c.forEachChunk(context.Background(), jobs, func(context.Context, chunkTransfer) (int64, error) {
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
	require.NoError(t, c.forEachChunk(context.Background(), nil, func(context.Context, chunkTransfer) (int64, error) {
		t.Fatal("fn should not be called")
		return 0, nil
	}))
}

func TestNewClient_DefaultTunerEnabled(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{})
	require.NotNil(t, c.tuner)
	require.Equal(t, defaultUploadWorkers, c.uploadWorkers)
	require.Equal(t, defaultUploadWorkers, c.limiter.currentLimit())
	require.NotNil(t, c.http)
}

func TestNewClient_PinnedUploadWorkers(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{}, WithUploadWorkers(7))
	require.Equal(t, 7, c.uploadWorkers)
	require.Nil(t, c.tuner)
	require.Equal(t, 7, c.limiter.currentLimit())
}

func TestNewClient_ClampsUploadWorkers(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{}, WithUploadWorkers(1000))
	require.Equal(t, maxUploadWorkers, c.uploadWorkers)

	c = NewClient(NewTransport(), &stubSession{}, WithUploadWorkers(0))
	require.Equal(t, defaultUploadWorkers, c.uploadWorkers)
}

func TestClient_SetUploadWorkers(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{})
	c.SetUploadWorkers(5)
	require.Equal(t, 5, c.uploadWorkers)
	require.Nil(t, c.tuner)
	require.Equal(t, 5, c.limiter.currentLimit())
}

func TestClient_WithHTTPClient(t *testing.T) {
	custom := &http.Client{}
	c := NewClient(NewTransport(), &stubSession{}, WithHTTPClient(custom))
	require.Same(t, custom, c.http)

	c = NewClient(NewTransport(), &stubSession{}, WithHTTPClient(nil))
	require.NotNil(t, c.http)
	require.NotSame(t, custom, c.http)
}
