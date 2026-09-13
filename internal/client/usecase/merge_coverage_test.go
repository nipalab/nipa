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
	"github.com/nipalab/nipa/internal/snow"
)

func contentFile(t *testing.T, local *stubLocalRepo, client *stubMergeClient, path, content string) serverDomain.File {
	t.Helper()
	h, chunks := cacheContent(t, local, client, content)
	return fileOf(path, h, chunks)
}

func uncachedFile(path string, hash serverDomain.Hash) serverDomain.File {
	return serverDomain.File{
		Name: path, Mode: 2, Hash: hash, SizeBytes: 1,
		Chunks: []serverDomain.Chunk{{Hash: hash, SizeBytes: 1}},
	}
}

// seedTrueMerge builds a three-way merge seed from content maps. Content for
// every file is cached locally and in the client's download map, and the union
// of all chunk hashes is reported as "missing" so downloads are exercised.
func seedTrueMerge(t *testing.T, base, ours, theirs map[string]string) (*stubMergeClient, *stubLocalRepo) {
	t.Helper()
	local := &stubLocalRepo{loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"}}
	client := &stubMergeClient{baseInfo: &domain.MergeBaseInfo{
		TargetBranch:      "main",
		SourceBranch:      "feature",
		TargetCommitID:    "T1",
		SourceCommitID:    "F1",
		SourceCommitHash:  "src-hash",
		MergeBaseCommitID: "B1",
	}}

	baseFiles := make(map[string]serverDomain.File, len(base))
	oursFiles := make(map[string]serverDomain.File, len(ours))
	theirsFiles := make(map[string]serverDomain.File, len(theirs))
	var want []serverDomain.Hash
	collect := func(files map[string]serverDomain.File, contents map[string]string) {
		for path, content := range contents {
			f := contentFile(t, local, client, path, content)
			files[path] = f
			for _, c := range f.Chunks {
				want = append(want, c.Hash)
			}
		}
	}
	collect(baseFiles, base)
	collect(oursFiles, ours)
	collect(theirsFiles, theirs)

	client.baseInfo.MergeBaseTree = treeWithFiles(baseFiles)
	client.treeByBranch = map[string]*serverDomain.TreeNode{
		"main":    treeWithFiles(oursFiles),
		"feature": treeWithFiles(theirsFiles),
	}
	local.missingChunks = want
	local.snapshot = &domain.Snapshot{}
	return client, local
}

func mergeResult() *serverDomain.PushResult {
	return &serverDomain.PushResult{CommitID: snow.ID(7), CommitHash: serverDomain.Hash{0xaa}, TreeHash: serverDomain.Hash{0xaa}}
}

// --- Run pre-flight error paths ---

func TestMerge_Run_Error_Init(t *testing.T) {
	wantErr := errors.New("init failed")
	local := &stubLocalRepo{initErr: wantErr}
	mergeUse := newTestMerge(t, &stubMergeClient{}, local, nil)

	_, err := mergeUse.Run(context.Background(), t.TempDir(), "feature", MergeOptions{})
	require.ErrorIs(t, err, wantErr)
}

func TestMerge_Run_Error_LoadConfig(t *testing.T) {
	wantErr := errors.New("config load failed")
	local := &stubLocalRepo{configLoadErr: wantErr}
	mergeUse := newTestMerge(t, &stubMergeClient{}, local, nil)

	_, err := mergeUse.Run(context.Background(), t.TempDir(), "feature", MergeOptions{})
	require.ErrorIs(t, err, wantErr)
}

func TestMerge_Run_Error_InvalidUrl(t *testing.T) {
	local := &stubLocalRepo{loadConfig: &domain.Config{Url: "not a url", Branch: "main"}}
	mergeUse := newTestMerge(t, &stubMergeClient{}, local, nil)

	_, err := mergeUse.Run(context.Background(), t.TempDir(), "feature", MergeOptions{})
	require.Error(t, err)
}

func TestMerge_Run_Error_Connect(t *testing.T) {
	wantErr := errors.New("connect failed")
	local := &stubLocalRepo{loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"}}
	client := &stubMergeClient{connectErr: wantErr}
	mergeUse := newTestMerge(t, client, local, nil)

	_, err := mergeUse.Run(context.Background(), t.TempDir(), "feature", MergeOptions{})
	require.ErrorIs(t, err, wantErr)
}

func TestMerge_Run_Error_MakeSureLoggedIn(t *testing.T) {
	wantErr := errors.New("login cancelled")
	storage := &stubSecureStorage{loadErr: errors.New("not found")}
	auth := NewAuth(&stubLoginExecutor{}, storage, &stubUserInput{err: wantErr})
	local := &stubLocalRepo{loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"}}
	mergeUse := newTestMerge(t, &stubMergeClient{}, local, nil, auth)

	_, err := mergeUse.Run(context.Background(), t.TempDir(), "feature", MergeOptions{})
	require.ErrorIs(t, err, wantErr)
}

func TestMerge_Run_Error_LoadMergeState(t *testing.T) {
	wantErr := errors.New("merge state load failed")
	local := &stubLocalRepo{
		loadConfig:    &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		mergeStateErr: wantErr,
	}
	mergeUse := newTestMerge(t, &stubMergeClient{}, local, nil)

	_, err := mergeUse.Run(context.Background(), t.TempDir(), "feature", MergeOptions{})
	require.ErrorIs(t, err, wantErr)
}

func TestMerge_Run_Error_ListStaged(t *testing.T) {
	wantErr := errors.New("staged load failed")
	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		stagedErr:  wantErr,
	}
	mergeUse := newTestMerge(t, &stubMergeClient{}, local, nil)

	_, err := mergeUse.Run(context.Background(), t.TempDir(), "feature", MergeOptions{})
	require.ErrorIs(t, err, wantErr)
}

// --- fast-forward error paths ---

func fastForwardSeed() (*stubMergeClient, *stubLocalRepo) {
	id := snow.ID(42)
	local := &stubLocalRepo{loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"}}
	client := &stubMergeClient{
		baseInfo: &domain.MergeBaseInfo{
			TargetCommitID:    "T1",
			SourceCommitID:    "F1",
			SourceCommitHash:  "src-hash",
			MergeBaseCommitID: "T1",
		},
		treeByBranch: map[string]*serverDomain.TreeNode{
			"main": treeWithFiles(map[string]serverDomain.File{"a.txt": fileOf("a.txt", serverDomain.Hash{0x01}, nil)}),
		},
		ffBranch: &serverDomain.Branch{Name: "main", CommitID: &id},
	}
	return client, local
}

func TestMerge_Run_FF_Error_ServerReject(t *testing.T) {
	wantErr := errors.New("ff rejected")
	client, local := fastForwardSeed()
	client.ffErr = wantErr
	mergeUse := newTestMerge(t, client, local, nil)

	_, err := mergeUse.Run(context.Background(), t.TempDir(), "feature", MergeOptions{})
	require.ErrorIs(t, err, wantErr)
}

func TestMerge_Run_FF_Error_Manifest(t *testing.T) {
	wantErr := errors.New("manifest failed")
	client, local := fastForwardSeed()
	client.treeErr = wantErr
	mergeUse := newTestMerge(t, client, local, nil)

	_, err := mergeUse.Run(context.Background(), t.TempDir(), "feature", MergeOptions{})
	require.ErrorIs(t, err, wantErr)
}

func TestMerge_Run_FF_Error_Sync(t *testing.T) {
	wantErr := errors.New("download failed")
	client, local := fastForwardSeed()
	hash := serverDomain.Hash{0x99}
	client.treeByBranch = map[string]*serverDomain.TreeNode{
		"main": treeWithFiles(map[string]serverDomain.File{"a.txt": fileOf("a.txt", hash, []serverDomain.Chunk{{Hash: hash, SizeBytes: 4}})}),
	}
	local.missingChunks = []serverDomain.Hash{hash}
	client.downloadErr = wantErr
	mergeUse := newTestMerge(t, client, local, nil)

	_, err := mergeUse.Run(context.Background(), t.TempDir(), "feature", MergeOptions{})
	require.ErrorIs(t, err, wantErr)
}

func TestMerge_Run_FF_Error_SaveTree(t *testing.T) {
	wantErr := errors.New("save tree failed")
	client, local := fastForwardSeed()
	local.treeErr = wantErr
	mergeUse := newTestMerge(t, client, local, nil)

	_, err := mergeUse.Run(context.Background(), t.TempDir(), "feature", MergeOptions{})
	require.ErrorIs(t, err, wantErr)
}

func TestMerge_Run_FF_Error_SaveCommit(t *testing.T) {
	wantErr := errors.New("save commit failed")
	client, local := fastForwardSeed()
	local.saveCommitErr = wantErr
	mergeUse := newTestMerge(t, client, local, nil)

	_, err := mergeUse.Run(context.Background(), t.TempDir(), "feature", MergeOptions{})
	require.ErrorIs(t, err, wantErr)
}

func TestMerge_Run_FF_NilBranch(t *testing.T) {
	client, local := fastForwardSeed()
	client.ffBranch = nil
	mergeUse := newTestMerge(t, client, local, nil)

	outcome, err := mergeUse.Run(context.Background(), t.TempDir(), "feature", MergeOptions{})
	require.NoError(t, err)
	require.True(t, outcome.FastForwarded)
	require.Equal(t, "", local.savedCommitID)
	require.Equal(t, "src-hash", local.savedCommitHash)
}

func TestMerge_Run_FF_BranchWithoutCommitID(t *testing.T) {
	client, local := fastForwardSeed()
	client.ffBranch = &serverDomain.Branch{Name: "main"}
	mergeUse := newTestMerge(t, client, local, nil)

	outcome, err := mergeUse.Run(context.Background(), t.TempDir(), "feature", MergeOptions{})
	require.NoError(t, err)
	require.True(t, outcome.FastForwarded)
	require.Equal(t, "", local.savedCommitID)
}

// --- trueMerge decision branches ---

func TestMerge_Run_KeepOurs(t *testing.T) {
	client, local := seedTrueMerge(t,
		map[string]string{"a.txt": "a\nb\n"},
		map[string]string{"a.txt": "a\nb\nc\n"},
		map[string]string{"a.txt": "a\nb\n", "b.txt": "x\n"})
	client.pushResult = mergeResult()
	mergeUse := newTestMerge(t, client, local, nil)

	outcome, err := mergeUse.Run(context.Background(), t.TempDir(), "feature", MergeOptions{})
	require.NoError(t, err)
	require.True(t, outcome.MergeCommitted)
	require.Empty(t, outcome.Conflicts)
	require.True(t, client.pushCalled)
	require.Equal(t, "src-hash", client.pushParent2Hash)
	require.NotNil(t, local.savedMerge)
	require.Empty(t, local.savedMerge.Conflicts)
}

func TestMerge_Run_KeepTheirs_MaterializeError(t *testing.T) {
	hash := serverDomain.Hash{0x77}
	local := &stubLocalRepo{loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"}}
	client := &stubMergeClient{baseInfo: &domain.MergeBaseInfo{
		TargetBranch: "main", SourceBranch: "feature",
		TargetCommitID: "T1", SourceCommitID: "F1", SourceCommitHash: "src-hash", MergeBaseCommitID: "B1",
	}}
	client.baseInfo.MergeBaseTree = treeWithFiles(nil)
	client.treeByBranch = map[string]*serverDomain.TreeNode{
		"main":    treeWithFiles(nil),
		"feature": treeWithFiles(map[string]serverDomain.File{"b.txt": uncachedFile("b.txt", hash)}),
	}
	local.snapshot = &domain.Snapshot{}
	mergeUse := newTestMerge(t, client, local, nil)

	_, err := mergeUse.Run(context.Background(), t.TempDir(), "feature", MergeOptions{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "load chunk")
}

func TestMerge_Run_Error_ManifestTarget(t *testing.T) {
	wantErr := errors.New("target manifest failed")
	client, local := seedTrueMerge(t,
		map[string]string{"a.txt": "a\nb\nc\n"},
		map[string]string{"a.txt": "a\nX\nc\n"},
		map[string]string{"a.txt": "a\nY\nc\n"})
	client.treeErr = wantErr
	mergeUse := newTestMerge(t, client, local, nil)

	_, err := mergeUse.Run(context.Background(), t.TempDir(), "feature", MergeOptions{})
	require.ErrorIs(t, err, wantErr)
}

func TestMerge_Run_Error_ManifestSource(t *testing.T) {
	wantErr := errors.New("source manifest failed")
	client, local := seedTrueMerge(t,
		map[string]string{"a.txt": "a\nb\nc\n"},
		map[string]string{"a.txt": "a\nX\nc\n"},
		map[string]string{"a.txt": "a\nY\nc\n"})
	client.treeErr = wantErr
	client.treeErrOn = 2
	mergeUse := newTestMerge(t, client, local, nil)

	_, err := mergeUse.Run(context.Background(), t.TempDir(), "feature", MergeOptions{})
	require.ErrorIs(t, err, wantErr)
}

func TestMerge_Run_Error_MissingChunks(t *testing.T) {
	wantErr := errors.New("missing chunks query failed")
	client, local := seedTrueMerge(t,
		map[string]string{"a.txt": "a\nb\nc\n"},
		map[string]string{"a.txt": "a\nX\nc\n"},
		map[string]string{"a.txt": "a\nY\nc\n"})
	local.missingChunksErr = wantErr
	mergeUse := newTestMerge(t, client, local, nil)

	_, err := mergeUse.Run(context.Background(), t.TempDir(), "feature", MergeOptions{})
	require.ErrorIs(t, err, wantErr)
}

func TestMerge_Run_Error_Download(t *testing.T) {
	wantErr := errors.New("download failed")
	client, local := seedTrueMerge(t,
		map[string]string{"a.txt": "a\nb\nc\n"},
		map[string]string{"a.txt": "a\nX\nc\n"},
		map[string]string{"a.txt": "a\nY\nc\n"})
	client.downloadErr = wantErr
	mergeUse := newTestMerge(t, client, local, nil)

	_, err := mergeUse.Run(context.Background(), t.TempDir(), "feature", MergeOptions{})
	require.ErrorIs(t, err, wantErr)
}

func TestMerge_Run_TextMerge_ErrorLoadBase(t *testing.T) {
	local := &stubLocalRepo{loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"}}
	client := &stubMergeClient{baseInfo: &domain.MergeBaseInfo{
		TargetBranch: "main", SourceBranch: "feature",
		TargetCommitID: "T1", SourceCommitID: "F1", SourceCommitHash: "src-hash", MergeBaseCommitID: "B1",
	}}
	oursHash, oursChunks := cacheContent(t, local, client, "a\nX\nc\n")
	theirsHash, theirsChunks := cacheContent(t, local, client, "a\nY\nc\n")
	baseHash := serverDomain.Hash{0xde, 0xad}
	client.baseInfo.MergeBaseTree = treeWithFiles(map[string]serverDomain.File{"a.txt": uncachedFile("a.txt", baseHash)})
	client.treeByBranch = map[string]*serverDomain.TreeNode{
		"main":    treeWithFiles(map[string]serverDomain.File{"a.txt": fileOf("a.txt", oursHash, oursChunks)}),
		"feature": treeWithFiles(map[string]serverDomain.File{"a.txt": fileOf("a.txt", theirsHash, theirsChunks)}),
	}
	local.snapshot = &domain.Snapshot{}
	mergeUse := newTestMerge(t, client, local, nil)

	_, err := mergeUse.Run(context.Background(), t.TempDir(), "feature", MergeOptions{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "load chunk")
}

func TestMerge_Run_TextMerge_ErrorLoadOurs(t *testing.T) {
	local := &stubLocalRepo{loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"}}
	client := &stubMergeClient{baseInfo: &domain.MergeBaseInfo{
		TargetBranch: "main", SourceBranch: "feature",
		TargetCommitID: "T1", SourceCommitID: "F1", SourceCommitHash: "src-hash", MergeBaseCommitID: "B1",
	}}
	baseHash, baseChunks := cacheContent(t, local, client, "a\nb\nc\n")
	theirsHash, theirsChunks := cacheContent(t, local, client, "a\nY\nc\n")
	oursHash := serverDomain.Hash{0x01}
	client.baseInfo.MergeBaseTree = treeWithFiles(map[string]serverDomain.File{"a.txt": fileOf("a.txt", baseHash, baseChunks)})
	client.treeByBranch = map[string]*serverDomain.TreeNode{
		"main":    treeWithFiles(map[string]serverDomain.File{"a.txt": uncachedFile("a.txt", oursHash)}),
		"feature": treeWithFiles(map[string]serverDomain.File{"a.txt": fileOf("a.txt", theirsHash, theirsChunks)}),
	}
	local.snapshot = &domain.Snapshot{}
	mergeUse := newTestMerge(t, client, local, nil)

	_, err := mergeUse.Run(context.Background(), t.TempDir(), "feature", MergeOptions{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "load chunk")
}

func TestMerge_Run_TextMerge_ErrorLoadTheirs(t *testing.T) {
	local := &stubLocalRepo{loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"}}
	client := &stubMergeClient{baseInfo: &domain.MergeBaseInfo{
		TargetBranch: "main", SourceBranch: "feature",
		TargetCommitID: "T1", SourceCommitID: "F1", SourceCommitHash: "src-hash", MergeBaseCommitID: "B1",
	}}
	baseHash, baseChunks := cacheContent(t, local, client, "a\nb\nc\n")
	oursHash, oursChunks := cacheContent(t, local, client, "a\nX\nc\n")
	theirsHash := serverDomain.Hash{0x02}
	client.baseInfo.MergeBaseTree = treeWithFiles(map[string]serverDomain.File{"a.txt": fileOf("a.txt", baseHash, baseChunks)})
	client.treeByBranch = map[string]*serverDomain.TreeNode{
		"main":    treeWithFiles(map[string]serverDomain.File{"a.txt": fileOf("a.txt", oursHash, oursChunks)}),
		"feature": treeWithFiles(map[string]serverDomain.File{"a.txt": uncachedFile("a.txt", theirsHash)}),
	}
	local.snapshot = &domain.Snapshot{}
	mergeUse := newTestMerge(t, client, local, nil)

	_, err := mergeUse.Run(context.Background(), t.TempDir(), "feature", MergeOptions{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "load chunk")
}

func TestMerge_Run_Error_StoreMergedFile(t *testing.T) {
	wantErr := errors.New("store chunk failed")
	client, local := seedTrueMerge(t,
		map[string]string{"a.txt": "a\nb\nc\n"},
		map[string]string{"a.txt": "a\nX\nc\n"},
		map[string]string{"a.txt": "a\nY\nc\n"})
	local.storeChunkErr = wantErr
	mergeUse := newTestMerge(t, client, local, nil)

	_, err := mergeUse.Run(context.Background(), t.TempDir(), "feature", MergeOptions{})
	require.ErrorIs(t, err, wantErr)
}

func TestMerge_Run_Error_MaterializeMergedFile(t *testing.T) {
	client, local := seedTrueMerge(t,
		map[string]string{"a.txt": "a\nb\nc\n"},
		map[string]string{"a.txt": "a\nX\nc\n"},
		map[string]string{"a.txt": "a\nY\nc\n"})
	mergeUse := newTestMerge(t, client, local, nil)

	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "a.txt"), 0o755))

	_, err := mergeUse.Run(context.Background(), root, "feature", MergeOptions{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "a.txt")
}

func TestMerge_Run_BinaryConflictNested(t *testing.T) {
	local := &stubLocalRepo{loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"}}
	client := &stubMergeClient{baseInfo: &domain.MergeBaseInfo{
		TargetBranch: "main", SourceBranch: "feature",
		TargetCommitID: "T1", SourceCommitID: "F1", SourceCommitHash: "src-hash", MergeBaseCommitID: "B1",
	}}
	var want []serverDomain.Hash
	bin := func(content string) *serverDomain.File {
		f := contentFile(t, local, client, "assets/logo.bin", content)
		f.IsBinary = true
		f.Name = "logo.bin"
		for _, c := range f.Chunks {
			want = append(want, c.Hash)
		}
		return &f
	}
	baseNode := &serverDomain.TreeNode{Name: "root", Mode: 0o040000, TreeChildren: []*serverDomain.TreeNode{{Name: "assets", Mode: 0o040000, FileChildren: []*serverDomain.File{bin("A")}}}}
	baseCopy := *baseNode
	client.baseInfo.MergeBaseTree = &baseCopy
	client.treeByBranch = map[string]*serverDomain.TreeNode{
		"main":    {Name: "root", Mode: 0o040000, TreeChildren: []*serverDomain.TreeNode{{Name: "assets", Mode: 0o040000, FileChildren: []*serverDomain.File{bin("B")}}}},
		"feature": {Name: "root", Mode: 0o040000, TreeChildren: []*serverDomain.TreeNode{{Name: "assets", Mode: 0o040000, FileChildren: []*serverDomain.File{bin("C")}}}},
	}
	local.missingChunks = want
	local.snapshot = &domain.Snapshot{}
	mergeUse := newTestMerge(t, client, local, nil)

	outcome, err := mergeUse.Run(context.Background(), t.TempDir(), "feature", MergeOptions{})
	require.NoError(t, err)
	require.Equal(t, []string{"assets/logo.bin"}, outcome.Conflicts)
	require.False(t, client.pushCalled)
	require.Equal(t, []string{"assets/logo.bin"}, local.savedMerge.Conflicts)
	require.NotNil(t, local.tree)
	require.Len(t, local.tree.TreeChildren, 1)
	require.Equal(t, "assets", local.tree.TreeChildren[0].Name)
	require.Len(t, local.tree.TreeChildren[0].FileChildren, 1)
}

func TestMerge_Run_AddAddConflict_NilBaseTree(t *testing.T) {
	client, local := seedTrueMerge(t,
		map[string]string{},
		map[string]string{"f.txt": "ours\n"},
		map[string]string{"f.txt": "theirs\n"})
	client.baseInfo.MergeBaseTree = nil
	mergeUse := newTestMerge(t, client, local, nil)

	outcome, err := mergeUse.Run(context.Background(), t.TempDir(), "feature", MergeOptions{})
	require.NoError(t, err)
	require.Equal(t, []string{"f.txt"}, outcome.Conflicts)
	require.False(t, client.pushCalled)
	require.Equal(t, "", local.savedMerge.BaseTreeHash)
}

func TestMerge_Run_ModifyDeleteConflict(t *testing.T) {
	client, local := seedTrueMerge(t,
		map[string]string{"f.txt": "base\n"},
		map[string]string{"f.txt": "ours\n"},
		map[string]string{})
	mergeUse := newTestMerge(t, client, local, nil)

	outcome, err := mergeUse.Run(context.Background(), t.TempDir(), "feature", MergeOptions{})
	require.NoError(t, err)
	require.Equal(t, []string{"f.txt"}, outcome.Conflicts)
	require.False(t, client.pushCalled)
	require.Equal(t, []string{"f.txt"}, local.savedMerge.Conflicts)
}

func TestMerge_Run_DeleteModifyConflict(t *testing.T) {
	client, local := seedTrueMerge(t,
		map[string]string{"f.txt": "base\n"},
		map[string]string{},
		map[string]string{"f.txt": "theirs\n"})
	mergeUse := newTestMerge(t, client, local, nil)

	outcome, err := mergeUse.Run(context.Background(), t.TempDir(), "feature", MergeOptions{})
	require.NoError(t, err)
	require.Equal(t, []string{"f.txt"}, outcome.Conflicts)
	require.False(t, client.pushCalled)
	require.Equal(t, []string{"f.txt"}, local.savedMerge.Conflicts)
	require.NotNil(t, local.tree)
	require.Empty(t, local.tree.FileChildren, "deleted-modify file must not be in the conflicted snapshot")
}

// --- deletion handling ---

func TestMerge_Run_Deleted_RemovesFile(t *testing.T) {
	client, local := seedTrueMerge(t,
		map[string]string{"f.txt": "base\n"},
		map[string]string{},
		map[string]string{})
	baseFile := contentFile(t, local, client, "f.txt", "base\n")
	local.snapshot = &domain.Snapshot{Files: []domain.SnapshotFile{{Path: "f.txt", Hash: baseFile.Hash}}}
	client.pushResult = mergeResult()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "f.txt"), []byte("base\n"), 0o644))

	mergeUse := newTestMerge(t, client, local, nil)
	outcome, err := mergeUse.Run(context.Background(), root, "feature", MergeOptions{})
	require.NoError(t, err)
	require.True(t, outcome.MergeCommitted)
	require.Contains(t, local.stageAdd, "f.txt")
	_, err = os.Stat(filepath.Join(root, "f.txt"))
	require.ErrorIs(t, err, os.ErrNotExist)
	require.True(t, client.pushCalled)
	require.Contains(t, client.pushRemoved, "f.txt")
}

func TestMerge_Run_Deleted_ModifiedStays(t *testing.T) {
	baseFile := contentFile(t, &stubLocalRepo{}, &stubMergeClient{}, "f.txt", "base\n")
	client, local := seedTrueMerge(t,
		map[string]string{"f.txt": "base\n"},
		map[string]string{},
		map[string]string{"b.txt": "theirs\n"})
	local.snapshot = &domain.Snapshot{Files: []domain.SnapshotFile{{Path: "f.txt", Hash: baseFile.Hash}}}
	client.pushResult = mergeResult()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "f.txt"), []byte("user change\n"), 0o644))

	mergeUse := newTestMerge(t, client, local, nil)
	outcome, err := mergeUse.Run(context.Background(), root, "feature", MergeOptions{})
	require.NoError(t, err)
	require.True(t, outcome.MergeCommitted)
	require.NotContains(t, local.stageAdd, "f.txt", "locally-modified deleted file must not be staged")
	data, err := os.ReadFile(filepath.Join(root, "f.txt"))
	require.NoError(t, err)
	require.Equal(t, "user change\n", string(data))
}

func TestMerge_Run_Error_RemoveGuarded(t *testing.T) {
	client, local := seedTrueMerge(t,
		map[string]string{"f.txt": "base\n"},
		map[string]string{},
		map[string]string{})
	baseFile := contentFile(t, local, client, "f.txt", "base\n")
	local.snapshot = &domain.Snapshot{Files: []domain.SnapshotFile{{Path: "f.txt", Hash: baseFile.Hash}}}
	mergeUse := newTestMerge(t, client, local, nil)

	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "f.txt"), 0o755))

	_, err := mergeUse.Run(context.Background(), root, "feature", MergeOptions{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "remove")
}

func TestMerge_Run_Error_StageAdd(t *testing.T) {
	wantErr := errors.New("stage failed")
	client, local := seedTrueMerge(t,
		map[string]string{},
		map[string]string{},
		map[string]string{"b.txt": "theirs\n"})
	local.stageAddErr = wantErr
	mergeUse := newTestMerge(t, client, local, nil)

	root := t.TempDir()
	_, err := mergeUse.Run(context.Background(), root, "feature", MergeOptions{})
	require.ErrorIs(t, err, wantErr)
}

// --- clean-path save/push errors ---

func TestMerge_Run_Error_SaveTreeConflict(t *testing.T) {
	wantErr := errors.New("save tree failed")
	client, local := seedTrueMerge(t,
		map[string]string{"a.txt": "a\nb\nc\n"},
		map[string]string{"a.txt": "a\nX\nc\n"},
		map[string]string{"a.txt": "a\nY\nc\n"})
	local.treeErr = wantErr
	mergeUse := newTestMerge(t, client, local, nil)

	_, err := mergeUse.Run(context.Background(), t.TempDir(), "feature", MergeOptions{})
	require.ErrorIs(t, err, wantErr)
}

func TestMerge_Run_Error_SaveMergeStateConflict(t *testing.T) {
	wantErr := errors.New("save merge state failed")
	client, local := seedTrueMerge(t,
		map[string]string{"a.txt": "a\nb\nc\n"},
		map[string]string{"a.txt": "a\nX\nc\n"},
		map[string]string{"a.txt": "a\nY\nc\n"})
	local.saveMergeErr = wantErr
	mergeUse := newTestMerge(t, client, local, nil)

	_, err := mergeUse.Run(context.Background(), t.TempDir(), "feature", MergeOptions{})
	require.ErrorIs(t, err, wantErr)
}

func TestMerge_Run_Error_SaveMergeStateClean(t *testing.T) {
	wantErr := errors.New("save merge state failed")
	client, local := seedTrueMerge(t,
		map[string]string{"a.txt": "a\nb\nc\n"},
		map[string]string{"a.txt": "a\nX\nc\n"},
		map[string]string{"a.txt": "a\nb\nY\n"})
	client.pushResult = mergeResult()
	local.saveMergeErr = wantErr
	mergeUse := newTestMerge(t, client, local, nil)

	_, err := mergeUse.Run(context.Background(), t.TempDir(), "feature", MergeOptions{})
	require.ErrorIs(t, err, wantErr)
}

func TestMerge_Run_Error_Push(t *testing.T) {
	wantErr := errors.New("push failed")
	client, local := seedTrueMerge(t,
		map[string]string{"a.txt": "a\nb\nc\n"},
		map[string]string{"a.txt": "a\nX\nc\n"},
		map[string]string{"a.txt": "a\nb\nY\n"})
	client.pushErr = wantErr
	mergeUse := newTestMerge(t, client, local, nil)

	_, err := mergeUse.Run(context.Background(), t.TempDir(), "feature", MergeOptions{})
	require.ErrorIs(t, err, wantErr)
}

// --- abort error paths ---

func TestMerge_Run_Abort_Error_LoadMergeState(t *testing.T) {
	wantErr := errors.New("load state failed")
	local := &stubLocalRepo{
		loadConfig:    &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		mergeStateErr: wantErr,
	}
	mergeUse := newTestMerge(t, &stubMergeClient{}, local, nil)

	_, err := mergeUse.Run(context.Background(), t.TempDir(), "", MergeOptions{Abort: true})
	require.ErrorIs(t, err, wantErr)
}

func TestMerge_Run_Abort_Error_Connect(t *testing.T) {
	wantErr := errors.New("connect failed")
	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		mergeState: &domain.MergeState{SourceBranch: "feature", Conflicts: []string{"a.txt"}},
	}
	client := &stubMergeClient{connectErr: wantErr}
	mergeUse := newTestMerge(t, client, local, nil)

	_, err := mergeUse.Run(context.Background(), t.TempDir(), "", MergeOptions{Abort: true})
	require.ErrorIs(t, err, wantErr)
}

func TestMerge_Run_Abort_Error_MakeSureLoggedIn(t *testing.T) {
	wantErr := errors.New("login cancelled")
	storage := &stubSecureStorage{loadErr: errors.New("not found")}
	auth := NewAuth(&stubLoginExecutor{}, storage, &stubUserInput{err: wantErr})
	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		mergeState: &domain.MergeState{SourceBranch: "feature", Conflicts: []string{"a.txt"}},
	}
	mergeUse := newTestMerge(t, &stubMergeClient{}, local, nil, auth)

	_, err := mergeUse.Run(context.Background(), t.TempDir(), "", MergeOptions{Abort: true})
	require.ErrorIs(t, err, wantErr)
}

func TestMerge_Run_Abort_Error_Manifest(t *testing.T) {
	wantErr := errors.New("manifest failed")
	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		mergeState: &domain.MergeState{SourceBranch: "feature", Conflicts: []string{"a.txt"}},
	}
	client := &stubMergeClient{treeErr: wantErr}
	mergeUse := newTestMerge(t, client, local, nil)

	_, err := mergeUse.Run(context.Background(), t.TempDir(), "", MergeOptions{Abort: true})
	require.ErrorIs(t, err, wantErr)
}

func TestMerge_Run_Abort_Error_Sync(t *testing.T) {
	wantErr := errors.New("download failed")
	hash := serverDomain.Hash{0x99}
	local := &stubLocalRepo{
		loadConfig:    &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		mergeState:    &domain.MergeState{SourceBranch: "feature", Conflicts: []string{"a.txt"}},
		missingChunks: []serverDomain.Hash{hash},
	}
	client := &stubMergeClient{
		downloadErr: wantErr,
		treeByBranch: map[string]*serverDomain.TreeNode{
			"main": treeWithFiles(map[string]serverDomain.File{"a.txt": fileOf("a.txt", hash, []serverDomain.Chunk{{Hash: hash, SizeBytes: 4}})}),
		},
	}
	mergeUse := newTestMerge(t, client, local, nil)

	_, err := mergeUse.Run(context.Background(), t.TempDir(), "", MergeOptions{Abort: true})
	require.ErrorIs(t, err, wantErr)
}

func TestMerge_Run_Abort_Error_SaveTree(t *testing.T) {
	wantErr := errors.New("save tree failed")
	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		mergeState: &domain.MergeState{SourceBranch: "feature", Conflicts: []string{"a.txt"}},
		treeErr:    wantErr,
	}
	mergeUse := newTestMerge(t, &stubMergeClient{}, local, nil)

	_, err := mergeUse.Run(context.Background(), t.TempDir(), "", MergeOptions{Abort: true})
	require.ErrorIs(t, err, wantErr)
}

func TestMerge_Run_Abort_Error_SaveCommit(t *testing.T) {
	wantErr := errors.New("save commit failed")
	local := &stubLocalRepo{
		loadConfig:    &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		mergeState:    &domain.MergeState{SourceBranch: "feature", Conflicts: []string{"a.txt"}},
		saveCommitErr: wantErr,
	}
	mergeUse := newTestMerge(t, &stubMergeClient{}, local, nil)

	_, err := mergeUse.Run(context.Background(), t.TempDir(), "", MergeOptions{Abort: true})
	require.ErrorIs(t, err, wantErr)
}

func TestMerge_Run_Abort_Error_ClearStaged(t *testing.T) {
	wantErr := errors.New("clear staged failed")
	local := &stubLocalRepo{
		loadConfig:     &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		mergeState:     &domain.MergeState{SourceBranch: "feature", Conflicts: []string{"a.txt"}},
		clearStagedErr: wantErr,
	}
	mergeUse := newTestMerge(t, &stubMergeClient{}, local, nil)

	_, err := mergeUse.Run(context.Background(), t.TempDir(), "", MergeOptions{Abort: true})
	require.ErrorIs(t, err, wantErr)
}

func TestMerge_Run_Abort_Error_ClearMerge(t *testing.T) {
	wantErr := errors.New("clear merge failed")
	local := &stubLocalRepo{
		loadConfig:    &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		mergeState:    &domain.MergeState{SourceBranch: "feature", Conflicts: []string{"a.txt"}},
		clearMergeErr: wantErr,
	}
	mergeUse := newTestMerge(t, &stubMergeClient{}, local, nil)

	_, err := mergeUse.Run(context.Background(), t.TempDir(), "", MergeOptions{Abort: true})
	require.ErrorIs(t, err, wantErr)
}
