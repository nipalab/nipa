package handler

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	httpApp "github.com/nipalab/nipa/internal/http"
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

func TestHandler_FileLockInvalidBody(t *testing.T) {
	env := newHandlerTestEnv(t)
	claims := &domain.Claims{UserID: env.userID, IsAdmin: true}
	projectParams := map[string]string{"org": "default", "project": "default"}

	appCtx := &fakeAppContext{claims: claims, pathParameters: projectParams, body: []byte("{")}
	env.handler.LockFile(appCtx)
	require.Equal(t, http.StatusInternalServerError, appCtx.statusCode)

	appCtx = &fakeAppContext{claims: claims, pathParameters: projectParams, body: []byte("{")}
	env.handler.UnlockFile(appCtx)
	require.Equal(t, http.StatusInternalServerError, appCtx.statusCode)
}

func TestHandler_FileLockContextError(t *testing.T) {
	env := newHandlerTestEnv(t)
	params := map[string]string{"org": "default", "project": "default"}

	tests := []struct {
		name string
		body string
		run  func(appCtx httpApp.AppContext)
	}{
		{name: "list", run: env.handler.ListFileLocks},
		{name: "lock", body: `{"path":"a.png"}`, run: env.handler.LockFile},
		{name: "unlock", body: `{"path":"a.png"}`, run: env.handler.UnlockFile},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			appCtx := canceledAppCtx(env.userID, params, tt.body)
			tt.run(appCtx)
			require.Equal(t, http.StatusInternalServerError, appCtx.statusCode)
		})
	}
}

func TestHandler_FileLockTTL(t *testing.T) {
	env := newHandlerTestEnv(t)
	claims := &domain.Claims{UserID: env.userID, IsAdmin: true}
	projectParams := map[string]string{"org": "default", "project": "default"}

	appCtx := &fakeAppContext{claims: claims, pathParameters: projectParams, body: []byte(`{"path":"a.png","ttl_seconds":600}`)}
	env.handler.LockFile(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	lock := appCtx.response.(model.FileLockResponse)
	require.NotNil(t, lock.ExpiresAt)
	require.WithinDuration(t, time.Now().Add(10*time.Minute), *lock.ExpiresAt, time.Minute)

	appCtx = &fakeAppContext{claims: claims, pathParameters: projectParams, body: []byte(`{"path":"b.png","ttl_seconds":1}`)}
	env.handler.LockFile(appCtx)
	require.Equal(t, http.StatusBadRequest, appCtx.statusCode)
}

func TestHandler_FileLockRequestRelease(t *testing.T) {
	env := newHandlerTestEnv(t)
	holder := &domain.Claims{UserID: env.userID, IsAdmin: true}
	_, err := env.db.Exec(`INSERT INTO users (id, name, email, password) VALUES (900, 'bob', 'bob@example.com', 'x')`)
	require.NoError(t, err)
	asker := &domain.Claims{UserID: 900, IsAdmin: true}
	params := map[string]string{"org": "default", "project": "default"}

	appCtx := &fakeAppContext{claims: holder, pathParameters: params, body: []byte(`{"path":"assets/orc.png","branch":"main"}`)}
	env.handler.LockFile(appCtx)
	lock := appCtx.response.(model.FileLockResponse)

	appCtx = &fakeAppContext{claims: asker, pathParameters: map[string]string{"org": "default", "project": "default", "id": lock.ID}, body: []byte(`{"message":"need it"}`)}
	env.handler.RequestFileLockRelease(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	request := appCtx.response.(model.FileLockRequestResponse)
	require.Equal(t, "need it", request.Message)

	appCtx = &fakeAppContext{claims: holder, pathParameters: params}
	env.handler.ListFileLocks(appCtx)
	locks := appCtx.response.([]model.FileLockResponse)
	require.Len(t, locks[0].Requests, 1)
	require.Equal(t, "bob", locks[0].Requests[0].RequesterName)

	for name, ctx := range map[string]*fakeAppContext{
		"bad id":     {claims: asker, pathParameters: map[string]string{"org": "default", "project": "default", "id": "!"}, body: []byte(`{}`)},
		"bad body":   {claims: asker, pathParameters: map[string]string{"org": "default", "project": "default", "id": lock.ID}, body: []byte(`{`)},
		"own lock":   {claims: holder, pathParameters: map[string]string{"org": "default", "project": "default", "id": lock.ID}, body: []byte(`{}`)},
		"no project": {claims: asker, pathParameters: map[string]string{"org": "default", "project": "ghost", "id": lock.ID}, body: []byte(`{}`)},
	} {
		env.handler.RequestFileLockRelease(ctx)
		require.GreaterOrEqual(t, ctx.statusCode, http.StatusBadRequest, name)
	}
}
