//go:build linux

package cli

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/usecase"
)

func writeLoginConfig(t *testing.T, sshKeyPath string) {
	t.Helper()
	dir := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "nipa")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "config.json"),
		[]byte(`{"sshKeyPath":"`+sshKeyPath+`"}`),
		0o600,
	))
}

func feedStdin(t *testing.T, lines ...string) {
	t.Helper()
	master, slave := openPty(t)
	t.Cleanup(func() { _ = master.Close(); _ = slave.Close() })
	withStdin(t, slave)
	go func() {
		for _, line := range lines {
			_, _ = master.WriteString(line + "\n")
		}
	}()
}

func TestLoginCmd_PromptsUsernameAndPassword(t *testing.T) {
	isolateLoginEnv(t)
	executor := &fakeSSHExecutor{passwordResult: &domain.LoginResult{AccessToken: "password-tok"}}
	cli, connector, storage := newLoginCli(executor)

	feedStdin(t, "alice@example.com", "secret")

	out, err := runLoginCmd(t, cli, "", "--host", "nipa.example.com")
	require.NoError(t, err)
	require.Contains(t, out, "Logged in to nipa.example.com as alice@example.com")
	require.Equal(t, "nipa.example.com", connector.lastHost)
	require.Equal(t, "alice@example.com", executor.lastUsername)
	require.Equal(t, "secret", executor.lastPassword)
	require.Equal(t, "password-tok", storage.saved[0].AccessToken)
	require.Equal(t, "alice@example.com", storage.saved[0].Email)
}

func TestLoginCmd_SSHFailureFallsBackToPassword(t *testing.T) {
	isolateLoginEnv(t)
	writeLoginConfig(t, writeLoginTestKey(t))
	executor := &fakeSSHExecutor{
		sshErr:         domain.NewUserError("invalid email or SSH key"),
		passwordResult: &domain.LoginResult{AccessToken: "password-tok"},
	}
	cli, _, storage := newLoginCli(executor)

	feedStdin(t, "secret")

	out, err := runLoginCmd(t, cli, "",
		"--host", "nipa.example.com",
		"--username", "alice@example.com",
	)
	require.NoError(t, err)
	require.Contains(t, out, "SSH key login failed: invalid email or SSH key")
	require.Contains(t, out, "Logged in to nipa.example.com as alice@example.com")
	require.Equal(t, "secret", executor.lastPassword)
	require.Equal(t, "password-tok", storage.saved[0].AccessToken)
}

func TestLoginCmd_EmptyUsername(t *testing.T) {
	isolateLoginEnv(t)
	cli, _, _ := newLoginCli(&fakeSSHExecutor{})

	feedStdin(t, "")

	_, err := runLoginCmd(t, cli, "", "--host", "nipa.example.com")
	require.ErrorContains(t, err, "username cannot be empty")
}

func TestLoginCmd_PasswordPromptError(t *testing.T) {
	isolateLoginEnv(t)
	cli, _, _ := newLoginCli(&fakeSSHExecutor{})
	withStdin(t, notATerminal(t))

	_, err := runLoginCmd(t, cli, "",
		"--host", "nipa.example.com",
		"--username", "alice@example.com",
	)
	require.Error(t, err)
}

func TestLoginCmd_PasswordLoginError(t *testing.T) {
	isolateLoginEnv(t)
	executor := &fakeSSHExecutor{passwordErr: domain.NewUserError("invalid email or password")}
	cli, _, _ := newLoginCli(executor)

	feedStdin(t, "secret")

	_, err := runLoginCmd(t, cli, "",
		"--host", "nipa.example.com",
		"--username", "alice@example.com",
	)
	require.ErrorContains(t, err, "invalid email or password")
}

func TestLoginCmd_OTPFlag(t *testing.T) {
	isolateLoginEnv(t)
	t.Setenv(usecase.EnvOTP, "")
	executor := &fakeSSHExecutor{mfaChallenge: "ch-1"}
	cli, _, storage := newLoginCli(executor)

	feedStdin(t, "secret")

	out, err := runLoginCmd(t, cli, "", "--host", "nipa.example.com", "--username", "alice@example.com", "--otp", "123456")
	require.NoError(t, err)
	require.Contains(t, out, "Logged in to nipa.example.com as alice@example.com")
	require.Equal(t, []string{"123456"}, executor.codes)
	require.Equal(t, "mfa-tok", storage.saved[0].AccessToken)
	require.Equal(t, "alice@example.com", storage.saved[0].Email)
}

func TestLoginCmd_OTPFromEnv(t *testing.T) {
	isolateLoginEnv(t)
	t.Setenv(usecase.EnvOTP, "654321")
	executor := &fakeSSHExecutor{mfaChallenge: "ch-1"}
	cli, _, storage := newLoginCli(executor)

	feedStdin(t, "secret")

	_, err := runLoginCmd(t, cli, "", "--host", "nipa.example.com", "--username", "alice@example.com")
	require.NoError(t, err)
	require.Equal(t, []string{"654321"}, executor.codes)
	require.Len(t, storage.saved, 1)
}

func TestLoginCmd_PromptsForOTP(t *testing.T) {
	isolateLoginEnv(t)
	t.Setenv(usecase.EnvOTP, "")
	executor := &fakeSSHExecutor{mfaChallenge: "ch-1", mfaErrs: []error{domain.NewUserError("invalid code"), nil}}
	cli, _, storage := newLoginCli(executor)

	feedStdin(t, "secret", "000000", "123456")

	_, err := runLoginCmd(t, cli, "", "--host", "nipa.example.com", "--username", "alice@example.com")
	require.NoError(t, err)
	require.Equal(t, []string{"000000", "123456"}, executor.codes)
	require.Equal(t, "mfa-tok", storage.saved[0].AccessToken)
}

func TestLoginCmd_WrongOTPFlagFails(t *testing.T) {
	isolateLoginEnv(t)
	t.Setenv(usecase.EnvOTP, "")
	executor := &fakeSSHExecutor{mfaChallenge: "ch-1", mfaErrs: []error{domain.NewUserError("invalid code")}}
	cli, _, storage := newLoginCli(executor)

	feedStdin(t, "secret")

	_, err := runLoginCmd(t, cli, "", "--host", "nipa.example.com", "--username", "alice@example.com", "--otp", "000000")
	require.ErrorContains(t, err, "invalid code")
	require.Equal(t, []string{"000000"}, executor.codes)
	require.Empty(t, storage.saved)
}

func TestPrompter_PromptOTP_Pty(t *testing.T) {
	master, slave := openPty(t)
	t.Cleanup(func() { _ = master.Close(); _ = slave.Close() })
	withStdin(t, slave)

	prompter := NewPrompter()
	go func() { _, _ = master.WriteString(" 123456 \n") }()

	code, err := prompter.PromptOTP(1)
	require.NoError(t, err)
	require.Equal(t, "123456", code)
}

func TestPrompter_PromptOTP_NotTerminal(t *testing.T) {
	withStdin(t, notATerminal(t))

	_, err := NewPrompter().PromptOTP(0)
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Contains(t, domErr.Hint, "SSH key")
}

func TestPrompter_PromptOTP_ReadError(t *testing.T) {
	master, slave := openPty(t)
	t.Cleanup(func() { _ = master.Close(); _ = slave.Close() })
	withStdin(t, slave)

	_, err := (&Prompter{reader: bufio.NewReader(strings.NewReader(""))}).PromptOTP(0)
	require.ErrorIs(t, err, io.EOF)
}
