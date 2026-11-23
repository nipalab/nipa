package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/chunkproof"
	"github.com/nipalab/nipa/internal/chunkurl"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/storage"
	"github.com/nipalab/nipa/internal/usecase"
)

func signedUpload(hash domain.Hash, size int64) string {
	return chunkurl.UploadPath(testChunkSigningKey, "default", "default", hash.String(), size, time.Now().Add(time.Hour).Unix())
}

func TestChunkHandler_PutChunkReturnsAReceipt(t *testing.T) {
	env := newHandlerTestEnv(t)
	h := NewChunkHandler(env.chunkUc, env.registry.common)
	data := []byte("chunk payload")
	hash := chunker.Sum(data)

	appCtx, recorder := newChunkAppContext(http.MethodPut, signedUpload(hash, int64(len(data))), data, hash.String())
	h.PutChunk(appCtx)

	require.Equal(t, http.StatusNoContent, recorder.Code)
	receipt := recorder.Header().Get(chunkproof.ReceiptHeader)
	require.NotEmpty(t, receipt)
	_, project, err := env.registry.common.ResolveBySlug(context.Background(), "default", "default")
	require.NoError(t, err)
	size, err := chunkproof.VerifyReceipt(testChunkSigningKey, project.ID, hash, receipt, time.Now())
	require.NoError(t, err)
	require.Equal(t, int64(len(data)), size)
}

func TestChunkHandler_PutChunkRejectionsWriteNothing(t *testing.T) {
	data := []byte("chunk payload")
	hash := chunker.Sum(data)
	size := int64(len(data))
	cases := map[string]struct {
		path string
		body []byte
		want int
	}{
		"hash mismatch": {signedUpload(hash, size), []byte("other bytes!!"), http.StatusBadRequest},
		"size mismatch": {signedUpload(hash, size+1), data, http.StatusBadRequest},
		"bad signature": {signedUpload(hash, size) + "0", data, http.StatusForbidden},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			env := newHandlerTestEnv(t)
			h := NewChunkHandler(env.chunkUc, env.registry.common)

			appCtx, recorder := newChunkAppContext(http.MethodPut, tc.path, tc.body, hash.String())
			h.PutChunk(appCtx)

			require.Equal(t, tc.want, appCtx.statusCode)
			require.Empty(t, recorder.Header().Get(chunkproof.ReceiptHeader))
			exists, err := env.chunkStore.Exists(context.Background(), defaultProjectID(t, env), hash)
			require.NoError(t, err)
			require.False(t, exists)
		})
	}
}

// gatedStore holds every Put until the gate opens and records how many ran at
// once.
type gatedStore struct {
	*storage.LocalStore
	gate     chan struct{}
	project  snow.ID
	inFlight atomic.Int64
	peak     atomic.Int64
}

func (s *gatedStore) Put(ctx context.Context, projectID snow.ID, hash domain.Hash, data []byte) error {
	n := s.inFlight.Add(1)
	defer s.inFlight.Add(-1)
	for {
		peak := s.peak.Load()
		if n <= peak || s.peak.CompareAndSwap(peak, n) {
			break
		}
	}
	<-s.gate
	return s.LocalStore.Put(ctx, projectID, hash, data)
}

func newGatedHandler(t *testing.T, budget int64) (*ChunkHandler, *gatedStore) {
	t.Helper()
	env := newHandlerTestEnv(t)
	local, err := storage.NewLocalStore(t.TempDir())
	require.NoError(t, err)
	store := &gatedStore{LocalStore: local, gate: make(chan struct{}), project: defaultProjectID(t, env)}
	chunks := usecase.NewChunk(nil, store, usecase.ChunkTransferConfig{
		SigningKey: testChunkSigningKey,
		PresignTTL: time.Hour,
	}).WithProxyBuffer(budget)
	return NewChunkHandler(chunks, env.registry.common), store
}

func putChunkWithContext(h *ChunkHandler, ctx context.Context, data []byte) (*fakeAppContext, string) {
	hash := chunker.Sum(data)
	appCtx, recorder := newChunkAppContext(http.MethodPut, signedUpload(hash, int64(len(data))), data, hash.String())
	appCtx.ctx = ctx
	h.PutChunk(appCtx)
	if appCtx.statusCode == 0 {
		appCtx.statusCode = recorder.Code
	}
	return appCtx, recorder.Header().Get(chunkproof.ReceiptHeader)
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	require.Eventually(t, cond, 5*time.Second, time.Millisecond)
}

func TestChunkHandler_PutChunkByteBudgetBoundsBodiesInFlight(t *testing.T) {
	const size = 1024
	h, store := newGatedHandler(t, 2*size)

	var wg sync.WaitGroup
	codes := make([]int, 6)
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			data := make([]byte, size)
			data[0] = byte(i)
			appCtx, _ := putChunkWithContext(h, context.Background(), data)
			codes[i] = appCtx.statusCode
		}()
	}
	waitFor(t, func() bool { return store.inFlight.Load() == 2 })
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, int64(2), store.inFlight.Load(), "a third body must wait for budget")
	close(store.gate)
	wg.Wait()

	require.Equal(t, int64(2), store.peak.Load())
	for _, code := range codes {
		require.Equal(t, http.StatusNoContent, code)
	}
}

func TestChunkHandler_PutChunkByteBudgetReleasedOnErrorAndCancel(t *testing.T) {
	const size = 1024
	h, store := newGatedHandler(t, size)
	data := make([]byte, size)
	hash := chunker.Sum(data)

	// A rejected body gives its bytes back.
	appCtx, recorder := newChunkAppContext(http.MethodPut, signedUpload(hash, size), make([]byte, size-1), hash.String())
	h.PutChunk(appCtx)
	require.Equal(t, http.StatusBadRequest, appCtx.statusCode)
	require.Empty(t, recorder.Header().Get(chunkproof.ReceiptHeader))

	// Hold the whole budget with an upload stuck in the store.
	held := make(chan int, 1)
	go func() {
		appCtx, _ := putChunkWithContext(h, context.Background(), data)
		held <- appCtx.statusCode
	}()
	waitFor(t, func() bool { return store.inFlight.Load() == 1 })

	// A request that gives up while waiting for budget fails and writes
	// nothing.
	ctx, cancel := context.WithCancel(context.Background())
	waiting := make(chan *fakeAppContext, 1)
	other := make([]byte, size)
	other[0] = 1
	go func() {
		appCtx, _ := putChunkWithContext(h, ctx, other)
		waiting <- appCtx
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	cancelled := <-waiting
	require.Equal(t, http.StatusServiceUnavailable, cancelled.statusCode)
	exists, err := store.Exists(context.Background(), store.project, chunker.Sum(other))
	require.NoError(t, err)
	require.False(t, exists)

	close(store.gate)
	require.Equal(t, http.StatusNoContent, <-held)

	// Everything was given back: a new upload gets the whole budget at once.
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	final, receipt := putChunkWithContext(h, ctx, other)
	require.Equal(t, http.StatusNoContent, final.statusCode)
	require.NotEmpty(t, receipt)
}

type failingBody struct{}

func (failingBody) Read([]byte) (int, error) { return 0, context.DeadlineExceeded }

func TestChunkHandler_PutChunkBodyReadError(t *testing.T) {
	env := newHandlerTestEnv(t)
	h := NewChunkHandler(env.chunkUc, env.registry.common)
	data := []byte("chunk payload")
	hash := chunker.Sum(data)

	request := httptest.NewRequest(http.MethodPut, signedUpload(hash, int64(len(data))), failingBody{})
	request.ContentLength = -1
	appCtx, recorder := chunkAppContext(request, hash.String())
	h.PutChunk(appCtx)

	require.Equal(t, http.StatusBadRequest, appCtx.statusCode)
	require.Empty(t, recorder.Header().Get(chunkproof.ReceiptHeader))
}
