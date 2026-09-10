package cli

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSetupRemoveCmd_UnmarksFile(t *testing.T) {
	root := setupRepo(t, "main")
	writeFile(t, root, "a.txt", "a")
	stagePath(t, root, "a.txt")
	cli := newStageCli()

	out, err := runCmdInDir(t, root, cli.setupRemoveCmd(), "a.txt")
	require.NoError(t, err)
	require.Empty(t, out)
	require.Empty(t, stagedPathsAt(t, root))
}

func TestSetupRemoveCmd_Directory(t *testing.T) {
	root := setupRepo(t, "main")
	writeFile(t, root, "a.txt", "a")
	writeFile(t, root, "b/c.txt", "c")
	stagePath(t, root, "a.txt")
	stagePath(t, root, "b/c.txt")
	cli := newStageCli()

	_, err := runCmdInDir(t, root, cli.setupRemoveCmd(), "b")
	require.NoError(t, err)
	require.Equal(t, []string{"a.txt"}, stagedPathsAt(t, root))
}

func TestSetupRemoveCmd_All(t *testing.T) {
	root := setupRepo(t, "main")
	writeFile(t, root, "a.txt", "a")
	writeFile(t, root, "b/c.txt", "c")
	stagePath(t, root, "a.txt")
	stagePath(t, root, "b/c.txt")
	cli := newStageCli()

	_, err := runCmdInDir(t, root, cli.setupRemoveCmd(), "-a")
	require.NoError(t, err)
	require.Empty(t, stagedPathsAt(t, root))
}

func TestSetupRemoveCmd_RequiresPath(t *testing.T) {
	root := setupRepo(t, "main")
	cli := newStageCli()

	_, err := runCmdInDir(t, root, cli.setupRemoveCmd())
	require.Error(t, err)
}

func TestSetupRemoveCmd_NotARepo(t *testing.T) {
	dir := t.TempDir()
	cli := newStageCli()

	_, err := runCmdInDir(t, dir, cli.setupRemoveCmd(), "a.txt")
	require.Error(t, err)
	require.Equal(t, "not a nipa repository (or any of the parent directories)", err.Error())
}
