//go:build linux

package cli

import (
	"bytes"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// openPty creates a pseudo-terminal pair (master/slave).
func openPty(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	master, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY, 0)
	require.NoError(t, err)
	err = unix.IoctlSetPointerInt(master, unix.TIOCSPTLCK, 0)
	require.NoError(t, err)
	ptno, err := unix.IoctlGetInt(master, unix.TIOCGPTN)
	require.NoError(t, err)
	slavePath := fmt.Sprintf("/dev/pts/%d", ptno)
	slave, err := unix.Open(slavePath, unix.O_RDWR|unix.O_NOCTTY, 0)
	require.NoError(t, err)
	return os.NewFile(uintptr(master), "pty-master"), os.NewFile(uintptr(slave), "pty-slave")
}

func TestRunLogInteractive(t *testing.T) {
	master, slave := openPty(t)
	defer master.Close()
	defer slave.Close()

	var out bytes.Buffer
	cli := newLogCli(testEntries(), nil)

	done := make(chan error, 1)
	go func() {
		done <- cli.runLogInteractive(&out, slave, testEntries(), false)
	}()

	// let the pager start, then quit via the pty master
	time.Sleep(50 * time.Millisecond)
	_, err := master.Write([]byte("q"))
	require.NoError(t, err)
	require.NoError(t, <-done)
	require.Contains(t, out.String(), "nipa log")
	require.Contains(t, out.String(), "commit ")
}

func TestRunLogInteractive_Oneline(t *testing.T) {
	master, slave := openPty(t)
	defer master.Close()
	defer slave.Close()

	var out bytes.Buffer
	cli := newLogCli(testEntries(), nil)

	done := make(chan error, 1)
	go func() {
		done <- cli.runLogInteractive(&out, slave, testEntries(), true)
	}()

	time.Sleep(50 * time.Millisecond)
	_, err := master.Write([]byte("q"))
	require.NoError(t, err)
	require.NoError(t, <-done)
	require.Contains(t, out.String(), "aaaaaaaaaaaa first commit")
}

func TestRunLogInteractive_NonTerminalInput(t *testing.T) {
	// a non-terminal input must make MakeRaw fail without hanging
	f, err := os.CreateTemp(t.TempDir(), "in")
	require.NoError(t, err)
	defer f.Close()

	var out bytes.Buffer
	cli := newLogCli(testEntries(), nil)
	err = cli.runLogInteractive(&out, f, testEntries(), false)
	require.Error(t, err)
}

func TestSetupLogCmd_InteractiveBranch(t *testing.T) {
	// stdout on a real terminal routes through runLogInteractive; os.Stdin is
	// not a terminal under `go test`, so MakeRaw fails and the command returns.
	master, slave := openPty(t)
	defer master.Close()
	defer slave.Close()

	root := setupRepo(t, "main")
	cli := newLogCli(testEntries(), nil)

	oldWD, err := os.Getwd()
	require.NoError(t, err)
	defer func() { require.NoError(t, os.Chdir(oldWD)) }()
	require.NoError(t, os.Chdir(root))

	cmd := cli.setupLogCmd()
	cmd.SetOut(master)
	err = cmd.Execute()
	require.Error(t, err)
}
