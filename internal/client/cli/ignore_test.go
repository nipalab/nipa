package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSetupIgnoreImportCmd(t *testing.T) {
	root := setupRepo(t, "main")
	cli := newStageCli()

	_, err := runCmdInDir(t, root, cli.setupIgnoreCmd(), "import")
	require.Error(t, err)

	writeFile(t, root, ".gitignore", "node_modules/\n[Bb]uild/\n")
	out, err := runCmdInDir(t, root, cli.setupIgnoreCmd(), "import")
	require.NoError(t, err)
	require.Contains(t, out, "added 2 rules to .nipaignore")
	require.Contains(t, out, `warning: "[Bb]uild/" uses gitignore syntax`)
	data, err := os.ReadFile(filepath.Join(root, ".nipaignore"))
	require.NoError(t, err)
	require.Contains(t, string(data), "node_modules/\n")

	_, err = runCmdInDir(t, t.TempDir(), cli.setupIgnoreCmd(), "import")
	require.Error(t, err)
}

func TestSetupAddCmd_WarnsAboutGeneratedFolders(t *testing.T) {
	root := setupRepo(t, "main")
	writeFile(t, root, "src/app.js", "x")
	for _, p := range []string{"a", "b", "c"} {
		writeFile(t, root, "web/node_modules/"+p+"/index.js", p)
	}
	cli := newStageCli()

	out, err := runCmdInDir(t, root, cli.setupAddCmd(), ".")
	require.NoError(t, err)
	require.Contains(t, out, "warning: staged 3 new files in web/node_modules/, which looks generated")
	require.Contains(t, out, "hint: add \"node_modules/\" to .nipaignore and run `nipa remove web/node_modules` to unstage it")
	require.Len(t, stagedPathsAt(t, root), 4)
}

func TestWarnGenerated_CapsTheList(t *testing.T) {
	root := setupRepo(t, "main")
	for _, name := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		writeFile(t, root, name+"/node_modules/x.js", "x")
	}
	out, err := runCmdInDir(t, root, newStageCli().setupAddCmd(), ".")
	require.NoError(t, err)
	require.Contains(t, out, "warning: 2 more folders look generated")
}
