package e2e

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	clientgrpc "github.com/nipalab/nipa/internal/client/grpc"
	"github.com/nipalab/nipa/internal/client/localrepo"
	clientusecase "github.com/nipalab/nipa/internal/client/usecase"
)

func TestEndToEnd_AddDeletedFilesPush(t *testing.T) {
	ctx := context.Background()
	host := startTestServer(t, openTestDB(t))

	store := newMemoryStore()
	transport := clientgrpc.NewTransport()
	grpcClient := clientgrpc.NewClient(transport, clientusecase.NewSession(store, transport, failPrompt{}))
	require.NoError(t, grpcClient.Connect(ctx, host))

	loginResult, err := grpcClient.LoginWithUsernamePassword(ctx, host, e2eSuperAdminEmail, e2eSuperAdminPass)
	require.NoError(t, err)
	require.NoError(t, store.SaveToken(loginResult))

	auth := clientusecase.NewAuth(grpcClient, store, failPrompt{})
	repo := clientusecase.NewRepo(auth, grpcClient, localrepo.NewLocalRepo())
	pusher := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())
	url := "http://" + host + "/" + e2eOrgSlug + "/" + e2eProjectSlug

	target := filepath.Join(t.TempDir(), "work")
	require.NoError(t, repo.Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", nil, target))

	lr := localrepo.NewLocalRepo()
	defer lr.Close()
	wc, err := clientusecase.NewWorkingCopy(lr, target)
	require.NoError(t, err)

	writeFile(t, target, "keep.txt", "keep\n")
	writeFile(t, target, "gone.txt", "gone\n")
	writeFile(t, target, "docs/a.txt", "a\n")
	writeFile(t, target, "docs/b.txt", "b\n")
	require.NoError(t, wc.Add(ctx, []string{"keep.txt", "gone.txt", "docs"}))
	require.NoError(t, pusher.Run(ctx, target, "seed files"))

	checkout := filepath.Join(t.TempDir(), "checkout")
	require.NoError(t, repo.Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", nil, checkout))
	require.NoError(t, os.Remove(filepath.Join(target, "gone.txt")))
	require.NoError(t, os.RemoveAll(filepath.Join(target, "docs")))

	require.NoError(t, wc.Add(ctx, []string{"gone.txt", "docs"}))
	st, err := wc.Status(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"docs/a.txt", "docs/b.txt", "gone.txt"}, st.Deleted)
	require.Empty(t, st.Staged)
	require.Empty(t, st.Missing)

	require.NoError(t, pusher.Run(ctx, target, "delete files"))
	assertStagedEmpty(t, target)

	snap := snapshotOf(t, target)
	require.Len(t, snap.Files, 1)
	require.Equal(t, "keep.txt", snap.Files[0].Path)

	updater := clientusecase.NewUpdate(auth, grpcClient, localrepo.NewLocalRepo())
	require.NoError(t, updater.Run(ctx, checkout), "update must apply the pushed deletions")
	assertFileContent(t, checkout, "keep.txt", "keep\n")
	require.NoFileExists(t, filepath.Join(checkout, "gone.txt"))
	require.NoFileExists(t, filepath.Join(checkout, "docs", "a.txt"))

	checkoutAfter := filepath.Join(t.TempDir(), "checkout-after")
	require.NoError(t, repo.Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", nil, checkoutAfter))
	assertFileContent(t, checkoutAfter, "keep.txt", "keep\n")
	require.NoFileExists(t, filepath.Join(checkoutAfter, "gone.txt"))
	require.NoFileExists(t, filepath.Join(checkoutAfter, "docs", "a.txt"))
}
