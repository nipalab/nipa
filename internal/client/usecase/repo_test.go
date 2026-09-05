package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

type stubRepoInterface struct {
	connectErr    error
	defaultBranch *serverDomain.Branch
	defaultErr    error
	manifest      *serverDomain.TreeNode
	manifestErr   error
	lastHost      string
}

func (s *stubRepoInterface) Connect(_ context.Context, host string) error {
	s.lastHost = host
	return s.connectErr
}

func (s *stubRepoInterface) GetDefaultBranch(_ context.Context, _, _ string) (*serverDomain.Branch, error) {
	return s.defaultBranch, s.defaultErr
}

func (s *stubRepoInterface) GetTreeNodeManifest(_ context.Context, _, _, _ string) (*serverDomain.TreeNode, error) {
	return s.manifest, s.manifestErr
}

func TestNewRepo(t *testing.T) {
	auth := NewAuth(nil, nil, nil)
	repo := NewRepo(auth, &stubRepoInterface{})
	require.Equal(t, auth, repo.auth)
}

func TestRepo_Clone_Success(t *testing.T) {
	token := signTestToken(t, "secret")
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: token}}
	auth := NewAuth(nil, storage, nil)
	repo := NewRepo(auth, &stubRepoInterface{
		defaultBranch: &serverDomain.Branch{Name: "main"},
		manifest:      &serverDomain.TreeNode{},
	})

	s := repo.repoInterface.(*stubRepoInterface)
	err := repo.Clone(context.Background(), "example.com", "org", "project", "main", "/src", "/target")
	require.NoError(t, err)
	require.Equal(t, "example.com", s.lastHost)
}

func TestRepo_Clone_Success_NeedLogin(t *testing.T) {
	token := signTestToken(t, "secret")
	storage := &stubSecureStorage{loadErr: errors.New("not found")}
	input := &stubUserInput{username: "apin", password: "secret"}
	executor := &stubLoginExecutor{usernameResult: &domain.LoginResult{
		AccessToken: token,
		Host:        "example.com",
	}}
	auth := NewAuth(executor, storage, input)
	repo := NewRepo(auth, &stubRepoInterface{
		defaultBranch: &serverDomain.Branch{Name: "main"},
		manifest:      &serverDomain.TreeNode{},
	})

	err := repo.Clone(context.Background(), "example.com", "org", "project", "main", "/src", "/target")
	require.NoError(t, err)
	require.Equal(t, "apin", executor.lastUsername)
	require.Equal(t, "secret", executor.lastPassword)
}

func TestRepo_Clone_Error_PromptFailed(t *testing.T) {
	wantErr := errors.New("prompt interrupted")
	storage := &stubSecureStorage{loadErr: errors.New("not found")}
	input := &stubUserInput{err: wantErr}
	auth := NewAuth(nil, storage, input)
	repo := NewRepo(auth, &stubRepoInterface{})

	err := repo.Clone(context.Background(), "example.com", "org", "project", "main", "/src", "/target")
	require.ErrorIs(t, err, wantErr)
}

func TestRepo_Clone_Error_ConnectFailed(t *testing.T) {
	wantErr := errors.New("connect failed")
	token := signTestToken(t, "secret")
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: token}}
	auth := NewAuth(nil, storage, nil)
	s := &stubRepoInterface{connectErr: wantErr}
	repo := NewRepo(auth, s)

	err := repo.Clone(context.Background(), "example.com", "org", "project", "main", "/src", "/target")
	require.ErrorIs(t, err, wantErr)
	require.Equal(t, "example.com", s.lastHost)
}

func TestRepo_Clone_Error_LoginFailed(t *testing.T) {
	wantErr := errors.New("login failed")
	storage := &stubSecureStorage{loadErr: errors.New("not found")}
	input := &stubUserInput{username: "apin", password: "secret"}
	executor := &stubLoginExecutor{usernameErr: wantErr}
	auth := NewAuth(executor, storage, input)
	repo := NewRepo(auth, &stubRepoInterface{})

	err := repo.Clone(context.Background(), "example.com", "org", "project", "main", "/src", "/target")
	require.ErrorIs(t, err, wantErr)
}

func TestRepo_Clone_MalformedToken(t *testing.T) {
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: "not-a-jwt"}}
	input := &stubUserInput{username: "apin", password: "secret"}
	executor := &stubLoginExecutor{usernameResult: &domain.LoginResult{
		AccessToken: "fresh-token",
		Host:        "example.com",
	}}
	auth := NewAuth(executor, storage, input)
	repo := NewRepo(auth, &stubRepoInterface{
		defaultBranch: &serverDomain.Branch{Name: "main"},
		manifest:      &serverDomain.TreeNode{},
	})

	err := repo.Clone(context.Background(), "example.com", "org", "project", "main", "/src", "/target")
	require.NoError(t, err)
	require.Equal(t, "apin", executor.lastUsername)
	require.Equal(t, "secret", executor.lastPassword)
}

func TestRepo_Clone_Error_GetDefaultBranchFailed(t *testing.T) {
	wantErr := errors.New("get default branch failed")
	token := signTestToken(t, "secret")
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: token}}
	auth := NewAuth(nil, storage, nil)
	repo := NewRepo(auth, &stubRepoInterface{defaultErr: wantErr})

	err := repo.Clone(context.Background(), "example.com", "org", "project", "main", "/src", "/target")
	require.ErrorIs(t, err, wantErr)
}

func TestRepo_Clone_Error_GetManifestFailed(t *testing.T) {
	wantErr := errors.New("get manifest failed")
	token := signTestToken(t, "secret")
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: token}}
	auth := NewAuth(nil, storage, nil)
	repo := NewRepo(auth, &stubRepoInterface{
		defaultBranch: &serverDomain.Branch{Name: "main"},
		manifestErr:   wantErr,
	})

	err := repo.Clone(context.Background(), "example.com", "org", "project", "main", "/src", "/target")
	require.ErrorIs(t, err, wantErr)
}
