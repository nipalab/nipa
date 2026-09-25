package e2e

import (
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	clientusecase "github.com/nipalab/nipa/internal/client/usecase"
)

func TestEndToEnd_MergeRequestLifecycle(t *testing.T) {
	ctx := context.Background()
	dbConn := openTestDB(t)
	host := startTestServer(t, dbConn)
	grpcClient, auth := newLoggedInClient(t, host)

	pusher := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())
	requests := clientusecase.NewMergeRequest(auth, grpcClient, localrepo.NewLocalRepo())

	mainDir := cloneWorktree(t, grpcClient, auth, host, "main")
	writeFile(t, mainDir, "a.txt", "base\n")
	stagePath(t, mainDir, "a.txt")
	require.NoError(t, pusher.Run(ctx, mainDir, "seed main"))

	_, err := grpcClient.CreateBranch(ctx, e2eOrgSlug, e2eProjectSlug, "feature", "main", "", "")
	require.NoError(t, err)
	featDir := cloneWorktree(t, grpcClient, auth, host, "feature")
	writeFile(t, featDir, "b.txt", "feature\n")
	stagePath(t, featDir, "b.txt")
	require.NoError(t, pusher.Run(ctx, featDir, "work on feature"))

	created, err := requests.Create(ctx, featDir, clientusecase.CreateMergeRequestOptions{
		Title:       " Add b ",
		Description: " body ",
	})
	require.NoError(t, err)
	require.Equal(t, domain.MergeRequestOpen, created.Status)
	require.Equal(t, int64(1), created.Number)
	require.Equal(t, "Add b", created.Title)
	require.Equal(t, "feature", created.SourceBranch)
	require.Equal(t, "main", created.TargetBranch)

	list, err := requests.List(ctx, mainDir, domain.MergeRequestOpen, 0)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, created.ID, list[0].ID)

	updated, err := requests.Update(ctx, mainDir, strconv.FormatInt(created.Number, 10), "Add b v2", "")
	require.NoError(t, err)
	require.Equal(t, "Add b v2", updated.Title)
	require.Equal(t, "body", updated.Description)

	err = grpcClient.DeleteBranch(ctx, e2eOrgSlug, e2eProjectSlug, "feature")
	require.Error(t, err, "a branch with an open merge request must not be deletable")
	var guardErr *domain.Error
	require.ErrorAs(t, err, &guardErr)
	require.Equal(t, 409, guardErr.Code)
	require.Contains(t, guardErr.Message, "open merge requests")

	merged, info, err := requests.Merge(ctx, mainDir, strconv.FormatInt(created.Number, 10))
	require.NoError(t, err)
	require.Equal(t, domain.MergeRequestMerged, merged.Status)
	require.NotEmpty(t, merged.MergeCommitID)
	require.Equal(t, "mergeable", info.Status)

	require.NoError(t, grpcClient.DeleteBranch(ctx, e2eOrgSlug, e2eProjectSlug, "feature"), "a merged merge request must not block branch deletion")

	mainManifest, err := grpcClient.GetTreeNodeManifest(ctx, e2eOrgSlug, e2eProjectSlug, "main", nil)
	require.NoError(t, err)
	require.Len(t, mainManifest.FileChildren, 2)

	_, _, err = requests.Merge(ctx, mainDir, strconv.FormatInt(created.Number, 10))
	require.Error(t, err)
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 409, domErr.Code)

	_, err = grpcClient.CreateBranch(ctx, e2eOrgSlug, e2eProjectSlug, "feature-2", "main", "", "")
	require.NoError(t, err)
	feat2Dir := cloneWorktree(t, grpcClient, auth, host, "feature-2")
	writeFile(t, feat2Dir, "c.txt", "more\n")
	stagePath(t, feat2Dir, "c.txt")
	require.NoError(t, pusher.Run(ctx, feat2Dir, "work on feature 2"))

	second, err := requests.Create(ctx, feat2Dir, clientusecase.CreateMergeRequestOptions{Title: "Add c"})
	require.NoError(t, err)
	require.Equal(t, int64(2), second.Number)
	closed, err := requests.Close(ctx, mainDir, strconv.FormatInt(second.Number, 10))
	require.NoError(t, err)
	require.Equal(t, domain.MergeRequestClosed, closed.Status)
	require.NoError(t, grpcClient.DeleteBranch(ctx, e2eOrgSlug, e2eProjectSlug, "feature-2"), "a closed merge request must not block branch deletion")

	_, _, err = requests.Merge(ctx, mainDir, strconv.FormatInt(second.Number, 10))
	require.Error(t, err)
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 409, domErr.Code)
	require.Contains(t, domErr.Message, "closed")
}
