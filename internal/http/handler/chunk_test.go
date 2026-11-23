package handler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/chunkurl"
	"github.com/nipalab/nipa/internal/snow"
)

const testChunkSigningKey = "test-chunk-signing-key"

func newChunkAppContext(method, rawURL string, body []byte, hash string) (*fakeAppContext, *httptest.ResponseRecorder) {
	request := httptest.NewRequest(method, rawURL, bytes.NewReader(body))
	if body != nil {
		request.ContentLength = int64(len(body))
	}
	return chunkAppContext(request, hash)
}

func chunkAppContext(request *http.Request, hash string) (*fakeAppContext, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	return &fakeAppContext{
		request:        request,
		responseWriter: recorder,
		pathParameters: map[string]string{"org": "default", "project": "default", "hash": hash},
	}, recorder
}

func TestChunkHandler_GetChunkRejectsMalformedURL(t *testing.T) {
	env := newHandlerTestEnv(t)
	h := NewChunkHandler(env.chunkUc, env.registry.common)

	appCtx, _ := newChunkAppContext(http.MethodGet, "/api/chunks/org/proj/abc?exp=1&sig=x", nil, "abc")
	h.GetChunk(appCtx)

	require.Equal(t, http.StatusBadRequest, appCtx.statusCode)
}

func TestChunkHandler_GetChunkRejectsInvalidHash(t *testing.T) {
	env := newHandlerTestEnv(t)
	h := NewChunkHandler(env.chunkUc, env.registry.common)

	exp := time.Now().Add(time.Hour).Unix()
	path := chunkurl.DownloadPath(testChunkSigningKey, "default", "default", "zz-not-hex", exp)

	appCtx, _ := newChunkAppContext(http.MethodGet, path, nil, "zz-not-hex")
	h.GetChunk(appCtx)

	require.Equal(t, http.StatusBadRequest, appCtx.statusCode)
}

func TestChunkHandler_GetChunk(t *testing.T) {
	env := newHandlerTestEnv(t)
	h := NewChunkHandler(env.chunkUc, env.registry.common)

	data := []byte("download payload")
	hash := chunker.Sum(data)
	require.NoError(t, env.chunkStore.Put(t.Context(), defaultProjectID(t, env), hash, data))
	exp := time.Now().Add(time.Hour).Unix()
	path := chunkurl.DownloadPath(testChunkSigningKey, "default", "default", hash.String(), exp)

	appCtx, _ := newChunkAppContext(http.MethodGet, path, nil, hash.String())
	h.GetChunk(appCtx)

	require.Equal(t, http.StatusOK, appCtx.statusCode)
	require.Equal(t, "application/octet-stream", appCtx.contentType)
	require.Equal(t, data, appCtx.raw)
}

func TestChunkHandler_GetChunkMissing(t *testing.T) {
	env := newHandlerTestEnv(t)
	h := NewChunkHandler(env.chunkUc, env.registry.common)

	hash := chunker.Sum([]byte("absent"))
	exp := time.Now().Add(time.Hour).Unix()
	path := chunkurl.DownloadPath(testChunkSigningKey, "default", "default", hash.String(), exp)

	appCtx, _ := newChunkAppContext(http.MethodGet, path, nil, hash.String())
	h.GetChunk(appCtx)

	require.Equal(t, http.StatusNotFound, appCtx.statusCode)
}

func TestChunkHandler_GetChunkRejectsTamperedURL(t *testing.T) {
	env := newHandlerTestEnv(t)
	h := NewChunkHandler(env.chunkUc, env.registry.common)

	data := []byte("download payload")
	hash := chunker.Sum(data)
	require.NoError(t, env.chunkStore.Put(t.Context(), defaultProjectID(t, env), hash, data))
	exp := time.Now().Add(time.Hour).Unix()
	path := chunkurl.DownloadPath(testChunkSigningKey, "default", "default", hash.String(), exp) + "0"

	appCtx, _ := newChunkAppContext(http.MethodGet, path, nil, hash.String())
	h.GetChunk(appCtx)

	require.Equal(t, http.StatusForbidden, appCtx.statusCode)
}

func TestChunkHandler_GetChunkRejectsWrongOperation(t *testing.T) {
	env := newHandlerTestEnv(t)
	h := NewChunkHandler(env.chunkUc, env.registry.common)

	data := []byte("download payload")
	hash := chunker.Sum(data)
	require.NoError(t, env.chunkStore.Put(t.Context(), defaultProjectID(t, env), hash, data))
	exp := time.Now().Add(time.Hour).Unix()
	// A single-chunk upload URL (deleted in favour of bundles) no longer
	// parses.
	path := "/api/chunks/default/default/" + hash.String() + "?op=put&size=16&exp=" + strconv.FormatInt(exp, 10) +
		"&sig=" + chunkurl.Sign(testChunkSigningKey, "default", "default", hash.String(), "put", 16, exp)

	appCtx, _ := newChunkAppContext(http.MethodGet, path, nil, hash.String())
	h.GetChunk(appCtx)

	require.Equal(t, http.StatusBadRequest, appCtx.statusCode)
}

func TestChunkHandler_UnknownProject(t *testing.T) {
	env := newHandlerTestEnv(t)
	h := NewChunkHandler(env.chunkUc, env.registry.common)

	data := []byte("chunk payload")
	hash := chunker.Sum(data)
	exp := time.Now().Add(time.Hour).Unix()

	body := encodeBundle(t, data)
	put, _ := newChunkAppContext(http.MethodPut, chunkurl.BundlePath(testChunkSigningKey, "default", "missing", 1<<20, 1, exp), body, "")
	put.pathParameters["project"] = "missing"
	h.PutBundle(put)
	require.Equal(t, http.StatusNotFound, put.statusCode)

	get, _ := newChunkAppContext(http.MethodGet, chunkurl.DownloadPath(testChunkSigningKey, "default", "missing", hash.String(), exp), nil, hash.String())
	get.pathParameters["project"] = "missing"
	h.GetChunk(get)
	require.Equal(t, http.StatusNotFound, get.statusCode)
}

// defaultProjectID is the id of the default/default project the signed chunk
// paths of these tests name.
func defaultProjectID(t *testing.T, env *handlerTestEnv) snow.ID {
	t.Helper()
	_, project, err := env.registry.common.ResolveBySlug(context.Background(), "default", "default")
	require.NoError(t, err)
	return project.ID
}
