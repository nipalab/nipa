package e2e

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	clientgrpc "github.com/nipalab/nipa/internal/client/grpc"
	"github.com/nipalab/nipa/internal/client/localrepo"
	clientusecase "github.com/nipalab/nipa/internal/client/usecase"
)

func TestEndToEnd_DiffRevisions(t *testing.T) {
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
	target := t.TempDir() + "/work"
	require.NoError(t, repo.Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", "", target))

	writeFile(t, target, "a.txt", "v1\n")
	stagePath(t, target, "a.txt")
	pusher := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())
	require.NoError(t, pusher.Run(ctx, target, "first"))

	writeFile(t, target, "a.txt", "v2\n")
	writeFile(t, target, "b.txt", "new\n")
	stagePath(t, target, "a.txt")
	stagePath(t, target, "b.txt")
	require.NoError(t, pusher.Run(ctx, target, "second"))

	entries, err := repo.Log(ctx, host, e2eOrgSlug, e2eProjectSlug, "main")
	require.NoError(t, err)
	require.Len(t, entries, 2)
	hash1 := entries[1].Hash.String()
	hash2 := entries[0].Hash.String()
	id1 := entries[1].ID.Base36()

	differ := clientusecase.NewDiff(auth, grpcClient, localrepo.NewLocalRepo())

	// working copy is clean at v2
	clean, err := differ.Run(ctx, target, nil)
	require.NoError(t, err)
	require.Empty(t, clean.Files)

	// commit vs commit over the real GetCommitTree RPC
	between, err := differ.Run(ctx, target, []string{hash1, hash2})
	require.NoError(t, err)
	require.Len(t, between.Files, 2)
	require.Equal(t, "a.txt", between.Files[0].Change.Path)
	require.Equal(t, []byte("v1\n"), between.Files[0].Old)
	require.Equal(t, []byte("v2\n"), between.Files[0].New)
	require.Equal(t, "b.txt", between.Files[1].Change.Path)
	require.Equal(t, []byte("new\n"), between.Files[1].New)

	// commit ID (base36) resolves through the branch fallback
	byID, err := differ.Run(ctx, target, []string{id1, hash2})
	require.NoError(t, err)
	require.Len(t, byID.Files, 2)

	// branch tip equals the second commit: empty diff
	same, err := differ.Run(ctx, target, []string{"main", hash2})
	require.NoError(t, err)
	require.Empty(t, same.Files)

	// revision vs working copy picks up uncommitted edits
	writeFile(t, target, "a.txt", "v3-local\n")
	vsWorking, err := differ.Run(ctx, target, []string{hash1})
	require.NoError(t, err)
	require.Len(t, vsWorking.Files, 1)
	require.Equal(t, "a.txt", vsWorking.Files[0].Change.Path)
	require.Equal(t, []byte("v1\n"), vsWorking.Files[0].Old)
	require.Equal(t, []byte("v3-local\n"), vsWorking.Files[0].New)

	// unknown revision
	_, err = differ.Run(ctx, target, []string{"nope!!!"})
	require.Error(t, err)
}
