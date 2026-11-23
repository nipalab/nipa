package handler

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunkbundle"
	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/chunkproof"
	"github.com/nipalab/nipa/internal/chunkurl"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/storage"
	"github.com/nipalab/nipa/internal/usecase"
)

func encodeBundle(t *testing.T, contents ...[]byte) []byte {
	t.Helper()
	entries := make([]chunkbundle.Entry, 0, len(contents))
	for _, data := range contents {
		entries = append(entries, chunkbundle.Entry{Hash: chunker.Sum(data), Data: data})
	}
	body, err := io.ReadAll(chunkbundle.NewEncoder(entries))
	require.NoError(t, err)
	return body
}

func signedBundle(maxSize int64, maxCount int) string {
	return chunkurl.BundlePath(testChunkSigningKey, "default", "default", maxSize, maxCount, time.Now().Add(time.Hour).Unix())
}

// countingStore is the local store with its writes counted.
type countingStore struct {
	*storage.LocalStore
	puts atomic.Int64
}

func (s *countingStore) Put(ctx context.Context, projectID snow.ID, hash domain.Hash, data []byte) error {
	s.puts.Add(1)
	return s.LocalStore.Put(ctx, projectID, hash, data)
}

// recordingGuard records the hashes whose GC marks were cleared and how many
// store writes had happened by then.
type recordingGuard struct {
	store     *countingStore
	protected []domain.Hash
	putsSeen  int64
}

func (g *recordingGuard) Protect(_ context.Context, _ snow.ID, hashes []domain.Hash) error {
	g.protected = append(g.protected, hashes...)
	g.putsSeen = g.store.puts.Load()
	return nil
}

func newCountingHandler(t *testing.T) (*ChunkHandler, *countingStore, *recordingGuard, snow.ID) {
	t.Helper()
	env := newHandlerTestEnv(t)
	local, err := storage.NewLocalStore(t.TempDir())
	require.NoError(t, err)
	store := &countingStore{LocalStore: local}
	guard := &recordingGuard{store: store}
	chunks := usecase.NewChunk(nil, store, usecase.ChunkTransferConfig{SigningKey: testChunkSigningKey, PresignTTL: time.Hour}).
		WithGCGuard(guard)
	return NewChunkHandler(chunks, env.registry.common), store, guard, defaultProjectID(t, env)
}

func TestChunkHandler_PutBundleStoresEveryEntryAndReturnsReceipts(t *testing.T) {
	h, store, guard, project := newCountingHandler(t)
	a, b := []byte("first chunk"), []byte("second chunk")
	body := encodeBundle(t, a, b, a)

	appCtx, _ := newChunkAppContext(http.MethodPut, signedBundle(1<<20, 3), body, "")
	h.PutBundle(appCtx)

	require.Equal(t, http.StatusOK, appCtx.statusCode)
	receipts, ok := appCtx.response.([]BundleReceipt)
	require.True(t, ok)
	require.Len(t, receipts, 2, "a repeated entry is stored once")
	require.Equal(t, int64(2), store.puts.Load(), "one store write per entry")
	require.Equal(t, []domain.Hash{chunker.Sum(a), chunker.Sum(b)}, guard.protected, "GC marks cleared for the entries")
	require.Zero(t, guard.putsSeen, "marks cleared before any write")
	for i, data := range [][]byte{a, b} {
		hash := chunker.Sum(data)
		require.Equal(t, hash.String(), receipts[i].Hash)
		size, location, err := chunkproof.VerifyReceipt(testChunkSigningKey, project, hash, receipts[i].Receipt, time.Now())
		require.NoError(t, err)
		require.Equal(t, int64(len(data)), size)
		require.Equal(t, chunkproof.Loose(), location)
		stored, err := store.Get(context.Background(), project, hash)
		require.NoError(t, err)
		require.Equal(t, data, stored)
	}
}

func TestChunkHandler_PutBundleRejectionsWriteNothing(t *testing.T) {
	a, b := []byte("first chunk"), []byte("second chunk")
	good := encodeBundle(t, a, b)
	tampered := bytes.Clone(good)
	tampered[len(tampered)-1] ^= 1
	exp := time.Now().Add(time.Hour).Unix()

	cases := map[string]struct {
		path          string
		body          []byte
		contentLength int64
		want          int
	}{
		"bad entry hash":       {signedBundle(1<<20, 10), tampered, int64(len(tampered)), http.StatusBadRequest},
		"truncated body":       {signedBundle(1<<20, 10), good[:len(good)-4], int64(len(good)), http.StatusBadRequest},
		"count over the limit": {signedBundle(1<<20, 1), good, int64(len(good)), http.StatusBadRequest},
		"size over the limit":  {signedBundle(int64(len(good))-1, 10), good, int64(len(good)), http.StatusBadRequest},
		"no content length":    {signedBundle(1<<20, 10), good, -1, http.StatusBadRequest},
		"empty body":           {signedBundle(1<<20, 10), nil, 0, http.StatusBadRequest},
		"bad length":           {signedBundle(1<<20, 10), append(good[:32:32], 0xff, 0xff, 0xff, 0xff), 36, http.StatusBadRequest},
		"bad signature":        {signedBundle(1<<20, 10) + "0", good, int64(len(good)), http.StatusForbidden},
		"expired":              {chunkurl.BundlePath(testChunkSigningKey, "default", "default", 1<<20, 10, time.Now().Add(-time.Minute).Unix()), good, int64(len(good)), http.StatusForbidden},
		"other project":        {chunkurl.BundlePath(testChunkSigningKey, "default", "other", 1<<20, 10, exp), good, int64(len(good)), http.StatusForbidden},
		"chunk url signature":  {"/api/chunk-bundles/default/default?size=1048576&count=10&exp=" + strconv.FormatInt(exp, 10) + "&sig=" + chunkurl.Sign(testChunkSigningKey, "default", "default", "", "get", 1<<20, exp), good, int64(len(good)), http.StatusForbidden},
		"malformed limits":     {"/api/chunk-bundles/default/default?size=x&count=10&exp=1&sig=a", good, int64(len(good)), http.StatusBadRequest},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h, store, guard, _ := newCountingHandler(t)
			request := httptest.NewRequest(http.MethodPut, tc.path, bytes.NewReader(tc.body))
			request.ContentLength = tc.contentLength
			appCtx, _ := chunkAppContext(request, "")
			h.PutBundle(appCtx)

			require.Equal(t, tc.want, appCtx.statusCode)
			_, isReceipts := appCtx.response.([]BundleReceipt)
			require.False(t, isReceipts, "no receipts")
			require.Zero(t, store.puts.Load(), "nothing is written")
			require.Empty(t, guard.protected)
		})
	}
}

func TestChunkHandler_PutBundleEntryOverTheChunkMaximum(t *testing.T) {
	h, store, _, _ := newCountingHandler(t)
	// A header announcing one byte more than the largest chunk.
	header := make([]byte, chunkbundle.HeaderSize)
	length := uint32(chunker.MaxChunkSize() + 1)
	header[32], header[33], header[34], header[35] = byte(length>>24), byte(length>>16), byte(length>>8), byte(length)
	body := append(header, make([]byte, 8)...)

	appCtx, _ := newChunkAppContext(http.MethodPut, signedBundle(1<<30, 1), body, "")
	h.PutBundle(appCtx)

	require.Equal(t, http.StatusBadRequest, appCtx.statusCode)
	require.Zero(t, store.puts.Load())
}

type failingBody struct{}

func (failingBody) Read([]byte) (int, error) { return 0, context.DeadlineExceeded }

func TestChunkHandler_PutBundleBodyReadError(t *testing.T) {
	h, store, _, _ := newCountingHandler(t)
	request := httptest.NewRequest(http.MethodPut, signedBundle(1<<20, 10), failingBody{})
	request.ContentLength = 100
	appCtx, _ := chunkAppContext(request, "")
	h.PutBundle(appCtx)

	require.Equal(t, http.StatusBadRequest, appCtx.statusCode)
	require.Zero(t, store.puts.Load())
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

// putBundleWithContext uploads a bundle of one chunk.
func putBundleWithContext(t *testing.T, h *ChunkHandler, ctx context.Context, data []byte) *fakeAppContext {
	body := encodeBundle(t, data)
	appCtx, _ := newChunkAppContext(http.MethodPut, signedBundle(1<<20, 1), body, "")
	appCtx.ctx = ctx
	h.PutBundle(appCtx)
	return appCtx
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	require.Eventually(t, cond, 5*time.Second, time.Millisecond)
}

// bundleBytes is the body size of a bundle holding one chunk of size bytes.
func bundleBytes(size int) int64 {
	return chunkbundle.EncodedSize(size)
}

func TestChunkHandler_PutBundleByteBudgetBoundsBodiesInFlight(t *testing.T) {
	const size = 1024
	h, store := newGatedHandler(t, 2*bundleBytes(size))

	var wg sync.WaitGroup
	codes := make([]int, 6)
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			data := make([]byte, size)
			data[0] = byte(i)
			codes[i] = putBundleWithContext(t, h, context.Background(), data).statusCode
		}()
	}
	waitFor(t, func() bool { return store.inFlight.Load() == 2 })
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, int64(2), store.inFlight.Load(), "a third body must wait for budget")
	close(store.gate)
	wg.Wait()

	require.Equal(t, int64(2), store.peak.Load())
	for _, code := range codes {
		require.Equal(t, http.StatusOK, code)
	}
}

func TestChunkHandler_PutBundleByteBudgetReleasedOnErrorAndCancel(t *testing.T) {
	const size = 1024
	h, store := newGatedHandler(t, bundleBytes(size))
	data := make([]byte, size)

	// A rejected body gives its bytes back.
	body := encodeBundle(t, data)
	appCtx, _ := newChunkAppContext(http.MethodPut, signedBundle(1<<20, 1), body[:len(body)-1], "")
	h.PutBundle(appCtx)
	require.Equal(t, http.StatusBadRequest, appCtx.statusCode)

	// Hold the whole budget with an upload stuck in the store.
	held := make(chan int, 1)
	go func() {
		held <- putBundleWithContext(t, h, context.Background(), data).statusCode
	}()
	waitFor(t, func() bool { return store.inFlight.Load() == 1 })

	// A request that gives up while waiting for budget fails and writes
	// nothing.
	ctx, cancel := context.WithCancel(context.Background())
	waiting := make(chan *fakeAppContext, 1)
	other := make([]byte, size)
	other[0] = 1
	go func() {
		waiting <- putBundleWithContext(t, h, ctx, other)
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	cancelled := <-waiting
	require.Equal(t, http.StatusServiceUnavailable, cancelled.statusCode)
	exists, err := store.Exists(context.Background(), store.project, chunker.Sum(other))
	require.NoError(t, err)
	require.False(t, exists)

	close(store.gate)
	require.Equal(t, http.StatusOK, <-held)

	// Everything was given back: a new upload gets the whole budget at once.
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	final := putBundleWithContext(t, h, ctx, other)
	require.Equal(t, http.StatusOK, final.statusCode)
	require.Len(t, final.response, 1)
}
