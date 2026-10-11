package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

func setupRepoWithStoredTree(t *testing.T, branch string, files map[string]string) string {
	t.Helper()
	target := t.TempDir()
	lr := localrepo.NewLocalRepo()
	require.NoError(t, lr.Init(target))
	defer lr.Close()
	require.NoError(t, lr.SaveConfig(domain.Config{Url: "http://example.com/org/project", Branch: branch}))

	tree := &serverDomain.TreeNode{Hash: serverDomain.Hash{0x01}, Name: "root"}
	for name, content := range files {
		data := []byte(content)
		_, chunks, err := chunker.EncodeBytes(data, name, false, chunker.EncodingRaw)
		require.NoError(t, err)
		hashes := make([]serverDomain.Hash, 0, len(chunks))
		stored := make([]*serverDomain.ChunkData, 0, len(chunks))
		wrapped := make([]serverDomain.Chunk, 0, len(chunks))
		for _, c := range chunks {
			hashes = append(hashes, c.Hash)
			stored = append(stored, &serverDomain.ChunkData{Hash: c.Hash, Data: c.Data})
			wrapped = append(wrapped, serverDomain.Chunk{Hash: c.Hash, SizeBytes: c.SizeBytes})
		}
		require.NoError(t, lr.StoreChunks(stored))
		tree.FileChildren = append(tree.FileChildren, &serverDomain.File{
			Name:      name,
			Hash:      chunker.FileHash(hashes),
			SizeBytes: int64(len(data)),
			Chunks:    wrapped,
		})
	}
	require.NoError(t, lr.SaveTree(tree))
	return target
}

func TestSetupRestoreCmd_ModifiedFile(t *testing.T) {
	root := setupRepoWithStoredTree(t, "main", map[string]string{"a.txt": "original"})
	writeFile(t, root, "a.txt", "edited")
	cli := newStageCli()

	out, err := runCmdInDir(t, root, cli.setupRestoreCmd(), "a.txt")
	require.NoError(t, err)
	require.Empty(t, out)

	data, err := os.ReadFile(filepath.Join(root, "a.txt"))
	require.NoError(t, err)
	require.Equal(t, "original", string(data))

	status, err := runCmdInDir(t, root, cli.setupStatusCmd())
	require.NoError(t, err)
	require.Equal(t, "On branch main\n", status)
}

func TestSetupRestoreCmd_DeletedFile(t *testing.T) {
	root := setupRepoWithStoredTree(t, "main", map[string]string{"a.txt": "keep me"})
	writeFile(t, root, "a.txt", "keep me")
	require.NoError(t, os.Remove(filepath.Join(root, "a.txt")))
	cli := newStageCli()

	_, err := runCmdInDir(t, root, cli.setupRestoreCmd(), "a.txt")
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(root, "a.txt"))
	require.NoError(t, err)
	require.Equal(t, "keep me", string(data))
}

func TestSetupRestoreCmd_Directory(t *testing.T) {
	root := setupRepoWithStoredTree(t, "main", map[string]string{
		"docs/a.txt":     "a",
		"docs/sub/b.txt": "b",
	})
	writeFile(t, root, "docs/a.txt", "edit a")
	writeFile(t, root, "docs/sub/b.txt", "edit b")
	cli := newStageCli()

	_, err := runCmdInDir(t, root, cli.setupRestoreCmd(), "docs")
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(root, "docs", "a.txt"))
	require.NoError(t, err)
	require.Equal(t, "a", string(data))
	data, err = os.ReadFile(filepath.Join(root, "docs", "sub", "b.txt"))
	require.NoError(t, err)
	require.Equal(t, "b", string(data))
}

func TestSetupRestoreCmd_RefusesStagedFile(t *testing.T) {
	root := setupRepoWithStoredTree(t, "main", map[string]string{"a.txt": "original"})
	stagePath(t, root, "a.txt")
	writeFile(t, root, "a.txt", "edited")
	cli := newStageCli()

	_, err := runCmdInDir(t, root, cli.setupRestoreCmd(), "a.txt")
	require.ErrorContains(t, err, "staged changes")

	data, err := os.ReadFile(filepath.Join(root, "a.txt"))
	require.NoError(t, err)
	require.Equal(t, "edited", string(data), "a refused restore must not touch the file")
}

func TestSetupRestoreCmd_RejectsUntrackedPath(t *testing.T) {
	root := setupRepoWithStoredTree(t, "main", map[string]string{"a.txt": "a"})
	writeFile(t, root, "local.txt", "mine")
	cli := newStageCli()

	_, err := runCmdInDir(t, root, cli.setupRestoreCmd(), "local.txt")
	require.ErrorContains(t, err, "not tracked")
}

func TestSetupRestoreCmd_NotARepo(t *testing.T) {
	cli := newStageCli()

	_, err := runCmdInDir(t, t.TempDir(), cli.setupRestoreCmd(), "a.txt")
	require.ErrorContains(t, err, "not a nipa repository")
}
