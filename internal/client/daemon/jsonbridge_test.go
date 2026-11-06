package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	"github.com/nipalab/nipa/internal/client/usecase"
)

type bridgeClient struct {
	base  string
	token string
}

func newBridgeClient(t *testing.T, opts Options) (*testServer, *bridgeClient) {
	t.Helper()
	srv := startTestServer(t, opts)
	ep := srv.Endpoint()
	require.NotZero(t, ep.JSONPort)
	return srv, &bridgeClient{base: fmt.Sprintf("http://127.0.0.1:%d", ep.JSONPort), token: ep.Token}
}

func (c *bridgeClient) do(t *testing.T, ctx context.Context, method, path, body string, headers map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, strings.NewReader(body))
	require.NoError(t, err)
	if c.token != "" {
		req.Header.Set(TokenHeader, c.token)
	}
	for k, v := range headers {
		if k == "Host" {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func (c *bridgeClient) call(t *testing.T, path, body string) (int, map[string]any) {
	t.Helper()
	resp := c.do(t, context.Background(), http.MethodPost, path, body, nil)
	var out map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return resp.StatusCode, out
}

func TestJSONBridge_UnaryPingAndStatus(t *testing.T) {
	root := newTestClone(t)
	srv, c := newBridgeClient(t, Options{Version: "9.9.9"})

	code, out := c.call(t, "/v1/Ping", "")
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "9.9.9", out["version"])
	assert.EqualValues(t, srv.Endpoint().PID, out["pid"])

	code, _ = c.call(t, "/v1/WatchRepo", fmt.Sprintf(`{"root":%q}`, root))
	require.Equal(t, http.StatusOK, code)

	writeWorkingFile(t, root, "new.txt", "hello")
	code, out = c.call(t, "/v1/Status", fmt.Sprintf(`{"root":%q}`, root))
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "main", out["branch"])
	assert.Contains(t, out["untracked"], "new.txt")
	assert.Contains(t, out, "staged", "unpopulated fields are emitted")
}

func TestJSONBridge_Guards(t *testing.T) {
	_, c := newBridgeClient(t, Options{})
	ctx := context.Background()

	noToken := &bridgeClient{base: c.base}
	resp := noToken.do(t, ctx, http.MethodPost, "/v1/Ping", "", nil)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	badToken := &bridgeClient{base: c.base, token: "nope"}
	resp = badToken.do(t, ctx, http.MethodPost, "/v1/Ping", "", nil)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	resp = c.do(t, ctx, http.MethodPost, "/v1/Ping", "", map[string]string{"Host": "evil.example.com"})
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)

	resp = c.do(t, ctx, http.MethodPost, "/v1/Ping", "", map[string]string{"Origin": "https://evil.example.com"})
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)

	resp = c.do(t, ctx, http.MethodPost, "/v1/Ping", "", map[string]string{"Origin": "http://localhost:5173", "Host": "localhost"})
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	resp = c.do(t, ctx, http.MethodGet, "/v1/Ping", "", nil)
	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
	assert.Equal(t, http.MethodPost, resp.Header.Get("Allow"))

	resp = c.do(t, ctx, http.MethodPost, "/v1/Nope", "", nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp = c.do(t, ctx, http.MethodPost, "/other", "", nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)

	resp = c.do(t, ctx, http.MethodPost, "/v1/Status", "{not json", nil)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestJSONBridge_ErrorBody(t *testing.T) {
	_, c := newBridgeClient(t, Options{})
	code, out := c.call(t, "/v1/WatchRepo", fmt.Sprintf(`{"root":%q}`, t.TempDir()))
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, "InvalidArgument", out["code"])
	assert.NotEmpty(t, out["message"])

	code, out = c.call(t, "/v1/Login", `{"host":"h"}`)
	assert.Equal(t, http.StatusConflict, code)
	assert.Equal(t, "FailedPrecondition", out["code"])
}

func ndjsonLines(t *testing.T, resp *http.Response) []map[string]any {
	t.Helper()
	var lines []map[string]any
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		var m map[string]any
		require.NoError(t, json.Unmarshal(sc.Bytes(), &m), sc.Text())
		lines = append(lines, m)
	}
	return lines
}

func TestJSONBridge_StreamsOperationEvents(t *testing.T) {
	root := newTestClone(t)
	_, c := newBridgeClient(t, Options{Runners: Runners{New: func(string) RepoOps {
		return RepoOps{Push: &stubPushRunner{run: func(_ context.Context, _, msg string, progress ...usecase.UploadProgress) error {
			require.Equal(t, "ship it", msg)
			return nil
		}}}
	}}})
	code, _ := c.call(t, "/v1/WatchRepo", fmt.Sprintf(`{"root":%q}`, root))
	require.Equal(t, http.StatusOK, code)

	resp := c.do(t, context.Background(), http.MethodPost, "/v1/Push", fmt.Sprintf(`{"root":%q,"message":"ship it"}`, root), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, mimeNDJSON, resp.Header.Get("Content-Type"))
	lines := ndjsonLines(t, resp)
	require.NotEmpty(t, lines)
	last := lines[len(lines)-1]
	assert.Contains(t, last, "result")
}

func TestJSONBridge_StreamCancelStopsOperation(t *testing.T) {
	root := newTestClone(t)
	started := make(chan struct{})
	cancelled := make(chan struct{})
	_, c := newBridgeClient(t, Options{Runners: Runners{New: func(string) RepoOps {
		return RepoOps{Update: &stubUpdateRunner{run: func(ctx context.Context, _ string, _ ...usecase.DownloadProgress) error {
			close(started)
			<-ctx.Done()
			close(cancelled)
			return ctx.Err()
		}}}
	}}})
	code, _ := c.call(t, "/v1/WatchRepo", fmt.Sprintf(`{"root":%q}`, root))
	require.Equal(t, http.StatusOK, code)

	ctx, cancel := context.WithCancel(context.Background())
	resp := c.do(t, ctx, http.MethodPost, "/v1/Update", fmt.Sprintf(`{"root":%q}`, root), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	<-started
	cancel()
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("operation was not cancelled with the client")
	}
}

func TestJSONBridge_WatchChangesFlushesHeadersAndStreams(t *testing.T) {
	root := newTestClone(t)
	_, c := newBridgeClient(t, Options{})
	code, _ := c.call(t, "/v1/WatchRepo", fmt.Sprintf(`{"root":%q}`, root))
	require.Equal(t, http.StatusOK, code)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resp := c.do(t, ctx, http.MethodPost, "/v1/WatchChanges", fmt.Sprintf(`{"root":%q}`, root), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	writeWorkingFile(t, root, "b.txt", "b")
	sc := bufio.NewScanner(resp.Body)
	require.True(t, sc.Scan())
	var change map[string]any
	require.NoError(t, json.Unmarshal(sc.Bytes(), &change))
	assert.Contains(t, change, "paths")
}

func TestJSONBridge_StreamSetupError(t *testing.T) {
	_, c := newBridgeClient(t, Options{})
	resp := c.do(t, context.Background(), http.MethodPost, "/v1/WatchChanges", fmt.Sprintf(`{"root":%q}`, t.TempDir()), nil)
	assert.GreaterOrEqual(t, resp.StatusCode, 400)
	assert.Equal(t, mimeJSON, resp.Header.Get("Content-Type"))

	bad := &bridgeClient{base: c.base}
	resp = bad.do(t, context.Background(), http.MethodPost, "/v1/WatchChanges", `{}`, nil)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestJSONBridge_WriteStreamError(t *testing.T) {
	rec := httptest.NewRecorder()
	writeStreamError(rec, nil)
	var out map[string]jsonError
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.Equal(t, "OK", out["error"].Code)
}

func TestJSONBridge_HTTPStatusMapping(t *testing.T) {
	cases := map[codes.Code]int{
		codes.OK:                 200,
		codes.InvalidArgument:    400,
		codes.OutOfRange:         400,
		codes.Unauthenticated:    401,
		codes.PermissionDenied:   403,
		codes.NotFound:           404,
		codes.AlreadyExists:      409,
		codes.Aborted:            409,
		codes.FailedPrecondition: 409,
		codes.ResourceExhausted:  429,
		codes.Canceled:           499,
		codes.Unimplemented:      501,
		codes.Unavailable:        503,
		codes.DeadlineExceeded:   504,
		codes.Internal:           500,
		codes.Unknown:            500,
	}
	for code, want := range cases {
		assert.Equal(t, want, httpStatusFor(code), code.String())
	}
}

func TestJSONBridge_LoopbackChecks(t *testing.T) {
	for _, h := range []string{"127.0.0.1:8080", "localhost", "LOCALHOST:1", "[::1]:9", "::1", "127.0.0.1"} {
		assert.True(t, loopbackHost(h), h)
	}
	for _, h := range []string{"example.com", "10.0.0.1:80", "evil.localhost.com", ""} {
		assert.False(t, loopbackHost(h), h)
	}
	assert.True(t, loopbackOrigin(""))
	assert.True(t, loopbackOrigin("http://127.0.0.1:3000"))
	assert.False(t, loopbackOrigin("https://example.com"))
	assert.False(t, loopbackOrigin("null"))
}

func TestJSONBridge_MethodsCoverEveryRPC(t *testing.T) {
	methods := bridgeMethods()
	for _, name := range []string{"Ping", "Status", "Update", "WatchChanges", "LockStatus", "MetaWarnings", "ProxyLockFile"} {
		assert.Contains(t, methods, name)
	}
	assert.True(t, methods["Update"].stream)
	assert.False(t, methods["Ping"].stream)
}

func TestServer_PublishesJSONPort(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.json")
	srv := startTestServer(t, Options{EndpointPath: path})
	ep, err := ReadEndpoint(path)
	require.NoError(t, err)
	assert.Equal(t, srv.Endpoint().JSONPort, ep.JSONPort)
	assert.NotEqual(t, ep.Port, ep.JSONPort)

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.True(t, bytes.Contains(raw, []byte(`"json_port"`)))
}

func TestServer_JSONPortEnv(t *testing.T) {
	t.Setenv(EnvJSONPort, "abc")
	_, err := NewServer(Options{EndpointPath: filepath.Join(t.TempDir(), "d.json")})
	require.ErrorContains(t, err, EnvJSONPort)

	t.Setenv(EnvJSONPort, "")
	port, err := resolvePort(1234, EnvJSONPort)
	require.NoError(t, err)
	assert.Equal(t, 1234, port)
	port, err = resolvePort(0, EnvJSONPort)
	require.NoError(t, err)
	assert.Zero(t, port)
	t.Setenv(EnvJSONPort, "4321")
	port, err = resolvePort(0, EnvJSONPort)
	require.NoError(t, err)
	assert.Equal(t, 4321, port)
}

func TestServer_ServeFailsWhenBridgeDies(t *testing.T) {
	srv, err := NewServer(Options{EndpointPath: filepath.Join(t.TempDir(), "daemon.json")})
	require.NoError(t, err)
	require.NoError(t, srv.bridge.listener.Close())

	done := make(chan error, 1)
	go func() { done <- srv.Serve(context.Background()) }()
	select {
	case err := <-done:
		require.ErrorContains(t, err, "json bridge")
	case <-time.After(5 * time.Second):
		t.Fatal("Serve kept running with a dead JSON bridge")
	}
	_, statErr := os.Stat(srv.endpointPath)
	assert.True(t, os.IsNotExist(statErr), "endpoint must not stay advertised")
}

func TestJSONBridge_CancelledStreamDoesNotPanic(t *testing.T) {
	root := newTestClone(t)
	_, c := newBridgeClient(t, Options{Runners: Runners{New: func(string) RepoOps {
		return RepoOps{Update: &stubUpdateRunner{run: func(ctx context.Context, _ string, _ ...usecase.DownloadProgress) error {
			<-ctx.Done()
			return ctx.Err()
		}}}
	}}})
	code, _ := c.call(t, "/v1/WatchRepo", fmt.Sprintf(`{"root":%q}`, root))
	require.Equal(t, http.StatusOK, code)
	for i := 0; i < 20; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(i)*20*time.Millisecond)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/v1/Update", strings.NewReader(fmt.Sprintf(`{"root":%q}`, root)))
		require.NoError(t, err)
		req.Header.Set(TokenHeader, c.token)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			_ = resp.Body.Close()
		}
		cancel()
	}
	code, _ = c.call(t, "/v1/Ping", "")
	assert.Equal(t, http.StatusOK, code)
}
