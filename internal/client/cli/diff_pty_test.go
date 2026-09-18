//go:build linux

package cli

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSetupDiffCmd_InteractiveBranch(t *testing.T) {
	// stdout on a real terminal routes through runInteractivePager; os.Stdin
	// is not a terminal under `go test`, so MakeRaw fails and the command
	// returns without hanging.
	master, slave := openPty(t)
	defer master.Close()
	defer slave.Close()

	root := setupRepoWithTree(t, "main", diffTree(diffFile("a.txt", "old\n")))
	writeFile(t, root, "a.txt", "new\n")
	storeContent(t, root, "old\n")
	cli := newDiffCli()

	oldWD, err := os.Getwd()
	require.NoError(t, err)
	defer func() { require.NoError(t, os.Chdir(oldWD)) }()
	require.NoError(t, os.Chdir(root))

	cmd := cli.setupDiffCmd()
	cmd.SetOut(master)
	cmd.SetArgs([]string{})
	require.Error(t, cmd.Execute())
}
