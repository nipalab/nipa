package cli

import (
	"bufio"
	"os"
	"strings"
	"testing"

	"github.com/creack/pty"
	"github.com/stretchr/testify/require"
)

func TestNewPrompter(t *testing.T) {
	p := NewPrompter()
	require.NotNil(t, p)
	require.NotNil(t, p.reader)
}

func TestPrompter_PromptUsernameAndPassword(t *testing.T) {
	ptmx, tty, err := pty.Open()
	require.NoError(t, err)
	defer func() { _ = ptmx.Close() }()
	defer func() { _ = tty.Close() }()

	oldStdin := os.Stdin
	os.Stdin = tty
	defer func() { os.Stdin = oldStdin }()

	_, err = ptmx.WriteString("apin\nsecret\n")
	require.NoError(t, err)

	username, password, err := NewPrompter().PromptUsernameAndPassword()
	require.NoError(t, err)
	require.Equal(t, "apin", username)
	require.Equal(t, "secret", password)
}

func TestPrompter_PromptTrimsUsernameWhitespace(t *testing.T) {
	ptmx, tty, err := pty.Open()
	require.NoError(t, err)
	defer func() { _ = ptmx.Close() }()
	defer func() { _ = tty.Close() }()

	oldStdin := os.Stdin
	os.Stdin = tty
	defer func() { os.Stdin = oldStdin }()

	_, err = ptmx.WriteString("  apin  \nsecret\n")
	require.NoError(t, err)

	username, password, err := NewPrompter().PromptUsernameAndPassword()
	require.NoError(t, err)
	require.Equal(t, "apin", username)
	require.Equal(t, "secret", password)
}

func TestPrompter_Reader(t *testing.T) {
	p := &Prompter{reader: bufio.NewReader(strings.NewReader("user\n"))}
	line, err := p.reader.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "user\n", line)
}
