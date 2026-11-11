package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	clientconfig "github.com/nipalab/nipa/internal/client/config"
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/client/securestorage"
)

type memKeys struct {
	key     string
	loadErr error
	saveErr error
}

func (m *memKeys) SaveLLMKey(key string) error {
	if m.saveErr != nil {
		return m.saveErr
	}
	m.key = key
	return nil
}

func (m *memKeys) LoadLLMKey() (string, error) { return m.key, m.loadErr }

func (m *memKeys) DeleteLLMKey() error {
	m.key = ""
	return nil
}

type generateDaemon struct {
	*blobDaemon
	resp *daemonpb.GenerateResponse
	err  error
	reqs []*daemonpb.GenerateRequest
}

func (d *generateDaemon) Generate(_ context.Context, req *daemonpb.GenerateRequest, _ ...grpc.CallOption) (*daemonpb.GenerateResponse, error) {
	d.reqs = append(d.reqs, req)
	return d.resp, d.err
}

func isolateLLM(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	t.Setenv(clientconfig.EnvLLMEndpoint, "")
	t.Setenv(clientconfig.EnvLLMModel, "")
	t.Setenv(clientconfig.EnvLLMAPIKey, "")
	t.Setenv(securestorage.TokenFileEnv, filepath.Join(dir, "tokens.json"))
	return dir
}

func TestService_Generate(t *testing.T) {
	d := &generateDaemon{blobDaemon: &blobDaemon{opsDaemon: &opsDaemon{fakeDaemon: &fakeDaemon{watchErr: map[string]error{}, changes: make(chan *daemonpb.RepoChange, 4)}}}}
	svc := New(func(context.Context) (Session, error) { return &opsSession{client: d}, nil }, &memStore{}, nil)
	t.Cleanup(func() { _ = svc.ServiceShutdown() })

	d.resp = &daemonpb.GenerateResponse{Text: "feat: add a", Model: "qwen"}
	draft, err := svc.GenerateCommitMessage(ctx, "/work/game")
	require.NoError(t, err)
	assert.Equal(t, Draft{Text: "feat: add a", Model: "qwen"}, draft)
	assert.Equal(t, "commit", d.reqs[0].GetKind())

	d.resp = &daemonpb.GenerateResponse{Title: "Add c", Text: "Body", Model: "qwen", Source: "feature", Target: "main"}
	draft, err = svc.GenerateMergeRequest(ctx, "/work/game", "feature", "")
	require.NoError(t, err)
	assert.Equal(t, Draft{Title: "Add c", Text: "Body", Model: "qwen", Source: "feature", Target: "main"}, draft)
	assert.Equal(t, "mr", d.reqs[1].GetKind())
	assert.Equal(t, "feature", d.reqs[1].GetSource())

	d.err = status.Error(codes.FailedPrecondition, "AI generation is not configured")
	_, err = svc.GenerateCommitMessage(ctx, "/work/game")
	assert.EqualError(t, err, "AI generation is not configured")

	down := New(func(context.Context) (Session, error) { return nil, errors.New("daemon down") }, &memStore{}, nil)
	t.Cleanup(func() { _ = down.ServiceShutdown() })
	_, err = down.GenerateMergeRequest(ctx, "/work/game", "", "")
	assert.Error(t, err)
}

func TestService_LLMSettingsRoundTrip(t *testing.T) {
	dir := isolateLLM(t)
	keys := &memKeys{}
	h := newHarness()
	h.svc.Keys = keys

	got, err := h.svc.LLMSettings()
	require.NoError(t, err)
	assert.Equal(t, LLMSettings{}, got)

	got, err = h.svc.SetLLM(" http://localhost:11434/v1 ", "qwen")
	require.NoError(t, err)
	assert.Equal(t, LLMSettings{Endpoint: "http://localhost:11434/v1", Model: "qwen", Configured: true}, got)

	require.NoError(t, h.svc.SetLLMKey("sk-secret"))
	got, err = h.svc.LLMSettings()
	require.NoError(t, err)
	assert.True(t, got.KeySaved)
	data, err := os.ReadFile(filepath.Join(dir, "nipa", "config.json"))
	require.NoError(t, err)
	assert.NotContains(t, string(data), "sk-secret")
	raw, err := json.Marshal(got)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "sk-secret")

	require.Error(t, h.svc.SetLLMKey("  "))
	keys.saveErr = errors.New("keyring locked")
	require.ErrorContains(t, h.svc.SetLLMKey("x"), "locked")
	require.NoError(t, h.svc.ClearLLMKey())
	keys.loadErr = errors.New("locked")
	got, err = h.svc.LLMSettings()
	require.NoError(t, err)
	assert.False(t, got.KeySaved)

	_, err = h.svc.SetLLM("http://x", "")
	require.ErrorContains(t, err, "both")
	_, err = h.svc.SetLLM("x", "m")
	require.ErrorContains(t, err, "http(s)")

	got, err = h.svc.SetLLM("", "")
	require.NoError(t, err)
	assert.False(t, got.Configured)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "nipa", "config.json"), []byte("{"), 0o600))
	_, err = h.svc.LLMSettings()
	require.Error(t, err)
}

func TestService_DefaultKeysUseSecureStorage(t *testing.T) {
	isolateLLM(t)
	h := newHarness()
	require.NoError(t, h.svc.SetLLMKey("sk-file"))
	got, err := h.svc.LLMSettings()
	require.NoError(t, err)
	assert.True(t, got.KeySaved)
	require.NoError(t, h.svc.ClearLLMKey())
}

func TestService_TestLLM(t *testing.T) {
	dir := isolateLLM(t)
	var auth string
	code := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if code != http.StatusOK {
			w.WriteHeader(code)
			_, _ = w.Write([]byte(`{"error":{"message":"invalid key sk-typed"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": "OK"}}}})
	}))
	t.Cleanup(srv.Close)
	keys := &memKeys{key: "sk-saved"}
	h := newHarness()
	h.svc.Keys = keys

	require.NoError(t, h.svc.TestLLM(ctx, srv.URL+"/v1", "m", "sk-typed"))
	assert.Equal(t, "Bearer sk-typed", auth)

	require.NoError(t, h.svc.TestLLM(ctx, srv.URL+"/v1", "m", ""))
	assert.Equal(t, "Bearer sk-saved", auth)

	t.Setenv(clientconfig.EnvLLMAPIKey, "sk-env")
	require.NoError(t, h.svc.TestLLM(ctx, srv.URL+"/v1", "m", ""))
	assert.Equal(t, "Bearer sk-env", auth)

	code = http.StatusUnauthorized
	err := h.svc.TestLLM(ctx, srv.URL+"/v1", "m", "sk-typed")
	require.ErrorContains(t, err, "answered 401")
	assert.NotContains(t, err.Error(), "sk-typed")

	require.ErrorContains(t, h.svc.TestLLM(ctx, "", "m", "k"), "http(s) endpoint")

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "nipa"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "nipa", "config.json"), []byte("{"), 0o600))
	require.Error(t, h.svc.TestLLM(ctx, srv.URL, "m", ""))
}
