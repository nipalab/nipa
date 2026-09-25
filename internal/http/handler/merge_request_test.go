package handler

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	httpApp "github.com/nipalab/nipa/internal/http"
	"github.com/nipalab/nipa/internal/http/model"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

func (env *handlerTestEnv) createBranch(t *testing.T, name, from string) {
	t.Helper()

	ctx := domain.ContextWithClaim(context.Background(), domain.Claims{UserID: env.userID, IsAdmin: true})
	_, err := env.handler.useCase.Branch().CreateBranch(ctx, snow.ID(1), name, usecase.BranchForkPoint{BranchName: from})
	require.NoError(t, err)
}

func TestHandler_MergeRequestFlow(t *testing.T) {
	env := newHandlerTestEnv(t)
	mainPush := env.seedFiles(t, map[string]string{"a.txt": "hello"})
	env.createBranch(t, "feature", "main")
	env.seedPushTo(t, "feature", mainPush.CommitID.Base36(), map[string]string{"b.txt": "feature"})

	claims := &domain.Claims{UserID: env.userID, IsAdmin: true}
	projectParams := map[string]string{"org": "default", "project": "default"}

	appCtx := &fakeAppContext{
		claims:         claims,
		pathParameters: projectParams,
		body:           []byte(`{"title":"Add b","description":"body","source_branch":"feature","target_branch":"main"}`),
	}
	env.handler.CreateMergeRequest(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	created := appCtx.response.(model.MergeRequestResponse)
	require.Equal(t, "open", created.Status)
	require.Equal(t, "feature", created.SourceBranch)

	mrParams := map[string]string{"org": "default", "project": "default", "id": created.ID}
	appCtx = &fakeAppContext{claims: claims, pathParameters: mrParams, body: []byte(`{"title":"Add b v2","description":"updated"}`)}
	env.handler.UpdateMergeRequest(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	updated := appCtx.response.(model.MergeRequestResponse)
	require.Equal(t, "Add b v2", updated.Title)
	require.Equal(t, "updated", updated.Description)

	appCtx = &fakeAppContext{claims: claims, pathParameters: mrParams, body: []byte(`{}`)}
	env.handler.UpdateMergeRequest(appCtx)
	require.Equal(t, http.StatusBadRequest, appCtx.statusCode)

	appCtx = &fakeAppContext{claims: claims, pathParameters: mrParams}
	env.handler.GetMergeRequest(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	detail := appCtx.response.(model.MergeRequestResponse)
	require.NotNil(t, detail.Mergeability)
	require.Equal(t, domain.MergeabilityMergeable, detail.Mergeability.Status)

	appCtx = &fakeAppContext{claims: claims, pathParameters: mrParams}
	env.handler.MergeRequestDiff(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	files := appCtx.response.(model.MergeRequestDiffResponse).Files
	require.Len(t, files, 1)
	require.Equal(t, "b.txt", files[0].Path)

	appCtx = &fakeAppContext{claims: claims, pathParameters: mrParams}
	env.handler.MergeMergeRequest(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	merged := appCtx.response.(model.MergeRequestResponse)
	require.Equal(t, domain.MergeRequestMerged, merged.Status)
	require.NotEmpty(t, merged.MergeCommitID)

	appCtx = &fakeAppContext{claims: claims, pathParameters: mrParams}
	env.handler.CheckMergeRequest(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	require.Equal(t, domain.MergeRequestMerged, appCtx.response.(*model.MergeabilityResponse).Status)

	appCtx = &fakeAppContext{claims: claims, pathParameters: mrParams}
	env.handler.MergeMergeRequest(appCtx)
	require.Equal(t, http.StatusConflict, appCtx.statusCode)
}

func TestHandler_MergeRequestBehindTarget(t *testing.T) {
	env := newHandlerTestEnv(t)
	mainPush := env.seedFiles(t, map[string]string{"a.txt": "hello"})
	env.createBranch(t, "feature", "main")
	env.seedPushTo(t, "feature", mainPush.CommitID.Base36(), map[string]string{"b.txt": "feature"})
	// Advance main so the feature branch is behind.
	env.seedPushTo(t, "main", mainPush.CommitID.Base36(), map[string]string{"c.txt": "main"})

	claims := &domain.Claims{UserID: env.userID, IsAdmin: true}
	projectParams := map[string]string{"org": "default", "project": "default"}
	appCtx := &fakeAppContext{
		claims:         claims,
		pathParameters: projectParams,
		body:           []byte(`{"title":"Behind","source_branch":"feature","target_branch":"main"}`),
	}
	env.handler.CreateMergeRequest(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	created := appCtx.response.(model.MergeRequestResponse)

	mrParams := map[string]string{"org": "default", "project": "default", "id": created.ID}
	appCtx = &fakeAppContext{claims: claims, pathParameters: mrParams}
	env.handler.CheckMergeRequest(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	require.Equal(t, domain.MergeabilityBehind, appCtx.response.(*model.MergeabilityResponse).Status)

	appCtx = &fakeAppContext{claims: claims, pathParameters: mrParams}
	env.handler.MergeMergeRequest(appCtx)
	require.Equal(t, http.StatusConflict, appCtx.statusCode)
}

func TestHandler_MergeRequestCloseReopen(t *testing.T) {
	env := newHandlerTestEnv(t)
	mainPush := env.seedFiles(t, map[string]string{"a.txt": "hello"})
	env.createBranch(t, "feature", "main")
	env.seedPushTo(t, "feature", mainPush.CommitID.Base36(), map[string]string{"b.txt": "feature"})

	claims := &domain.Claims{UserID: env.userID, IsAdmin: true}
	appCtx := &fakeAppContext{
		claims:         claims,
		pathParameters: map[string]string{"org": "default", "project": "default"},
		body:           []byte(`{"title":"Close me","source_branch":"feature","target_branch":"main"}`),
	}
	env.handler.CreateMergeRequest(appCtx)
	created := appCtx.response.(model.MergeRequestResponse)

	mrParams := map[string]string{"org": "default", "project": "default", "id": created.ID}
	appCtx = &fakeAppContext{claims: claims, pathParameters: mrParams}
	env.handler.CloseMergeRequest(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	require.Equal(t, domain.MergeRequestClosed, appCtx.response.(model.MergeRequestResponse).Status)

	appCtx = &fakeAppContext{claims: claims, pathParameters: mrParams}
	env.handler.ReopenMergeRequest(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	require.Equal(t, domain.MergeRequestOpen, appCtx.response.(model.MergeRequestResponse).Status)

	appCtx = &fakeAppContext{claims: claims, pathParameters: mrParams}
	env.handler.ReopenMergeRequest(appCtx)
	require.Equal(t, http.StatusConflict, appCtx.statusCode)
}

func TestHandler_MergeRequestValidation(t *testing.T) {
	env := newHandlerTestEnv(t)
	env.seedFiles(t, map[string]string{"a.txt": "hello"})

	claims := &domain.Claims{UserID: env.userID, IsAdmin: true}
	projectParams := map[string]string{"org": "default", "project": "default"}

	appCtx := &fakeAppContext{claims: claims, pathParameters: projectParams, body: []byte(`{"title":"","source_branch":"main","target_branch":"main"}`)}
	env.handler.CreateMergeRequest(appCtx)
	require.Equal(t, http.StatusBadRequest, appCtx.statusCode)

	appCtx = &fakeAppContext{claims: claims, pathParameters: projectParams, body: []byte(`{"title":"t","source_branch":"main","target_branch":"main"}`)}
	env.handler.CreateMergeRequest(appCtx)
	require.Equal(t, http.StatusBadRequest, appCtx.statusCode)

	appCtx = &fakeAppContext{claims: claims, pathParameters: projectParams, body: []byte(`{"title":"t","source_branch":"ghost","target_branch":"main"}`)}
	env.handler.CreateMergeRequest(appCtx)
	require.Equal(t, http.StatusNotFound, appCtx.statusCode)

	missing := map[string]string{"org": "default", "project": "default", "id": snow.ID(999999).Base36()}
	appCtx = &fakeAppContext{claims: claims, pathParameters: missing}
	env.handler.GetMergeRequest(appCtx)
	require.Equal(t, http.StatusNotFound, appCtx.statusCode)

	appCtx = &fakeAppContext{claims: claims, pathParameters: map[string]string{"org": "default", "project": "default", "id": "!!!"}}
	env.handler.GetMergeRequest(appCtx)
	require.Equal(t, http.StatusBadRequest, appCtx.statusCode)

	appCtx = &fakeAppContext{claims: claims, pathParameters: projectParams}
	env.handler.ListMergeRequests(appCtx)
	require.Equal(t, http.StatusOK, appCtx.statusCode)
	require.Empty(t, appCtx.response.([]model.MergeRequestResponse))

	appCtx = &fakeAppContext{claims: claims, pathParameters: projectParams, queryParameters: map[string]string{"status": "bogus"}}
	env.handler.ListMergeRequests(appCtx)
	require.Equal(t, http.StatusBadRequest, appCtx.statusCode)
}

func TestHandler_MergeRequestContextError(t *testing.T) {
	env := newHandlerTestEnv(t)
	params := map[string]string{"org": "default", "project": "default", "id": "1"}

	tests := []struct {
		name string
		body string
		run  func(appCtx httpApp.AppContext)
	}{
		{name: "list", run: env.handler.ListMergeRequests},
		{name: "create", body: `{"title":"t"}`, run: env.handler.CreateMergeRequest},
		{name: "get", run: env.handler.GetMergeRequest},
		{name: "update", body: `{"title":"t"}`, run: env.handler.UpdateMergeRequest},
		{name: "check", run: env.handler.CheckMergeRequest},
		{name: "merge", run: env.handler.MergeMergeRequest},
		{name: "close", run: env.handler.CloseMergeRequest},
		{name: "reopen", run: env.handler.ReopenMergeRequest},
		{name: "diff", run: env.handler.MergeRequestDiff},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			appCtx := canceledAppCtx(env.userID, params, tt.body)
			tt.run(appCtx)
			require.Equal(t, http.StatusInternalServerError, appCtx.statusCode)
		})
	}
}
