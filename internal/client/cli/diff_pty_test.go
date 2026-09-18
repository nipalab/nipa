//go:build linux

package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func drainPty(t *testing.T, slave *os.File) string {
	t.Helper()
	require.NoError(t, unix.SetNonblock(int(slave.Fd()), true))
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := slave.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return sb.String()
}

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

func TestSetupDiffCmd_ColorsOnTTY(t *testing.T) {
	// With stdout on a terminal and --no-pager, patch lines carry colors.
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
	cmd.SetArgs([]string{"--no-pager"})
	require.NoError(t, cmd.Execute())

	out := drainPty(t, slave)
	require.Contains(t, out, "\x1b[32m+new\x1b[m")
	require.Contains(t, out, "\x1b[31m-old\x1b[m")
	require.Contains(t, out, "\x1b[36m@@ -1,1 +1,1 @@\x1b[m")
}

func TestSetupDiffCmd_NoColorEnv(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

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
	cmd.SetArgs([]string{"--no-pager"})
	require.NoError(t, cmd.Execute())

	out := drainPty(t, slave)
	require.Contains(t, out, "+new")
	require.NotContains(t, out, "\x1b[")
}
