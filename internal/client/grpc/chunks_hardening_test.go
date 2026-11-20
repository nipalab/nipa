package grpc

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

// stallingServer answers its first stallFor requests with half a body (or no
// response at all) and then hangs; later requests get the whole body.
func stallingServer(t *testing.T, body string, stallFor int32, beforeHeaders bool) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var requests atomic.Int32
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) > stallFor {
			_, _ = io.WriteString(w, body)
			return
		}
		if !beforeHeaders {
			w.Header().Set("Content-Length", "1000")
			_, _ = io.WriteString(w, body[:len(body)/2])
			w.(http.Flusher).Flush()
		}
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() {
		close(release)
		srv.Close()
	})
	return srv, &requests
}

func stallClient(timeout time.Duration, attempts int) *Client {
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	c.stallTimeout = timeout
	c.retry = fastRetry(attempts)
	return c
}

func TestDownloadChunk_RetriesAStalledTransfer(t *testing.T) {
	for name, beforeHeaders := range map[string]bool{"stalls mid body": false, "never answers": true} {
		t.Run(name, func(t *testing.T) {
			srv, requests := stallingServer(t, "a complete chunk body", 1, beforeHeaders)
			c := stallClient(50*time.Millisecond, 3)
			h := testChunkHash(0x60)

			got := newDeliveries()
			_, err := c.downloadChunk(context.Background(), &chunkSink{fn: got.sink}, chunkTransfer{hash: h, url: srv.URL})
			require.NoError(t, err)
			require.Equal(t, []byte("a complete chunk body"), got.data[h])
			require.Equal(t, int32(2), requests.Load(), "the stalled attempt is cancelled and repeated")
		})
	}
}

func TestDownloadChunk_StallErrorAfterAttempts(t *testing.T) {
	srv, requests := stallingServer(t, "a complete chunk body", 100, false)
	c := stallClient(30*time.Millisecond, 2)

	_, err := c.downloadChunk(context.Background(), &chunkSink{fn: newDeliveries().sink}, chunkTransfer{hash: testChunkHash(0x61), url: srv.URL})
	require.ErrorIs(t, err, errTransferStalled)
	require.Equal(t, int32(2), requests.Load())

	rec := newStreamRecorder(testChunkHash(0x61))
	_, err = c.streamChunk(context.Background(), rec.stream(), chunkTransfer{hash: testChunkHash(0x61), url: srv.URL})
	require.ErrorIs(t, err, errTransferStalled, "a streamed chunk stalls the same way")
}

func TestDownloadChunk_SlowButSteadyTransferIsNotAStall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		for range 5 {
			_, _ = io.WriteString(w, "ab")
			w.(http.Flusher).Flush()
			time.Sleep(20 * time.Millisecond)
		}
	}))
	t.Cleanup(srv.Close)
	c := stallClient(80*time.Millisecond, 1)
	h := testChunkHash(0x62)

	got := newDeliveries()
	_, err := c.downloadChunk(context.Background(), &chunkSink{fn: got.sink}, chunkTransfer{hash: h, url: srv.URL})
	require.NoError(t, err, "progress resets the timer, however slow")
	require.Equal(t, "ababababab", string(got.data[h]))
}

func TestStallGuard_DisabledWithoutTimeout(t *testing.T) {
	cancelled := false
	g := newStallGuard(0, func() { cancelled = true })
	g.body = io.NopCloser(io.LimitReader(zeroReader{}, 4))
	data, err := io.ReadAll(g)
	require.NoError(t, err)
	require.Len(t, data, 4)
	require.NoError(t, g.Close())
	require.True(t, cancelled, "closing always releases the request context")
}

func tlsSessionClient() *Client {
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	c.transport.url = "https://nipa.example.com"
	return c
}

func TestTransferURL_RefusesCleartextOnATLSSession(t *testing.T) {
	c := tlsSessionClient()

	got, err := c.transferURL("/api/chunks/abc")
	require.NoError(t, err)
	require.Equal(t, "https://nipa.example.com/api/chunks/abc", got)
	got, err = c.transferURL("https://storage.example.com/bucket/abc")
	require.NoError(t, err)
	require.Equal(t, "https://storage.example.com/bucket/abc", got)

	for _, raw := range []string{"http://storage.example.com/bucket/abc", "HTTP://storage.example.com/x"} {
		_, err = c.transferURL(raw)
		var userErr *domain.Error
		require.ErrorAs(t, err, &userErr)
		require.Contains(t, userErr.Message, "cleartext")
		require.Contains(t, userErr.Action, allowInsecureChunkURLsEnv)
	}

	t.Setenv(allowInsecureChunkURLsEnv, "1")
	got, err = c.transferURL("http://storage.example.com/bucket/abc")
	require.NoError(t, err, "the escape hatch allows http storage behind an https server")
	require.Equal(t, "http://storage.example.com/bucket/abc", got)
}

func TestTransferURL_CleartextSessionMayUseCleartextStorage(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	c.transport.url = "nipa.local:8080"
	got, err := c.transferURL("http://minio.local:9000/bucket/abc")
	require.NoError(t, err)
	require.Equal(t, "http://minio.local:9000/bucket/abc", got)
}

func TestChunkTransfers_RefuseCleartextURLsOnATLSSession(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	t.Cleanup(srv.Close)
	c := tlsSessionClient()
	h := testChunkHash(0x63)

	_, err := c.downloadChunk(context.Background(), &chunkSink{fn: newDeliveries().sink}, chunkTransfer{hash: h, url: srv.URL})
	require.ErrorContains(t, err, "cleartext")
	_, err = c.putChunk(context.Background(), chunkTransfer{hash: h, url: srv.URL, data: []byte("secret")})
	require.ErrorContains(t, err, "cleartext")
	require.Zero(t, hits.Load(), "nothing is sent to the cleartext endpoint")
}

func TestCheckTransferRedirect(t *testing.T) {
	request := func(raw string) *http.Request {
		u, err := url.Parse(raw)
		require.NoError(t, err)
		return &http.Request{URL: u}
	}
	secure, plain := request("https://a.example/x"), request("http://b.example/x")

	require.NoError(t, checkTransferRedirect(secure, []*http.Request{secure}))
	require.NoError(t, checkTransferRedirect(plain, []*http.Request{plain}), "a cleartext transfer may stay cleartext")
	require.ErrorContains(t, checkTransferRedirect(plain, []*http.Request{secure}), "cleartext")
	require.ErrorContains(t, checkTransferRedirect(secure, []*http.Request{secure, secure, secure, secure}), "redirects")

	t.Setenv(allowInsecureChunkURLsEnv, "true")
	require.NoError(t, checkTransferRedirect(plain, []*http.Request{secure}))
}

func TestDefaultHTTPClient_BoundsRedirectsAndHeaderWait(t *testing.T) {
	client := defaultHTTPClient()
	require.NotNil(t, client.CheckRedirect)
	require.Equal(t, responseHeaderTimeout, client.Transport.(*http.Transport).ResponseHeaderTimeout)

	var hops atomic.Int32
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hops.Add(1)
		http.Redirect(w, r, srv.URL, http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	c.retry = fastRetry(1)
	_, err := c.downloadChunk(context.Background(), &chunkSink{fn: newDeliveries().sink}, chunkTransfer{hash: serverDomain.Hash{1}, url: srv.URL})
	require.ErrorContains(t, err, "redirects")
	require.LessOrEqual(t, hops.Load(), int32(maxTransferRedirects+1))
}
