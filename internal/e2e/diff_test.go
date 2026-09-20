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
	"github.com/nipalab/nipa/internal/diff"
)

func TestEndToEnd_DiffWorkingTree(t *testing.T) {
	ctx := context.Background()
	host := startTestServer(t, openTestDB(t))

	store := newMemoryStore()
	transport := clientgrpc.NewTransport()
	session := clientusecase.NewSession(store, transport, failPrompt{})
	grpcClient := clientgrpc.NewClient(transport, session)
	auth := clientusecase.NewAuth(grpcClient, store, failPrompt{})
	require.NoError(t, grpcClient.Connect(ctx, host))
	loginResult, err := grpcClient.LoginWithUsernamePassword(ctx, host, e2eSuperAdminEmail, e2eSuperAdminPass)
	require.NoError(t, err)
	require.NoError(t, store.SaveToken(loginResult))

	repo := clientusecase.NewRepo(auth, grpcClient, localrepo.NewLocalRepo())
	url := "http://" + host + "/" + e2eOrgSlug + "/" + e2eProjectSlug
	target := filepath.Join(t.TempDir(), "work")
	require.NoError(t, repo.Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", "", target))

	const original = "line one\nline two\n"
	writeFile(t, target, "a.txt", original)
	stagePath(t, target, "a.txt")
	pusher := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())
	require.NoError(t, pusher.Run(ctx, target, "add a.txt"))

	snap := snapshotOf(t, target)
	require.Len(t, snap.Files, 1)
	require.NotEmpty(t, snap.Files[0].Chunks, "clone/push must persist per-file chunk hashes")

	writeFile(t, target, "a.txt", "line one\nLINE TWO\n")
	writeFile(t, target, "new.txt", "brand new\n")
	stagePath(t, target, "new.txt")

	differ := clientusecase.NewDiff(localrepo.NewLocalRepo())
	files, err := differ.Run(ctx, target, clientusecase.DiffOptions{})
	require.NoError(t, err)
	require.Len(t, files, 2)
	require.Equal(t, diff.Modified, files[0].Change.Status)
	require.Equal(t, "a.txt", files[0].Change.Path)
	require.Equal(t, []byte(original), files[0].Old)
	require.Equal(t, []byte("line one\nLINE TWO\n"), files[0].New)
	require.Equal(t, diff.Added, files[1].Change.Status)
	require.Equal(t, []byte("brand new\n"), files[1].New)

	patch := diff.Patch(files, diff.Options{Context: 3})
	require.Contains(t, patch, "diff --nipa a/a.txt b/a.txt")
	require.Contains(t, patch, "-line two")
	require.Contains(t, patch, "+LINE TWO")
	require.Contains(t, patch, "new file mode 100644")

	require.NoError(t, os.Remove(filepath.Join(target, "a.txt")))
	files, err = differ.Run(ctx, target, clientusecase.DiffOptions{})
	require.NoError(t, err)
	require.Len(t, files, 2)
	require.Equal(t, diff.Deleted, files[0].Change.Status)
	require.Equal(t, []byte(original), files[0].Old)
	require.False(t, files[0].OldUnavailable)
}
