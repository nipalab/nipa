package cli

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/usecase"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

type fakeExecutor struct{}

func (fakeExecutor) LoginWithUsernamePassword(_ context.Context, _, _, _ string) (*domain.LoginResult, error) {
	return &domain.LoginResult{AccessToken: "tok"}, nil
}

func (fakeExecutor) LoginWithRefreshToken(_ context.Context, _, _ string) (*domain.LoginResult, error) {
	return &domain.LoginResult{AccessToken: "tok"}, nil
}

type fakeStorage struct {
	token   string
	loadErr error
	lastHost string
	saved   []*domain.LoginResult
}

func (s *fakeStorage) SaveToken(data *domain.LoginResult) error {
	s.saved = append(s.saved, data)
	return nil
}

func (s *fakeStorage) LoadToken(host string) (*domain.LoginResult, error) {
	s.lastHost = host
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	return &domain.LoginResult{AccessToken: s.token, Host: host}, nil
}

type fakeInput struct {
	err error
}

func (f *fakeInput) PromptUsernameAndPassword() (string, string, error) {
	return "user", "pass", f.err
}

type fakeConnector struct {
	lastHost string
	err      error
}

func (f *fakeConnector) Connect(_ context.Context, host string) error {
	f.lastHost = host
	return f.err
}

type fakeRepoInterface struct{}

func (fakeRepoInterface) GetDefaultBranch(_ context.Context, _, _ string) (*serverDomain.Branch, error) {
	return &serverDomain.Branch{Name: "main"}, nil
}

func (fakeRepoInterface) GetTreeNodeManifest(_ context.Context, _, _, _ string) (*serverDomain.TreeNode, error) {
	return &serverDomain.TreeNode{}, nil
}

func helperAuth(t *testing.T) (*usecase.Repo, *fakeStorage) {
	t.Helper()

	token := signTestJWT(t)
	storage := &fakeStorage{token: token}
	auth := usecase.NewAuth(fakeExecutor{}, storage, &fakeInput{})
	repo := usecase.NewRepo(auth, fakeRepoInterface{})
	return repo, storage
}

func signTestJWT(t *testing.T) string {
	t.Helper()
	claims := serverDomain.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "42",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("test-secret"))
	require.NoError(t, err)
	return signed
}

func newCloneCli(repo *usecase.Repo) *Cli {
	auth := usecase.NewAuth(fakeExecutor{}, &fakeStorage{}, &fakeInput{})
	return NewCli(&fakeUsecaseContainer{auth: auth, repo: repo}, &fakeConnector{})
}

func TestSetupCloneCmd_Valid(t *testing.T) {
	repo, storage := helperAuth(t)
	connector := &fakeConnector{}
	cli := NewCli(&fakeUsecaseContainer{auth: usecase.NewAuth(fakeExecutor{}, storage, &fakeInput{}), repo: repo}, connector)

	cmd := cli.setupCloneCmd()
	cmd.SetArgs([]string{"http://example.com/org/project/path/to/repo", "./target"})
	require.NoError(t, cmd.Execute())
	require.Equal(t, "example.com", connector.lastHost)
	require.Equal(t, "example.com", storage.lastHost)
}

func TestSetupCloneCmd_InvalidURL(t *testing.T) {
	repo, _ := helperAuth(t)
	cli := newCloneCli(repo)

	cmd := cli.setupCloneCmd()
	cmd.SetArgs([]string{"http://example.com/onlyone", "./target"})
	err := cmd.Execute()
	require.Error(t, err)
}

func TestSetupCloneCmd_RepoError(t *testing.T) {
	wantErr := errors.New("prompt interrupted")
	storage := &fakeStorage{loadErr: errors.New("no stored token")}
	auth := usecase.NewAuth(fakeExecutor{}, storage, &fakeInput{err: wantErr})
	cli := NewCli(&fakeUsecaseContainer{auth: auth, repo: usecase.NewRepo(auth, fakeRepoInterface{})}, &fakeConnector{})

	cmd := cli.setupCloneCmd()
	cmd.SetArgs([]string{"http://example.com/org/project", "./target"})
	err := cmd.Execute()
	require.ErrorIs(t, err, wantErr)
}

func TestSetupCloneCmd_ConnectError(t *testing.T) {
	wantErr := errors.New("connection refused")
	connector := &fakeConnector{err: wantErr}
	cli := NewCli(&fakeUsecaseContainer{}, connector)

	cmd := cli.setupCloneCmd()
	cmd.SetArgs([]string{"http://example.com/org/project", "./target"})
	err := cmd.Execute()
	require.ErrorIs(t, err, wantErr)
	require.Equal(t, "example.com", connector.lastHost)
}
