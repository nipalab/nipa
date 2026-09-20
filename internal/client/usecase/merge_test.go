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
	"github.com/nipalab/nipa/internal/client/merge"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type stubMergeClient struct {
	connectHost string
	connectErr  error

	treeByBranch map[string]*serverDomain.TreeNode
	treeErr      error
	treeErrOn    int
	manifestFor  []string

	baseInfo *domain.MergeBaseInfo
	baseErr  error
	lastBase *struct{ target, source string }

	ffBranch *serverDomain.Branch
	ffErr    error
	lastFF   *struct{ target, source string }

	download    map[serverDomain.Hash][]byte
	downloadErr error
	downloaded  []serverDomain.Hash

	pushOrg, pushProject, pushBranch, pushBaseTreeHash, pushMessage string
	pushFiles                                                       []*serverDomain.PushFile
	pushRemoved                                                     []string
	pushParent2Hash                                                 string
	pushResult                                                      *serverDomain.PushResult
	pushErr                                                         error
	uploadedChunks                                                  []*serverDomain.ChunkData
	uploadErr                                                       error
	pushCalled                                                      bool
}

func (s *stubMergeClient) Connect(_ context.Context, host string) error {
	s.connectHost = host
	return s.connectErr
}

func (s *stubMergeClient) GetBranchByName(_ context.Context, _, _, name string) (*serverDomain.Branch, error) {
	return &serverDomain.Branch{Name: name}, nil
}

func (s *stubMergeClient) GetTreeNodeManifest(_ context.Context, _, _, branch string, _ []string) (*serverDomain.TreeNode, error) {
	s.manifestFor = append(s.manifestFor, branch)
	if (s.treeErrOn > 0 && len(s.manifestFor) == s.treeErrOn) || (s.treeErrOn == 0 && s.treeErr != nil) {
		return nil, s.treeErr
	}
	if s.treeByBranch != nil {
		if t, ok := s.treeByBranch[branch]; ok {
			return t, nil
		}
	}
	return &serverDomain.TreeNode{}, nil
}

func (s *stubMergeClient) DownloadChunks(_ context.Context, _ domain.ChunkScope, hashes []serverDomain.Hash, onChunk func(h serverDomain.Hash, data []byte) error) error {
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

func (s *stubMergeClient) GetMergeBase(_ context.Context, _, _ string, target, source domain.MergeRef) (*domain.MergeBaseInfo, error) {
	if s.lastBase == nil {
		s.lastBase = &struct{ target, source string }{}
	}
	s.lastBase.target, s.lastBase.source = target.Branch, source.Branch
	return s.baseInfo, s.baseErr
}

func (s *stubMergeClient) MergeFastForward(_ context.Context, _, _, target, source string) (*serverDomain.Branch, error) {
	if s.lastFF == nil {
		s.lastFF = &struct{ target, source string }{}
	}
	s.lastFF.target, s.lastFF.source = target, source
	return s.ffBranch, s.ffErr
}

func (s *stubMergeClient) Push(_ context.Context, org, project, branch, baseTreeHash, message string, files []*serverDomain.PushFile, removed []string, parent2CommitHash, baseCommitID string) (*serverDomain.PushResult, error) {
	s.pushCalled = true
	s.pushOrg, s.pushProject, s.pushBranch = org, project, branch
	s.pushBaseTreeHash, s.pushMessage = baseTreeHash, message
	s.pushFiles, s.pushRemoved = files, removed
	s.pushParent2Hash = parent2CommitHash
	if s.pushResult == nil {
		return &serverDomain.PushResult{}, s.pushErr
	}
	return s.pushResult, s.pushErr
}

func (s *stubMergeClient) UploadChunks(_ context.Context, _ domain.ChunkScope, chunks []*serverDomain.ChunkData, _ ...func(ch *serverDomain.ChunkData)) (int, int, error) {
	s.uploadedChunks = chunks
	return 0, 0, s.uploadErr
}

// cacheContent chunks content and seeds the stub local cache and download map
// with it, returning the file hash and chunk list for manifest building.
func cacheContent(t *testing.T, local *stubLocalRepo, client *stubMergeClient, content string) (serverDomain.Hash, []serverDomain.Chunk) {
	t.Helper()
	chunks, err := chunker.ChunkAll([]byte(content))
	require.NoError(t, err)
	hashes := make([]serverDomain.Hash, len(chunks))
	chunkList := make([]serverDomain.Chunk, len(chunks))
	if local.storedChunks == nil {
		local.storedChunks = make(map[serverDomain.Hash][]byte)
	}
	if client.download == nil {
		client.download = make(map[serverDomain.Hash][]byte)
	}
	for i, c := range chunks {
		hashes[i] = c.Hash
		chunkList[i] = serverDomain.Chunk{Hash: c.Hash, SizeBytes: int64(len(c.Data))}
		local.storedChunks[c.Hash] = c.Data
		client.download[c.Hash] = c.Data
	}
	return chunker.FileHash(hashes), chunkList
}

func treeWithFiles(entries map[string]serverDomain.File) *serverDomain.TreeNode {
	tree := &serverDomain.TreeNode{Name: "root"}
	for path, file := range entries {
		name := path
		for i := len(path) - 1; i >= 0; i-- {
			if path[i] == '/' {
				name = path[i+1:]
				break
			}
		}
		copied := file
		copied.Name = name
		tree.FileChildren = append(tree.FileChildren, &copied)
	}
	return tree
}

func fileOf(path string, hash serverDomain.Hash, chunks []serverDomain.Chunk) serverDomain.File {
	var size int64
	for _, c := range chunks {
		size += c.SizeBytes
	}
	return serverDomain.File{Name: path, Mode: 2, Hash: hash, SizeBytes: size, Chunks: chunks}
}

func newTestMerge(t *testing.T, client *stubMergeClient, local *stubLocalRepo, push *Push, auth ...*Auth) *Merge {
	t.Helper()
	var a *Auth
	if len(auth) > 0 && auth[0] != nil {
		a = auth[0]
	} else {
		token := signTestToken(t, "secret")
		storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: token}}
		a = NewAuth(nil, storage, nil)
	}
	if push == nil {
		push = NewPush(a, client, local)
	}
	return NewMerge(a, client, local, push)
}

func TestMerge_Run_Error_FFOnlyAndNoFF(t *testing.T) {
	local := &stubLocalRepo{loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"}}
	mergeUse := newTestMerge(t, &stubMergeClient{}, local, nil)

	_, err := mergeUse.Run(context.Background(), t.TempDir(), "feature", MergeOptions{FFOnly: true, NoFF: true})
	require.Error(t, err)
	require.Contains(t, err.Error(), "mutually exclusive")
}

func TestMerge_Run_Error_EmptySource(t *testing.T) {
	local := &stubLocalRepo{loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"}}
	mergeUse := newTestMerge(t, &stubMergeClient{}, local, nil)

	_, err := mergeUse.Run(context.Background(), t.TempDir(), "  ", MergeOptions{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "source branch is required")
}

func TestMerge_Run_Error_MergeInProgress(t *testing.T) {
	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		mergeState: &domain.MergeState{Conflicts: []string{"a.txt"}},
	}
	mergeUse := newTestMerge(t, &stubMergeClient{}, local, nil)

	_, err := mergeUse.Run(context.Background(), t.TempDir(), "feature", MergeOptions{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "already in progress")
}

func TestMerge_Run_Error_StagedChanges(t *testing.T) {
	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		staged:     []string{"a.txt"},
	}
	mergeUse := newTestMerge(t, &stubMergeClient{}, local, nil)

	_, err := mergeUse.Run(context.Background(), t.TempDir(), "feature", MergeOptions{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "staged changes")
}

func TestMerge_Run_Error_SourceNoCommits(t *testing.T) {
	local := &stubLocalRepo{loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"}}
	client := &stubMergeClient{baseInfo: &domain.MergeBaseInfo{SourceCommitID: ""}}
	mergeUse := newTestMerge(t, client, local, nil)

	_, err := mergeUse.Run(context.Background(), t.TempDir(), "feature", MergeOptions{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no commits")
}

func TestMerge_Run_UpToDate(t *testing.T) {
	local := &stubLocalRepo{loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"}}
	client := &stubMergeClient{baseInfo: &domain.MergeBaseInfo{
		SourceCommitID:    "F1",
		MergeBaseCommitID: "F1", // base == source head → already up to date
	}}
	mergeUse := newTestMerge(t, client, local, nil)

	outcome, err := mergeUse.Run(context.Background(), t.TempDir(), "feature", MergeOptions{})
	require.NoError(t, err)
	require.True(t, outcome.UpToDate)
	require.False(t, client.pushCalled)
}

func TestMerge_Run_FastForward(t *testing.T) {
	id := snow.ID(42)
	ffHash := serverDomain.Hash{0xaa}
	local := &stubLocalRepo{loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"}}
	client := &stubMergeClient{
		baseInfo: &domain.MergeBaseInfo{
			TargetCommitID:    "T1",
			SourceCommitID:    "F1",
			SourceCommitHash:  ffHash.String(),
			MergeBaseCommitID: "T1", // base == target head → fast-forward possible
		},
		treeByBranch: map[string]*serverDomain.TreeNode{
			"main": treeWithFiles(map[string]serverDomain.File{"a.txt": fileOf("a.txt", ffHash, nil)}),
		},
		ffBranch: &serverDomain.Branch{Name: "main", CommitID: &id},
	}
	mergeUse := newTestMerge(t, client, local, nil)

	outcome, err := mergeUse.Run(context.Background(), t.TempDir(), "feature", MergeOptions{})
	require.NoError(t, err)
	require.True(t, outcome.FastForwarded)
	require.Equal(t, "feature", client.lastFF.source)
	require.Equal(t, "main", client.lastFF.target)
	require.Equal(t, id.Base36(), local.savedCommitID)
	require.Equal(t, ffHash.String(), local.savedCommitHash)
	require.NotNil(t, local.tree, "local snapshot must be refreshed after the pointer move")
}

func TestMerge_Run_FFOnlyNotPossible(t *testing.T) {
	local := &stubLocalRepo{loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"}}
	client := &stubMergeClient{baseInfo: &domain.MergeBaseInfo{
		TargetCommitID:    "T1",
		SourceCommitID:    "F1",
		MergeBaseCommitID: "B1",
	}}
	mergeUse := newTestMerge(t, client, local, nil)

	_, err := mergeUse.Run(context.Background(), t.TempDir(), "feature", MergeOptions{FFOnly: true})
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot fast-forward")
	require.Nil(t, client.lastFF)
}

// setupThreeWaySeed configures the client/local stubs for a true merge where
// both branches changed a.txt textually. It returns the merged content the test
// expects on disk.
func setupThreeWaySeed(t *testing.T, baseContent, oursContent, theirsContent string) (*stubMergeClient, *stubLocalRepo, []serverDomain.Hash) {
	local := &stubLocalRepo{loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"}}
	client := &stubMergeClient{baseInfo: &domain.MergeBaseInfo{
		TargetBranch:      "main",
		SourceBranch:      "feature",
		TargetCommitID:    "T1",
		SourceCommitID:    "F1",
		SourceCommitHash:  "src-hash",
		MergeBaseCommitID: "B1",
	}}

	baseHash, baseChunks := cacheContent(t, local, client, baseContent)
	oursHash, oursChunks := cacheContent(t, local, client, oursContent)
	theirsHash, theirsChunks := cacheContent(t, local, client, theirsContent)

	client.treeByBranch = map[string]*serverDomain.TreeNode{
		"main":    treeWithFiles(map[string]serverDomain.File{"a.txt": fileOf("a.txt", oursHash, oursChunks)}),
		"feature": treeWithFiles(map[string]serverDomain.File{"a.txt": fileOf("a.txt", theirsHash, theirsChunks)}),
	}
	baseTree := treeWithFiles(map[string]serverDomain.File{"a.txt": fileOf("a.txt", baseHash, baseChunks)})
	baseTree.Hash = baseHash
	client.baseInfo.MergeBaseTree = baseTree

	var want []serverDomain.Hash
	for _, c := range baseChunks {
		want = append(want, c.Hash)
	}
	for _, c := range oursChunks {
		want = append(want, c.Hash)
	}
	for _, c := range theirsChunks {
		want = append(want, c.Hash)
	}
	local.missingChunks = want
	local.snapshot = &domain.Snapshot{}

	return client, local, want
}

func TestMerge_Run_CleanMergeCommit(t *testing.T) {
	client, local, _ := setupThreeWaySeed(t, "a\nb\nc\n", "a\nX\nc\n", "a\nb\nY\n")
	merged := "a\nX\nY\n"
	mergedHash, _ := cacheContent(t, local, client, merged)
	client.pushResult = &serverDomain.PushResult{
		CommitID:   snow.ID(7),
		CommitHash: mergedHash,
		TreeHash:   mergedHash,
	}

	root := t.TempDir()
	mergeUse := newTestMerge(t, client, local, nil)

	outcome, err := mergeUse.Run(context.Background(), root, "feature", MergeOptions{})
	require.NoError(t, err)
	require.True(t, outcome.MergeCommitted)
	require.Empty(t, outcome.Conflicts)

	data, err := os.ReadFile(filepath.Join(root, "a.txt"))
	require.NoError(t, err)
	require.Equal(t, merged, string(data), "clean three-way content must be written to the working copy")

	require.True(t, client.pushCalled, "clean merges push the merge commit immediately")
	require.Equal(t, "src-hash", client.pushParent2Hash, "the merge commit must record the source head as second parent")
	require.Equal(t, "Merge branch 'feature' into 'main'", client.pushMessage)
	require.Equal(t, snow.ID(7).Base36(), local.savedCommitID)
	require.NotNil(t, local.savedMerge, "pending merge state is written before the push for parent2")
	require.Empty(t, local.savedMerge.Conflicts)
}

func TestMerge_Run_ConflictingMerge(t *testing.T) {
	client, local, _ := setupThreeWaySeed(t, "a\nb\nc\n", "a\nX\nc\n", "a\nY\nc\n")
	root := t.TempDir()
	mergeUse := newTestMerge(t, client, local, nil)

	outcome, err := mergeUse.Run(context.Background(), root, "feature", MergeOptions{})
	require.NoError(t, err)
	require.Nil(t, err)
	require.Equal(t, []string{"a.txt"}, outcome.Conflicts)
	require.False(t, client.pushCalled, "conflicting merges must not push")

	data, err := os.ReadFile(filepath.Join(root, "a.txt"))
	require.NoError(t, err)
	require.Contains(t, string(data), "<<<<<<< ours")
	require.Contains(t, string(data), ">>>>>>> theirs")

	require.NotNil(t, local.savedMerge, "conflict state must be persisted")
	require.Equal(t, []string{"a.txt"}, local.savedMerge.Conflicts)
	require.Equal(t, "src-hash", local.savedMerge.SourceCommitHash)
	require.Equal(t, "feature", local.savedMerge.SourceBranch)
	require.NotNil(t, local.tree, "the local snapshot must record the conflicted working copy")
}

func TestMerge_Run_ConflictingMergeKeepsUntouchedFile(t *testing.T) {
	client, local, _ := setupThreeWaySeed(t, "a\nb\nc\n", "a\nX\nc\n", "a\nY\nc\n")
	blobHash, blobChunks := cacheContent(t, local, client, "keep me\n")
	blob := fileOf("b.txt", blobHash, blobChunks)
	copied := blob
	client.treeByBranch["main"].FileChildren = append(client.treeByBranch["main"].FileChildren, &blob)
	client.treeByBranch["feature"].FileChildren = append(client.treeByBranch["feature"].FileChildren, &copied)
	client.baseInfo.MergeBaseTree.FileChildren = append(client.baseInfo.MergeBaseTree.FileChildren, &copied)

	root := t.TempDir()
	writeRepoFile(t, root, "b.txt", "keep me\n")
	local.snapshot = &domain.Snapshot{
		Files: []domain.SnapshotFile{{Path: "b.txt", Hash: blobHash, Mode: 2, SizeBytes: blob.SizeBytes}},
	}

	mergeUse := newTestMerge(t, client, local, nil)
	outcome, err := mergeUse.Run(context.Background(), root, "feature", MergeOptions{})
	require.NoError(t, err)
	require.Equal(t, []string{"a.txt"}, outcome.Conflicts)
	require.Contains(t, merge.Flatten(local.tree), "b.txt", "the conflicted snapshot must keep untouched files")
}

func TestMerge_Run_Error_AbortNoMerge(t *testing.T) {
	local := &stubLocalRepo{loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"}}
	mergeUse := newTestMerge(t, &stubMergeClient{}, local, nil)

	_, err := mergeUse.Run(context.Background(), t.TempDir(), "", MergeOptions{Abort: true})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no merge in progress")
}

func TestMerge_Run_AbortRestoresWorkingCopy(t *testing.T) {
	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		mergeState: &domain.MergeState{SourceBranch: "feature", Conflicts: []string{"a.txt"}},
	}
	client := &stubMergeClient{
		treeByBranch: map[string]*serverDomain.TreeNode{
			"main": treeWithFiles(map[string]serverDomain.File{"a.txt": fileOf("a.txt", serverDomain.Hash{0x01}, nil)}),
		},
	}
	mergeUse := newTestMerge(t, client, local, nil)

	_, err := mergeUse.Run(context.Background(), t.TempDir(), "", MergeOptions{Abort: true})
	require.NoError(t, err)
	require.Equal(t, "main", client.lastAddedBranch(0), "abort re-fetches the target branch tree")
	require.True(t, local.clearedMerge)
	require.True(t, local.clearedStaged)
	require.NotNil(t, local.tree)
	require.Equal(t, "", local.savedCommitID, "abort unpins the local commit")
}

func (s *stubMergeClient) lastAddedBranch(n int) string {
	if n < len(s.manifestFor) {
		return s.manifestFor[n]
	}
	return ""
}

func TestMerge_Error_SubpathClone(t *testing.T) {
	local := &stubLocalRepo{loadConfig: &domain.Config{Url: "http://example.com/org/project/src", Branch: "main"}}
	mergeUse := newTestMerge(t, &stubMergeClient{}, local, nil)

	_, err := mergeUse.Run(context.Background(), t.TempDir(), "feature", MergeOptions{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "subdirectory")
}

func idBase36(id snow.ID) string {
	return id.Base36()
}

func TestMerge_Run_UnrelatedError(t *testing.T) {
	wantErr := errors.New("base lookup failed")
	local := &stubLocalRepo{loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"}}
	client := &stubMergeClient{baseErr: wantErr}
	mergeUse := newTestMerge(t, client, local, nil)

	_, err := mergeUse.Run(context.Background(), t.TempDir(), "feature", MergeOptions{})
	require.ErrorIs(t, err, wantErr)
}
