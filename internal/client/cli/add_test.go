package cli

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func newStageCli() *Cli {
	return NewCli(&fakeUsecaseContainer{}, &fakeConnector{})
}

func TestSetupAddCmd_MarksFile(t *testing.T) {
	root := setupRepo(t, "main")
	writeFile(t, root, "a.txt", "hello")
	cli := newStageCli()

	out, err := runCmdInDir(t, root, cli.setupAddCmd(), "a.txt")
	require.NoError(t, err)
	require.Empty(t, out)
	require.Equal(t, []string{"a.txt"}, stagedPathsAt(t, root))
}

func TestSetupAddCmd_Directory(t *testing.T) {
	root := setupRepo(t, "main")
	writeFile(t, root, "assets/logo.txt", "logo")
	writeFile(t, root, "assets/sub/readme.md", "readme")
	cli := newStageCli()

	_, err := runCmdInDir(t, root, cli.setupAddCmd(), "assets")
	require.NoError(t, err)
	require.Equal(t, []string{"assets/logo.txt", "assets/sub/readme.md"}, stagedPathsAt(t, root))
}

func TestSetupAddCmd_SkipsNipaDir(t *testing.T) {
	root := setupRepo(t, "main")
	writeFile(t, root, "a.txt", "a")
	cli := newStageCli()

	_, err := runCmdInDir(t, root, cli.setupAddCmd(), "")
	require.NoError(t, err)
	require.Equal(t, []string{"a.txt"}, stagedPathsAt(t, root))
}

func TestSetupAddCmd_MissingPath(t *testing.T) {
	root := setupRepo(t, "main")
	cli := newStageCli()

	_, err := runCmdInDir(t, root, cli.setupAddCmd(), "nope.txt")
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not exist")
}

func TestSetupAddCmd_NotARepo(t *testing.T) {
	dir := t.TempDir()
	cli := newStageCli()

	_, err := runCmdInDir(t, dir, cli.setupAddCmd(), "a.txt")
	require.Error(t, err)
	require.Equal(t, "not a nipa repository (or any of the parent directories)", err.Error())
}

func TestSetupAddCmd_RequiresPath(t *testing.T) {
	root := setupRepo(t, "main")
	cli := newStageCli()

	_, err := runCmdInDir(t, root, cli.setupAddCmd())
	require.Error(t, err)
}
