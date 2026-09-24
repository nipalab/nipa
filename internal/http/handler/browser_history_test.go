package handler

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/http/model"
	"github.com/nipalab/nipa/internal/snow"
)

func TestHandler_BrowserTreeHistory(t *testing.T) {
	env := newHandlerTestEnv(t)
	first := env.seedFiles(t, map[string]string{
		"public/a.txt": "hello",
		"top.txt":      "one",
	})
	second := env.seedPushTo(t, "main", first.CommitID.Base36(), map[string]string{
		"top.txt": "two",
	})

	claims := &domain.Claims{UserID: env.userID, IsAdmin: true}
	projectParams := map[string]string{"org": "default", "project": "default"}

	appCtx := browserAppCtx(claims, projectParams, map[string]string{"rev": "main", "history": "1"})
	env.handler.GetTree(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	tree, ok := appCtx.response.(model.TreeResponse)
	require.True(t, ok)
	require.Len(t, tree.Entries, 2)
	require.NotNil(t, tree.LatestCommit)
	require.Equal(t, second.CommitID.Base36(), tree.LatestCommit.ID)

	require.Equal(t, "public", tree.Entries[0].Path)
	require.NotNil(t, tree.Entries[0].LastCommit)
	require.Equal(t, first.CommitID.Base36(), tree.Entries[0].LastCommit.ID)

	require.Equal(t, "top.txt", tree.Entries[1].Path)
	require.NotNil(t, tree.Entries[1].LastCommit)
	require.Equal(t, second.CommitID.Base36(), tree.Entries[1].LastCommit.ID)
}

func TestHandler_BrowserTreeHistorySubdirectory(t *testing.T) {
	env := newHandlerTestEnv(t)
	first := env.seedFiles(t, map[string]string{
		"public/a.txt": "hello",
		"top.txt":      "one",
	})
	second := env.seedPushTo(t, "main", first.CommitID.Base36(), map[string]string{
		"public/a.txt": "hello world",
	})

	claims := &domain.Claims{UserID: env.userID, IsAdmin: true}
	projectParams := map[string]string{"org": "default", "project": "default"}

	appCtx := browserAppCtx(claims, projectParams, map[string]string{"rev": "main", "path": "public", "history": "1"})
	env.handler.GetTree(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	tree, ok := appCtx.response.(model.TreeResponse)
	require.True(t, ok)
	require.Len(t, tree.Entries, 1)
	require.Equal(t, "public/a.txt", tree.Entries[0].Path)
	require.NotNil(t, tree.Entries[0].LastCommit)
	require.Equal(t, second.CommitID.Base36(), tree.Entries[0].LastCommit.ID)
	require.NotNil(t, tree.LatestCommit)
	require.Equal(t, second.CommitID.Base36(), tree.LatestCommit.ID)
}

func TestHandler_BrowserTreeErrorPaths(t *testing.T) {
	env := newHandlerTestEnv(t)
	env.seedFiles(t, map[string]string{
		"public/a.txt":   "hello",
		"secret/key.bin": "top secret",
	})

	claims := &domain.Claims{UserID: env.userID, IsAdmin: true}
	projectParams := map[string]string{"org": "default", "project": "default"}

	appCtx := browserAppCtx(claims, projectParams, map[string]string{"rev": "!!!", "recursive": "1"})
	env.handler.GetTree(appCtx)
	require.Equal(t, http.StatusNotFound, appCtx.statusCode)

	appCtx = browserAppCtx(claims, projectParams, map[string]string{"rev": "!!!", "history": "1"})
	env.handler.GetTree(appCtx)
	require.Equal(t, http.StatusNotFound, appCtx.statusCode)

	reader := env.createUser(t, "reader2", "reader2@example.com")
	projectID := snow.ID(1)
	_, err := env.pbacRepo.CreateRule(context.Background(), domain.PBACRule{
		UserID: &reader.ID, OrgID: 1, ProjectID: &projectID,
		PathPrefix: "public", Permission: domain.PermissionRead,
	})
	require.NoError(t, err)

	appCtx = browserAppCtx(&domain.Claims{UserID: reader.ID}, projectParams, map[string]string{"branch": "main", "path": "secret"})
	env.handler.ListCommits(appCtx)
	require.Equal(t, http.StatusNotFound, appCtx.statusCode)
}

func TestHandler_BrowserTreeRecursive(t *testing.T) {
	env := newHandlerTestEnv(t)
	env.seedFiles(t, map[string]string{
		"public/a.txt": "hello",
		"top.txt":      "one",
	})

	claims := &domain.Claims{UserID: env.userID, IsAdmin: true}
	projectParams := map[string]string{"org": "default", "project": "default"}

	appCtx := browserAppCtx(claims, projectParams, map[string]string{"rev": "main", "recursive": "1"})
	env.handler.GetTree(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	tree, ok := appCtx.response.(model.TreeResponse)
	require.True(t, ok)
	require.Len(t, tree.Entries, 2)
	require.Equal(t, "public/a.txt", tree.Entries[0].Path)
	require.Equal(t, "a.txt", tree.Entries[0].Name)
	require.Equal(t, "top.txt", tree.Entries[1].Path)

	appCtx = browserAppCtx(claims, projectParams, map[string]string{"rev": "main", "recursive": "1", "path": "public"})
	env.handler.GetTree(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	tree, ok = appCtx.response.(model.TreeResponse)
	require.True(t, ok)
	require.Len(t, tree.Entries, 1)
	require.Equal(t, "public/a.txt", tree.Entries[0].Path)
}

func TestHandler_BrowserCommitsPathFilter(t *testing.T) {
	env := newHandlerTestEnv(t)
	first := env.seedFiles(t, map[string]string{
		"public/a.txt": "hello",
		"top.txt":      "one",
	})
	second := env.seedPushTo(t, "main", first.CommitID.Base36(), map[string]string{
		"top.txt": "two",
	})

	claims := &domain.Claims{UserID: env.userID, IsAdmin: true}
	projectParams := map[string]string{"org": "default", "project": "default"}

	appCtx := browserAppCtx(claims, projectParams, map[string]string{"branch": "main", "path": "top.txt"})
	env.handler.ListCommits(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	commits, ok := appCtx.response.([]model.CommitResponse)
	require.True(t, ok)
	require.Len(t, commits, 2)
	require.Equal(t, second.CommitID.Base36(), commits[0].ID)
	require.Equal(t, first.CommitID.Base36(), commits[1].ID)

	appCtx = browserAppCtx(claims, projectParams, map[string]string{"branch": "main", "path": "public/a.txt"})
	env.handler.ListCommits(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	commits, ok = appCtx.response.([]model.CommitResponse)
	require.True(t, ok)
	require.Len(t, commits, 1)
	require.Equal(t, first.CommitID.Base36(), commits[0].ID)
}
