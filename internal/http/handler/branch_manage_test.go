package handler

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	httpApp "github.com/nipalab/nipa/internal/http"
	"github.com/nipalab/nipa/internal/http/model"
)

func TestHandler_BranchManagement(t *testing.T) {
	env := newHandlerTestEnv(t)
	claims := &domain.Claims{UserID: env.userID, IsAdmin: true}
	projectParams := map[string]string{"org": "default", "project": "default"}

	appCtx := &fakeAppContext{
		claims:         claims,
		pathParameters: projectParams,
		body:           []byte(`{"name":"feature","from":"main"}`),
	}
	env.handler.CreateProjectBranch(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	created := appCtx.response.(model.BranchResponse)
	require.Equal(t, "feature", created.Name)
	require.False(t, created.IsDefault)

	appCtx = &fakeAppContext{
		claims:         claims,
		pathParameters: map[string]string{"org": "default", "project": "default", "name": "feature"},
		body:           []byte(`{"name":"renamed"}`),
	}
	env.handler.RenameProjectBranch(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	require.Equal(t, "renamed", appCtx.response.(model.BranchResponse).Name)

	appCtx = &fakeAppContext{
		claims:         claims,
		pathParameters: map[string]string{"org": "default", "project": "default", "name": "renamed"},
		body:           []byte(`{"protected":true}`),
	}
	env.handler.SetProjectBranchProtection(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	require.True(t, appCtx.response.(model.BranchResponse).IsProtected)

	appCtx = browserAppCtx(claims, map[string]string{"org": "default", "project": "default", "name": "renamed"}, nil)
	env.handler.SetProjectBranchDefault(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	require.True(t, appCtx.response.(model.BranchResponse).IsDefault)

	appCtx = browserAppCtx(claims, map[string]string{"org": "default", "project": "default", "name": "renamed"}, nil)
	env.handler.DeleteProjectBranch(appCtx)
	require.Equal(t, http.StatusConflict, appCtx.statusCode, "default branch cannot be deleted")

	appCtx = browserAppCtx(claims, map[string]string{"org": "default", "project": "default", "name": "main"}, nil)
	env.handler.SetProjectBranchDefault(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)

	appCtx = browserAppCtx(claims, map[string]string{"org": "default", "project": "default", "name": "renamed"}, nil)
	env.handler.DeleteProjectBranch(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)

	appCtx = browserAppCtx(claims, projectParams, nil)
	env.handler.ListProjectBranches(appCtx)
	branches := appCtx.response.([]model.BranchResponse)
	require.Len(t, branches, 1)
	require.Equal(t, "main", branches[0].Name)
}

func TestHandler_BranchManagement_ValidationAndAccess(t *testing.T) {
	env := newHandlerTestEnv(t)
	admin := &domain.Claims{UserID: env.userID, IsAdmin: true}
	projectParams := map[string]string{"org": "default", "project": "default"}

	appCtx := &fakeAppContext{claims: admin, pathParameters: projectParams, body: []byte(`{"name":"  "}`)}
	env.handler.CreateProjectBranch(appCtx)
	require.Equal(t, http.StatusBadRequest, appCtx.statusCode)

	appCtx = &fakeAppContext{claims: admin, pathParameters: projectParams, body: []byte(`{"name":"x","from":"missing"}`)}
	env.handler.CreateProjectBranch(appCtx)
	require.Equal(t, http.StatusNotFound, appCtx.statusCode)

	appCtx = &fakeAppContext{
		claims:         admin,
		pathParameters: map[string]string{"org": "default", "project": "default", "name": "ghost"},
		body:           []byte(`{"name":"renamed"}`),
	}
	env.handler.RenameProjectBranch(appCtx)
	require.Equal(t, http.StatusNotFound, appCtx.statusCode)

	appCtx = &fakeAppContext{
		claims:         admin,
		pathParameters: map[string]string{"org": "default", "project": "default", "name": "main"},
		body:           []byte(`{"protected":true}`),
	}
	env.handler.SetProjectBranchProtection(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	appCtx = &fakeAppContext{
		claims:         admin,
		pathParameters: map[string]string{"org": "default", "project": "default", "name": "main"},
		body:           []byte(`{"protected":false}`),
	}
	env.handler.SetProjectBranchProtection(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)

	regular := &domain.Claims{UserID: env.userID}
	appCtx = &fakeAppContext{claims: regular, pathParameters: projectParams, body: []byte(`{"name":"feature"}`)}
	env.handler.CreateProjectBranch(appCtx)
	require.Equal(t, http.StatusForbidden, appCtx.statusCode)

	appCtx = &fakeAppContext{
		claims:         regular,
		pathParameters: map[string]string{"org": "default", "project": "default", "name": "main"},
		body:           []byte(`{"protected":true}`),
	}
	env.handler.SetProjectBranchProtection(appCtx)
	require.Equal(t, http.StatusForbidden, appCtx.statusCode)

	appCtx = browserAppCtx(regular, map[string]string{"org": "default", "project": "default", "name": "main"}, nil)
	env.handler.SetProjectBranchDefault(appCtx)
	require.Equal(t, http.StatusForbidden, appCtx.statusCode)

	appCtx = browserAppCtx(regular, map[string]string{"org": "default", "project": "default", "name": "main"}, nil)
	env.handler.DeleteProjectBranch(appCtx)
	require.Equal(t, http.StatusForbidden, appCtx.statusCode)
}

func TestHandler_BranchManagement_Errors(t *testing.T) {
	env := newHandlerTestEnv(t)
	admin := &domain.Claims{UserID: env.userID, IsAdmin: true}
	missingOrg := map[string]string{"org": "missing", "project": "default"}

	appCtx := &fakeAppContext{claims: admin, pathParameters: missingOrg, body: []byte(`{"name":"x"}`)}
	env.handler.CreateProjectBranch(appCtx)
	require.Equal(t, http.StatusNotFound, appCtx.statusCode)

	appCtx = &fakeAppContext{claims: admin, pathParameters: missingOrg, body: []byte(`{"name":"x"}`)}
	env.handler.RenameProjectBranch(appCtx)
	require.Equal(t, http.StatusNotFound, appCtx.statusCode)

	appCtx = browserAppCtx(admin, missingOrg, nil)
	env.handler.DeleteProjectBranch(appCtx)
	require.Equal(t, http.StatusNotFound, appCtx.statusCode)

	appCtx = browserAppCtx(admin, missingOrg, nil)
	env.handler.SetProjectBranchDefault(appCtx)
	require.Equal(t, http.StatusNotFound, appCtx.statusCode)

	appCtx = &fakeAppContext{claims: admin, pathParameters: missingOrg, body: []byte(`{}`)}
	env.handler.SetProjectBranchProtection(appCtx)
	require.Equal(t, http.StatusNotFound, appCtx.statusCode)

	projectParams := map[string]string{"org": "default", "project": "default"}
	appCtx = &fakeAppContext{claims: admin, pathParameters: projectParams, body: []byte(`{"name":`)}
	env.handler.CreateProjectBranch(appCtx)
	require.Equal(t, http.StatusInternalServerError, appCtx.statusCode)

	appCtx = &fakeAppContext{
		claims:         admin,
		pathParameters: map[string]string{"org": "default", "project": "default", "name": "main"},
		body:           []byte(`{"name":`),
	}
	env.handler.RenameProjectBranch(appCtx)
	require.Equal(t, http.StatusInternalServerError, appCtx.statusCode)

	appCtx = &fakeAppContext{
		claims:         admin,
		pathParameters: map[string]string{"org": "default", "project": "default", "name": "main"},
		body:           []byte(`{"protected":`),
	}
	env.handler.SetProjectBranchProtection(appCtx)
	require.Equal(t, http.StatusInternalServerError, appCtx.statusCode)

	appCtx = &fakeAppContext{claims: admin, pathParameters: projectParams, body: []byte(`{"name":"a","from":"zzzz"}`)}
	env.handler.CreateProjectBranch(appCtx)
	require.Equal(t, http.StatusNotFound, appCtx.statusCode, "missing fork commit")

	appCtx = &fakeAppContext{claims: admin, pathParameters: projectParams, body: []byte(`{"name":"taken","from":"main"}`)}
	env.handler.CreateProjectBranch(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	appCtx = &fakeAppContext{
		claims:         admin,
		pathParameters: map[string]string{"org": "default", "project": "default", "name": "main"},
		body:           []byte(`{"name":"taken"}`),
	}
	env.handler.RenameProjectBranch(appCtx)
	require.Equal(t, http.StatusConflict, appCtx.statusCode)
}

func TestHandler_BranchManagement_ContextError(t *testing.T) {
	env := newHandlerTestEnv(t)
	projectParams := map[string]string{"org": "default", "project": "default", "name": "main"}

	tests := []struct {
		name string
		body string
		run  func(appCtx httpApp.AppContext)
	}{
		{name: "create", body: `{"name":"feature"}`, run: env.handler.CreateProjectBranch},
		{name: "rename", body: `{"name":"renamed"}`, run: env.handler.RenameProjectBranch},
		{name: "delete", run: env.handler.DeleteProjectBranch},
		{name: "default", run: env.handler.SetProjectBranchDefault},
		{name: "protection", body: `{"protected":true}`, run: env.handler.SetProjectBranchProtection},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			appCtx := canceledAppCtx(env.userID, projectParams, tt.body)
			tt.run(appCtx)
			require.Equal(t, http.StatusInternalServerError, appCtx.statusCode)
		})
	}
}
