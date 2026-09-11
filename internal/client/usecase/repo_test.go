package usecase

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

type stubRepoInterface struct {
	defaultBranch *serverDomain.Branch
	defaultErr    error
	manifest      *serverDomain.TreeNode
	manifestErr   error
	listBranches  []*serverDomain.Branch
	listErr       error
}

func (s *stubRepoInterface) GetDefaultBranch(_ context.Context, _, _ string) (*serverDomain.Branch, error) {
	return s.defaultBranch, s.defaultErr
}

func (s *stubRepoInterface) GetTreeNodeManifest(_ context.Context, _, _, _, _ string) (*serverDomain.TreeNode, error) {
	return s.manifest, s.manifestErr
}

func (s *stubRepoInterface) ListBranches(_ context.Context, _, _ string) ([]*serverDomain.Branch, error) {
	return s.listBranches, s.listErr
}

type stubLocalRepo struct {
	initTarget     string
	config         domain.Config
	loadConfig     *domain.Config
	tree           *serverDomain.TreeNode
	snapshot       *domain.Snapshot
	staged         []string
	initErr        error
	configErr      error
	configLoadErr  error
	treeErr        error
	snapshotErr    error
	stagedErr      error
	stageAdd       []string
	stageAddErr    error
	stageRemove    []string
	stageRemoveErr error

	missingChunks      []serverDomain.Hash
	missingChunksErr   error
	missingChunksInput []serverDomain.Hash
	clearedStaged      bool
	clearStagedErr     error
}

func (s *stubLocalRepo) Init(target string) error {
	s.initTarget = target
	return s.initErr
}

func (s *stubLocalRepo) SaveConfig(cfg domain.Config) error {
	s.config = cfg
	return s.configErr
}

func (s *stubLocalRepo) LoadConfig() (*domain.Config, error) {
	if s.configLoadErr != nil {
		return nil, s.configLoadErr
	}
	if s.loadConfig != nil {
		return s.loadConfig, nil
	}
	cfg := s.config
	return &cfg, nil
}

func (s *stubLocalRepo) SaveTree(root *serverDomain.TreeNode) error {
	s.tree = root
	return s.treeErr
}

func (s *stubLocalRepo) Snapshot() (*domain.Snapshot, error) {
	return s.snapshot, s.snapshotErr
}

func (s *stubLocalRepo) ListStaged() ([]string, error) {
	return s.staged, s.stagedErr
}

func (s *stubLocalRepo) StageAdd(path string) error {
	s.stageAdd = append(s.stageAdd, path)
	return s.stageAddErr
}

func (s *stubLocalRepo) StageRemove(paths []string) error {
	s.stageRemove = append(s.stageRemove, paths...)
	return s.stageRemoveErr
}

func (s *stubLocalRepo) MissingChunks(hashes []serverDomain.Hash) ([]serverDomain.Hash, error) {
	s.missingChunksInput = hashes
	return s.missingChunks, s.missingChunksErr
}

func (s *stubLocalRepo) ClearStaged() error {
	s.clearedStaged = true
	return s.clearStagedErr
}

func TestNewRepo(t *testing.T) {
	auth := NewAuth(nil, nil, nil)
	repo := NewRepo(auth, &stubRepoInterface{}, &stubLocalRepo{})
	require.Equal(t, auth, repo.auth)
}

func TestRepo_Clone_Success(t *testing.T) {
	token := signTestToken(t, "secret")
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: token}}
	auth := NewAuth(nil, storage, nil)
	local := &stubLocalRepo{}
	target := t.TempDir()
	repo := NewRepo(auth, &stubRepoInterface{
		defaultBranch: &serverDomain.Branch{Name: "main"},
		manifest:      &serverDomain.TreeNode{},
	}, local)

	err := repo.Clone(context.Background(), "http://example.com/org/project", "example.com", "org", "project", "main", "/src", target)
	require.NoError(t, err)
	require.Equal(t, target, local.initTarget)
	require.Equal(t, domain.Config{Url: "http://example.com/org/project", Branch: "main"}, local.config)
	require.NotNil(t, local.tree)
}

func TestRepo_Clone_Success_DefaultBranch(t *testing.T) {
	token := signTestToken(t, "secret")
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: token}}
	auth := NewAuth(nil, storage, nil)
	local := &stubLocalRepo{}
	repo := NewRepo(auth, &stubRepoInterface{
		defaultBranch: &serverDomain.Branch{Name: "dev"},
		manifest:      &serverDomain.TreeNode{},
	}, local)

	err := repo.Clone(context.Background(), "http://example.com/org/project", "example.com", "org", "project", "", "/src", t.TempDir())
	require.NoError(t, err)
	require.Equal(t, domain.Config{Url: "http://example.com/org/project", Branch: "dev"}, local.config)
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
	}, &stubLocalRepo{})

	err := repo.Clone(context.Background(), "http://example.com/org/project", "example.com", "org", "project", "main", "/src", t.TempDir())
	require.NoError(t, err)
	require.Equal(t, "apin", executor.lastUsername)
	require.Equal(t, "secret", executor.lastPassword)
}

func TestRepo_Clone_Error_PromptFailed(t *testing.T) {
	wantErr := errors.New("prompt interrupted")
	storage := &stubSecureStorage{loadErr: errors.New("not found")}
	input := &stubUserInput{err: wantErr}
	auth := NewAuth(nil, storage, input)
	repo := NewRepo(auth, &stubRepoInterface{}, &stubLocalRepo{})

	err := repo.Clone(context.Background(), "http://example.com/org/project", "example.com", "org", "project", "main", "/src", t.TempDir())
	require.ErrorIs(t, err, wantErr)
}

func TestRepo_Clone_Error_LoginFailed(t *testing.T) {
	wantErr := errors.New("login failed")
	storage := &stubSecureStorage{loadErr: errors.New("not found")}
	input := &stubUserInput{username: "apin", password: "secret"}
	executor := &stubLoginExecutor{usernameErr: wantErr}
	auth := NewAuth(executor, storage, input)
	repo := NewRepo(auth, &stubRepoInterface{}, &stubLocalRepo{})

	err := repo.Clone(context.Background(), "http://example.com/org/project", "example.com", "org", "project", "main", "/src", t.TempDir())
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
	}, &stubLocalRepo{})

	err := repo.Clone(context.Background(), "http://example.com/org/project", "example.com", "org", "project", "main", "/src", t.TempDir())
	require.NoError(t, err)
	require.Equal(t, "apin", executor.lastUsername)
	require.Equal(t, "secret", executor.lastPassword)
}

func TestRepo_Clone_EmptyRepo(t *testing.T) {
	token := signTestToken(t, "secret")
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: token}}
	auth := NewAuth(nil, storage, nil)
	local := &stubLocalRepo{}
	repo := NewRepo(auth, &stubRepoInterface{
		defaultBranch: &serverDomain.Branch{Name: "main"},
		manifest:      nil,
	}, local)

	err := repo.Clone(context.Background(), "http://example.com/org/project", "example.com", "org", "project", "main", "/src", t.TempDir())
	require.NoError(t, err)
	require.Equal(t, domain.Config{Url: "http://example.com/org/project", Branch: "main"}, local.config)
	require.Nil(t, local.tree)
}

func TestRepo_Clone_Error_GetDefaultBranchFailed(t *testing.T) {
	wantErr := errors.New("get default branch failed")
	token := signTestToken(t, "secret")
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: token}}
	auth := NewAuth(nil, storage, nil)
	repo := NewRepo(auth, &stubRepoInterface{defaultErr: wantErr}, &stubLocalRepo{})

	err := repo.Clone(context.Background(), "http://example.com/org/project", "example.com", "org", "project", "", "/src", t.TempDir())
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
	}, &stubLocalRepo{})

	err := repo.Clone(context.Background(), "http://example.com/org/project", "example.com", "org", "project", "main", "/src", t.TempDir())
	require.ErrorIs(t, err, wantErr)
}

func TestRepo_Clone_Error_LocalInitFailed(t *testing.T) {
	wantErr := errors.New("init failed")
	token := signTestToken(t, "secret")
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: token}}
	auth := NewAuth(nil, storage, nil)
	repo := NewRepo(auth, &stubRepoInterface{
		manifest: &serverDomain.TreeNode{},
	}, &stubLocalRepo{initErr: wantErr})

	err := repo.Clone(context.Background(), "http://example.com/org/project", "example.com", "org", "project", "main", "/src", t.TempDir())
	require.ErrorIs(t, err, wantErr)
}

func TestRepo_Clone_Error_SaveConfigFailed(t *testing.T) {
	wantErr := errors.New("save config failed")
	token := signTestToken(t, "secret")
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: token}}
	auth := NewAuth(nil, storage, nil)
	repo := NewRepo(auth, &stubRepoInterface{
		manifest: &serverDomain.TreeNode{},
	}, &stubLocalRepo{configErr: wantErr})

	err := repo.Clone(context.Background(), "http://example.com/org/project", "example.com", "org", "project", "main", "/src", t.TempDir())
	require.ErrorIs(t, err, wantErr)
}

func TestRepo_Clone_Error_TargetNotEmpty(t *testing.T) {
	token := signTestToken(t, "secret")
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: token}}
	auth := NewAuth(nil, storage, nil)
	repo := NewRepo(auth, &stubRepoInterface{
		defaultBranch: &serverDomain.Branch{Name: "main"},
		manifest:      &serverDomain.TreeNode{},
	}, &stubLocalRepo{})

	target := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(target, "existing.txt"), []byte("x"), 0o644))

	err := repo.Clone(context.Background(), "http://example.com/org/project", "example.com", "org", "project", "main", "/src", target)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not empty")
}

func TestRepo_Clone_Error_TargetIsFile(t *testing.T) {
	token := signTestToken(t, "secret")
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: token}}
	auth := NewAuth(nil, storage, nil)
	repo := NewRepo(auth, &stubRepoInterface{
		defaultBranch: &serverDomain.Branch{Name: "main"},
		manifest:      &serverDomain.TreeNode{},
	}, &stubLocalRepo{})

	target := filepath.Join(t.TempDir(), "file.txt")
	require.NoError(t, os.WriteFile(target, []byte("x"), 0o644))

	err := repo.Clone(context.Background(), "http://example.com/org/project", "example.com", "org", "project", "main", "/src", target)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not a directory")
}

func TestRepo_Clone_Error_SaveTreeFailed(t *testing.T) {
	wantErr := errors.New("save tree failed")
	token := signTestToken(t, "secret")
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: token}}
	auth := NewAuth(nil, storage, nil)
	repo := NewRepo(auth, &stubRepoInterface{
		manifest: &serverDomain.TreeNode{},
	}, &stubLocalRepo{treeErr: wantErr})

	err := repo.Clone(context.Background(), "http://example.com/org/project", "example.com", "org", "project", "main", "/src", t.TempDir())
	require.ErrorIs(t, err, wantErr)
}

func TestRepo_ListBranches_Success(t *testing.T) {
	token := signTestToken(t, "secret")
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: token}}
	auth := NewAuth(nil, storage, nil)
	stub := &stubRepoInterface{listBranches: []*serverDomain.Branch{
		{Name: "main", IsDefault: true},
		{Name: "dev"},
	}}
	repo := NewRepo(auth, stub, &stubLocalRepo{})

	got, err := repo.ListBranches(context.Background(), "example.com", "org", "project")
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, "main", got[0].Name)
	require.True(t, got[0].IsDefault)
	require.Equal(t, "dev", got[1].Name)
}

func TestRepo_ListBranches_LoginFailed(t *testing.T) {
	wantErr := errors.New("login failed")
	storage := &stubSecureStorage{loadErr: errors.New("not found")}
	input := &stubUserInput{username: "apin", password: "secret"}
	executor := &stubLoginExecutor{usernameErr: wantErr}
	auth := NewAuth(executor, storage, input)
	repo := NewRepo(auth, &stubRepoInterface{}, &stubLocalRepo{})

	_, err := repo.ListBranches(context.Background(), "example.com", "org", "project")
	require.ErrorIs(t, err, wantErr)
}

func TestRepo_ListBranches_ServerError(t *testing.T) {
	wantErr := errors.New("list failed")
	token := signTestToken(t, "secret")
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: token}}
	auth := NewAuth(nil, storage, nil)
	repo := NewRepo(auth, &stubRepoInterface{listErr: wantErr}, &stubLocalRepo{})

	_, err := repo.ListBranches(context.Background(), "example.com", "org", "project")
	require.ErrorIs(t, err, wantErr)
}
