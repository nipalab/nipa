package cli

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/usecase"
)

type fakeUsecaseContainer struct {
	auth *usecase.Auth
	repo *usecase.Repo
}

func (f *fakeUsecaseContainer) Auth() *usecase.Auth { return f.auth }
func (f *fakeUsecaseContainer) Repo() *usecase.Repo { return f.repo }

func TestNewCli(t *testing.T) {
	c := NewCli(&fakeUsecaseContainer{})
	require.NotNil(t, c)
	require.Equal(t, &fakeUsecaseContainer{}, c.useCase)
}

func withArgs(t *testing.T, args []string, fn func()) {
	t.Helper()
	old := os.Args
	os.Args = args
	defer func() { os.Args = old }()
	fn()
}

func TestCli_Run_Success(t *testing.T) {
	repo, _ := helperAuth(t)
	cli := newCloneCli(repo)

	withArgs(t, []string{"nipa", "clone", "http://example.com/org/project", "."}, func() {
		require.NoError(t, cli.Run())
	})
}

func TestCli_Run_CloneInvalidURL(t *testing.T) {
	repo, _ := helperAuth(t)
	cli := newCloneCli(repo)

	withArgs(t, []string{"nipa", "clone", "http://example.com/onlyone", "."}, func() {
		require.Error(t, cli.Run())
	})
}

func TestCli_Run_NoArgs(t *testing.T) {
	auth := usecase.NewAuth(fakeExecutor{}, &fakeStorage{}, &fakeInput{})
	cli := newCloneCli(usecase.NewRepo(auth, fakeRepoInterface{}))

	withArgs(t, []string{"nipa"}, func() {
		require.NoError(t, cli.Run())
	})
}
