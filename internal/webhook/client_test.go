package webhook

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"testing/iotest"
	"time"

	"github.com/stretchr/testify/require"
)

func TestClient_PostSuccess(t *testing.T) {
	var gotHeaders http.Header
	var gotBody []byte
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		gotHeaders = r.Header.Clone()
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		gotBody = body
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	payload := []byte(`{"event":"push"}`)
	res := NewClient(ClientConfig{}).Post(context.Background(), PostRequest{
		URL:        server.URL + "/hook",
		Secret:     "test-secret",
		Event:      "push",
		DeliveryID: "delivery-1",
		HookID:     "hook-1",
		Payload:    payload,
	})

	require.NoError(t, res.Err)
	require.Equal(t, http.StatusNoContent, res.StatusCode)
	require.Positive(t, res.Duration)
	require.Equal(t, int32(1), hits.Load())
	require.Equal(t, "application/json", gotHeaders.Get("Content-Type"))
	require.Equal(t, UserAgent, gotHeaders.Get("User-Agent"))
	require.Equal(t, "push", gotHeaders.Get(EventHeader))
	require.Equal(t, "delivery-1", gotHeaders.Get(DeliveryHeader))
	require.Equal(t, "hook-1", gotHeaders.Get(HookHeader))
	require.Equal(t, payload, gotBody)
	require.True(t, Verify("test-secret", gotBody, gotHeaders.Get(SignatureHeader)))
}

func TestClient_PostWithoutSecretSkipsSignature(t *testing.T) {
	var signature string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		signature = r.Header.Get(SignatureHeader)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	res := NewClient(ClientConfig{}).Post(context.Background(), PostRequest{
		URL: server.URL, Event: "ping", Payload: []byte(`{}`),
	})
	require.NoError(t, res.Err)
	require.Empty(t, signature)
}

func TestClient_PostNon2xxIsFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "receiver exploded", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	res := NewClient(ClientConfig{}).Post(context.Background(), PostRequest{
		URL: server.URL, Secret: "s", Event: "push", Payload: []byte(`{}`),
	})
	require.Equal(t, http.StatusInternalServerError, res.StatusCode)
	require.ErrorContains(t, res.Err, "500")
	require.ErrorContains(t, res.Err, "receiver exploded")
}

func TestClient_PostRedirectIsFailure(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(target.Close)
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	t.Cleanup(redirector.Close)

	res := NewClient(ClientConfig{}).Post(context.Background(), PostRequest{
		URL: redirector.URL, Secret: "s", Event: "push", Payload: []byte(`{}`),
	})
	require.Equal(t, http.StatusFound, res.StatusCode)
	require.ErrorContains(t, res.Err, "302")
	require.Zero(t, targetHits.Load(), "the signed payload must not follow a redirect")
}

func TestClient_PostTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	res := NewClient(ClientConfig{Timeout: 20 * time.Millisecond}).Post(context.Background(), PostRequest{
		URL: server.URL, Event: "push", Payload: []byte(`{}`),
	})
	require.Error(t, res.Err)
	require.Zero(t, res.StatusCode)
	require.True(t, os.IsTimeout(res.Err), res.Err.Error())
}

func TestClient_PostInvalidURL(t *testing.T) {
	res := NewClient(ClientConfig{}).Post(context.Background(), PostRequest{URL: "://nope"})
	require.Error(t, res.Err)
	require.Zero(t, res.StatusCode)
}

func TestResponseSnippet(t *testing.T) {
	require.Empty(t, responseSnippet(strings.NewReader("")))
	require.Equal(t, ": boom", responseSnippet(strings.NewReader(" boom \n")))
	require.Empty(t, responseSnippet(iotest.ErrReader(errors.New("read failed"))))
}

func TestClient_PostInsecureTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	secure := NewClient(ClientConfig{}).Post(context.Background(), PostRequest{
		URL: server.URL, Event: "push", Payload: []byte(`{}`),
	})
	require.Error(t, secure.Err, "a self-signed certificate must fail by default")

	insecure := NewClient(ClientConfig{}).Post(context.Background(), PostRequest{
		URL: server.URL, Secret: "s", Event: "push", Payload: []byte(`{}`), InsecureTLS: true,
	})
	require.NoError(t, insecure.Err)
	require.Equal(t, http.StatusOK, insecure.StatusCode)
}

func TestClient_EgressAllowlist(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	blocked := NewClient(ClientConfig{EgressAllowlist: []string{"example.com"}}).Post(context.Background(), PostRequest{
		URL: server.URL, Event: "push", Payload: []byte(`{}`),
	})
	require.Error(t, blocked.Err)
	require.True(t, IsEgressBlocked(blocked.Err))
	require.Zero(t, hits.Load(), "a blocked host is never contacted")

	allowed := NewClient(ClientConfig{EgressAllowlist: []string{strings.TrimPrefix(server.URL, "http://")}}).Post(context.Background(), PostRequest{
		URL: server.URL, Event: "push", Payload: []byte(`{}`),
	})
	require.NoError(t, allowed.Err)
	require.Equal(t, int32(1), hits.Load())

	emptyEntries := NewClient(ClientConfig{EgressAllowlist: []string{"", "   "}}).Post(context.Background(), PostRequest{
		URL: server.URL, Event: "push", Payload: []byte(`{}`),
	})
	require.True(t, IsEgressBlocked(emptyEntries.Err))

	badURL := NewClient(ClientConfig{EgressAllowlist: []string{"example.com"}}).Post(context.Background(), PostRequest{
		URL: "http://%zz", Event: "push", Payload: []byte(`{}`),
	})
	require.Error(t, badURL.Err)
	require.False(t, IsEgressBlocked(badURL.Err))
}
