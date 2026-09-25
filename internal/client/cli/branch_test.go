package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/usecase"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type fakeListRepoInterface struct {
	branches         []*serverDomain.Branch
	createdBranch    *serverDomain.Branch
	createdBranchErr error
	deletedBranch    string
	deleteErr        error
}

func (f fakeListRepoInterface) GetDefaultBranch(_ context.Context, _, _ string) (*serverDomain.Branch, error) {
	return &serverDomain.Branch{Name: "main"}, nil
}

func (f fakeListRepoInterface) GetBranchByName(_ context.Context, _, _, name string) (*serverDomain.Branch, error) {
	return &serverDomain.Branch{Name: name}, nil
}

func (f fakeListRepoInterface) GetTreeNodeManifest(_ context.Context, _, _, _ string, _ []string) (*serverDomain.TreeNode, error) {
	return &serverDomain.TreeNode{}, nil
}

func (f fakeListRepoInterface) ListBranches(_ context.Context, _, _ string) ([]*serverDomain.Branch, error) {
	return f.branches, nil
}

func (f fakeListRepoInterface) CreateBranch(_ context.Context, _, _, name, _, _, _ string) (*serverDomain.Branch, error) {
	if f.createdBranch != nil {
		return f.createdBranch, f.createdBranchErr
	}
	return &serverDomain.Branch{Name: name}, f.createdBranchErr
}

func (f *fakeListRepoInterface) DeleteBranch(_ context.Context, _, _, name string) error {
	f.deletedBranch = name
	return f.deleteErr
}

func (f fakeListRepoInterface) DownloadChunks(_ context.Context, _ domain.ChunkScope, _ []serverDomain.Hash, _ func(h serverDomain.Hash, data []byte) error) error {
	return nil
}

func (f fakeListRepoInterface) GetCommitLog(_ context.Context, _, _, _ string, _ *snow.ID, _ int) ([]*serverDomain.CommitLogEntry, error) {
	return nil, nil
}

func newBranchCli(branches ...*serverDomain.Branch) *Cli {
	return newBranchCliWithRepo(&fakeListRepoInterface{branches: branches})
}

func newBranchCliWithRepo(fake *fakeListRepoInterface) *Cli {
	auth := usecase.NewAuth(fakeExecutor{}, &fakeStorage{}, &fakeInput{})
	repo := usecase.NewRepo(auth, fake, fakeLocalRepo{})
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

func TestSetupBranchCmd_Create(t *testing.T) {
	root := setupRepo(t, "main")
	cli := newBranchCli()

	out, err := runBranchCmd(t, cli, root, "-c", "feature")
	require.NoError(t, err)
	require.Equal(t, "Created and switched to branch \"feature\"\n", out)
}

func TestSetupBranchCmd_Create_LongFlag(t *testing.T) {
	root := setupRepo(t, "main")
	cli := newBranchCli()

	out, err := runBranchCmd(t, cli, root, "--create", "feature")
	require.NoError(t, err)
	require.Equal(t, "Created and switched to branch \"feature\"\n", out)
}

func TestSetupBranchCmd_Create_NotARepo(t *testing.T) {
	dir := t.TempDir()
	cli := newBranchCli()

	_, err := runBranchCmd(t, cli, dir, "-c", "feature")
	require.Error(t, err)
	require.Equal(t, "not a nipa repository (or any of the parent directories)", err.Error())
}

func TestSetupBranchCmd_Create_ServerError(t *testing.T) {
	root := setupRepo(t, "main")
	wantErr := errors.New(`branch "feature" already exists`)
	cli := NewCli(&fakeUsecaseContainer{
		auth: usecase.NewAuth(fakeExecutor{}, &fakeStorage{}, &fakeInput{}),
		repo: usecase.NewRepo(
			usecase.NewAuth(fakeExecutor{}, &fakeStorage{}, &fakeInput{}),
			&fakeListRepoInterface{createdBranchErr: wantErr},
			fakeLocalRepo{},
		),
	}, &fakeConnector{})

	_, err := runBranchCmd(t, cli, root, "-c", "feature")
	require.ErrorIs(t, err, wantErr)
}

func TestSetupBranchCmd_Delete(t *testing.T) {
	root := setupRepo(t, "main")
	fake := &fakeListRepoInterface{}
	cli := newBranchCliWithRepo(fake)

	out, err := runBranchCmd(t, cli, root, "-d", "feature")
	require.NoError(t, err)
	require.Equal(t, "Deleted branch \"feature\"\n", out)
	require.Equal(t, "feature", fake.deletedBranch)
}

func TestSetupBranchCmd_Delete_LongFlag(t *testing.T) {
	root := setupRepo(t, "main")
	fake := &fakeListRepoInterface{}
	cli := newBranchCliWithRepo(fake)

	out, err := runBranchCmd(t, cli, root, "--delete", "feature")
	require.NoError(t, err)
	require.Equal(t, "Deleted branch \"feature\"\n", out)
	require.Equal(t, "feature", fake.deletedBranch)
}

func TestSetupBranchCmd_Delete_CurrentBranch(t *testing.T) {
	root := setupRepo(t, "main")
	fake := &fakeListRepoInterface{}
	cli := newBranchCliWithRepo(fake)

	_, err := runBranchCmd(t, cli, root, "-d", "main")
	require.Error(t, err)
	require.Contains(t, err.Error(), "current branch")
	require.Empty(t, fake.deletedBranch)
}

func TestSetupBranchCmd_Delete_ServerError(t *testing.T) {
	root := setupRepo(t, "main")
	wantErr := errors.New("protected branch")
	cli := newBranchCliWithRepo(&fakeListRepoInterface{deleteErr: wantErr})

	_, err := runBranchCmd(t, cli, root, "-d", "feature")
	require.ErrorIs(t, err, wantErr)
}

func TestSetupBranchCmd_FlagsMutuallyExclusive(t *testing.T) {
	root := setupRepo(t, "main")
	cli := newBranchCli()

	_, err := runBranchCmd(t, cli, root, "-a", "-d", "feature")
	require.Error(t, err)
	require.Contains(t, err.Error(), "mutually exclusive")
}
