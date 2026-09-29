package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewMCPUseCases_BuildsGraphAndCloses(t *testing.T) {
	uc := newMCPUseCases(newServeTestClient, fakeStorage{})
	require.NotNil(t, uc.Repo)
	require.NotNil(t, uc.Push)
	require.NotNil(t, uc.Diff)
	require.NotNil(t, uc.MR)
	require.NotNil(t, uc.Lock)
	require.NotNil(t, uc.Connector)
	require.NotNil(t, uc.Close)

	uc.Close()
}

func TestNewMCPCommand_Flags(t *testing.T) {
	cmd := newMCPCommand(newServeTestClient, fakeStorage{})
	require.Equal(t, "mcp", cmd.Use)
	require.NotNil(t, cmd.Flags().Lookup("repo"))
	require.NotNil(t, cmd.Flags().Lookup("allow-write"))
}

func TestNonInteractiveInput(t *testing.T) {
	username, password, err := nonInteractiveInput{}.PromptUsernameAndPassword()
	require.Empty(t, username)
	require.Empty(t, password)
	require.EqualError(t, err, "interactive login is not available in mcp mode")
}

func TestMCPCommand_EndsOnEOFStdin(t *testing.T) {
	empty, err := os.CreateTemp(t.TempDir(), "stdin")
	require.NoError(t, err)
	require.NoError(t, empty.Close())
	in, err := os.Open(empty.Name())
	require.NoError(t, err)
	defer in.Close()

	out, err := os.CreateTemp(t.TempDir(), "stdout")
	require.NoError(t, err)
	defer out.Close()

	oldStdin, oldStdout := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = in, out
	defer func() { os.Stdin, os.Stdout = oldStdin, oldStdout }()

	cmd := newMCPCommand(newServeTestClient, fakeStorage{})
	cmd.SetArgs([]string{})
	require.NoError(t, cmd.Execute())
}
