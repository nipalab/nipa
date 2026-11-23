package grpc

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunkbundle"
	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	pb "github.com/nipalab/nipa/internal/grpc/pb"
)

// realChunks makes n chunks of size bytes whose hashes match their content,
// as a bundle server checks them.
func realChunks(n, size int) []*serverDomain.ChunkData {
	chunks := make([]*serverDomain.ChunkData, 0, n)
	for i := 0; i < n; i++ {
		data := []byte(fmt.Sprintf("%0*d", size, i))
		chunks = append(chunks, &serverDomain.ChunkData{Hash: chunker.Sum(data), Data: data})
	}
	return chunks
}

func newBundleClient(t *testing.T, srv *chunkTestServer, policy retryPolicy) *Client {
	t.Helper()
	srv.bundles = true
	return newDownloadClient(t, srv, policy)
}

func uploadScope() domain.ChunkScope {
	return domain.ChunkScope{Org: "org", Project: "proj"}
}

func TestClient_UploadChunks_SendsOneBundleAndItsReceipts(t *testing.T) {
	srv := newChunkTestServer()
	c := newBundleClient(t, srv, fastRetry(3))
	chunks := realChunks(3, 64)

	uploaded, skipped, err := c.UploadChunks(context.Background(), uploadScope(), chunks)
	require.NoError(t, err)
	require.Equal(t, 3, uploaded)
	require.Zero(t, skipped)

	require.Len(t, srv.bundleLog(), 1, "one request for the whole window")
	require.Equal(t, "application/octet-stream", srv.putHeaders.Get("Content-Type"))
	confirm := srv.lastConfirm
	require.Len(t, confirm.GetHashes(), 3)
	require.Len(t, confirm.GetReceipts(), 3)
	for _, receipt := range confirm.GetReceipts() {
		require.Equal(t, "receipt-"+receipt.GetHash(), receipt.GetReceipt())
	}
	for _, chunk := range chunks {
		got, ok := srv.storedChunk(chunk.Hash.String())
		require.True(t, ok)
		require.Equal(t, chunk.Data, got)
	}
}

func TestClient_UploadChunks_SplitsBundlesBySizeAndCount(t *testing.T) {
	entry := chunkbundle.EncodedSize(100)
	for name, tc := range map[string]struct {
		maxBytes  int64
		maxChunks int32
		want      []int
	}{
		"by count":          {maxBytes: 0, maxChunks: 2, want: []int{2, 2, 1}},
		"by size":           {maxBytes: 3 * entry, maxChunks: 0, want: []int{3, 2}},
		"by size and count": {maxBytes: 2*entry + 1, maxChunks: 5, want: []int{2, 2, 1}},
		"no limits":         {want: []int{5}},
	} {
		t.Run(name, func(t *testing.T) {
			srv := newChunkTestServer()
			srv.bundleMaxBytes, srv.bundleMaxChunks = tc.maxBytes, tc.maxChunks
			c := newBundleClient(t, srv, fastRetry(3))
			chunks := realChunks(5, 100)

			uploaded, _, err := c.UploadChunks(context.Background(), uploadScope(), chunks)
			require.NoError(t, err)
			require.Equal(t, 5, uploaded)

			var sizes []int
			seen := map[string]bool{}
			for _, hashes := range srv.bundleLog() {
				sizes = append(sizes, len(hashes))
				for _, hash := range hashes {
					seen[hash] = true
				}
			}
			sort.Sort(sort.Reverse(sort.IntSlice(sizes)))
			require.Equal(t, tc.want, sizes)
			require.Len(t, seen, 5)
			require.Len(t, srv.lastConfirm.GetReceipts(), 5)
		})
	}
}

func TestSplitBundles(t *testing.T) {
	chunks := realChunks(4, 10)
	bundles := splitBundles("/b", chunks, chunkbundle.EncodedSize(10, 10), 3)
	require.Len(t, bundles, 2)
	require.Equal(t, chunkbundle.EncodedSize(10, 10), bundles[0].size)
	require.Equal(t, chunks[0].Hash, bundles[0].entries[0].Hash)
	require.Equal(t, chunks[3].Hash, bundles[1].entries[1].Hash)

	// A chunk larger than the limit still gets a bundle of its own.
	big := splitBundles("/b", realChunks(2, 50), 10, 0)
	require.Len(t, big, 2)
	require.Empty(t, splitBundles("/b", nil, 10, 1))
}

func TestClient_UploadChunks_SkipsStoredChunksInBundles(t *testing.T) {
	srv := newChunkTestServer()
	chunks := realChunks(3, 32)
	srv.put(chunks[1].Hash.String(), chunks[1].Data)
	srv.link(chunks[1].Hash.String())
	c := newBundleClient(t, srv, fastRetry(3))

	uploaded, skipped, err := c.UploadChunks(context.Background(), uploadScope(), chunks)
	require.NoError(t, err)
	require.Equal(t, 2, uploaded)
	require.Equal(t, 1, skipped)
	require.Equal(t, [][]string{{chunks[0].Hash.String(), chunks[2].Hash.String()}}, srv.bundleLog())
	require.Len(t, srv.lastConfirm.GetHashes(), 2, "stored chunks are not confirmed (presign protected them)")
}

func TestClient_UploadChunks_RetriesAFailedBundleWhole(t *testing.T) {
	srv := newChunkTestServer()
	srv.failBundles = 1
	srv.failBundleStatus = http.StatusBadGateway
	c := newBundleClient(t, srv, fastRetry(3))
	chunks := realChunks(4, 40)

	uploaded, _, err := c.UploadChunks(context.Background(), uploadScope(), chunks)
	require.NoError(t, err)
	require.Equal(t, 4, uploaded)
	require.Equal(t, 2, srv.bundleAttempts, "the failed bundle was sent again")
	require.Len(t, srv.bundleLog(), 1)
	require.Len(t, srv.bundleLog()[0], 4, "whole")
	require.Len(t, srv.confirms, 1)
}

func TestClient_UploadChunks_RetriesAFailedBundleInTheNextAttempt(t *testing.T) {
	srv := newChunkTestServer()
	srv.failBundles = 2 // more than one transfer allows: the attempt gives the bundle up
	srv.failBundleStatus = http.StatusServiceUnavailable
	c := newBundleClient(t, srv, fastRetry(2))
	chunks := realChunks(2, 40)

	uploaded, _, err := c.UploadChunks(context.Background(), uploadScope(), chunks)
	require.NoError(t, err)
	require.Equal(t, 2, uploaded)
	require.Equal(t, 3, srv.bundleAttempts)
	require.Len(t, srv.bundleLog(), 1)
	require.Len(t, srv.confirms, 1, "the failed attempt confirmed nothing: its only bundle failed")
	require.Len(t, srv.lastConfirm.GetReceipts(), 2)
}

func TestClient_UploadChunks_GivesUpAfterMaxAttemptsWithTheCause(t *testing.T) {
	srv := newChunkTestServer()
	srv.failBundles = 1000
	srv.failBundleStatus = http.StatusBadGateway
	c := newBundleClient(t, srv, fastRetry(1))

	_, _, err := c.UploadChunks(context.Background(), uploadScope(), realChunks(2, 40))
	require.Error(t, err)
	require.Contains(t, err.Error(), "upload incomplete: 2 chunk(s) missing on the server")
	require.Contains(t, err.Error(), "status 502")
	require.Equal(t, maxUploadAttempts, srv.bundleAttempts)
}

func TestClient_UploadChunks_FinalBundleFailureStops(t *testing.T) {
	srv := newChunkTestServer()
	srv.failBundles = 1
	srv.failBundleStatus = http.StatusBadRequest
	c := newBundleClient(t, srv, fastRetry(3))

	_, _, err := c.UploadChunks(context.Background(), uploadScope(), realChunks(2, 40))
	require.Error(t, err)
	require.Equal(t, 1, srv.bundleAttempts, "a refused bundle is not retried")
	require.Empty(t, srv.confirms)
}

func TestClient_UploadChunks_KeepsOnlyReceiptsOfItsOwnChunks(t *testing.T) {
	srv := newChunkTestServer()
	srv.bundleReceipts = func(hashes []string) []bundleReceipt {
		out := []bundleReceipt{{Hash: testChunkHash(0x99).String(), Receipt: "forged"}, {Hash: hashes[0]}}
		for _, hash := range hashes[1:] {
			out = append(out, bundleReceipt{Hash: hash, Receipt: "receipt-" + hash})
		}
		return out
	}
	c := newBundleClient(t, srv, fastRetry(3))
	chunks := realChunks(3, 20)

	_, _, err := c.UploadChunks(context.Background(), uploadScope(), chunks)
	require.NoError(t, err)
	receipts := srv.confirms[0].GetReceipts()
	require.Len(t, receipts, 2, "a foreign or empty receipt is dropped")
	for _, receipt := range receipts {
		require.NotEqual(t, chunks[0].Hash.String(), receipt.GetHash())
	}
}

func TestClient_UploadChunks_IgnoresReceiptsFromAbsoluteURLs(t *testing.T) {
	srv := newChunkTestServer()
	c := newBundleClient(t, srv, fastRetry(3))
	srv.absoluteBase = "http://" + strings.TrimPrefix(c.transport.url, "http://")

	_, _, err := c.UploadChunks(context.Background(), uploadScope(), realChunks(2, 20))
	require.NoError(t, err)
	require.Empty(t, srv.confirms[0].GetReceipts(), "an absolute URL never carries a server receipt")
}

func TestClient_UploadChunks_BadReceiptsBody(t *testing.T) {
	srv := newChunkTestServer()
	srv.bundleBody = "not json"
	c := newBundleClient(t, srv, fastRetry(1))

	_, _, err := c.UploadChunks(context.Background(), uploadScope(), realChunks(1, 20))
	require.ErrorContains(t, err, "read chunk bundle receipts")
}

func TestClient_UploadChunks_DirectModeKeepsPerChunkPuts(t *testing.T) {
	srv := newChunkTestServer()
	c := newDownloadClient(t, srv, fastRetry(3))
	chunks := realChunks(3, 20)

	uploaded, _, err := c.UploadChunks(context.Background(), uploadScope(), chunks)
	require.NoError(t, err)
	require.Equal(t, 3, uploaded)
	require.Empty(t, srv.bundleLog())
	require.Equal(t, "*", srv.putHeaders.Get("If-None-Match"), "direct PUTs carry their signed headers")
	require.Empty(t, srv.lastConfirm.GetReceipts(), "the server reads direct uploads back")
}

// stalledBundleServer stops reading the body of its first stallFor requests
// and hangs; later requests are read whole and answered with no receipts.
func stalledBundleServer(t *testing.T, stallFor int32) (*httptest.Server, *atomic.Int32, *atomic.Int64) {
	t.Helper()
	var requests atomic.Int32
	var received atomic.Int64
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) > stallFor {
			n, _ := io.Copy(io.Discard, r.Body)
			received.Store(n)
			_, _ = io.WriteString(w, "[]")
			return
		}
		_, _ = io.ReadFull(r.Body, make([]byte, 1024))
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() {
		close(release)
		srv.Close()
	})
	return srv, &requests, &received
}

func TestPutBundle_RetriesAStalledUpload(t *testing.T) {
	srv, requests, received := stalledBundleServer(t, 1)
	c := stallClient(150*time.Millisecond, 3)
	data := make([]byte, stallUploadSize/3)
	bundle := splitBundles(srv.URL, []*serverDomain.ChunkData{
		{Hash: testChunkHash(1), Data: data}, {Hash: testChunkHash(2), Data: data}, {Hash: testChunkHash(3), Data: data},
	}, 0, 0)[0]

	n, err := c.putBundle(context.Background(), bundle)
	require.NoError(t, err)
	require.Equal(t, bundle.size, n)
	require.Equal(t, int32(2), requests.Load(), "the stalled attempt is cancelled and repeated")
	require.Equal(t, bundle.size, received.Load(), "the repeated attempt sends the whole bundle with its exact length")
}

func TestPutBundle_MaintenanceIsFinal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Nipa-Maintenance", "server;read_only")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"error":"server is in maintenance (read-only): backup"}`)
	}))
	t.Cleanup(srv.Close)
	c := stallClient(time.Second, 3)
	bundle := splitBundles(srv.URL, realChunks(1, 8), 0, 0)[0]

	_, err := c.putBundle(context.Background(), bundle)
	require.Error(t, err)
	require.Nil(t, bundle.failed, "maintenance is not retried by a later attempt")
}

func TestErrUploadIncomplete(t *testing.T) {
	require.EqualError(t, errUploadIncomplete(2, nil), "upload incomplete: 2 chunk(s) missing on the server")
	require.Contains(t, errUploadIncomplete(1, io.ErrUnexpectedEOF).Error(), "unexpected EOF")
	require.Contains(t, errUploadIncomplete(1, domain.NewUserError("boom")).Error(), ": boom")
}

func TestKeepBundleReceipts(t *testing.T) {
	chunks := realChunks(1, 4)
	bundle := splitBundles("/api/chunk-bundles/o/p", chunks, 0, 0)[0]
	got := keepBundleReceipts(bundle, []bundleReceipt{{Hash: chunks[0].Hash.String(), Receipt: "r"}})
	require.Equal(t, []*pb.ChunkReceipt{{Hash: chunks[0].Hash.String(), Receipt: "r"}}, got)
	bundle.url = "https://s3.example.com/x"
	require.Nil(t, keepBundleReceipts(bundle, []bundleReceipt{{Hash: chunks[0].Hash.String(), Receipt: "r"}}))
}

func TestRetryLaterWrapsItsCause(t *testing.T) {
	err := &retryLater{err: io.ErrUnexpectedEOF}
	require.EqualError(t, err, io.ErrUnexpectedEOF.Error())
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
}
