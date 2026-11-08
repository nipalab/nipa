package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/aiprompt"
	"github.com/nipalab/nipa/internal/client/config"
	"github.com/nipalab/nipa/internal/client/domain"
)

func serve(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func TestNew_Validation(t *testing.T) {
	_, err := New(config.LLMSettings{})
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Contains(t, domErr.Message, "NIPA_LLM_ENDPOINT and NIPA_LLM_MODEL")
	require.NotEmpty(t, domErr.Hint)

	_, err = New(config.LLMSettings{Endpoint: "http://x/v1"})
	require.ErrorContains(t, err, "no LLM endpoint configured")

	for _, bad := range []string{"localhost:11434", "ftp://x/v1", "http://", "::"} {
		_, err = New(config.LLMSettings{Endpoint: bad, Model: "m"})
		require.ErrorContains(t, err, "http(s) URL", bad)
	}

	c, err := New(config.LLMSettings{Endpoint: " http://localhost:11434/v1/ ", Model: " qwen "})
	require.NoError(t, err)
	require.Equal(t, "http://localhost:11434/v1/chat/completions", c.url)
	require.Equal(t, "qwen", c.Model())
	require.Equal(t, DefaultTimeout, c.timeout)
}

func TestComplete_Success(t *testing.T) {
	var got request
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/chat/completions", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "Bearer sk-test", r.Header.Get("Authorization"))
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"  fix: things \n"}}]}`)
	})
	c, err := New(config.LLMSettings{Endpoint: srv.URL + "/v1", Model: "m", APIKey: "sk-test"}, WithHTTPClient(srv.Client()))
	require.NoError(t, err)
	text, err := c.Complete(context.Background(), aiprompt.Prompt{System: "sys", User: "usr"})
	require.NoError(t, err)
	require.Equal(t, "fix: things", text)
	require.Equal(t, "m", got.Model)
	require.InDelta(t, 0.2, got.Temperature, 1e-9)
	require.False(t, got.Stream)
	require.Equal(t, []message{{Role: "system", Content: "sys"}, {Role: "user", Content: "usr"}}, got.Messages)
}

func TestComplete_NoKeyNoAuthHeader(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		require.Empty(t, r.Header.Get("Authorization"))
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	})
	c, err := New(config.LLMSettings{Endpoint: srv.URL, Model: "m"})
	require.NoError(t, err)
	text, err := c.Complete(context.Background(), aiprompt.Prompt{})
	require.NoError(t, err)
	require.Equal(t, "ok", text)
}

func TestComplete_Errors(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"openai error", 401, `{"error":{"message":"invalid key sk-secret"}}`, "LLM endpoint answered 401: invalid key [redacted]"},
		{"plain error", 404, `{"error":"model \"m\" not found"}`, `answered 404: model "m" not found`},
		{"message field", 500, `{"message":"boom"}`, "answered 500: boom"},
		{"raw body", 502, "bad\n  gateway", "answered 502: bad gateway"},
		{"empty body", 503, "", "answered 503: 503 Service Unavailable"},
		{"long body", 500, strings.Repeat("x", 1000), "…"},
		{"malformed", 200, "{not json", "invalid response"},
		{"no choices", 200, `{"choices":[]}`, "no completion"},
		{"blank content", 200, `{"choices":[{"message":{"content":"  "}}]}`, "no completion"},
		{"too large", 200, strings.Repeat("a", maxResponseBytes+10), "larger than 1 MiB"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := serve(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			})
			c, err := New(config.LLMSettings{Endpoint: srv.URL, Model: "m", APIKey: "sk-secret"})
			require.NoError(t, err)
			_, err = c.Complete(context.Background(), aiprompt.Prompt{})
			require.ErrorContains(t, err, tc.want)
			require.NotContains(t, err.Error(), "sk-secret")
			var domErr *domain.Error
			require.ErrorAs(t, err, &domErr)
		})
	}
}

func TestComplete_Timeout(t *testing.T) {
	release := make(chan struct{})
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	defer close(release)
	c, err := New(config.LLMSettings{Endpoint: srv.URL, Model: "m"}, WithTimeout(50*time.Millisecond))
	require.NoError(t, err)
	_, err = c.Complete(context.Background(), aiprompt.Prompt{})
	require.ErrorContains(t, err, "timed out after 50ms")
}

func TestComplete_CallerCancelled(t *testing.T) {
	release := make(chan struct{})
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	defer close(release)
	c, err := New(config.LLMSettings{Endpoint: srv.URL, Model: "m"})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	_, err = c.Complete(ctx, aiprompt.Prompt{})
	require.ErrorIs(t, err, context.Canceled)
}

type failingTransport struct{ err error }

func (f failingTransport) RoundTrip(*http.Request) (*http.Response, error) { return nil, f.err }

type badBody struct{}

func (badBody) Read([]byte) (int, error) { return 0, errors.New("read sk-secret broke") }
func (badBody) Close() error             { return nil }

type bodyTransport struct{}

func (bodyTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Body: badBody{}, Header: http.Header{}}, nil
}

func TestComplete_TransportErrors(t *testing.T) {
	c, err := New(config.LLMSettings{Endpoint: "http://127.0.0.1:1/v1", Model: "m", APIKey: "sk-secret"},
		WithHTTPClient(&http.Client{Transport: failingTransport{err: errors.New("dial refused sk-secret")}}))
	require.NoError(t, err)
	_, err = c.Complete(context.Background(), aiprompt.Prompt{})
	require.ErrorContains(t, err, "the LLM request failed: dial refused [redacted]")

	c, err = New(config.LLMSettings{Endpoint: "http://127.0.0.1:1/v1", Model: "m", APIKey: "sk-secret"}, WithHTTPClient(&http.Client{Transport: bodyTransport{}}))
	require.NoError(t, err)
	_, err = c.Complete(context.Background(), aiprompt.Prompt{})
	require.ErrorContains(t, err, "read [redacted] broke")
}

func TestErrorText(t *testing.T) {
	require.Equal(t, "x", errorText([]byte(`{"error":{"message":"x"}}`), "s"))
	require.Equal(t, `{"error":{}}`, errorText([]byte(`{"error":{}}`), "s"))
	require.Equal(t, "s", errorText([]byte("   "), "s"))
	require.Equal(t, "plain", (&Client{}).redact("plain"))
}
