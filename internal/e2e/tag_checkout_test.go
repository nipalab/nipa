package e2e

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	clientgrpc "github.com/nipalab/nipa/internal/client/grpc"
	"github.com/nipalab/nipa/internal/client/localrepo"
	clientusecase "github.com/nipalab/nipa/internal/client/usecase"
)

func repoConfigOf(t *testing.T, target string) domain.Config {
	t.Helper()
	lr := localrepo.NewLocalRepo()
	require.NoError(t, lr.Init(target))
	defer func() { _ = lr.Close() }()
	cfg, err := lr.LoadConfig()
	require.NoError(t, err)
	return *cfg
}

func TestEndToEnd_TagCheckoutDetachedHead(t *testing.T) {
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

	url := "http://" + host + "/" + e2eOrgSlug + "/" + e2eProjectSlug
	repo := clientusecase.NewRepo(auth, grpcClient, localrepo.NewLocalRepo())
	target := filepath.Join(t.TempDir(), "work")
	require.NoError(t, repo.Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", nil, target))

	pusher := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())
	updater := clientusecase.NewUpdate(auth, grpcClient, localrepo.NewLocalRepo())
	tagger := clientusecase.NewTag(auth, grpcClient, localrepo.NewLocalRepo())

	const release = "release one\n"
	writeFile(t, target, "a.txt", release)
	stagePath(t, target, "a.txt")
	require.NoError(t, pusher.Run(ctx, target, "release one"))

	tag, err := tagger.Create(ctx, target, "v1.0.0", "first release", clientusecase.TagTarget{})
	require.NoError(t, err)
	require.Equal(t, "v1.0.0", tag.Name)

	const later = "later work\n"
	writeFile(t, target, "a.txt", later)
	stagePath(t, target, "a.txt")
	require.NoError(t, pusher.Run(ctx, target, "later work"))

	require.NoError(t, updater.SwitchTag(ctx, target, "v1.0.0"))
	assertFileContent(t, target, "a.txt", release)
	cfg := repoConfigOf(t, target)
	require.Equal(t, "main", cfg.Branch, "the branch identity must stay configured while detached")
	require.NotNil(t, cfg.Head)
	require.Equal(t, domain.HeadKindTag, cfg.Head.Kind)
	require.Equal(t, "v1.0.0", cfg.Head.Name)

	err = pusher.Run(ctx, target, "should not land")
	require.Error(t, err)
	require.Contains(t, err.Error(), `HEAD is detached at tag "v1.0.0"`)

	require.NoError(t, updater.Run(ctx, target), "update while detached must resync the tag")
	assertFileContent(t, target, "a.txt", release)
	cfg = repoConfigOf(t, target)
	require.NotNil(t, cfg.Head, "update must keep HEAD detached")

	require.NoError(t, updater.Switch(ctx, target, "main"))
	assertFileContent(t, target, "a.txt", later)
	cfg = repoConfigOf(t, target)
	require.Nil(t, cfg.Head, "switching to a branch must re-attach HEAD")
}
