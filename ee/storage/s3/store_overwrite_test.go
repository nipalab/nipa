package s3

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
)

func TestStore_OverwriteReplacesGarbageWithoutHead(t *testing.T) {
	store, fake := newTestStore(t)
	ctx := context.Background()
	data := []byte("verified content")
	hash := chunker.Sum(data)
	fake.mu.Lock()
	fake.buckets["test-bucket"][store.key(hash)] = []byte("garbage planted by a direct upload")
	fake.mu.Unlock()
	headsBefore := fake.heads.Load()

	require.NoError(t, store.Overwrite(ctx, hash, data))

	require.Equal(t, headsBefore, fake.heads.Load(), "overwrite must not check for the object first")
	got, err := store.Get(ctx, hash)
	require.NoError(t, err)
	require.Equal(t, data, got)

	// Writing the same content again is one more PUT, never an error.
	require.NoError(t, store.Overwrite(ctx, hash, data))
	fake.mu.Lock()
	defer fake.mu.Unlock()
	require.Equal(t, 2, fake.puts)
}

func TestStore_OverwriteError(t *testing.T) {
	store, fake := newTestStore(t)
	fake.failPut.Store(true)
	data := []byte("verified content")

	err := store.Overwrite(context.Background(), chunker.Sum(data), data)
	require.ErrorContains(t, err, "s3: put chunk")
}

// TestStore_PutPayloadSigning pins how minio-go signs the PUT body: over https
// the payload is UNSIGNED-PAYLOAD (TLS protects it, no extra SHA-256 pass over
// every byte); over plain http it keeps the streaming SigV4 payload signature.
func TestStore_PutPayloadSigning(t *testing.T) {
	data := []byte("signed or not")
	hash := chunker.Sum(data)

	t.Run("https", func(t *testing.T) {
		fake := &fakeS3{buckets: map[string]map[string][]byte{"test-bucket": {}}}
		server := httptest.NewTLSServer(http.HandlerFunc(fake.serveHTTP))
		t.Cleanup(server.Close)
		cfg := testConfig(server)
		cfg.Transport = server.Client().Transport
		store, err := New(context.Background(), cfg)
		require.NoError(t, err)

		require.NoError(t, store.Overwrite(context.Background(), hash, data))
		require.Equal(t, "UNSIGNED-PAYLOAD", fake.payloadSHA.Load())
	})

	t.Run("http", func(t *testing.T) {
		store, fake := newTestStore(t)

		require.NoError(t, store.Overwrite(context.Background(), hash, data))
		require.Equal(t, "STREAMING-AWS4-HMAC-SHA256-PAYLOAD", fake.payloadSHA.Load())
	})
}
