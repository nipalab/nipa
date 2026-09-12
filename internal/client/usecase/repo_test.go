package usecase

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
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
	download      map[serverDomain.Hash][]byte
	downloadErr   error
	downloaded    []serverDomain.Hash
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

func (s *stubRepoInterface) DownloadChunks(_ context.Context, hashes []serverDomain.Hash, onChunk ...func(h serverDomain.Hash, data []byte)) (map[serverDomain.Hash][]byte, error) {
	s.downloaded = append(s.downloaded, hashes...)
	if s.downloadErr != nil {
		return nil, s.downloadErr
	}
	out := make(map[serverDomain.Hash][]byte, len(hashes))
	for _, h := range hashes {
		out[h] = s.download[h]
		if len(onChunk) > 0 && onChunk[0] != nil {
			onChunk[0](h, out[h])
		}
	}
	return out, nil
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

	storedChunks  map[serverDomain.Hash][]byte
	storeChunkErr error
	loadChunkErr  error
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
	if s.snapshot != nil {
		return s.snapshot, s.snapshotErr
	}
	return &domain.Snapshot{}, s.snapshotErr
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

func (s *stubLocalRepo) StoreChunk(hash serverDomain.Hash, data []byte) error {
	if s.storeChunkErr != nil {
		return s.storeChunkErr
	}
	if s.storedChunks == nil {
		s.storedChunks = make(map[serverDomain.Hash][]byte)
	}
	s.storedChunks[hash] = data
	return nil
}

func (s *stubLocalRepo) LoadChunk(hash serverDomain.Hash) ([]byte, error) {
	if s.loadChunkErr != nil {
		return nil, s.loadChunkErr
	}
	data, ok := s.storedChunks[hash]
	if !ok {
		return nil, errors.New("chunk not found")
	}
	return data, nil
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

func TestRepo_Clone_MaterializesWorkingCopy(t *testing.T) {
	content := "real file bytes"
	chunked, err := chunker.ChunkAll([]byte(content))
	require.NoError(t, err)
	chunkHash := chunked[0].Hash
	fileHash := chunker.FileHash([]serverDomain.Hash{chunkHash})

	manifest := &serverDomain.TreeNode{
		Name: "root",
		FileChildren: []*serverDomain.File{{
			Name:      "hello.txt",
			Mode:      2,
			SizeBytes: int64(len(content)),
			Hash:      fileHash,
			Chunks:    []serverDomain.Chunk{{Hash: chunkHash, SizeBytes: int64(len(content))}},
		}},
	}
	token := signTestToken(t, "secret")
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: token}}
	auth := NewAuth(nil, storage, nil)
	local := &stubLocalRepo{missingChunks: []serverDomain.Hash{chunkHash}}
	target := t.TempDir()
	repo := NewRepo(auth, &stubRepoInterface{
		defaultBranch: &serverDomain.Branch{Name: "main"},
		manifest:      manifest,
		download:      map[serverDomain.Hash][]byte{chunkHash: []byte(content)},
	}, local)

	require.NoError(t, repo.Clone(context.Background(), "http://example.com/org/project", "example.com", "org", "project", "main", "", target))

	data, err := os.ReadFile(filepath.Join(target, "hello.txt"))
	require.NoError(t, err)
	require.Equal(t, content, string(data), "clone must download the real file content into the working copy")
	require.Equal(t, []byte(content), local.storedChunks[chunkHash], "downloaded content must land in the local chunk cache")
	require.NotNil(t, local.tree)
}

func TestRepo_Clone_NestedFilesMaterialized(t *testing.T) {
	content := "nested"
	chunked, err := chunker.ChunkAll([]byte(content))
	require.NoError(t, err)
	chunkHash := chunked[0].Hash

	manifest := &serverDomain.TreeNode{
		Name: "root",
		TreeChildren: []*serverDomain.TreeNode{{
			Name: "assets",
			FileChildren: []*serverDomain.File{{
				Name:      "logo.png",
				Mode:      2,
				SizeBytes: int64(len(content)),
				Chunks:    []serverDomain.Chunk{{Hash: chunkHash, SizeBytes: int64(len(content))}},
			}},
		}},
	}
	token := signTestToken(t, "secret")
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: token}}
	auth := NewAuth(nil, storage, nil)
	local := &stubLocalRepo{missingChunks: []serverDomain.Hash{chunkHash}}
	repo := NewRepo(auth, &stubRepoInterface{
		defaultBranch: &serverDomain.Branch{Name: "main"},
		manifest:      manifest,
		download:      map[serverDomain.Hash][]byte{chunkHash: []byte(content)},
	}, local)

	target := t.TempDir()
	require.NoError(t, repo.Clone(context.Background(), "http://example.com/org/project", "example.com", "org", "project", "main", "", target))

	data, err := os.ReadFile(filepath.Join(target, "assets", "logo.png"))
	require.NoError(t, err)
	require.Equal(t, content, string(data), "nested files must be flattened into their directory path")
}

func TestRepo_Clone_SubdirCloneSkipsMaterialization(t *testing.T) {
	fileHash := chunker.FileHash(nil)
	token := signTestToken(t, "secret")
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: token}}
	auth := NewAuth(nil, storage, nil)
	local := &stubLocalRepo{}
	clientStub := &stubRepoInterface{
		defaultBranch: &serverDomain.Branch{Name: "main"},
		manifest: &serverDomain.TreeNode{
			Name:         "src",
			FileChildren: []*serverDomain.File{{Name: "main.go", Mode: 2, Hash: fileHash}},
		},
	}
	repo := NewRepo(auth, clientStub, local)
	target := t.TempDir()

	require.NoError(t, repo.Clone(context.Background(), "http://example.com/org/project/src", "example.com", "org", "project", "main", "/src", target))

	require.Empty(t, clientStub.downloaded, "subdirectory clones stay metadata-only until layout support lands")
	_, err := os.Stat(filepath.Join(target, "main.go"))
	require.Error(t, err)
	require.NotNil(t, local.tree)
}

func TestRepo_Clone_Error_DownloadFails(t *testing.T) {
	content := "bytes"
	chunked, err := chunker.ChunkAll([]byte(content))
	require.NoError(t, err)
	chunkHash := chunked[0].Hash
	manifest := &serverDomain.TreeNode{
		Name: "root",
		FileChildren: []*serverDomain.File{{
			Name:      "a.txt",
			Mode:      2,
			SizeBytes: int64(len(content)),
			Chunks:    []serverDomain.Chunk{{Hash: chunkHash, SizeBytes: int64(len(content))}},
		}},
	}
	wantErr := errors.New("download failed")
	token := signTestToken(t, "secret")
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: token}}
	auth := NewAuth(nil, storage, nil)
	repo := NewRepo(auth, &stubRepoInterface{
		defaultBranch: &serverDomain.Branch{Name: "main"},
		manifest:      manifest,
		downloadErr:   wantErr,
	}, &stubLocalRepo{missingChunks: []serverDomain.Hash{chunkHash}})

	err = repo.Clone(context.Background(), "http://example.com/org/project", "example.com", "org", "project", "main", "", t.TempDir())
	require.ErrorIs(t, err, wantErr)
}

func TestRepo_Clone_Error_ChunkHashMismatch(t *testing.T) {
	content := "bytes"
	chunked, err := chunker.ChunkAll([]byte(content))
	require.NoError(t, err)
	chunkHash := chunked[0].Hash
	manifest := &serverDomain.TreeNode{
		Name: "root",
		FileChildren: []*serverDomain.File{{
			Name:      "a.txt",
			Mode:      2,
			SizeBytes: int64(len(content)),
			Chunks:    []serverDomain.Chunk{{Hash: chunkHash, SizeBytes: int64(len(content))}},
		}},
	}
	token := signTestToken(t, "secret")
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: token}}
	auth := NewAuth(nil, storage, nil)
	repo := NewRepo(auth, &stubRepoInterface{
		defaultBranch: &serverDomain.Branch{Name: "main"},
		manifest:      manifest,
		download:      map[serverDomain.Hash][]byte{chunkHash: []byte("corrupted")},
	}, &stubLocalRepo{missingChunks: []serverDomain.Hash{chunkHash}})

	err = repo.Clone(context.Background(), "http://example.com/org/project", "example.com", "org", "project", "main", "", t.TempDir())
	require.Error(t, err)
	require.Contains(t, err.Error(), "hash mismatch")
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
