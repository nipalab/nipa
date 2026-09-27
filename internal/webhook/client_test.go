package webhook

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"testing/iotest"
	"time"

	"github.com/stretchr/testify/require"
)

// loopbackClient allows the httptest loopback address, which the default
// public-only egress policy would otherwise reject.
func loopbackClient() *Client {
	return NewClient(ClientConfig{EgressAllowlist: []string{"127.0.0.1"}})
}

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
	res := loopbackClient().Post(context.Background(), PostRequest{
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

	res := loopbackClient().Post(context.Background(), PostRequest{
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

	res := loopbackClient().Post(context.Background(), PostRequest{
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

	res := loopbackClient().Post(context.Background(), PostRequest{
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

	res := NewClient(ClientConfig{Timeout: 20 * time.Millisecond, EgressAllowlist: []string{"127.0.0.1"}}).
		Post(context.Background(), PostRequest{
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

	secure := loopbackClient().Post(context.Background(), PostRequest{
		URL: server.URL, Event: "push", Payload: []byte(`{}`),
	})
	require.Error(t, secure.Err, "a self-signed certificate must fail by default")

	insecure := loopbackClient().Post(context.Background(), PostRequest{
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

	blocked := NewClient(ClientConfig{}).Post(context.Background(), PostRequest{
		URL: server.URL, Event: "push", Payload: []byte(`{}`),
	})
	require.True(t, IsEgressBlocked(blocked.Err), "an empty allowlist must not reach loopback")
	require.Zero(t, hits.Load(), "a blocked address is never contacted")

	for _, entry := range []string{"127.0.0.1", "127.0.0.0/8"} {
		allowed := NewClient(ClientConfig{EgressAllowlist: []string{entry}}).Post(context.Background(), PostRequest{
			URL: server.URL, Event: "push", Payload: []byte(`{}`),
		})
		require.NoError(t, allowed.Err, entry)
	}
	require.Equal(t, int32(2), hits.Load())

	unnamed := NewClient(ClientConfig{EgressAllowlist: []string{"", "   "}}).Post(context.Background(), PostRequest{
		URL: server.URL, Event: "push", Payload: []byte(`{}`),
	})
	require.True(t, IsEgressBlocked(unnamed.Err))
	require.Equal(t, int32(2), hits.Load())

	denied := NewClient(ClientConfig{EgressAllowlist: []string{"example.com"}}).Post(context.Background(), PostRequest{
		URL: server.URL, Event: "push", Payload: []byte(`{}`),
	})
	require.True(t, IsEgressBlocked(denied.Err))

	badURL := NewClient(ClientConfig{EgressAllowlist: []string{"example.com"}}).Post(context.Background(), PostRequest{
		URL: "http://%zz", Event: "push", Payload: []byte(`{}`),
	})
	require.Error(t, badURL.Err)
	require.False(t, IsEgressBlocked(badURL.Err))
}

func TestClient_ResolvesBeforeDial(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	_, port, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	require.NoError(t, err)
	url := "http://internal.example:" + port + "/hook"
	lookup := func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}

	untrusted := NewClient(ClientConfig{})
	untrusted.lookupIP = lookup
	res := untrusted.Post(context.Background(), PostRequest{URL: url, Event: "push", Payload: []byte(`{}`)})
	require.True(t, IsEgressBlocked(res.Err), "a hostname must not hide a loopback destination")
	require.Zero(t, hits.Load())

	trusted := NewClient(ClientConfig{EgressAllowlist: []string{"internal.example"}})
	trusted.lookupIP = lookup
	res = trusted.Post(context.Background(), PostRequest{URL: url, Event: "push", Payload: []byte(`{}`)})
	require.NoError(t, res.Err)

	cidrOnly := NewClient(ClientConfig{EgressAllowlist: []string{"127.0.0.0/8"}})
	cidrOnly.lookupIP = lookup
	res = cidrOnly.Post(context.Background(), PostRequest{URL: url, Event: "push", Payload: []byte(`{}`)})
	require.NoError(t, res.Err, "the resolved address may fall inside a listed range")
	require.Equal(t, int32(2), hits.Load())
}

func TestClient_DialGuardEdges(t *testing.T) {
	ctx := context.Background()

	t.Run("lookup failure is not an egress block", func(t *testing.T) {
		client := NewClient(ClientConfig{})
		client.lookupIP = func(context.Context, string) ([]net.IPAddr, error) {
			return nil, errors.New("dns down")
		}

		res := client.Post(ctx, PostRequest{URL: "http://internal.example/hook", Event: "push", Payload: []byte(`{}`)})
		require.ErrorContains(t, res.Err, "dns down")
		require.False(t, IsEgressBlocked(res.Err))
	})

	t.Run("no resolved addresses is blocked", func(t *testing.T) {
		client := NewClient(ClientConfig{EgressAllowlist: []string{"internal.example"}})
		client.lookupIP = func(context.Context, string) ([]net.IPAddr, error) {
			return nil, nil
		}

		res := client.Post(ctx, PostRequest{URL: "http://internal.example/hook", Event: "push", Payload: []byte(`{}`)})
		require.True(t, IsEgressBlocked(res.Err))
		require.ErrorContains(t, res.Err, "no addresses")
	})

	t.Run("a denied address falls through to an allowed one", func(t *testing.T) {
		var hits atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hits.Add(1)
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(server.Close)
		_, port, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
		require.NoError(t, err)

		client := NewClient(ClientConfig{EgressAllowlist: []string{"127.0.0.0/8"}})
		client.lookupIP = func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{
				{IP: net.ParseIP("10.0.0.1")},
				{IP: net.ParseIP("127.0.0.1")},
			}, nil
		}

		res := client.Post(ctx, PostRequest{URL: "http://multi.example:" + port + "/hook", Event: "push", Payload: []byte(`{}`)})
		require.NoError(t, res.Err)
		require.Equal(t, int32(1), hits.Load())
	})

	t.Run("dial failure is not an egress block", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		port := listener.Addr().(*net.TCPAddr).Port
		require.NoError(t, listener.Close())

		client := NewClient(ClientConfig{Timeout: time.Second, EgressAllowlist: []string{"127.0.0.0/8"}})
		client.lookupIP = func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
		}

		res := client.Post(ctx, PostRequest{
			URL: "http://refused.example:" + strconv.Itoa(port) + "/hook", Event: "push", Payload: []byte(`{}`),
		})
		require.Error(t, res.Err)
		require.False(t, IsEgressBlocked(res.Err))
	})
}

func TestIsPublicIP(t *testing.T) {
	tests := []struct {
		ip     string
		public bool
	}{
		{"8.8.8.8", true},
		{"2606:4700::1111", true},
		{"127.0.0.1", false},
		{"::1", false},
		{"10.0.0.1", false},
		{"172.16.0.1", false},
		{"192.168.1.1", false},
		{"169.254.169.254", false},
		{"fd00::1", false},
		{"fe80::1", false},
		{"0.0.0.0", false},
		{"224.0.0.1", false},
	}
	for _, tt := range tests {
		require.Equal(t, tt.public, isPublicIP(net.ParseIP(tt.ip)), tt.ip)
	}
}

func TestEgressPolicyAllowsDial(t *testing.T) {
	empty := parseEgressAllowlist(nil)
	require.True(t, empty.allowsDial("example.com", net.ParseIP("8.8.8.8")))
	require.False(t, empty.allowsDial("localhost", net.ParseIP("127.0.0.1")))

	hostOnly := parseEgressAllowlist([]string{" Internal.Example. "})
	require.True(t, hostOnly.allowsDial("internal.example", net.ParseIP("127.0.0.1")))
	require.False(t, hostOnly.allowsDial("other.example", net.ParseIP("8.8.8.8")))

	listed := parseEgressAllowlist([]string{"10.0.0.0/8", "192.168.1.5"})
	require.True(t, listed.allowsDial("jenkins.internal", net.ParseIP("10.1.2.3")))
	require.True(t, listed.allowsDial("jenkins.internal", net.ParseIP("192.168.1.5")))
	require.False(t, listed.allowsDial("jenkins.internal", net.ParseIP("192.168.1.6")))
}
