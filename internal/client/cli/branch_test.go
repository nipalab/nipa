package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/usecase"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

type fakeListRepoInterface struct {
	branches []*serverDomain.Branch
}

func (f fakeListRepoInterface) GetDefaultBranch(_ context.Context, _, _ string) (*serverDomain.Branch, error) {
	return &serverDomain.Branch{Name: "main"}, nil
}

func (f fakeListRepoInterface) GetTreeNodeManifest(_ context.Context, _, _, _, _ string) (*serverDomain.TreeNode, error) {
	return &serverDomain.TreeNode{}, nil
}

func (f fakeListRepoInterface) ListBranches(_ context.Context, _, _ string) ([]*serverDomain.Branch, error) {
	return f.branches, nil
}

func newBranchCli(branches ...*serverDomain.Branch) *Cli {
	auth := usecase.NewAuth(fakeExecutor{}, &fakeStorage{}, &fakeInput{})
	repo := usecase.NewRepo(auth, fakeListRepoInterface{branches: branches}, fakeLocalRepo{})
	return NewCli(&fakeUsecaseContainer{auth: auth, repo: repo}, &fakeConnector{})
}

func setupRepo(t *testing.T, branch string) string {
	t.Helper()
	target := t.TempDir()
	lr := localrepo.NewLocalRepo()
	require.NoError(t, lr.Init(target))
	defer lr.Close()
	require.NoError(t, lr.SaveConfig(domain.Config{Url: "http://example.com/org/project", Branch: branch}))
	return target
}

func runBranchCmd(t *testing.T, cli *Cli, dir string, args ...string) (string, error) {
	t.Helper()
	oldWD, err := os.Getwd()
	require.NoError(t, err)
	defer func() { require.NoError(t, os.Chdir(oldWD)) }()
	require.NoError(t, os.Chdir(dir))

	var buf bytes.Buffer
	cmd := cli.setupBranchCmd()
	cmd.SetOut(&buf)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return buf.String(), err
}

func TestSetupBranchCmd_AtRoot(t *testing.T) {
	root := setupRepo(t, "main")
	cli := newBranchCli()

	out, err := runBranchCmd(t, cli, root)
	require.NoError(t, err)
	require.Equal(t, "main\n", out)
}

func TestSetupBranchCmd_AtChild(t *testing.T) {
	root := setupRepo(t, "dev")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "a", "b", "c"), 0o755))
	cli := newBranchCli()

	out, err := runBranchCmd(t, cli, filepath.Join(root, "a", "b", "c"))
	require.NoError(t, err)
	require.Equal(t, "dev\n", out)
}

func TestSetupBranchCmd_NotARepo(t *testing.T) {
	dir := t.TempDir()
	cli := newBranchCli()

	_, err := runBranchCmd(t, cli, dir)
	require.Error(t, err)
	require.Equal(t, "not a nipa repository (or any of the parent directories)", err.Error())
}

func TestSetupBranchCmd_DepthMax32(t *testing.T) {
	root := setupRepo(t, "feature")
	deep := root
	for i := 0; i < 33; i++ {
		deep = filepath.Join(deep, "d")
	}
	require.NoError(t, os.MkdirAll(deep, 0o755))
	cli := newBranchCli()

	_, err := runBranchCmd(t, cli, deep)
	require.Error(t, err)
	require.Equal(t, "not a nipa repository (or any of the parent directories)", err.Error())
}

func TestSetupBranchCmd_All(t *testing.T) {
	root := setupRepo(t, "feature")
	cli := newBranchCli(
		&serverDomain.Branch{Name: "main", IsDefault: true},
		&serverDomain.Branch{Name: "dev"},
		&serverDomain.Branch{Name: "feature"},
	)

	out, err := runBranchCmd(t, cli, root, "-a")
	require.NoError(t, err)
	require.Equal(t, "main\ndev\nfeature *\n", out)
}

func TestSetupBranchCmd_All_NotARepo(t *testing.T) {
	dir := t.TempDir()
	cli := newBranchCli()

	_, err := runBranchCmd(t, cli, dir, "-a")
	require.Error(t, err)
	require.Equal(t, "not a nipa repository (or any of the parent directories)", err.Error())
}
