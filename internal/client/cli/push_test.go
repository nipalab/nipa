package cli

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/usecase"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

type fakePushClient struct {
	host         string
	org          string
	project      string
	branch       string
	baseTreeHash string
	message      string
	files        []*serverDomain.PushFile
	removed      []string
	uploaded     [][]*serverDomain.ChunkData
}

func (f *fakePushClient) Connect(_ context.Context, host string) error {
	f.host = host
	return nil
}

func (f *fakePushClient) Push(_ context.Context, org, project, branch, baseTreeHash, message string, files []*serverDomain.PushFile, removed []string) (*serverDomain.PushResult, error) {
	f.org, f.project, f.branch, f.baseTreeHash, f.message = org, project, branch, baseTreeHash, message
	f.files, f.removed = files, removed
	return &serverDomain.PushResult{}, nil
}

func (f *fakePushClient) UploadChunks(_ context.Context, chunks []*serverDomain.ChunkData) (int, int, error) {
	f.uploaded = append(f.uploaded, chunks)
	return len(chunks), 0, nil
}

func (f *fakePushClient) GetTreeNodeManifest(_ context.Context, _, _, _, _ string) (*serverDomain.TreeNode, error) {
	return &serverDomain.TreeNode{Name: "root"}, nil
}

func newPushCli(t *testing.T, client *fakePushClient) *Cli {
	t.Helper()
	auth := usecase.NewAuth(fakeExecutor{}, &fakeStorage{token: signTestJWT(t)}, &fakeInput{})
	pusher := usecase.NewPush(auth, client, localrepo.NewLocalRepo())
	return NewCli(&fakeUsecaseContainer{push: pusher}, &fakeConnector{})
}

func TestSetupPushCmd_Success(t *testing.T) {
	root := setupRepo(t, "main")
	writeFile(t, root, "a.txt", "hello world")
	stagePath(t, root, "a.txt")

	client := &fakePushClient{}
	cli := newPushCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupPushCmd(), "-m", "add a.txt")
	require.NoError(t, err)
	require.Empty(t, out)

	require.Equal(t, "example.com", client.host)
	require.Equal(t, "org", client.org)
	require.Equal(t, "project", client.project)
	require.Equal(t, "main", client.branch)
	require.Empty(t, client.baseTreeHash)
	require.Equal(t, "add a.txt", client.message)

	require.Len(t, client.files, 1)
	require.Equal(t, "a.txt", client.files[0].Path)
	require.Empty(t, client.removed)
	require.NotEmpty(t, client.uploaded, "chunk content must be streamed before push")
	require.Empty(t, stagedPathsAt(t, root), "staged markers must be cleared after a successful push")
}

func TestSetupPushCmd_Success_WithSecondChunk(t *testing.T) {
	root := setupRepo(t, "main")
	writeFile(t, root, "a.txt", "hello world extra content to make more than one chunk")
	stagePath(t, root, "a.txt")

	client := &fakePushClient{}
	cli := newPushCli(t, client)

	_, err := runCmdInDir(t, root, cli.setupPushCmd(), "-m", "push")
	require.NoError(t, err)
	require.Len(t, client.files, 1)
	require.NotEmpty(t, client.files[0].ChunkHashes)
}

func TestSetupPushCmd_NotARepo(t *testing.T) {
	cli := newPushCli(t, &fakePushClient{})

	_, err := runCmdInDir(t, t.TempDir(), cli.setupPushCmd(), "-m", "push")
	require.Error(t, err)
	require.Equal(t, "not a nipa repository (or any of the parent directories)", err.Error())
}

func TestSetupPushCmd_MissingMessage(t *testing.T) {
	root := setupRepo(t, "main")
	stagePath(t, root, "a.txt")
	writeFile(t, root, "a.txt", "content")

	cli := newPushCli(t, &fakePushClient{})
	_, err := runCmdInDir(t, root, cli.setupPushCmd())
	require.Error(t, err)
	require.Contains(t, err.Error(), "message")
}
