package cli

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/sshkey"
	"github.com/nipalab/nipa/internal/client/usecase"
)

type fakeSSHExecutor struct {
	sshResult      *domain.LoginResult
	sshErr         error
	passwordResult *domain.LoginResult
	passwordErr    error
	lastEmail      string
	lastUsername   string
	lastPassword   string
}

func (f *fakeSSHExecutor) LoginWithUsernamePassword(_ context.Context, _, username, password string) (*domain.LoginResult, error) {
	f.lastUsername = username
	f.lastPassword = password
	if f.passwordErr != nil {
		return nil, f.passwordErr
	}
	if f.passwordResult != nil {
		return f.passwordResult, nil
	}
	return &domain.LoginResult{AccessToken: "password-tok"}, nil
}

func (f *fakeSSHExecutor) LoginWithRefreshToken(_ context.Context, _, _ string) (*domain.LoginResult, error) {
	return &domain.LoginResult{AccessToken: "tok"}, nil
}

func (f *fakeSSHExecutor) BeginLoginSSHKey(_ context.Context, _, email string) (*domain.SSHKeyChallenge, error) {
	f.lastEmail = email
	return &domain.SSHKeyChallenge{Nonce: "nonce", ExpiresIn: 60}, nil
}

func (f *fakeSSHExecutor) CompleteLoginSSHKey(_ context.Context, _, _, _ string, _, _ []byte) (*domain.LoginResult, error) {
	return f.sshResult, f.sshErr
}

func withStdin(t *testing.T, file *os.File) {
	t.Helper()
	old := os.Stdin
	os.Stdin = file
	t.Cleanup(func() { os.Stdin = old })
}

func isolateLoginEnv(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("SSH_AUTH_SOCK", "")
	t.Setenv(sshkey.EnvSSHKey, "")
}

func writeLoginTestKey(t *testing.T) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	block, err := ssh.MarshalPrivateKey(priv, "")
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "id_ed25519")
	require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(block), 0o600))
	return path
}

func newLoginCli(executor usecaseLoginExecutor) (*Cli, *fakeConnector, *fakeStorage) {
	storage := &fakeStorage{}
	connector := &fakeConnector{}
	auth := usecase.NewAuth(executor, storage, &fakeInput{})
	return NewCli(&fakeUsecaseContainer{auth: auth}, connector), connector, storage
}

type usecaseLoginExecutor interface {
	LoginWithUsernamePassword(ctx context.Context, host, username, password string) (*domain.LoginResult, error)
}

func runLoginCmd(t *testing.T, cli *Cli, dir string, args ...string) (string, error) {
	t.Helper()
	oldWD, err := os.Getwd()
	require.NoError(t, err)
	defer func() { require.NoError(t, os.Chdir(oldWD)) }()
	if dir != "" {
		require.NoError(t, os.Chdir(dir))
	}

	var buf bytes.Buffer
	cmd := cli.setupLoginCmd()
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return buf.String(), err
}

func TestLoginCmd_HostFlagAndSSHKey(t *testing.T) {
	isolateLoginEnv(t)
	keyPath := writeLoginTestKey(t)
	cli, connector, storage := newLoginCli(&fakeSSHExecutor{
		sshResult: &domain.LoginResult{AccessToken: "ssh-tok", Host: "nipa.example.com"},
	})

	out, err := runLoginCmd(t, cli, "",
		"--host", "nipa.example.com",
		"--username", "alice@example.com",
		"--ssh-key", keyPath,
	)
	require.NoError(t, err)
	require.Equal(t, "nipa.example.com", connector.lastHost)
	require.Contains(t, out, "Logged in to nipa.example.com as alice@example.com using SSH key")
	require.Len(t, storage.saved, 1)
	require.Equal(t, "ssh-tok", storage.saved[0].AccessToken)
	require.Equal(t, "alice@example.com", storage.saved[0].Email)
}

func TestLoginCmd_HostFlagAcceptsURL(t *testing.T) {
	isolateLoginEnv(t)
	keyPath := writeLoginTestKey(t)
	cli, connector, _ := newLoginCli(&fakeSSHExecutor{
		sshResult: &domain.LoginResult{AccessToken: "ssh-tok"},
	})

	_, err := runLoginCmd(t, cli, "",
		"--host", "http://nipa.example.com:8080/org/project",
		"--username", "alice@example.com",
		"--ssh-key", keyPath,
	)
	require.NoError(t, err)
	require.Equal(t, "nipa.example.com:8080", connector.lastHost)
}

func TestLoginCmd_HostFromRepoConfig(t *testing.T) {
	isolateLoginEnv(t)
	keyPath := writeLoginTestKey(t)
	repo := setupRepo(t, "main")
	cli, connector, _ := newLoginCli(&fakeSSHExecutor{
		sshResult: &domain.LoginResult{AccessToken: "ssh-tok"},
	})

	_, err := runLoginCmd(t, cli, repo,
		"--username", "alice@example.com",
		"--ssh-key", keyPath,
	)
	require.NoError(t, err)
	require.Equal(t, "example.com", connector.lastHost)
}

func TestLoginCmd_MissingHost(t *testing.T) {
	isolateLoginEnv(t)
	cli, _, _ := newLoginCli(&fakeSSHExecutor{})

	_, err := runLoginCmd(t, cli, t.TempDir(),
		"--username", "alice@example.com",
		"--ssh-key", writeLoginTestKey(t),
	)
	require.ErrorContains(t, err, "host is required")
}

func TestLoginCmd_MissingSSHKey(t *testing.T) {
	isolateLoginEnv(t)
	cli, _, _ := newLoginCli(&fakeSSHExecutor{})

	_, err := runLoginCmd(t, cli, "",
		"--host", "nipa.example.com",
		"--username", "alice@example.com",
		"--ssh-key", filepath.Join(t.TempDir(), "missing"),
	)
	require.ErrorContains(t, err, "read SSH key")
}

func TestLoginCmd_SSHFailureWithExplicitKey(t *testing.T) {
	isolateLoginEnv(t)
	keyPath := writeLoginTestKey(t)
	cli, _, _ := newLoginCli(&fakeSSHExecutor{
		sshErr: domain.NewUserError("invalid email or SSH key"),
	})

	_, err := runLoginCmd(t, cli, "",
		"--host", "nipa.example.com",
		"--username", "alice@example.com",
		"--ssh-key", keyPath,
	)
	require.ErrorContains(t, err, "invalid email or SSH key")
}

func TestLoginCmd_InvalidRepoURL(t *testing.T) {
	isolateLoginEnv(t)
	repo := setupRepoURL(t, "main", "not-a-url")
	cli, _, _ := newLoginCli(&fakeSSHExecutor{})

	_, err := runLoginCmd(t, cli, repo,
		"--username", "alice@example.com",
		"--ssh-key", writeLoginTestKey(t),
	)
	require.ErrorContains(t, err, "invalid URL scheme")
}

func TestLoginCmd_ConnectError(t *testing.T) {
	isolateLoginEnv(t)
	cli, connector, _ := newLoginCli(&fakeSSHExecutor{})
	connector.err = errors.New("connection refused")

	_, err := runLoginCmd(t, cli, "",
		"--host", "nipa.example.com",
		"--username", "alice@example.com",
		"--ssh-key", writeLoginTestKey(t),
	)
	require.ErrorContains(t, err, "connection refused")
}

func TestLoginCmd_UsernamePromptError(t *testing.T) {
	isolateLoginEnv(t)
	cli, _, _ := newLoginCli(&fakeSSHExecutor{})

	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	t.Cleanup(func() { _ = reader.Close() })
	withStdin(t, reader)

	_, err = runLoginCmd(t, cli, "", "--host", "nipa.example.com")
	require.Error(t, err)
}
