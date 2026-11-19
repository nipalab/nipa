package s3

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

type countingTransport struct{ calls atomic.Int64 }

func (c *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.calls.Add(1)
	return http.DefaultTransport.RoundTrip(r)
}

func newProbeStore(t *testing.T, prefix string) (*Store, *fakeS3, *countingTransport) {
	t.Helper()
	fake, server := newFakeS3(t)
	fake.wantLength.Store(probeSize)
	transport := &countingTransport{}
	cfg := testConfig(server)
	cfg.Prefix = prefix
	cfg.Transport = transport
	store, err := New(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	return store, fake, transport
}

func requireProbeCheck(t *testing.T, err error, check string) {
	t.Helper()
	var probeErr *ProbeError
	require.True(t, errors.As(err, &probeErr), "want a ProbeError, got %v", err)
	require.Equal(t, check, probeErr.Check)
	require.NotNil(t, errors.Unwrap(err))
	require.Contains(t, err.Error(), check)
}

func (f *fakeS3) objectCount(bucket string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.buckets[bucket])
}

func TestProbe_Succeeds(t *testing.T) {
	store, fake, transport := newProbeStore(t, "tenant")
	before := transport.calls.Load()

	require.NoError(t, store.Probe(context.Background()))
	require.Zero(t, fake.objectCount("test-bucket"), "the probe object is removed")
	require.Greater(t, transport.calls.Load()-before, int64(5), "probe traffic goes through the configured transport")
}

func TestProbe_KeyUnderPrefix(t *testing.T) {
	store, _, _ := newProbeStore(t, "tenant")
	key, data, err := store.probeObject()
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(key, "tenant/.nipa-probe-"))
	require.Len(t, data, probeSize)

	bare, _, _ := newProbeStore(t, "")
	key, _, err = bare.probeObject()
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(key, ".nipa-probe-"))
}

func TestProbe_RejectsBackendWithoutConditionalCreate(t *testing.T) {
	store, fake, _ := newProbeStore(t, "")
	fake.ignoreIfNoneMatch.Store(true)

	requireProbeCheck(t, store.Probe(context.Background()), ProbeCreateOnly)
	require.Zero(t, fake.objectCount("test-bucket"), "cleanup runs after a failed check")
}

func TestProbe_RejectsUnsignedLength(t *testing.T) {
	store, fake, _ := newProbeStore(t, "")
	fake.wantLength.Store(0)

	requireProbeCheck(t, store.Probe(context.Background()), ProbeContentLength)
	require.Zero(t, fake.objectCount("test-bucket"))
}

func TestProbe_UploadRefused(t *testing.T) {
	store, fake, _ := newProbeStore(t, "")
	fake.failPut.Store(true)

	requireProbeCheck(t, store.Probe(context.Background()), ProbeUpload)
}

func TestProbe_DownloadFails(t *testing.T) {
	store, fake, _ := newProbeStore(t, "")
	fake.failGet.Store(true)

	requireProbeCheck(t, store.Probe(context.Background()), ProbeDownload)
}

func TestProbe_StatFails(t *testing.T) {
	store, fake, _ := newProbeStore(t, "")
	fake.failHead.Store(true)

	requireProbeCheck(t, store.Probe(context.Background()), ProbeStat)
}

func TestProbe_DeleteFails(t *testing.T) {
	store, fake, _ := newProbeStore(t, "")
	fake.failDelete.Store(true)

	requireProbeCheck(t, store.Probe(context.Background()), ProbeDelete)
}

func TestProbe_MissingBucketWithSkipProbe(t *testing.T) {
	_, server := newFakeS3(t)
	cfg := testConfig(server)
	cfg.Bucket = "missing"
	cfg.SkipProbe = true
	store, err := New(context.Background(), cfg)
	require.NoError(t, err, "SkipProbe defers the bucket check")

	requireProbeCheck(t, store.Probe(context.Background()), ProbeBucket)
}

func TestProbe_UnreachableEndpoint(t *testing.T) {
	_, server := newFakeS3(t)
	cfg := testConfig(server)
	cfg.SkipProbe = true
	store, err := New(context.Background(), cfg)
	require.NoError(t, err)
	server.Close()

	requireProbeCheck(t, store.Probe(context.Background()), ProbeBucket)
}

func TestProbe_DownloadReturnsOtherBytes(t *testing.T) {
	store, fake, _ := newProbeStore(t, "")
	fake.corruptGet.Store(true)

	requireProbeCheck(t, store.Probe(context.Background()), ProbeDownload)
}
