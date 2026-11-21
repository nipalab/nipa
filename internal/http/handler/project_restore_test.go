package handler

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/http/model"
	"github.com/nipalab/nipa/internal/snow"
)

func TestHandler_DeletedProjectsAndRestore(t *testing.T) {
	env := newHandlerTestEnv(t)

	appCtx := adminAppCtx(env.userID, map[string]string{"org": "default"}, `{"name":"Old Game"}`)
	env.handler.CreateProject(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	created := appCtx.response.(model.ProjectResponse)

	appCtx = adminAppCtx(env.userID, map[string]string{"org": "default", "project": "old-game"}, "")
	env.handler.DeleteProject(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)

	appCtx = adminAppCtx(env.userID, map[string]string{"org": "default"}, "")
	env.handler.ListDeletedProjects(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	deleted, ok := appCtx.response.([]model.DeletedProjectResponse)
	require.True(t, ok)
	require.Len(t, deleted, 1)
	require.Equal(t, created.ID, deleted[0].ID)
	require.Equal(t, "old-game", deleted[0].Slug)
	require.NotEmpty(t, deleted[0].DeletedAt)
	require.Empty(t, deleted[0].PurgeAfter, "no retention configured: kept until restored")

	appCtx = adminAppCtx(env.userID, map[string]string{"org": "default", "id": created.ID}, "")
	env.handler.RestoreProject(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	require.Equal(t, "old-game", appCtx.response.(model.ProjectResponse).Slug)

	appCtx = adminAppCtx(env.userID, map[string]string{"org": "default", "project": "old-game"}, "")
	env.handler.GetProject(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode, "the restored project is reachable at its slug")

	appCtx = adminAppCtx(env.userID, map[string]string{"org": "default", "id": created.ID}, "")
	env.handler.RestoreProject(appCtx)
	require.Equal(t, http.StatusNotFound, appCtx.statusCode, "a live project is not a deleted one")
}

func TestHandler_DeletedProjectsErrors(t *testing.T) {
	env := newHandlerTestEnv(t)

	appCtx := userAppCtx(env.userID, map[string]string{"org": "default"}, "")
	env.handler.ListDeletedProjects(appCtx)
	require.Equal(t, http.StatusForbidden, appCtx.statusCode)

	appCtx = userAppCtx(env.userID, map[string]string{"org": "default", "id": "abc"}, "")
	env.handler.RestoreProject(appCtx)
	require.Equal(t, http.StatusForbidden, appCtx.statusCode)

	appCtx = adminAppCtx(env.userID, map[string]string{"org": "missing"}, "")
	env.handler.ListDeletedProjects(appCtx)
	require.Equal(t, http.StatusNotFound, appCtx.statusCode)

	appCtx = adminAppCtx(env.userID, map[string]string{"org": "missing", "id": "abc"}, "")
	env.handler.RestoreProject(appCtx)
	require.Equal(t, http.StatusNotFound, appCtx.statusCode)

	appCtx = adminAppCtx(env.userID, map[string]string{"org": "default", "id": "not base36!"}, "")
	env.handler.RestoreProject(appCtx)
	require.Equal(t, http.StatusNotFound, appCtx.statusCode)
}

func TestToDeletedProjectResponse_PurgeAfter(t *testing.T) {
	at := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	resp := toDeletedProjectResponse(&domain.Project{ID: snow.ID(5), OrgID: 1, Slug: "s", DeletedAt: &at}, 30*24*time.Hour)
	require.Equal(t, "2026-11-01T10:00:00Z", resp.DeletedAt)
	require.Equal(t, "2026-12-01T10:00:00Z", resp.PurgeAfter)

	resp = toDeletedProjectResponse(&domain.Project{ID: snow.ID(5)}, time.Hour)
	require.Empty(t, resp.DeletedAt)
	require.Empty(t, resp.PurgeAfter)
}
