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
	"github.com/nipalab/nipa/internal/snow"
)

type fakeUpdateClient struct {
	host        string
	org         string
	project     string
	branch      string
	path        string
	manifest    *serverDomain.TreeNode
	chunkData   map[serverDomain.Hash][]byte
	downloaded  []serverDomain.Hash
	branchErr   error
	manifestErr error
	head        *snow.ID
	manifests   int

	tagInfo      *domain.Tag
	tagErr       error
	tagLookup    string
	commitTree   *serverDomain.TreeNode
	commitTreeID string
}

func (f *fakeUpdateClient) Connect(_ context.Context, host string) error {
	f.host = host
	return nil
}

func (f *fakeUpdateClient) GetBranchByName(_ context.Context, org, project, name string) (*serverDomain.Branch, error) {
	f.org, f.project, f.branch = org, project, name
	if f.branchErr != nil {
		return nil, f.branchErr
	}
	return &serverDomain.Branch{Name: name, CommitID: f.head}, nil
}

func (f *fakeUpdateClient) GetTreeNodeManifest(_ context.Context, org, project, branch string, paths []string) (*serverDomain.TreeNode, error) {
	f.org, f.project, f.branch = org, project, branch
	f.manifests++
	f.path = ""
	if len(paths) > 0 {
		f.path = paths[0]
	}
	if f.manifestErr != nil {
		return nil, f.manifestErr
	}
	if f.manifest == nil {
		return &serverDomain.TreeNode{Name: "root"}, nil
	}
	return f.manifest, nil
}

func (f *fakeUpdateClient) GetCommitTreeManifest(_ context.Context, org, project, commitID string, paths []string) (*serverDomain.TreeNode, error) {
	f.org, f.project, f.commitTreeID = org, project, commitID
	f.path = ""
	if len(paths) > 0 {
		f.path = paths[0]
	}
	if f.manifestErr != nil {
		return nil, f.manifestErr
	}
	if f.commitTree == nil {
		return &serverDomain.TreeNode{Name: "root"}, nil
	}
	return f.commitTree, nil
}

func (f *fakeUpdateClient) GetTagByName(_ context.Context, org, project, name string) (*domain.Tag, error) {
	f.org, f.project, f.tagLookup = org, project, name
	return f.tagInfo, f.tagErr
}

func (f *fakeUpdateClient) DownloadChunks(_ context.Context, _ domain.ChunkScope, hashes []serverDomain.Hash, onChunk func(h serverDomain.Hash, data []byte) error) error {
	f.downloaded = append(f.downloaded, hashes...)
	for _, h := range hashes {
		data, ok := f.chunkData[h]
		if !ok {
			data = []byte("from server")
		}
		if err := onChunk(h, data); err != nil {
			return err
		}
	}
	return nil
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
	require.Contains(t, out, "Downloading 1 objects (17 B)...", "update must announce how many objects are fetched")

	require.Equal(t, "http://example.com", client.host)
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
	for name, pinned := range map[string]bool{"pinned raw file is kept": true, "unpinned raw file is pruned": false} {
		t.Run(name, func(t *testing.T) { testUpdateCachePolicy(t, pinned) })
	}
}

func testUpdateCachePolicy(t *testing.T, pinned bool) {
	root := setupRepo(t, "main")
	if pinned {
		pin := localrepo.NewLocalRepo()
		require.NoError(t, pin.Init(root))
		require.NoError(t, pin.PinPaths([]string{"a.txt"}))
		require.NoError(t, pin.Close())
	}
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
	defer func() { _ = lr.Close() }()
	data, err := lr.LoadChunk(chunks[0].Hash)
	if !pinned {
		require.Error(t, err)
		return
	}
	require.NoError(t, err)
	require.Equal(t, content, string(data))
}

// editedRepo is a clone that tracks a.txt, has a local edit to it, and whose
// server now has other content for it.
func editedRepo(t *testing.T) (string, *fakeUpdateClient) {
	t.Helper()
	tracked := func(content string) *serverDomain.TreeNode {
		chunks := chunksOf(t, content)
		return &serverDomain.TreeNode{Name: "root", FileChildren: []*serverDomain.File{{
			Name:      "a.txt",
			Mode:      2,
			SizeBytes: int64(len(content)),
			Hash:      chunker.FileHash([]serverDomain.Hash{chunks[0].Hash}),
			Chunks:    chunks,
		}}}
	}
	root := setupRepoWithTree(t, "main", tracked("synced content"))
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("my local edit"), 0o644))
	tree := tracked("content from the server")
	return root, &fakeUpdateClient{chunkData: chunkDataMap(t, "content from the server"), manifest: tree, commitTree: tree, tagInfo: &domain.Tag{Name: "v1", CommitID: "6y1"}}
}

func requireFileContent(t *testing.T, root, name, want string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(root, name))
	require.NoError(t, err)
	require.Equal(t, want, string(got))
}

func TestSetupUpdateCmd_RefusesToOverwriteLocalEditsWithoutForce(t *testing.T) {
	root, client := editedRepo(t)
	cli := newUpdateCli(t, client)

	_, err := runCmdInDir(t, root, cli.setupUpdateCmd())
	require.ErrorContains(t, err, "local changes to 1 file(s) would be overwritten")
	require.ErrorContains(t, err, "a.txt")
	requireFileContent(t, root, "a.txt", "my local edit")
	require.Empty(t, client.downloaded, "a refused update downloads nothing")

	_, err = runCmdInDir(t, root, cli.setupUpdateCmd(), "--force")
	require.NoError(t, err)
	requireFileContent(t, root, "a.txt", "content from the server")
}

func TestSetupSwitchCmd_RefusesToOverwriteLocalEditsWithoutForce(t *testing.T) {
	for name, args := range map[string][]string{"branch": {"dev"}, "tag": {"--tag", "v1"}} {
		t.Run(name, func(t *testing.T) {
			root, client := editedRepo(t)
			cli := newUpdateCli(t, client)

			_, err := runCmdInDir(t, root, cli.setupSwitchCmd(), args...)
			require.ErrorContains(t, err, "local changes to 1 file(s) would be overwritten")
			requireFileContent(t, root, "a.txt", "my local edit")
			require.Equal(t, "main", loadRepoConfig(t, root).Branch, "a refused switch stays on its branch")

			_, err = runCmdInDir(t, root, cli.setupSwitchCmd(), append(args, "--force")...)
			require.NoError(t, err)
			requireFileContent(t, root, "a.txt", "content from the server")
		})
	}
}

func TestSetupUpdateCmd_AlreadyUpToDate(t *testing.T) {
	root := setupRepo(t, "main")
	content := "tracked content"
	chunks := chunksOf(t, content)
	head := snow.ID(42)
	client := &fakeUpdateClient{
		head:      &head,
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

	out, err := runCmdInDir(t, root, cli.setupUpdateCmd())
	require.NoError(t, err)
	require.NotContains(t, out, "Already up to date.")
	require.Equal(t, 1, client.manifests)

	require.NoError(t, os.Remove(filepath.Join(root, "a.txt")))
	out, err = runCmdInDir(t, root, cli.setupUpdateCmd())
	require.NoError(t, err)
	require.Equal(t, "Already up to date.\n", out)
	require.Equal(t, 1, client.manifests, "an up-to-date working copy fetches no manifest")
	require.NoFileExists(t, filepath.Join(root, "a.txt"), "the no-op does not look at the working copy")

	out, err = runCmdInDir(t, root, cli.setupUpdateCmd(), "--force")
	require.NoError(t, err)
	require.NotContains(t, out, "Already up to date.")
	require.Equal(t, 2, client.manifests)
	got, err := os.ReadFile(filepath.Join(root, "a.txt"))
	require.NoError(t, err)
	require.Equal(t, content, string(got), "a forced update restores the deleted file")
}
