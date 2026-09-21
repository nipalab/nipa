package handler

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	httpApp "github.com/nipalab/nipa/internal/http"
	"github.com/nipalab/nipa/internal/http/model"
)

func TestHandler_ProjectLifecycle(t *testing.T) {
	env := newHandlerTestEnv(t)

	appCtx := adminAppCtx(env.userID, map[string]string{"org": "default"},
		`{"name":"My Game","description":"fun"}`)
	env.handler.CreateProject(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	project, ok := appCtx.response.(model.ProjectResponse)
	require.True(t, ok)
	require.Equal(t, "my-game", project.Slug)
	require.Equal(t, "My Game", project.Name)

	appCtx = adminAppCtx(env.userID, map[string]string{"org": "default"}, "")
	env.handler.ListProjects(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	projects, ok := appCtx.response.([]model.ProjectResponse)
	require.True(t, ok)
	require.Len(t, projects, 2, "seeded default project plus the created one")

	appCtx = adminAppCtx(env.userID, map[string]string{"org": "default", "project": "my-game"}, "")
	env.handler.GetProject(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)

	appCtx = adminAppCtx(env.userID, map[string]string{"org": "default", "project": "my-game"},
		`{"name":"My Game 2","description":"v2"}`)
	env.handler.UpdateProject(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	updated, ok := appCtx.response.(model.ProjectResponse)
	require.True(t, ok)
	require.Equal(t, "My Game 2", updated.Name)
	require.Equal(t, "my-game", updated.Slug, "slug is stable across renames")

	appCtx = adminAppCtx(env.userID, map[string]string{"org": "default", "project": "my-game"}, "")
	env.handler.DeleteProject(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)

	appCtx = adminAppCtx(env.userID, map[string]string{"org": "default", "project": "my-game"}, "")
	env.handler.GetProject(appCtx)
	require.Equal(t, http.StatusNotFound, appCtx.statusCode)
}

func TestHandler_ProjectValidation(t *testing.T) {
	env := newHandlerTestEnv(t)

	appCtx := adminAppCtx(env.userID, map[string]string{"org": "default"}, `{"name":"Default","slug":"default"}`)
	env.handler.CreateProject(appCtx)
	require.Equal(t, http.StatusConflict, appCtx.statusCode)

	appCtx = adminAppCtx(env.userID, map[string]string{"org": "default"}, `{"name":"  "}`)
	env.handler.CreateProject(appCtx)
	require.Equal(t, http.StatusBadRequest, appCtx.statusCode)

	appCtx = adminAppCtx(env.userID, map[string]string{"org": "default"}, `{"name":"Assets","slug":"Bad Slug"}`)
	env.handler.CreateProject(appCtx)
	require.Equal(t, http.StatusBadRequest, appCtx.statusCode)

	appCtx = adminAppCtx(env.userID, map[string]string{"org": "default", "project": "default"}, `{"name":"  "}`)
	env.handler.UpdateProject(appCtx)
	require.Equal(t, http.StatusBadRequest, appCtx.statusCode)

	appCtx = adminAppCtx(env.userID, map[string]string{"org": "missing"}, "")
	env.handler.ListProjects(appCtx)
	require.Equal(t, http.StatusNotFound, appCtx.statusCode)

	appCtx = adminAppCtx(env.userID, map[string]string{"org": "default", "project": "missing"}, "")
	env.handler.GetProject(appCtx)
	require.Equal(t, http.StatusNotFound, appCtx.statusCode)
}

func TestHandler_ProjectAccess(t *testing.T) {
	env := newHandlerTestEnv(t)

	listCtx := userAppCtx(env.userID, map[string]string{"org": "default"}, "")
	env.handler.ListProjects(listCtx)
	require.Equal(t, http.StatusOK, listCtx.statusCode)
	projects, ok := listCtx.response.([]model.ProjectResponse)
	require.True(t, ok)
	require.Empty(t, projects, "users without read rules see no projects")

	getCtx := userAppCtx(env.userID, map[string]string{"org": "default", "project": "default"}, "")
	env.handler.GetProject(getCtx)
	require.Equal(t, http.StatusNotFound, getCtx.statusCode, "hidden projects look missing")

	createCtx := userAppCtx(env.userID, map[string]string{"org": "default"}, `{"name":"Assets"}`)
	env.handler.CreateProject(createCtx)
	require.Equal(t, http.StatusForbidden, createCtx.statusCode)

	updateCtx := userAppCtx(env.userID, map[string]string{"org": "default", "project": "default"}, `{"name":"X"}`)
	env.handler.UpdateProject(updateCtx)
	require.Equal(t, http.StatusForbidden, updateCtx.statusCode)

	deleteCtx := userAppCtx(env.userID, map[string]string{"org": "default", "project": "default"}, "")
	env.handler.DeleteProject(deleteCtx)
	require.Equal(t, http.StatusForbidden, deleteCtx.statusCode)
}

func TestHandler_ProjectContextError(t *testing.T) {
	env := newHandlerTestEnv(t)

	tests := []struct {
		name   string
		params map[string]string
		body   string
		run    func(appCtx httpApp.AppContext)
	}{
		{name: "list", params: map[string]string{"org": "default"}, run: env.handler.ListProjects},
		{name: "create", params: map[string]string{"org": "default"}, body: `{"name":"X"}`, run: env.handler.CreateProject},
		{name: "get", params: map[string]string{"org": "default", "project": "default"}, run: env.handler.GetProject},
		{name: "update", params: map[string]string{"org": "default", "project": "default"}, body: `{"name":"X"}`, run: env.handler.UpdateProject},
		{name: "delete", params: map[string]string{"org": "default", "project": "default"}, run: env.handler.DeleteProject},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			appCtx := canceledAppCtx(env.userID, tt.params, tt.body)
			tt.run(appCtx)
			require.Equal(t, http.StatusInternalServerError, appCtx.statusCode)
		})
	}
}
