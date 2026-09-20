package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/usecase"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

type fakeRevertClient struct {
	details    map[string]*domain.CommitDetail
	walk       []*domain.CommitWalkEntry
	headTree   *serverDomain.TreeNode
	pushResult *serverDomain.PushResult
	pushCalled bool
	pushBase   string
	pushMsg    string
}

func (f *fakeRevertClient) Connect(_ context.Context, _ string) error { return nil }

func (f *fakeRevertClient) GetCommit(_ context.Context, _, _, commitID string) (*domain.CommitDetail, error) {
	if detail, ok := f.details[commitID]; ok {
		return detail, nil
	}
	return nil, domain.NewUserError("commit " + commitID + " not found")
}

func (f *fakeRevertClient) WalkCommits(_ context.Context, _, _, _, _ string, _ int) ([]*domain.CommitWalkEntry, error) {
	return f.walk, nil
}

func (f *fakeRevertClient) GetTreeNodeManifest(_ context.Context, _, _, _ string, _ []string) (*serverDomain.TreeNode, error) {
	return f.headTree, nil
}

func (f *fakeRevertClient) GetBranchByName(_ context.Context, _, _, name string) (*serverDomain.Branch, error) {
	return &serverDomain.Branch{Name: name}, nil
}

func (f *fakeRevertClient) DownloadChunks(_ context.Context, _ domain.ChunkScope, _ []serverDomain.Hash, _ func(h serverDomain.Hash, data []byte) error) error {
	return nil
}

func (f *fakeRevertClient) Push(_ context.Context, _, _, _, baseTreeHash, message string, _ []*serverDomain.PushFile, _ []string, _ string) (*serverDomain.PushResult, error) {
	f.pushCalled = true
	f.pushBase = baseTreeHash
	f.pushMsg = message
	if f.pushResult != nil {
		return f.pushResult, nil
	}
	return &serverDomain.PushResult{}, nil
}

func (f *fakeRevertClient) UploadChunks(_ context.Context, _ domain.ChunkScope, _ []*serverDomain.ChunkData, _ ...func(ch *serverDomain.ChunkData)) (int, int, error) {
	return 0, 0, nil
}

func newRevertCli(t *testing.T, client *fakeRevertClient) *Cli {
	t.Helper()
	auth := usecase.NewAuth(fakeExecutor{}, &fakeStorage{token: signTestJWT(t)}, &fakeInput{})
	lr := localrepo.NewLocalRepo()
	push := usecase.NewPush(auth, client, lr)
	revert := usecase.NewRevert(auth, client, lr, push)
	return NewCli(&fakeUsecaseContainer{revert: revert}, &fakeConnector{})
}

func revertCliTree(t *testing.T, hashByte byte, files map[string]string) *serverDomain.TreeNode {
	t.Helper()
	root := &serverDomain.TreeNode{Name: "", Mode: 0o040000, Hash: serverDomain.Hash{hashByte}}
	for path, content := range files {
		root.FileChildren = append(root.FileChildren, &serverDomain.File{
			Name:      path,
			Mode:      0o644,
			SizeBytes: int64(len(content)),
			IsBinary:  chunker.IsBinary([]byte(content)),
			Hash:      fileHash(t, content),
			Chunks:    chunksOf(t, content),
		})
	}
	return root
}

func setupRevertRepo(t *testing.T, tree *serverDomain.TreeNode, contents ...string) string {
	t.Helper()
	target := t.TempDir()
	lr := localrepo.NewLocalRepo()
	require.NoError(t, lr.Init(target))
	t.Cleanup(func() { _ = lr.Close() })
	require.NoError(t, lr.SaveConfig(domain.Config{Url: "http://example.com/org/project", Branch: "main"}))
	require.NoError(t, lr.SaveTree(tree))
	for _, content := range contents {
		chunks, err := chunker.ChunkAll([]byte(content))
		require.NoError(t, err)
		data := make([]*serverDomain.ChunkData, len(chunks))
		for i, c := range chunks {
			data[i] = &serverDomain.ChunkData{Hash: c.Hash, Data: c.Data}
		}
		require.NoError(t, lr.StoreChunks(data))
	}
	return target
}

func TestSetupRevertCmd_MissingCommit(t *testing.T) {
	root := setupRepo(t, "main")
	cli := newRevertCli(t, &fakeRevertClient{})

	_, err := runCmdInDir(t, root, cli.setupRevertCmd())
	require.Error(t, err)
	require.Contains(t, err.Error(), "commit to revert is required")
}

func TestSetupRevertCmd_ModeWithArgument(t *testing.T) {
	root := setupRepo(t, "main")
	cli := newRevertCli(t, &fakeRevertClient{})

	_, err := runCmdInDir(t, root, cli.setupRevertCmd(), "--abort", "2")
	require.Error(t, err)
	require.Contains(t, err.Error(), "do not take a commit argument")
}

func TestSetupRevertCmd_MutuallyExclusiveModes(t *testing.T) {
	root := setupRepo(t, "main")
	cli := newRevertCli(t, &fakeRevertClient{})

	_, err := runCmdInDir(t, root, cli.setupRevertCmd(), "--abort", "--skip")
	require.Error(t, err)
	require.Contains(t, err.Error(), "mutually exclusive")
}

func TestSetupRevertCmd_AbortNoRevert(t *testing.T) {
	root := setupRepo(t, "main")
	cli := newRevertCli(t, &fakeRevertClient{})

	_, err := runCmdInDir(t, root, cli.setupRevertCmd(), "--abort")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no revert in progress")
}

func TestSetupRevertCmd_ContinueNoRevert(t *testing.T) {
	root := setupRepo(t, "main")
	cli := newRevertCli(t, &fakeRevertClient{})

	_, err := runCmdInDir(t, root, cli.setupRevertCmd(), "--continue")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no revert in progress")
}

func TestSetupRevertCmd_CleanRevert(t *testing.T) {
	v1, v2 := "v1", "v2"
	head := revertCliTree(t, 0xaa, map[string]string{"a.txt": v2})
	client := &fakeRevertClient{
		details: map[string]*domain.CommitDetail{
			"2": {ID: "2", Hash: "c2hash", Parent1ID: "1", Message: "change a", Tree: revertCliTree(t, 0x02, map[string]string{"a.txt": v2})},
			"1": {ID: "1", Hash: "c1hash", Message: "add a", Tree: revertCliTree(t, 0x01, map[string]string{"a.txt": v1})},
		},
		headTree: head,
	}
	root := setupRevertRepo(t, head, v2, v1)
	cli := newRevertCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupRevertCmd(), "2")
	require.NoError(t, err)
	require.Contains(t, out, "Revert committed.")
	require.True(t, client.pushCalled)
	require.Equal(t, head.Hash.String(), client.pushBase)
	require.Contains(t, client.pushMsg, `Revert "change a"`)

	data, err := os.ReadFile(filepath.Join(root, "a.txt"))
	require.NoError(t, err)
	require.Equal(t, v1, string(data))
}

func TestSetupRevertCmd_NoCommit(t *testing.T) {
	v1, v2 := "v1", "v2"
	head := revertCliTree(t, 0xaa, map[string]string{"a.txt": v2})
	client := &fakeRevertClient{
		details: map[string]*domain.CommitDetail{
			"2": {ID: "2", Hash: "c2hash", Parent1ID: "1", Message: "change a", Tree: revertCliTree(t, 0x02, map[string]string{"a.txt": v2})},
			"1": {ID: "1", Hash: "c1hash", Message: "add a", Tree: revertCliTree(t, 0x01, map[string]string{"a.txt": v1})},
		},
		headTree: head,
	}
	root := setupRevertRepo(t, head, v2, v1)
	cli := newRevertCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupRevertCmd(), "2", "--no-commit")
	require.NoError(t, err)
	require.Contains(t, out, "changes are staged")
	require.False(t, client.pushCalled)
	require.Equal(t, []string{"a.txt"}, stagedPathsAt(t, root))
}

func TestSetupRevertCmd_ConflictAndAbort(t *testing.T) {
	v1 := "top\nv1\nbottom"
	v2 := "top\nv2\nbottom"
	v3 := "top\nv3\nbottom"
	head := revertCliTree(t, 0xaa, map[string]string{"a.txt": v3})
	client := &fakeRevertClient{
		details: map[string]*domain.CommitDetail{
			"2": {ID: "2", Hash: "c2hash", Parent1ID: "1", Message: "change a", Tree: revertCliTree(t, 0x02, map[string]string{"a.txt": v2})},
			"1": {ID: "1", Hash: "c1hash", Message: "add a", Tree: revertCliTree(t, 0x01, map[string]string{"a.txt": v1})},
		},
		headTree: head,
	}
	root := setupRevertRepo(t, head, v1, v2, v3)
	cli := newRevertCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupRevertCmd(), "2")
	require.Error(t, err)
	require.Contains(t, err.Error(), "revert conflicts")
	require.Contains(t, out, "C a.txt")
	require.False(t, client.pushCalled)

	out, err = runCmdInDir(t, root, cli.setupRevertCmd(), "--abort")
	require.NoError(t, err)
	require.Contains(t, out, "Revert aborted")
	require.Empty(t, stagedPathsAt(t, root))
}
