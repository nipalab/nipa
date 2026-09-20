package usecase

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type stubRepoInterface struct {
	defaultBranch   *serverDomain.Branch
	defaultErr      error
	branch          *serverDomain.Branch
	branchErr       error
	branchName      string
	manifest        *serverDomain.TreeNode
	manifestErr     error
	listBranches    []*serverDomain.Branch
	listErr         error
	createdBranch   *serverDomain.Branch
	createErr       error
	created         string
	createdFrom     string
	createdFromID   string
	createdFromHash string
	download        map[serverDomain.Hash][]byte
	downloadErr     error
	downloaded      []serverDomain.Hash
	commitLogFn     func(ctx context.Context, org, project, branch string, startCommitID *snow.ID, limit int) ([]*serverDomain.CommitLogEntry, error)
}

func (s *stubRepoInterface) GetDefaultBranch(_ context.Context, _, _ string) (*serverDomain.Branch, error) {
	return s.defaultBranch, s.defaultErr
}

func (s *stubRepoInterface) GetBranchByName(_ context.Context, _, _, name string) (*serverDomain.Branch, error) {
	s.branchName = name
	return s.branch, s.branchErr
}

func (s *stubRepoInterface) GetTreeNodeManifest(_ context.Context, _, _, _, _ string) (*serverDomain.TreeNode, error) {
	return s.manifest, s.manifestErr
}

func (s *stubRepoInterface) ListBranches(_ context.Context, _, _ string) ([]*serverDomain.Branch, error) {
	return s.listBranches, s.listErr
}

func (s *stubRepoInterface) CreateBranch(_ context.Context, _, _, name, fromBranch, fromCommitID, fromCommitHash string) (*serverDomain.Branch, error) {
	s.created = name
	s.createdFrom = fromBranch
	s.createdFromID = fromCommitID
	s.createdFromHash = fromCommitHash
	return s.createdBranch, s.createErr
}

func (s *stubRepoInterface) DownloadChunks(_ context.Context, hashes []serverDomain.Hash, onChunk func(h serverDomain.Hash, data []byte) error) error {
	s.downloaded = append(s.downloaded, hashes...)
	if s.downloadErr != nil {
		return s.downloadErr
	}
	for _, h := range hashes {
		data, ok := s.download[h]
		if !ok {
			continue
		}
		if err := onChunk(h, data); err != nil {
			return err
		}
	}
	return nil
}

func (s *stubRepoInterface) GetCommitLog(ctx context.Context, org, project, branch string, startCommitID *snow.ID, limit int) ([]*serverDomain.CommitLogEntry, error) {
	if s.commitLogFn != nil {
		return s.commitLogFn(ctx, org, project, branch, startCommitID, limit)
	}
	return nil, nil
}

type stubLocalRepo struct {
	initTarget     string
	config         domain.Config
	loadConfig     *domain.Config
	tree           *serverDomain.TreeNode
	snapshot       *domain.Snapshot
	snapshotCalls  int
	snapshotErrOn  int
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

	loadCommit      *domain.LocalCommit
	loadCommitErr   error
	savedCommitID   string
	savedCommitHash string
	saveCommitErr   error

	missingChunks      []serverDomain.Hash
	missingChunksErr   error
	missingChunksInput []serverDomain.Hash
	clearedStaged      bool
	clearStagedErr     error

	mergeState    *domain.MergeState
	mergeStateErr error
	savedMerge    *domain.MergeState
	saveMergeErr  error
	clearedMerge  bool
	clearMergeErr error

	revertState    *domain.RevertState
	revertStateErr error
	savedRevert    *domain.RevertState
	saveRevertErr  error
	clearedRevert  bool
	clearRevertErr error

	storedChunks  map[serverDomain.Hash][]byte
	storeCalls    int
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
	s.snapshotCalls++
	err := s.snapshotErr
	if s.snapshotErrOn > 0 && s.snapshotCalls != s.snapshotErrOn {
		err = nil
	}
	if s.snapshot != nil {
		return s.snapshot, err
	}
	return &domain.Snapshot{}, err
}

func (s *stubLocalRepo) ListStaged() ([]string, error) {
	return s.staged, s.stagedErr
}

func (s *stubLocalRepo) StageAdd(path string) error {
	s.stageAdd = append(s.stageAdd, path)
	s.staged = append(s.staged, path)
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

func (s *stubLocalRepo) LoadMergeState() (*domain.MergeState, error) {
	return s.mergeState, s.mergeStateErr
}

func (s *stubLocalRepo) SaveMergeState(state *domain.MergeState) error {
	s.savedMerge = state
	s.mergeState = state
	return s.saveMergeErr
}

func (s *stubLocalRepo) ClearMergeState() error {
	s.clearedMerge = true
	return s.clearMergeErr
}

func (s *stubLocalRepo) LoadRevertState() (*domain.RevertState, error) {
	return s.revertState, s.revertStateErr
}

func (s *stubLocalRepo) SaveRevertState(state *domain.RevertState) error {
	s.savedRevert = state
	s.revertState = state
	return s.saveRevertErr
}

func (s *stubLocalRepo) ClearRevertState() error {
	s.clearedRevert = true
	return s.clearRevertErr
}

func (s *stubLocalRepo) SaveCommit(commitID, commitHash string) error {
	s.savedCommitID = commitID
	s.savedCommitHash = commitHash
	return s.saveCommitErr
}

func (s *stubLocalRepo) LoadCommit() (*domain.LocalCommit, error) {
	if s.loadCommitErr != nil {
		return nil, s.loadCommitErr
	}
	if s.loadCommit != nil {
		return s.loadCommit, nil
	}
	return &domain.LocalCommit{}, nil
}

func (s *stubLocalRepo) StoreChunks(chunks []*serverDomain.ChunkData) error {
	s.storeCalls++
	if s.storeChunkErr != nil {
		return s.storeChunkErr
	}
	if s.storedChunks == nil {
		s.storedChunks = make(map[serverDomain.Hash][]byte)
	}
	for _, c := range chunks {
		s.storedChunks[c.Hash] = c.Data
	}
	return nil
}

func (s *stubLocalRepo) OpenChunk(hash serverDomain.Hash) (io.ReadCloser, error) {
	if s.loadChunkErr != nil {
		return nil, s.loadChunkErr
	}
	data, ok := s.storedChunks[hash]
	if !ok {
		return nil, errors.New("chunk not found in cache")
	}
	return io.NopCloser(bytes.NewReader(data)), nil
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

func TestRepo_Clone_PinsHeadCommit(t *testing.T) {
	token := signTestToken(t, "secret")
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: token}}
	auth := NewAuth(nil, storage, nil)
	local := &stubLocalRepo{}
	head := snow.ID(7)
	repo := NewRepo(auth, &stubRepoInterface{
		branch:   &serverDomain.Branch{Name: "main", CommitID: &head},
		manifest: &serverDomain.TreeNode{},
	}, local)

	err := repo.Clone(context.Background(), "http://example.com/org/project", "example.com", "org", "project", "main", "", t.TempDir())
	require.NoError(t, err)
	require.Equal(t, "main", local.config.Branch)
	require.Equal(t, head.Base36(), local.savedCommitID, "clone must pin the branch head commit locally")
	require.Empty(t, local.savedCommitHash)
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

func TestRepo_CreateBranch_Success(t *testing.T) {
	token := signTestToken(t, "secret")
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: token}}
	auth := NewAuth(nil, storage, nil)
	local := &stubLocalRepo{loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"}}
	stub := &stubRepoInterface{createdBranch: &serverDomain.Branch{Name: "feature"}}
	repo := NewRepo(auth, stub, local)
	root := t.TempDir()

	created, err := repo.CreateBranch(context.Background(), root, "example.com", "org", "project", "feature")
	require.NoError(t, err)
	require.Equal(t, "feature", created.Name)
	require.Equal(t, "feature", stub.created)
	require.Equal(t, "main", stub.createdFrom, "must fork from the current local branch")
	require.Empty(t, stub.createdFromID, "no pinned commit on record yet")
	require.Empty(t, stub.createdFromHash, "no pinned commit on record yet")
	require.Equal(t, root, local.initTarget)
	require.Equal(t, domain.Config{Url: "http://example.com/org/project", Branch: "feature"}, local.config)
}

func TestRepo_CreateBranch_FromPinnedCommit(t *testing.T) {
	token := signTestToken(t, "secret")
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: token}}
	auth := NewAuth(nil, storage, nil)
	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		loadCommit: &domain.LocalCommit{CommitID: "abc123", CommitHash: "beef"},
	}
	stub := &stubRepoInterface{createdBranch: &serverDomain.Branch{Name: "feature"}}
	repo := NewRepo(auth, stub, local)

	created, err := repo.CreateBranch(context.Background(), t.TempDir(), "example.com", "org", "project", "feature")
	require.NoError(t, err)
	require.Equal(t, "feature", created.Name)
	require.Equal(t, "main", stub.createdFrom, "branch name must still be sent as the fallback source")
	require.Equal(t, "abc123", stub.createdFromID, "the pinned commit id must accompany the branch name")
	require.Equal(t, "beef", stub.createdFromHash, "the pinned commit hash must accompany the branch name")
}

func TestRepo_CreateBranch_LoadCommitFailed(t *testing.T) {
	wantErr := errors.New("meta unreadable")
	token := signTestToken(t, "secret")
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: token}}
	auth := NewAuth(nil, storage, nil)
	stub := &stubRepoInterface{}
	repo := NewRepo(auth, stub, &stubLocalRepo{
		loadConfig:    &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		loadCommitErr: wantErr,
	})

	_, err := repo.CreateBranch(context.Background(), t.TempDir(), "example.com", "org", "project", "feature")
	require.ErrorIs(t, err, wantErr)
	require.Empty(t, stub.created, "create must not run without knowing the fork source")
}

func TestRepo_CreateBranch_EmptyName(t *testing.T) {
	token := signTestToken(t, "secret")
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: token}}
	auth := NewAuth(nil, storage, nil)
	repo := NewRepo(auth, &stubRepoInterface{}, &stubLocalRepo{})

	_, err := repo.CreateBranch(context.Background(), t.TempDir(), "example.com", "org", "project", "  ")
	require.Error(t, err)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 400, domErr.Code)
	require.Equal(t, "branch name is required", domErr.Message)
}

func TestRepo_CreateBranch_LoginFailed(t *testing.T) {
	wantErr := errors.New("login failed")
	storage := &stubSecureStorage{loadErr: errors.New("not found")}
	input := &stubUserInput{username: "apin", password: "secret"}
	executor := &stubLoginExecutor{usernameErr: wantErr}
	auth := NewAuth(executor, storage, input)
	repo := NewRepo(auth, &stubRepoInterface{}, &stubLocalRepo{})

	_, err := repo.CreateBranch(context.Background(), t.TempDir(), "example.com", "org", "project", "feature")
	require.ErrorIs(t, err, wantErr)
}

func TestRepo_CreateBranch_InitFailed(t *testing.T) {
	wantErr := errors.New("init failed")
	token := signTestToken(t, "secret")
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: token}}
	auth := NewAuth(nil, storage, nil)
	repo := NewRepo(auth, &stubRepoInterface{}, &stubLocalRepo{initErr: wantErr})

	_, err := repo.CreateBranch(context.Background(), t.TempDir(), "example.com", "org", "project", "feature")
	require.ErrorIs(t, err, wantErr)
}

func TestRepo_CreateBranch_LoadConfigFailed(t *testing.T) {
	wantErr := errors.New("config missing")
	token := signTestToken(t, "secret")
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: token}}
	auth := NewAuth(nil, storage, nil)
	stub := &stubRepoInterface{}
	repo := NewRepo(auth, stub, &stubLocalRepo{configLoadErr: wantErr})

	_, err := repo.CreateBranch(context.Background(), t.TempDir(), "example.com", "org", "project", "feature")
	require.ErrorIs(t, err, wantErr)
	require.Empty(t, stub.created, "create must not be called when config can't be loaded")
}

func TestRepo_CreateBranch_ServerError(t *testing.T) {
	wantErr := domain.NewUserError(`branch "denied" already exists`)
	token := signTestToken(t, "secret")
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: token}}
	auth := NewAuth(nil, storage, nil)
	local := &stubLocalRepo{loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"}}
	repo := NewRepo(auth, &stubRepoInterface{createErr: wantErr}, local)

	_, err := repo.CreateBranch(context.Background(), t.TempDir(), "example.com", "org", "project", "denied")
	require.ErrorIs(t, err, wantErr)
	require.Empty(t, local.config, "config must not be switched when creation fails on the server")
}

func TestRepo_CreateBranch_SaveConfigFailed(t *testing.T) {
	wantErr := errors.New("save config failed")
	token := signTestToken(t, "secret")
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: token}}
	auth := NewAuth(nil, storage, nil)
	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		configErr:  wantErr,
	}
	stub := &stubRepoInterface{createdBranch: &serverDomain.Branch{Name: "feature"}}
	repo := NewRepo(auth, stub, local)

	_, err := repo.CreateBranch(context.Background(), t.TempDir(), "example.com", "org", "project", "feature")
	require.ErrorIs(t, err, wantErr)
	require.Equal(t, "feature", stub.created, "server creation must have happened before the local switch")
}

func commitLogStub(entries []*serverDomain.CommitLogEntry) *stubRepoInterface {
	return &stubRepoInterface{
		commitLogFn: func(_ context.Context, _, _, _ string, _ *snow.ID, _ int) ([]*serverDomain.CommitLogEntry, error) {
			return entries, nil
		},
	}
}

func TestRepo_Log_LoginFailed(t *testing.T) {
	wantErr := errors.New("login failed")
	storage := &stubSecureStorage{loadErr: errors.New("not found")}
	input := &stubUserInput{username: "apin", password: "secret"}
	executor := &stubLoginExecutor{usernameErr: wantErr}
	auth := NewAuth(executor, storage, input)
	repo := NewRepo(auth, &stubRepoInterface{}, &stubLocalRepo{})

	_, err := repo.Log(context.Background(), "example.com", "org", "project", "main")
	require.ErrorIs(t, err, wantErr)
}

func TestRepo_Log_SinglePage(t *testing.T) {
	token := signTestToken(t, "secret")
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: token}}
	auth := NewAuth(nil, storage, nil)
	entries := []*serverDomain.CommitLogEntry{
		{Commit: serverDomain.Commit{ID: 1, Message: "one"}},
		{Commit: serverDomain.Commit{ID: 2, Message: "two"}},
	}
	repo := NewRepo(auth, commitLogStub(entries), &stubLocalRepo{})

	got, err := repo.Log(context.Background(), "example.com", "org", "project", "main")
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, snow.ID(1), got[0].ID)
	require.Equal(t, "two", got[1].Message)
}

func TestRepo_Log_Paginates(t *testing.T) {
	token := signTestToken(t, "secret")
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: token}}
	auth := NewAuth(nil, storage, nil)
	var pages [][]*serverDomain.CommitLogEntry
	for i := 0; i < 3; i++ {
		page := make([]*serverDomain.CommitLogEntry, 0, commitLogPageSize)
		for j := 0; j < commitLogPageSize; j++ {
			page = append(page, &serverDomain.CommitLogEntry{Commit: serverDomain.Commit{ID: snow.ID(i*commitLogPageSize + j + 1)}})
		}
		pages = append(pages, page)
	}
	stub := &stubRepoInterface{
		commitLogFn: func(_ context.Context, _, _, _ string, startCommitID *snow.ID, limit int) ([]*serverDomain.CommitLogEntry, error) {
			require.Equal(t, commitLogPageSize, limit)
			if startCommitID == nil {
				return pages[0], nil
			}
			switch *startCommitID {
			case snow.ID(commitLogPageSize):
				return pages[1], nil
			case snow.ID(2 * commitLogPageSize):
				return pages[2], nil
			case snow.ID(3 * commitLogPageSize):
				return []*serverDomain.CommitLogEntry{{Commit: serverDomain.Commit{ID: snow.ID(3*commitLogPageSize + 1)}}}, nil
			}
			t.Fatalf("unexpected cursor %v", *startCommitID)
			return nil, nil
		},
	}
	repo := NewRepo(auth, stub, &stubLocalRepo{})

	got, err := repo.Log(context.Background(), "example.com", "org", "project", "main")
	require.NoError(t, err)
	require.Len(t, got, 3*commitLogPageSize+1)
}

func TestRepo_Log_StopsAtShortPage(t *testing.T) {
	token := signTestToken(t, "secret")
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: token}}
	auth := NewAuth(nil, storage, nil)
	full := make([]*serverDomain.CommitLogEntry, 0, commitLogPageSize)
	for j := 0; j < commitLogPageSize; j++ {
		full = append(full, &serverDomain.CommitLogEntry{Commit: serverDomain.Commit{ID: snow.ID(j + 1)}})
	}
	calls := 0
	stub := &stubRepoInterface{
		commitLogFn: func(_ context.Context, _, _, _ string, _ *snow.ID, _ int) ([]*serverDomain.CommitLogEntry, error) {
			calls++
			if calls == 1 {
				return full, nil
			}
			return []*serverDomain.CommitLogEntry{{Commit: serverDomain.Commit{ID: snow.ID(501)}}}, nil
		},
	}
	repo := NewRepo(auth, stub, &stubLocalRepo{})

	got, err := repo.Log(context.Background(), "example.com", "org", "project", "main")
	require.NoError(t, err)
	require.Len(t, got, commitLogPageSize+1)
	require.Equal(t, 2, calls)
}

func TestRepo_Log_ServerError(t *testing.T) {
	wantErr := errors.New("log failed")
	token := signTestToken(t, "secret")
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: token}}
	auth := NewAuth(nil, storage, nil)
	stub := &stubRepoInterface{
		commitLogFn: func(_ context.Context, _, _, _ string, _ *snow.ID, _ int) ([]*serverDomain.CommitLogEntry, error) {
			return nil, wantErr
		},
	}
	repo := NewRepo(auth, stub, &stubLocalRepo{})

	_, err := repo.Log(context.Background(), "example.com", "org", "project", "main")
	require.ErrorIs(t, err, wantErr)
}

func TestRepo_Log_WithMaxCap(t *testing.T) {
	token := signTestToken(t, "secret")
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: token}}
	auth := NewAuth(nil, storage, nil)
	page := make([]*serverDomain.CommitLogEntry, 0, commitLogPageSize)
	for j := 0; j < commitLogPageSize; j++ {
		page = append(page, &serverDomain.CommitLogEntry{Commit: serverDomain.Commit{ID: snow.ID(j + 1)}})
	}
	stub := &stubRepoInterface{
		commitLogFn: func(_ context.Context, _, _, _ string, _ *snow.ID, _ int) ([]*serverDomain.CommitLogEntry, error) {
			return page, nil
		},
	}
	repo := NewRepo(auth, stub, &stubLocalRepo{})

	got, err := repo.Log(context.Background(), "example.com", "org", "project", "main", WithCommitLogMax(10))
	require.NoError(t, err)
	require.Len(t, got, 10)
}
