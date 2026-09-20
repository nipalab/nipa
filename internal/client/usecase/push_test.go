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

type stubPushCall struct {
	baseTreeHash string
	message      string
	parent2      string
	files        []*serverDomain.PushFile
	removed      []string
}

type stubPushClient struct {
	connectHost    string
	connectErr     error
	org            string
	project        string
	branch         string
	baseTreeHash   string
	message        string
	parent2hash    string
	pushFiles      []*serverDomain.PushFile
	pushRemoved    []string
	pushResult     *serverDomain.PushResult
	pushResults    []*serverDomain.PushResult
	pushes         []stubPushCall
	pushErr        error
	uploadedChunks []*serverDomain.ChunkData
	uploadCalls    int
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

func (s *stubPushClient) Push(_ context.Context, org, project, branch, baseTreeHash, message string, files []*serverDomain.PushFile, removed []string, parent2CommitHash string) (*serverDomain.PushResult, error) {
	s.org, s.project, s.branch, s.baseTreeHash, s.message = org, project, branch, baseTreeHash, message
	s.pushFiles, s.pushRemoved = files, removed
	s.parent2hash = parent2CommitHash
	s.pushes = append(s.pushes, stubPushCall{
		baseTreeHash: baseTreeHash,
		message:      message,
		parent2:      parent2CommitHash,
		files:        files,
		removed:      removed,
	})
	if len(s.pushResults) > 0 {
		result := s.pushResults[0]
		s.pushResults = s.pushResults[1:]
		return result, s.pushErr
	}
	if s.pushResult == nil {
		return &serverDomain.PushResult{}, s.pushErr
	}
	return s.pushResult, s.pushErr
}

func (s *stubPushClient) UploadChunks(_ context.Context, chunks []*serverDomain.ChunkData, onChunk ...func(ch *serverDomain.ChunkData)) (int, int, error) {
	s.uploadCalls++
	s.uploadedChunks = append(s.uploadedChunks, chunks...)
	for _, ch := range chunks {
		if len(onChunk) > 0 && onChunk[0] != nil {
			onChunk[0](ch)
		}
	}
	return s.uploaded, s.skipped, s.uploadErr
}

func (s *stubPushClient) GetTreeNodeManifest(_ context.Context, _, _, _ string, _ []string) (*serverDomain.TreeNode, error) {
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
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		snapshot:   &domain.Snapshot{},
		staged:     []string{"a.txt"},
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

	require.Len(t, client.uploadedChunks, 1)
	require.Equal(t, chunks[0].Hash, client.uploadedChunks[0].Hash)
	require.Equal(t, "hello world", string(client.uploadedChunks[0].Data))

	require.Equal(t, "hello world", string(local.storedChunks[chunks[0].Hash]),
		"uploaded chunks must be cached locally so nipa diff can rebuild the old side")

	require.NotNil(t, local.tree, "working copy snapshot must be refreshed from the server after push")
	require.True(t, local.clearedStaged, "staged markers are cleared only after a fully successful push")
	require.Equal(t, snow.ID(1).Base36(), local.savedCommitID, "the pushed commit id must be pinned locally")
	require.Equal(t, fileHash.String(), local.savedCommitHash, "the pushed commit hash must be pinned locally")
}

func TestPush_Run_DeduplicatesNewChunks(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "hello world")
	writeRepoFile(t, root, "b.txt", "hello world")

	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		snapshot:   &domain.Snapshot{},
		staged:     []string{"a.txt", "b.txt"},
	}
	client := &stubPushClient{manifest: &serverDomain.TreeNode{Name: "root"}}
	pusher := newTestPush(t, local, client)

	require.NoError(t, pusher.Run(context.Background(), root, "add two"))

	require.Len(t, client.pushFiles, 2)
	require.Len(t, client.uploadedChunks, 1, "an identical chunk in two files is uploaded only once")
}

func TestPush_Run_UploadsEveryChunkForIdempotentStore(t *testing.T) {
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

	require.Equal(t, chunks[0].Hash, client.pushFiles[0].ChunkHashes[0], "file still lists every chunk hash")
	require.Len(t, client.uploadedChunks, 1, "the client offers every staged chunk; the server dedupes by hash")
}

func TestPush_Run_StreamsChunksInBatches(t *testing.T) {
	oldBatch := uploadBatchBytes
	uploadBatchBytes = 1024
	t.Cleanup(func() { uploadBatchBytes = oldBatch })

	root := t.TempDir()
	content := make([]byte, 1<<20)
	x := uint32(12345)
	for i := range content {
		x = x*1664525 + 1013904223
		content[i] = byte(x >> 24)
	}
	writeRepoFile(t, root, "big.bin", string(content))

	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		snapshot:   &domain.Snapshot{},
		staged:     []string{"big.bin"},
	}
	client := &stubPushClient{manifest: &serverDomain.TreeNode{Name: "root"}}
	pusher := newTestPush(t, local, client)

	require.NoError(t, pusher.Run(context.Background(), root, "add big"))

	require.Greater(t, client.uploadCalls, 1, "large files must not buffer every chunk before uploading")

	hashes := make([]serverDomain.Hash, len(client.uploadedChunks))
	var total int
	seen := make(map[serverDomain.Hash]bool, len(hashes))
	for i, ch := range client.uploadedChunks {
		require.False(t, seen[ch.Hash], "chunk %s uploaded twice", ch.Hash)
		seen[ch.Hash] = true
		hashes[i] = ch.Hash
		total += len(ch.Data)
	}
	require.Equal(t, client.pushFiles[0].ChunkHashes, hashes)
	require.Equal(t, len(content), total)
	require.Equal(t, int64(len(content)), client.pushFiles[0].SizeBytes)
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

func TestPush_Run_ReportsUploadProgress(t *testing.T) {
	root := t.TempDir()
	content := "new upload bytes"
	writeRepoFile(t, root, "a.txt", content)

	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		snapshot:   &domain.Snapshot{},
		staged:     []string{"a.txt"},
	}
	client := &stubPushClient{manifest: &serverDomain.TreeNode{Name: "root"}}
	pusher := newTestPush(t, local, client)
	prog := &stubProgress{}

	require.NoError(t, pusher.Run(context.Background(), root, "add a.txt", prog))

	require.Equal(t, []progressStart{{objects: 1, bytes: int64(len(content))}}, prog.starts)
	require.Equal(t, []progressCount{{objects: 1, bytes: int64(len(content))}}, prog.counts)
	require.True(t, prog.endCalled)
}

func TestPush_Run_Error_UploadFails(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "hello world")
	unexpected := errors.New("upload failed")

	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		snapshot:   &domain.Snapshot{},
		staged:     []string{"a.txt"},
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

func TestPush_Run_UsesRevertStateBaseTree(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "reverted")

	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		snapshot: &domain.Snapshot{
			TreeHash: "marker-tree",
			Files:    []domain.SnapshotFile{{Path: "a.txt", Hash: serverDomain.Hash{0x01}}},
		},
		staged: []string{"a.txt"},
		revertState: &domain.RevertState{
			Targets:          []domain.CommitRef{{ID: "3", Hash: "abc", Subject: "third"}},
			CurrentTreeHash:  "head-tree",
			OriginalTreeHash: "orig-tree",
		},
	}
	client := &stubPushClient{manifest: &serverDomain.TreeNode{Name: "root"}}
	pusher := newTestPush(t, local, client)

	require.NoError(t, pusher.Run(context.Background(), root, `Revert "third"`))

	require.Equal(t, "head-tree", client.baseTreeHash, "a pending revert commits on top of the branch head")
	require.Empty(t, client.parent2hash, "a revert is a single-parent commit")
	require.True(t, local.clearedRevert)
}

func TestPush_Run_Error_RevertSequencePending(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "hello")

	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		snapshot:   &domain.Snapshot{},
		staged:     []string{"a.txt"},
		revertState: &domain.RevertState{
			Targets:         []domain.CommitRef{{ID: "3"}, {ID: "2"}},
			CurrentTreeHash: "head-tree",
		},
	}
	pusher := newTestPush(t, local, &stubPushClient{})

	err := pusher.Run(context.Background(), root, "push")
	require.Error(t, err)
	require.Contains(t, err.Error(), "revert sequence")
	require.False(t, local.clearedStaged)
}

func TestPush_Run_Error_ClearRevertState(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "reverted")
	wantErr := errors.New("clear revert failed")

	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		snapshot: &domain.Snapshot{
			TreeHash: "marker-tree",
			Files:    []domain.SnapshotFile{{Path: "a.txt", Hash: serverDomain.Hash{0x01}}},
		},
		staged: []string{"a.txt"},
		revertState: &domain.RevertState{
			Targets:         []domain.CommitRef{{ID: "3"}},
			CurrentTreeHash: "head-tree",
		},
		clearRevertErr: wantErr,
	}
	client := &stubPushClient{manifest: &serverDomain.TreeNode{Name: "root"}}
	pusher := newTestPush(t, local, client)

	err := pusher.Run(context.Background(), root, "revert")
	require.ErrorIs(t, err, wantErr)
}
