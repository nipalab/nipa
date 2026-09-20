package cli

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/usecase"
)

func setupSparseRepo(t *testing.T, sparse []string) string {
	t.Helper()

	root := t.TempDir()
	lr := localrepo.NewLocalRepo()
	require.NoError(t, lr.Init(root))
	defer lr.Close()
	require.NoError(t, lr.SaveConfig(domain.Config{
		Url:    "http://example.com/org/project",
		Branch: "main",
		Sparse: sparse,
	}))
	return root
}

func newSparseCli(t *testing.T, client *fakeUpdateClient) *Cli {
	t.Helper()

	auth := usecase.NewAuth(fakeExecutor{}, &fakeStorage{token: signTestJWT(t)}, &fakeInput{})
	updater := usecase.NewUpdate(auth, client, localrepo.NewLocalRepo())
	return NewCli(&fakeUsecaseContainer{update: updater}, &fakeConnector{})
}

func loadSparseConfig(t *testing.T, root string) *domain.Config {
	t.Helper()

	lr := localrepo.NewLocalRepoWithTarget(root)
	cfg, err := lr.LoadConfig()
	require.NoError(t, err)
	return cfg
}

func TestSetupSparseListCmd_Disabled(t *testing.T) {
	root := setupSparseRepo(t, nil)
	cli := newSparseCli(t, &fakeUpdateClient{})

	out, err := runCmdInDir(t, root, cli.setupSparseCheckoutCmd(), "list")
	require.NoError(t, err)
	require.Contains(t, out, "sparse checkout disabled (full checkout)")
}

func TestSetupSparseListCmd_Paths(t *testing.T) {
	root := setupSparseRepo(t, []string{"assets", "src"})
	cli := newSparseCli(t, &fakeUpdateClient{})

	out, err := runCmdInDir(t, root, cli.setupSparseCheckoutCmd(), "list")
	require.NoError(t, err)
	require.Equal(t, "assets\nsrc\n", out)
}

func TestSetupSparseSetCmd_Success(t *testing.T) {
	root := setupSparseRepo(t, nil)
	client := &fakeUpdateClient{}
	cli := newSparseCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupSparseCheckoutCmd(), "set", "assets", "src")
	require.NoError(t, err)
	require.Contains(t, out, "Sparse checkout set to 2 path(s)")

	cfg := loadSparseConfig(t, root)
	require.Equal(t, []string{"assets", "src"}, cfg.Sparse)
	require.Equal(t, "assets", client.path, "update syncs the configured sparse paths")
}

func TestSetupSparseSetCmd_InvalidPath(t *testing.T) {
	root := setupSparseRepo(t, nil)
	cli := newSparseCli(t, &fakeUpdateClient{})

	_, err := runCmdInDir(t, root, cli.setupSparseCheckoutCmd(), "set", "../etc")
	require.Error(t, err)
	require.Nil(t, loadSparseConfig(t, root).Sparse)
}

func TestSetupSparseSetCmd_RunError(t *testing.T) {
	root := setupSparseRepo(t, nil)
	wantErr := errors.New("connection refused")
	cli := newSparseCli(t, &fakeUpdateClient{branchErr: wantErr})

	_, err := runCmdInDir(t, root, cli.setupSparseCheckoutCmd(), "set", "assets")
	require.ErrorIs(t, err, wantErr)
}

func TestSetupSparseAddCmd_Success(t *testing.T) {
	root := setupSparseRepo(t, []string{"assets"})
	cli := newSparseCli(t, &fakeUpdateClient{})

	out, err := runCmdInDir(t, root, cli.setupSparseCheckoutCmd(), "add", "docs", "src")
	require.NoError(t, err)
	require.Contains(t, out, "Added 2 path(s)")
	require.Equal(t, []string{"assets", "docs", "src"}, loadSparseConfig(t, root).Sparse)
}

func TestSetupSparseRemoveCmd_Success(t *testing.T) {
	root := setupSparseRepo(t, []string{"assets", "docs", "src"})
	cli := newSparseCli(t, &fakeUpdateClient{})

	out, err := runCmdInDir(t, root, cli.setupSparseCheckoutCmd(), "remove", "/assets/")
	require.NoError(t, err)
	require.Contains(t, out, "Removed 1 path(s)")
	require.Equal(t, []string{"docs", "src"}, loadSparseConfig(t, root).Sparse)
}

func TestSetupSparseDisableCmd_Success(t *testing.T) {
	root := setupSparseRepo(t, []string{"assets", "docs"})
	cli := newSparseCli(t, &fakeUpdateClient{})

	out, err := runCmdInDir(t, root, cli.setupSparseCheckoutCmd(), "disable")
	require.NoError(t, err)
	require.Contains(t, out, "Sparse checkout disabled")
	require.Nil(t, loadSparseConfig(t, root).Sparse)
}

func TestSparseCheckout_NotARepo(t *testing.T) {
	cli := newSparseCli(t, &fakeUpdateClient{})

	_, err := runCmdInDir(t, t.TempDir(), cli.setupSparseCheckoutCmd(), "list")
	require.EqualError(t, err, "not a nipa repository (or any of the parent directories)")
}

func TestSetupSparseSetCmd_InvalidConfigURL(t *testing.T) {
	root := t.TempDir()
	lr := localrepo.NewLocalRepo()
	require.NoError(t, lr.Init(root))
	require.NoError(t, lr.SaveConfig(domain.Config{Url: "http://example.com/onlyone", Branch: "main"}))
	require.NoError(t, lr.Close())

	cli := newSparseCli(t, &fakeUpdateClient{})
	_, err := runCmdInDir(t, root, cli.setupSparseCheckoutCmd(), "set", "assets")
	require.Error(t, err)
}
