package handler

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/domain"
	httpApp "github.com/nipalab/nipa/internal/http"
	"github.com/nipalab/nipa/internal/http/model"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/treehash"
)

func (env *handlerTestEnv) seedFiles(t *testing.T, files map[string]string) *domain.PushResult {
	t.Helper()
	return env.seedPushTo(t, "main", "", files)
}

func (env *handlerTestEnv) seedPushTo(t *testing.T, branch, baseCommitID string, files map[string]string) *domain.PushResult {
	t.Helper()

	ctx := domain.ContextWithClaim(context.Background(), domain.Claims{UserID: env.userID, IsAdmin: true})
	pushFiles := make([]*domain.PushFile, 0, len(files))
	for path, content := range files {
		chunks, err := chunker.ChunkAll([]byte(content))
		require.NoError(t, err)
		hashes := make([]domain.Hash, 0, len(chunks))
		for _, c := range chunks {
			_, err := env.chunkUc.Upload(ctx, c.Hash, c.Data)
			require.NoError(t, err)
			hashes = append(hashes, c.Hash)
		}
		pushFiles = append(pushFiles, &domain.PushFile{
			Path:        path,
			Mode:        0o644,
			SizeBytes:   int64(len(content)),
			IsBinary:    bytes.ContainsRune([]byte(content), 0),
			FileHash:    treehash.FileHash(hashes),
			ChunkHashes: hashes,
		})
	}
	result, err := env.pusher.Push(ctx, snow.ID(1), branch, "", "seed", pushFiles, nil, "", baseCommitID)
	require.NoError(t, err)
	return result
}

func browserAppCtx(claims *domain.Claims, pathParams, queryParams map[string]string) *fakeAppContext {
	return &fakeAppContext{
		claims:          claims,
		pathParameters:  pathParams,
		queryParameters: queryParams,
	}
}

func TestHandler_BrowserFlow(t *testing.T) {
	env := newHandlerTestEnv(t)
	result := env.seedFiles(t, map[string]string{
		"public/a.txt":   "hello",
		"secret/key.bin": "top secret",
	})

	claims := &domain.Claims{UserID: env.userID, IsAdmin: true}
	projectParams := map[string]string{"org": "default", "project": "default"}

	appCtx := browserAppCtx(claims, projectParams, map[string]string{"rev": "main"})
	env.handler.GetTree(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	tree, ok := appCtx.response.(model.TreeResponse)
	require.True(t, ok)
	require.Len(t, tree.Entries, 2)
	require.Equal(t, "tree", tree.Entries[0].Type)
	require.Equal(t, "public", tree.Entries[0].Path)
	require.Equal(t, "tree", tree.Entries[1].Type)
	require.Equal(t, "secret", tree.Entries[1].Path)

	appCtx = browserAppCtx(claims, projectParams, map[string]string{"rev": "main", "path": "public"})
	env.handler.GetTree(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	tree = appCtx.response.(model.TreeResponse)
	require.Len(t, tree.Entries, 1)
	require.Equal(t, "file", tree.Entries[0].Type)
	require.Equal(t, "public/a.txt", tree.Entries[0].Path)
	require.Equal(t, int64(5), tree.Entries[0].SizeBytes)

	appCtx = browserAppCtx(claims, projectParams, map[string]string{"rev": "main", "path": "public/a.txt"})
	env.handler.GetBlob(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	require.Equal(t, "hello", string(appCtx.raw))
	require.Equal(t, "text/plain; charset=utf-8", appCtx.contentType)

	appCtx = browserAppCtx(claims, projectParams, map[string]string{"branch": "main", "limit": "10"})
	env.handler.ListCommits(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	commits, ok := appCtx.response.([]model.CommitResponse)
	require.True(t, ok)
	require.Len(t, commits, 1)
	require.Equal(t, result.CommitID.Base36(), commits[0].ID)
	require.Equal(t, "seed", commits[0].Message)

	appCtx = browserAppCtx(claims, map[string]string{
		"org": "default", "project": "default", "commit": result.CommitID.Base36(),
	}, nil)
	env.handler.GetCommitDiff(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	diffResp, ok := appCtx.response.(model.CommitDiffResponse)
	require.True(t, ok)
	require.Len(t, diffResp.Files, 2)
	require.Equal(t, "A", diffResp.Files[0].Status)
	require.NotEmpty(t, diffResp.Files[0].Patch)
	require.Equal(t, 1, diffResp.Files[0].Additions)

	appCtx = browserAppCtx(claims, projectParams, map[string]string{"limit": "10"})
	env.handler.ListProjectBranches(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	branches, ok := appCtx.response.([]model.BranchResponse)
	require.True(t, ok)
	require.Len(t, branches, 1)
	require.Equal(t, "main", branches[0].Name)
	require.True(t, branches[0].IsDefault)
	require.Equal(t, result.CommitID.Base36(), branches[0].CommitID)
}

func TestHandler_BrowserHiddenPaths(t *testing.T) {
	env := newHandlerTestEnv(t)
	env.seedFiles(t, map[string]string{
		"public/a.txt":   "hello",
		"secret/key.bin": "top secret",
	})

	reader := env.createUser(t, "reader", "reader@example.com")
	projectID := snow.ID(1)
	_, err := env.pbacRepo.CreateRule(context.Background(), domain.PBACRule{
		UserID: &reader.ID, OrgID: 1, ProjectID: &projectID,
		PathPrefix: "public", Permission: domain.PermissionRead,
	})
	require.NoError(t, err)

	claims := &domain.Claims{UserID: reader.ID}
	projectParams := map[string]string{"org": "default", "project": "default"}

	appCtx := browserAppCtx(claims, projectParams, map[string]string{"rev": "main"})
	env.handler.GetTree(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	tree := appCtx.response.(model.TreeResponse)
	require.Len(t, tree.Entries, 1)
	require.Equal(t, "public", tree.Entries[0].Path)

	appCtx = browserAppCtx(claims, projectParams, map[string]string{"rev": "main", "path": "secret/key.bin"})
	env.handler.GetBlob(appCtx)
	require.Equal(t, http.StatusNotFound, appCtx.statusCode)

	appCtx = browserAppCtx(claims, projectParams, map[string]string{"rev": "main", "path": "public/a.txt"})
	env.handler.GetBlob(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	require.Equal(t, "hello", string(appCtx.raw))
}

func TestHandler_BrowserErrors(t *testing.T) {
	env := newHandlerTestEnv(t)
	regular := &domain.Claims{UserID: env.userID}
	projectParams := map[string]string{"org": "default", "project": "default"}

	denied := []struct {
		name   string
		params map[string]string
		query  map[string]string
		run    func(appCtx httpApp.AppContext)
	}{
		{name: "tree", params: projectParams, query: map[string]string{"rev": "main"}, run: env.handler.GetTree},
		{name: "blob", params: projectParams, query: map[string]string{"rev": "main", "path": "a.txt"}, run: env.handler.GetBlob},
		{name: "commits", params: projectParams, query: map[string]string{"branch": "main"}, run: env.handler.ListCommits},
		{name: "diff", params: map[string]string{"org": "default", "project": "default", "commit": "1"}, run: env.handler.GetCommitDiff},
		{name: "branches", params: projectParams, run: env.handler.ListProjectBranches},
	}
	for _, tt := range denied {
		t.Run("denied "+tt.name, func(t *testing.T) {
			appCtx := browserAppCtx(regular, tt.params, tt.query)
			tt.run(appCtx)
			require.Equal(t, http.StatusForbidden, appCtx.statusCode)
		})
	}

	admin := &domain.Claims{UserID: env.userID, IsAdmin: true}
	appCtx := browserAppCtx(admin, map[string]string{"org": "missing", "project": "default"}, nil)
	env.handler.ListProjectBranches(appCtx)
	require.Equal(t, http.StatusNotFound, appCtx.statusCode)

	appCtx = browserAppCtx(admin, projectParams, map[string]string{"last_id": "!!!"})
	env.handler.ListProjectBranches(appCtx)
	require.Equal(t, http.StatusBadRequest, appCtx.statusCode)

	appCtx = browserAppCtx(admin, projectParams, map[string]string{"last_updated_at": "yesterday"})
	env.handler.ListProjectBranches(appCtx)
	require.Equal(t, http.StatusBadRequest, appCtx.statusCode)

	appCtx = browserAppCtx(admin, projectParams, map[string]string{"start": "!!!"})
	env.handler.ListCommits(appCtx)
	require.Equal(t, http.StatusBadRequest, appCtx.statusCode)

	appCtx = browserAppCtx(admin, map[string]string{"org": "default", "project": "default", "commit": "!!!"}, nil)
	env.handler.GetCommitDiff(appCtx)
	require.Equal(t, http.StatusBadRequest, appCtx.statusCode)

	appCtx = browserAppCtx(admin, map[string]string{"org": "default", "project": "default", "commit": "1"}, map[string]string{"base": "!!!"})
	env.handler.GetCommitDiff(appCtx)
	require.Equal(t, http.StatusBadRequest, appCtx.statusCode)
}

func TestHandler_BlobBinaryContent(t *testing.T) {
	env := newHandlerTestEnv(t)
	env.seedFiles(t, map[string]string{"data.bin": "\x00\x01\x02"})

	claims := &domain.Claims{UserID: env.userID, IsAdmin: true}
	params := map[string]string{"org": "default", "project": "default"}

	appCtx := browserAppCtx(claims, params, map[string]string{"rev": "main", "path": "data.bin"})
	env.handler.GetBlob(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	require.Equal(t, "application/octet-stream", appCtx.contentType)
	require.Equal(t, "\x00\x01\x02", string(appCtx.raw))
}
