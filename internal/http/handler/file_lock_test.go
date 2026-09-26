package handler

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/http/model"
)

func TestHandler_FileLockFlow(t *testing.T) {
	env := newHandlerTestEnv(t)
	claims := &domain.Claims{UserID: env.userID, IsAdmin: true}
	projectParams := map[string]string{"org": "default", "project": "default"}

	appCtx := &fakeAppContext{
		claims:         claims,
		pathParameters: projectParams,
		body:           []byte(`{"path":"assets/orc.png","branch":"main"}`),
	}
	env.handler.LockFile(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	lock, ok := appCtx.response.(model.FileLockResponse)
	require.True(t, ok)
	require.Equal(t, "assets/orc.png", lock.Path)
	require.True(t, lock.Global)

	appCtx = &fakeAppContext{claims: claims, pathParameters: projectParams}
	env.handler.ListFileLocks(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	locks, ok := appCtx.response.([]model.FileLockResponse)
	require.True(t, ok)
	require.Len(t, locks, 1)
	require.Equal(t, "assets/orc.png", locks[0].Path)
	require.NotEmpty(t, locks[0].HeldByName)

	appCtx = &fakeAppContext{
		claims:         claims,
		pathParameters: projectParams,
		body:           []byte(`{"path":"assets/orc.png","branch":"main"}`),
	}
	env.handler.UnlockFile(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)

	appCtx = &fakeAppContext{claims: claims, pathParameters: projectParams}
	env.handler.ListFileLocks(appCtx)
	require.Empty(t, appCtx.response.([]model.FileLockResponse))
}

func TestHandler_FileLockErrors(t *testing.T) {
	env := newHandlerTestEnv(t)
	claims := &domain.Claims{UserID: env.userID, IsAdmin: true}
	projectParams := map[string]string{"org": "default", "project": "default"}

	appCtx := &fakeAppContext{claims: claims, pathParameters: projectParams, body: []byte(`{"path":""}`)}
	env.handler.LockFile(appCtx)
	require.Equal(t, http.StatusBadRequest, appCtx.statusCode)

	appCtx = &fakeAppContext{
		claims:         claims,
		pathParameters: map[string]string{"org": "default", "project": "ghost"},
		body:           []byte(`{"path":"a.png"}`),
	}
	env.handler.LockFile(appCtx)
	require.Equal(t, http.StatusNotFound, appCtx.statusCode)

	appCtx = &fakeAppContext{claims: claims, pathParameters: projectParams, body: []byte(`{"path":"missing.png"}`)}
	env.handler.UnlockFile(appCtx)
	require.Equal(t, http.StatusNotFound, appCtx.statusCode)
}
