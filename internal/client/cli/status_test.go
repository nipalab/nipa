package cli

import (
	"testing"

	"github.com/stretchr/testify/require"

	serverDomain "github.com/nipalab/nipa/internal/domain"
)

func TestSetupStatusCmd_ShowsAllSections(t *testing.T) {
	base := &serverDomain.TreeNode{
		Hash: serverDomain.Hash{0x01},
		Name: "root",
		FileChildren: []*serverDomain.File{
			{Name: "tracked.txt", SizeBytes: int64(len("hello world")), Chunks: chunksOf(t, "hello world")},
			{Name: "modified.txt", SizeBytes: int64(len("orig")), Chunks: chunksOf(t, "orig")},
			{Name: "missing.txt", SizeBytes: int64(len("gone")), Chunks: chunksOf(t, "gone")},
		},
	}
	root := setupRepoWithTree(t, "main", base)
	writeFile(t, root, "tracked.txt", "hello world")
	writeFile(t, root, "modified.txt", "changed")
	writeFile(t, root, "new.txt", "untracked")
	stagePath(t, root, "staged.txt")
	writeFile(t, root, "staged.txt", "ready to push")
	cli := newStageCli()

	out, err := runCmdInDir(t, root, cli.setupStatusCmd())
	require.NoError(t, err)
	require.Equal(t, "A  staged.txt\nM  modified.txt\n?  new.txt\n!  missing.txt\n", out)
}

func TestSetupStatusCmd_Subdirectory(t *testing.T) {
	root := setupRepo(t, "main")
	writeFile(t, root, "src/a.txt", "a")
	stagePath(t, root, "src/a.txt")
	cli := newStageCli()

	sub := root + "/src"
	out, err := runCmdInDir(t, sub, cli.setupStatusCmd())
	require.NoError(t, err)
	require.Equal(t, "A  src/a.txt\n", out, "paths are reported relative to the repository root")
}

func TestSetupStatusCmd_Clean(t *testing.T) {
	root := setupRepo(t, "main")
	cli := newStageCli()

	out, err := runCmdInDir(t, root, cli.setupStatusCmd())
	require.NoError(t, err)
	require.Empty(t, out)
}

func TestSetupStatusCmd_NotARepo(t *testing.T) {
	dir := t.TempDir()
	cli := newStageCli()

	_, err := runCmdInDir(t, dir, cli.setupStatusCmd())
	require.Error(t, err)
	require.Equal(t, "not a nipa repository (or any of the parent directories)", err.Error())
}
