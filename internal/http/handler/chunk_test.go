package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/chunkurl"
)

const testChunkSigningKey = "test-chunk-signing-key"

func newChunkAppContext(method, rawURL string, body []byte, hash string) (*fakeAppContext, *httptest.ResponseRecorder) {
	request := httptest.NewRequest(method, rawURL, bytes.NewReader(body))
	if body != nil {
		request.ContentLength = int64(len(body))
	}
	recorder := httptest.NewRecorder()
	return &fakeAppContext{
		request:        request,
		responseWriter: recorder,
		pathParameters: map[string]string{"org": "org", "project": "proj", "hash": hash},
	}, recorder
}

func TestChunkHandler_PutChunk(t *testing.T) {
	env := newHandlerTestEnv(t)
	h := NewChunkHandler(env.chunkUc)

	data := []byte("chunk payload")
	hash := chunker.Sum(data)
	exp := time.Now().Add(time.Hour).Unix()
	path := chunkurl.UploadPath(testChunkSigningKey, "org", "proj", hash.String(), int64(len(data)), exp)

	appCtx, recorder := newChunkAppContext(http.MethodPut, path, data, hash.String())
	h.PutChunk(appCtx)

	require.Equal(t, http.StatusNoContent, recorder.Code)
	stored, err := env.chunkStore.Get(appCtx.Context(), hash)
	require.NoError(t, err)
	require.Equal(t, data, stored)
}

func TestChunkHandler_PutChunkRejectsTamperedURL(t *testing.T) {
	env := newHandlerTestEnv(t)
	h := NewChunkHandler(env.chunkUc)

	data := []byte("chunk payload")
	hash := chunker.Sum(data)
	exp := time.Now().Add(time.Hour).Unix()
	path := chunkurl.UploadPath(testChunkSigningKey, "org", "proj", hash.String(), int64(len(data)), exp)
	tampered := path + "0"

	appCtx, _ := newChunkAppContext(http.MethodPut, tampered, data, hash.String())
	h.PutChunk(appCtx)

	require.Equal(t, http.StatusForbidden, appCtx.statusCode)
}

func TestChunkHandler_PutChunkRejectsExpiredURL(t *testing.T) {
	env := newHandlerTestEnv(t)
	h := NewChunkHandler(env.chunkUc)

	data := []byte("chunk payload")
	hash := chunker.Sum(data)
	exp := time.Now().Add(-time.Hour).Unix()
	path := chunkurl.UploadPath(testChunkSigningKey, "org", "proj", hash.String(), int64(len(data)), exp)

	appCtx, _ := newChunkAppContext(http.MethodPut, path, data, hash.String())
	h.PutChunk(appCtx)

	require.Equal(t, http.StatusForbidden, appCtx.statusCode)
}

func TestChunkHandler_PutChunkRejectsWrongOperation(t *testing.T) {
	env := newHandlerTestEnv(t)
	h := NewChunkHandler(env.chunkUc)

	data := []byte("chunk payload")
	hash := chunker.Sum(data)
	exp := time.Now().Add(time.Hour).Unix()
	path := chunkurl.DownloadPath(testChunkSigningKey, "org", "proj", hash.String(), exp)

	appCtx, _ := newChunkAppContext(http.MethodPut, path, data, hash.String())
	h.PutChunk(appCtx)

	require.Equal(t, http.StatusForbidden, appCtx.statusCode)
}

func TestChunkHandler_PutChunkRejectsSizeMismatch(t *testing.T) {
	env := newHandlerTestEnv(t)
	h := NewChunkHandler(env.chunkUc)

	data := []byte("chunk payload")
	hash := chunker.Sum(data)
	exp := time.Now().Add(time.Hour).Unix()
	path := chunkurl.UploadPath(testChunkSigningKey, "org", "proj", hash.String(), int64(len(data))+10, exp)

	appCtx, _ := newChunkAppContext(http.MethodPut, path, data, hash.String())
	h.PutChunk(appCtx)

	require.Equal(t, http.StatusBadRequest, appCtx.statusCode)
}

func TestChunkHandler_PutChunkRejectsHashMismatch(t *testing.T) {
	env := newHandlerTestEnv(t)
	h := NewChunkHandler(env.chunkUc)

	data := []byte("chunk payload")
	hash := chunker.Sum(data)
	exp := time.Now().Add(time.Hour).Unix()
	path := chunkurl.UploadPath(testChunkSigningKey, "org", "proj", hash.String(), int64(len(data)), exp)

	appCtx, _ := newChunkAppContext(http.MethodPut, path, []byte("other bytes!"), hash.String())
	h.PutChunk(appCtx)

	require.Equal(t, http.StatusBadRequest, appCtx.statusCode)
}

func TestChunkHandler_GetChunk(t *testing.T) {
	env := newHandlerTestEnv(t)
	h := NewChunkHandler(env.chunkUc)

	data := []byte("download payload")
	hash := chunker.Sum(data)
	require.NoError(t, env.chunkStore.Put(t.Context(), hash, data))
	exp := time.Now().Add(time.Hour).Unix()
	path := chunkurl.DownloadPath(testChunkSigningKey, "org", "proj", hash.String(), exp)

	appCtx, _ := newChunkAppContext(http.MethodGet, path, nil, hash.String())
	h.GetChunk(appCtx)

	require.Equal(t, http.StatusOK, appCtx.statusCode)
	require.Equal(t, "application/octet-stream", appCtx.contentType)
	require.Equal(t, data, appCtx.raw)
}

func TestChunkHandler_GetChunkMissing(t *testing.T) {
	env := newHandlerTestEnv(t)
	h := NewChunkHandler(env.chunkUc)

	hash := chunker.Sum([]byte("absent"))
	exp := time.Now().Add(time.Hour).Unix()
	path := chunkurl.DownloadPath(testChunkSigningKey, "org", "proj", hash.String(), exp)

	appCtx, _ := newChunkAppContext(http.MethodGet, path, nil, hash.String())
	h.GetChunk(appCtx)

	require.Equal(t, http.StatusNotFound, appCtx.statusCode)
}

func TestChunkHandler_GetChunkRejectsWrongOperation(t *testing.T) {
	env := newHandlerTestEnv(t)
	h := NewChunkHandler(env.chunkUc)

	data := []byte("download payload")
	hash := chunker.Sum(data)
	require.NoError(t, env.chunkStore.Put(t.Context(), hash, data))
	exp := time.Now().Add(time.Hour).Unix()
	path := chunkurl.UploadPath(testChunkSigningKey, "org", "proj", hash.String(), int64(len(data)), exp)

	appCtx, _ := newChunkAppContext(http.MethodGet, path, nil, hash.String())
	h.GetChunk(appCtx)

	require.Equal(t, http.StatusForbidden, appCtx.statusCode)
}
