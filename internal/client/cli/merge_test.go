package cli

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/usecase"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type fakeMergeClient struct {
	baseInfo      *domain.MergeBaseInfo
	treeByBranch  map[string]*serverDomain.TreeNode
	ffBranch      *serverDomain.Branch
	download      map[serverDomain.Hash][]byte
	pushResult    *serverDomain.PushResult
	ffErr         error
	mergeBaseErr  error
	treeErr       error
	downloadErr   error
	pushErr       error
	connectCalled bool
	pushCalled    bool
}

func (f *fakeMergeClient) Connect(_ context.Context, _ string) error {
	f.connectCalled = true
	return nil
}

func (f *fakeMergeClient) GetTreeNodeManifest(_ context.Context, _, _, branch, _ string) (*serverDomain.TreeNode, error) {
	if f.treeErr != nil {
		return nil, f.treeErr
	}
	if f.treeByBranch == nil {
		return &serverDomain.TreeNode{Name: "root"}, nil
	}
	if t, ok := f.treeByBranch[branch]; ok {
		return t, nil
	}
	return &serverDomain.TreeNode{Name: "root"}, nil
}

func (f *fakeMergeClient) DownloadChunks(_ context.Context, hashes []serverDomain.Hash, onChunk func(h serverDomain.Hash, data []byte) error) error {
	if f.downloadErr != nil {
		return f.downloadErr
	}
	for _, h := range hashes {
		data, ok := f.download[h]
		if !ok {
			continue
		}
		if err := onChunk(h, data); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeMergeClient) GetMergeBase(_ context.Context, _, _ string, _, _ domain.MergeRef) (*domain.MergeBaseInfo, error) {
	return f.baseInfo, f.mergeBaseErr
}

func (f *fakeMergeClient) MergeFastForward(_ context.Context, _, _, _, _ string) (*serverDomain.Branch, error) {
	return f.ffBranch, f.ffErr
}

func (f *fakeMergeClient) Push(_ context.Context, _, _, _, _, _ string, _ []*serverDomain.PushFile, _ []string, _ string) (*serverDomain.PushResult, error) {
	f.pushCalled = true
	if f.pushResult != nil {
		return f.pushResult, f.pushErr
	}
	return &serverDomain.PushResult{}, f.pushErr
}

func (f *fakeMergeClient) UploadChunks(_ context.Context, _ []*serverDomain.ChunkData, _ ...func(ch *serverDomain.ChunkData)) (int, int, error) {
	return 0, 0, nil
}

func newMergeCli(t *testing.T, client *fakeMergeClient) *Cli {
	t.Helper()
	auth := usecase.NewAuth(fakeExecutor{}, &fakeStorage{token: signTestJWT(t)}, &fakeInput{})
	lr := localrepo.NewLocalRepo()
	push := usecase.NewPush(auth, client, lr)
	mergeUse := usecase.NewMerge(auth, client, lr, push)
	return NewCli(&fakeUsecaseContainer{merge: mergeUse}, &fakeConnector{})
}

func TestSetupMergeCmd_MissingBranch(t *testing.T) {
	root := setupRepo(t, "main")
	cli := newMergeCli(t, &fakeMergeClient{})

	_, err := runCmdInDir(t, root, cli.setupMergeCmd())
	require.Error(t, err)
	require.Equal(t, "a source branch to merge is required (or use --abort)", err.Error())
}

func TestSetupMergeCmd_AbortNoMergeInProgress(t *testing.T) {
	root := setupRepo(t, "main")
	cli := newMergeCli(t, &fakeMergeClient{})

	_, err := runCmdInDir(t, root, cli.setupMergeCmd(), "--abort")
	require.Error(t, err)
	require.Equal(t, "no merge in progress to abort", err.Error())
}

func TestSetupMergeCmd_UpToDate(t *testing.T) {
	root := setupRepo(t, "main")
	client := &fakeMergeClient{baseInfo: &domain.MergeBaseInfo{
		SourceCommitID:    "S",
		MergeBaseCommitID: "S",
	}}
	cli := newMergeCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupMergeCmd(), "feature")
	require.NoError(t, err)
	require.Equal(t, "Already up to date.\n", out)
	require.True(t, client.connectCalled)
}

func TestSetupMergeCmd_FastForward(t *testing.T) {
	root := setupRepo(t, "main")
	commit := snow.ID(5)
	client := &fakeMergeClient{
		baseInfo: &domain.MergeBaseInfo{
			TargetCommitID:    "B",
			SourceCommitID:    "S",
			MergeBaseCommitID: "B",
			SourceCommitHash:  "src-hash",
		},
		ffBranch: &serverDomain.Branch{Name: "main", CommitID: &commit},
	}
	cli := newMergeCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupMergeCmd(), "feature")
	require.NoError(t, err)
	require.Equal(t, "Fast-forwarded current branch to \"feature\".\n", out)
}

func TestSetupMergeCmd_FFOnlyNotPossible(t *testing.T) {
	root := setupRepo(t, "main")
	client := &fakeMergeClient{baseInfo: &domain.MergeBaseInfo{
		TargetCommitID:    "T",
		SourceCommitID:    "S",
		MergeBaseCommitID: "B",
	}}
	cli := newMergeCli(t, client)

	_, err := runCmdInDir(t, root, cli.setupMergeCmd(), "--ff-only", "feature")
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot fast-forward")
}

func TestSetupMergeCmd_Conflicts(t *testing.T) {
	root := setupRepoWithTree(t, "main", cliTree("a.txt", "a\nX\nc\n"))
	storeLocalChunks(t, root, "a\nX\nc\n")

	baseTree := cliTree("a.txt", "a\nb\nc\n")
	oursTree := cliTree("a.txt", "a\nX\nc\n")
	theirsTree := cliTree("a.txt", "a\nY\nc\n")
	download := chunkMapOf(t, "a\nb\nc\n", "a\nX\nc\n", "a\nY\nc\n")

	client := &fakeMergeClient{
		baseInfo: &domain.MergeBaseInfo{
			TargetCommitID:    "T",
			SourceCommitID:    "S",
			MergeBaseCommitID: "B",
			SourceCommitHash:  "src-hash",
			MergeBaseTree:     baseTree,
		},
		treeByBranch: map[string]*serverDomain.TreeNode{"main": oursTree, "feature": theirsTree},
		download:     download,
	}
	cli := newMergeCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupMergeCmd(), "feature")
	require.Error(t, err)
	require.Contains(t, out, "the following files conflict")
	require.Contains(t, out, "  C a.txt")
	require.Contains(t, err.Error(), "merge conflicts")
	require.False(t, client.pushCalled, "a conflicted merge must not push")
}

// cliTree builds a recursive manifest for a single flat file entry.
func cliTree(path, content string) *serverDomain.TreeNode {
	chunks := chunkerChunks(content)
	hashes := make([]serverDomain.Hash, len(chunks))
	for i, c := range chunks {
		hashes[i] = c.Hash
	}
	name := path
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			name = path[i+1:]
			break
		}
	}
	return &serverDomain.TreeNode{
		Name: "root",
		FileChildren: []*serverDomain.File{{
			Name:      name,
			Mode:      0o644,
			SizeBytes: int64(len(content)),
			Hash:      chunker.FileHash(hashes),
			Chunks:    chunks,
		}},
	}
}

func chunkerChunks(content string) []serverDomain.Chunk {
	chunks, err := chunker.ChunkAll([]byte(content))
	if err != nil {
		panic(err)
	}
	out := make([]serverDomain.Chunk, len(chunks))
	for i, c := range chunks {
		out[i] = serverDomain.Chunk{Hash: c.Hash, SizeBytes: int64(len(c.Data))}
	}
	return out
}

func chunkMapOf(t *testing.T, contents ...string) map[serverDomain.Hash][]byte {
	t.Helper()
	out := make(map[serverDomain.Hash][]byte)
	for _, c := range contents {
		chunks, err := chunker.ChunkAll([]byte(c))
		require.NoError(t, err)
		for _, ch := range chunks {
			out[ch.Hash] = wrapChunk(ch)
		}
	}
	return out
}

func wrapChunk(ch chunker.Chunk) []byte {
	return ch.Data
}

// storeLocalChunks seeds chunk content into the real local cache, mimicking a
// clone/update that materialized the current tree.
func storeLocalChunks(t *testing.T, root string, contents ...string) {
	t.Helper()
	lr := localrepo.NewLocalRepo()
	require.NoError(t, lr.Init(root))
	defer lr.Close()
	for _, c := range contents {
		chunks, err := chunker.ChunkAll([]byte(c))
		require.NoError(t, err)
		for _, ch := range chunks {
			require.NoError(t, lr.StoreChunk(ch.Hash, ch.Data))
		}
	}
}
