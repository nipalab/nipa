package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type stubPushClient struct {
	connectHost    string
	connectErr     error
	org            string
	project        string
	branch         string
	baseTreeHash   string
	message        string
	pushFiles      []*serverDomain.PushFile
	pushRemoved    []string
	pushResult     *serverDomain.PushResult
	pushErr        error
	uploadedChunks []*serverDomain.ChunkData
	uploaded       int
	skipped        int
	uploadErr      error
	manifest       *serverDomain.TreeNode
	manifestErr    error
}

func (s *stubPushClient) Connect(_ context.Context, host string) error {
	s.connectHost = host
	return s.connectErr
}

func (s *stubPushClient) Push(_ context.Context, org, project, branch, baseTreeHash, message string, files []*serverDomain.PushFile, removed []string) (*serverDomain.PushResult, error) {
	s.org, s.project, s.branch, s.baseTreeHash, s.message = org, project, branch, baseTreeHash, message
	s.pushFiles, s.pushRemoved = files, removed
	return s.pushResult, s.pushErr
}

func (s *stubPushClient) UploadChunks(_ context.Context, chunks []*serverDomain.ChunkData) (int, int, error) {
	s.uploadedChunks = chunks
	return s.uploaded, s.skipped, s.uploadErr
}

func (s *stubPushClient) GetTreeNodeManifest(_ context.Context, _, _, _, _ string) (*serverDomain.TreeNode, error) {
	return s.manifest, s.manifestErr
}

func newTestPush(t *testing.T, local pushLocalRepo, client pushClient) *Push {
	t.Helper()
	token := signTestToken(t, "secret")
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: token}}
	auth := NewAuth(nil, storage, nil)
	return NewPush(auth, client, local)
}

func TestPush_Run_Success(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "hello world")
	fileHash, chunks, err := chunkFile([]byte("hello world"))
	require.NoError(t, err)

	local := &stubLocalRepo{
		loadConfig:    &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		snapshot:      &domain.Snapshot{},
		staged:        []string{"a.txt"},
		missingChunks: []serverDomain.Hash{chunks[0].Hash},
	}
	client := &stubPushClient{
		pushResult: &serverDomain.PushResult{
			CommitID:   snow.ID(1),
			CommitHash: fileHash,
			TreeHash:   fileHash,
		},
		manifest: &serverDomain.TreeNode{Name: "root"},
	}
	pusher := newTestPush(t, local, client)

	require.NoError(t, pusher.Run(context.Background(), root, "add a.txt"))

	require.Equal(t, root, local.initTarget)
	require.Equal(t, "example.com", client.connectHost)
	require.Equal(t, "org", client.org)
	require.Equal(t, "project", client.project)
	require.Equal(t, "main", client.branch)
	require.Equal(t, "", client.baseTreeHash, "empty repo pushes with an empty base tree hash")
	require.Equal(t, "add a.txt", client.message)
	require.Empty(t, client.pushRemoved)

	require.Len(t, client.pushFiles, 1)
	require.Equal(t, "a.txt", client.pushFiles[0].Path)
	require.Equal(t, fileHash, client.pushFiles[0].FileHash)
	require.Equal(t, int64(len("hello world")), client.pushFiles[0].SizeBytes)
	require.Equal(t, chunks[0].Hash, client.pushFiles[0].ChunkHashes[0])

	require.Equal(t, []serverDomain.Hash{chunks[0].Hash}, local.missingChunksInput)
	require.Len(t, client.uploadedChunks, 1)
	require.Equal(t, chunks[0].Hash, client.uploadedChunks[0].Hash)
	require.Equal(t, "hello world", string(client.uploadedChunks[0].Data))

	require.NotNil(t, local.tree, "working copy snapshot must be refreshed from the server after push")
	require.True(t, local.clearedStaged, "staged markers are cleared only after a fully successful push")
}

func TestPush_Run_DeduplicatesNewChunks(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "hello world")
	writeRepoFile(t, root, "b.txt", "hello world")
	_, chunks, err := chunkFile([]byte("hello world"))
	require.NoError(t, err)

	local := &stubLocalRepo{
		loadConfig:    &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		snapshot:      &domain.Snapshot{},
		staged:        []string{"a.txt", "b.txt"},
		missingChunks: []serverDomain.Hash{chunks[0].Hash},
	}
	client := &stubPushClient{manifest: &serverDomain.TreeNode{Name: "root"}}
	pusher := newTestPush(t, local, client)

	require.NoError(t, pusher.Run(context.Background(), root, "add two"))

	require.Len(t, client.pushFiles, 2)
	require.Len(t, client.uploadedChunks, 1, "an identical chunk in two files is uploaded only once")
}

func TestPush_Run_SkipsChunksAlreadyCached(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "hello world")
	_, chunks, err := chunkFile([]byte("hello world"))
	require.NoError(t, err)

	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		snapshot:   &domain.Snapshot{},
		staged:     []string{"a.txt"},
	}
	client := &stubPushClient{manifest: &serverDomain.TreeNode{Name: "root"}}
	pusher := newTestPush(t, local, client)

	require.NoError(t, pusher.Run(context.Background(), root, "add"))

	require.Empty(t, client.uploadedChunks, "chunks already present locally are not re-uploaded")
	require.Equal(t, chunks[0].Hash, client.pushFiles[0].ChunkHashes[0], "file still lists every chunk hash")
}

func TestPush_Run_UsesBaseTreeHash(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "hello world")

	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		snapshot:   &domain.Snapshot{TreeHash: "abc123", Files: []domain.SnapshotFile{{Path: "a.txt", Hash: serverDomain.Hash{0x01}}}},
		staged:     []string{"a.txt"},
	}
	client := &stubPushClient{manifest: &serverDomain.TreeNode{Name: "root"}}
	pusher := newTestPush(t, local, client)

	require.NoError(t, pusher.Run(context.Background(), root, "update"))
	require.Equal(t, "abc123", client.baseTreeHash)
}

func TestPush_Run_SendsRemovedStagedFile(t *testing.T) {
	root := t.TempDir()
	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		snapshot: &domain.Snapshot{
			TreeHash: "abc123",
			Files:    []domain.SnapshotFile{{Path: "gone.txt", Hash: serverDomain.Hash{0x01}}},
		},
		staged: []string{"gone.txt"},
	}
	client := &stubPushClient{manifest: &serverDomain.TreeNode{Name: "root"}}
	pusher := newTestPush(t, local, client)

	require.NoError(t, pusher.Run(context.Background(), root, "remove"))
	require.Equal(t, []string{"gone.txt"}, client.pushRemoved)
	require.Empty(t, client.pushFiles)
}

func TestPush_Run_Error_StagedFileMissing(t *testing.T) {
	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		snapshot:   &domain.Snapshot{},
		staged:     []string{"ghost.txt"},
	}
	pusher := newTestPush(t, local, &stubPushClient{})

	err := pusher.Run(context.Background(), t.TempDir(), "push")
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not exist")
}

func TestPush_Run_Error_EmptyMessage(t *testing.T) {
	local := &stubLocalRepo{staged: []string{"a.txt"}}
	pusher := newTestPush(t, local, &stubPushClient{})

	err := pusher.Run(context.Background(), t.TempDir(), "  ")
	require.Error(t, err)
	require.Contains(t, err.Error(), "message")
}

func TestPush_Run_Error_NothingStaged(t *testing.T) {
	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		snapshot:   &domain.Snapshot{},
	}
	pusher := newTestPush(t, local, &stubPushClient{})

	err := pusher.Run(context.Background(), t.TempDir(), "push")
	require.Error(t, err)
	require.Contains(t, err.Error(), "nothing staged")
}

func TestPush_Run_Error_SubpathClone(t *testing.T) {
	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project/src", Branch: "main"},
		staged:     []string{"a.txt"},
	}
	pusher := newTestPush(t, local, &stubPushClient{})

	err := pusher.Run(context.Background(), t.TempDir(), "push")
	require.Error(t, err)
	require.Contains(t, err.Error(), "subdirectory")
}

func TestPush_Run_Error_UploadFails(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "hello world")
	unexpected := errors.New("upload failed")

	local := &stubLocalRepo{
		loadConfig:    &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		snapshot:      &domain.Snapshot{},
		staged:        []string{"a.txt"},
		missingChunks: []serverDomain.Hash{{0x01}},
	}
	client := &stubPushClient{uploadErr: unexpected}
	pusher := newTestPush(t, local, client)

	err := pusher.Run(context.Background(), root, "push")
	require.ErrorIs(t, err, unexpected)
	require.False(t, local.clearedStaged)
}

func TestPush_Run_Error_PushRejected(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "hello world")
	conflict := domain.NewUserError("branch moved")

	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		snapshot:   &domain.Snapshot{TreeHash: "stale"},
		staged:     []string{"a.txt"},
	}
	client := &stubPushClient{pushErr: conflict}
	pusher := newTestPush(t, local, client)

	err := pusher.Run(context.Background(), root, "push")
	require.ErrorIs(t, err, conflict)
	require.False(t, local.clearedStaged)
}

func TestPush_Run_Error_ManifestRefreshFails(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "hello world")
	unexpected := errors.New("manifest failed")

	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		snapshot:   &domain.Snapshot{},
		staged:     []string{"a.txt"},
	}
	client := &stubPushClient{manifestErr: unexpected}
	pusher := newTestPush(t, local, client)

	err := pusher.Run(context.Background(), root, "push")
	require.ErrorIs(t, err, unexpected)
	require.Nil(t, local.tree)
	require.False(t, local.clearedStaged)
}

func TestPush_Run_Error_SaveTreeFails(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "hello world")
	unexpected := errors.New("save failed")

	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		snapshot:   &domain.Snapshot{},
		staged:     []string{"a.txt"},
		treeErr:    unexpected,
	}
	client := &stubPushClient{manifest: &serverDomain.TreeNode{Name: "root"}}
	pusher := newTestPush(t, local, client)

	err := pusher.Run(context.Background(), root, "push")
	require.ErrorIs(t, err, unexpected)
	require.False(t, local.clearedStaged)
}

func TestPush_Run_Error_LoginRequired(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "hello world")
	wantErr := errors.New("login failed")
	storage := &stubSecureStorage{loadErr: errors.New("not found")}
	input := &stubUserInput{username: "apin", password: "secret"}
	executor := &stubLoginExecutor{usernameErr: wantErr}
	auth := NewAuth(executor, storage, input)

	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		snapshot:   &domain.Snapshot{},
		staged:     []string{"a.txt"},
	}
	pusher := NewPush(auth, &stubPushClient{}, local)

	err := pusher.Run(context.Background(), root, "push")
	require.ErrorIs(t, err, wantErr)
}
