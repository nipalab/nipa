//go:build linux

package cli

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func withStdin(t *testing.T, file *os.File) {
	t.Helper()
	old := os.Stdin
	os.Stdin = file
	t.Cleanup(func() { os.Stdin = old })
}

func TestPrompter_PromptUsername_Pty(t *testing.T) {
	master, slave := openPty(t)
	t.Cleanup(func() { _ = master.Close(); _ = slave.Close() })
	withStdin(t, slave)

	prompter := NewPrompter()
	go func() { _, _ = master.WriteString("alice@example.com\n") }()

	username, err := prompter.PromptUsername()
	require.NoError(t, err)
	require.Equal(t, "alice@example.com", username)
}

func TestPrompter_PromptPassword_Pty(t *testing.T) {
	master, slave := openPty(t)
	t.Cleanup(func() { _ = master.Close(); _ = slave.Close() })
	withStdin(t, slave)

	prompter := NewPrompter()
	go func() { _, _ = master.WriteString("secret\n") }()

	password, err := prompter.PromptPassword()
	require.NoError(t, err)
	require.Equal(t, "secret", password)
}

func TestPrompter_PromptPassphrase_Pty(t *testing.T) {
	master, slave := openPty(t)
	t.Cleanup(func() { _ = master.Close(); _ = slave.Close() })
	withStdin(t, slave)

	prompter := NewPrompter()
	go func() { _, _ = master.WriteString("secret\n") }()

	passphrase, err := prompter.PromptPassphrase("/home/alice/.ssh/id_ed25519")
	require.NoError(t, err)
	require.Equal(t, "secret", passphrase)
}

func TestPrompter_PromptUsernameAndPassword_Pty(t *testing.T) {
	master, slave := openPty(t)
	t.Cleanup(func() { _ = master.Close(); _ = slave.Close() })
	withStdin(t, slave)

	prompter := NewPrompter()
	go func() {
		_, _ = master.WriteString("alice@example.com\n")
		_, _ = master.WriteString("secret\n")
	}()

	username, password, err := prompter.PromptUsernameAndPassword()
	require.NoError(t, err)
	require.Equal(t, "alice@example.com", username)
	require.Equal(t, "secret", password)
}

func notATerminal(t *testing.T) *os.File {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "notty")
	require.NoError(t, err)
	t.Cleanup(func() { _ = file.Close() })
	return file
}

func TestPrompter_PromptUsername_NotTerminal(t *testing.T) {
	withStdin(t, notATerminal(t))

	_, err := NewPrompter().PromptUsername()
	require.Error(t, err)
}

func TestPrompter_PromptPassword_NotTerminal(t *testing.T) {
	withStdin(t, notATerminal(t))

	_, err := NewPrompter().PromptPassword()
	require.Error(t, err)
}

func TestPrompter_PromptUsernameAndPassword_NotTerminal(t *testing.T) {
	withStdin(t, notATerminal(t))

	_, _, err := NewPrompter().PromptUsernameAndPassword()
	require.Error(t, err)
}

func TestPrompter_PromptUsernameAndPassword_PasswordNotTerminal(t *testing.T) {
	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() { _ = reader.Close(); _ = writer.Close() })
	_, _ = writer.WriteString("alice\n")
	require.NoError(t, writer.Close())
	withStdin(t, reader)

	_, _, err = NewPrompter().PromptUsernameAndPassword()
	require.Error(t, err)
}
