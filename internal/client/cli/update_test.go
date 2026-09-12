package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/usecase"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

type fakeUpdateClient struct {
	host       string
	org        string
	project    string
	branch     string
	path       string
	manifest   *serverDomain.TreeNode
	chunkData  map[serverDomain.Hash][]byte
	downloaded []serverDomain.Hash
}

func (f *fakeUpdateClient) Connect(_ context.Context, host string) error {
	f.host = host
	return nil
}

func (f *fakeUpdateClient) GetTreeNodeManifest(_ context.Context, org, project, branch, path string) (*serverDomain.TreeNode, error) {
	f.org, f.project, f.branch, f.path = org, project, branch, path
	if f.manifest == nil {
		return &serverDomain.TreeNode{Name: "root"}, nil
	}
	return f.manifest, nil
}

func (f *fakeUpdateClient) DownloadChunks(_ context.Context, hashes []serverDomain.Hash) (map[serverDomain.Hash][]byte, error) {
	f.downloaded = append(f.downloaded, hashes...)
	out := make(map[serverDomain.Hash][]byte, len(hashes))
	for _, h := range hashes {
		data, ok := f.chunkData[h]
		if !ok {
			data = []byte("from server")
		}
		out[h] = data
	}
	return out, nil
}

func newUpdateCli(t *testing.T, client *fakeUpdateClient) *Cli {
	t.Helper()
	auth := usecase.NewAuth(fakeExecutor{}, &fakeStorage{token: signTestJWT(t)}, &fakeInput{})
	updater := usecase.NewUpdate(auth, client, localrepo.NewLocalRepo())
	return NewCli(&fakeUsecaseContainer{update: updater}, &fakeConnector{})
}

func chunkDataMap(t *testing.T, content string) map[serverDomain.Hash][]byte {
	t.Helper()
	chunked, err := chunker.ChunkAll([]byte(content))
	require.NoError(t, err)
	m := make(map[serverDomain.Hash][]byte, len(chunked))
	for _, c := range chunked {
		m[c.Hash] = c.Data
	}
	return m
}

func TestSetupUpdateCmd_MaterializesFiles(t *testing.T) {
	root := setupRepo(t, "main")
	content := "hello from update"
	chunks := chunksOf(t, content)
	fileHash := chunker.FileHash([]serverDomain.Hash{chunks[0].Hash})

	client := &fakeUpdateClient{
		chunkData: chunkDataMap(t, content),
		manifest: &serverDomain.TreeNode{
			Name: "root",
			FileChildren: []*serverDomain.File{{
				Name:      "a.txt",
				Mode:      0o644,
				SizeBytes: int64(len(content)),
				Hash:      fileHash,
				Chunks:    chunks,
			}},
		},
	}
	cli := newUpdateCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupUpdateCmd())
	require.NoError(t, err)
	require.Empty(t, out)

	require.Equal(t, "example.com", client.host)
	require.Equal(t, "org", client.org)
	require.Equal(t, "project", client.project)
	require.Equal(t, "main", client.branch)
	require.Equal(t, "", client.path, "update always refreshes the whole tree")

	got, err := os.ReadFile(filepath.Join(root, "a.txt"))
	require.NoError(t, err)
	require.Equal(t, content, string(got))
}

func TestSetupUpdateCmd_NotARepo(t *testing.T) {
	cli := newUpdateCli(t, &fakeUpdateClient{})

	_, err := runCmdInDir(t, t.TempDir(), cli.setupUpdateCmd())
	require.Error(t, err)
	require.Equal(t, "not a nipa repository (or any of the parent directories)", err.Error())
}

func TestSetupUpdateCmd_StoresChunksInCache(t *testing.T) {
	root := setupRepo(t, "main")
	content := "cached content"
	chunks := chunksOf(t, content)

	client := &fakeUpdateClient{
		chunkData: chunkDataMap(t, content),
		manifest: &serverDomain.TreeNode{
			Name: "root",
			FileChildren: []*serverDomain.File{{
				Name:      "a.txt",
				Mode:      0o644,
				SizeBytes: int64(len(content)),
				Hash:      chunker.FileHash([]serverDomain.Hash{chunks[0].Hash}),
				Chunks:    chunks,
			}},
		},
	}
	cli := newUpdateCli(t, client)
	_, err := runCmdInDir(t, root, cli.setupUpdateCmd())
	require.NoError(t, err)

	lr := localrepo.NewLocalRepo()
	require.NoError(t, lr.Init(root))
	defer lr.Close()
	data, err := lr.LoadChunk(chunks[0].Hash)
	require.NoError(t, err)
	require.Equal(t, content, string(data))
}
