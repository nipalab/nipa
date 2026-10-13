package sshkey

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

func generateKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	return priv
}

func writePrivateKey(t *testing.T, dir, name string, priv ed25519.PrivateKey, passphrase string) string {
	t.Helper()
	var block *pem.Block
	var err error
	if passphrase == "" {
		block, err = ssh.MarshalPrivateKey(priv, "")
	} else {
		block, err = ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte(passphrase))
	}
	require.NoError(t, err)
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(block), 0o600))
	return path
}

func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("SSH_AUTH_SOCK", "")
	t.Setenv(EnvSSHKey, "")
	return home
}

func TestResolve_ExplicitPath(t *testing.T) {
	home := isolateHome(t)
	path := writePrivateKey(t, home, "id_ed25519", generateKey(t), "")

	signers, err := Resolve(path, nil)
	require.NoError(t, err)
	require.Len(t, signers, 1)
	require.Equal(t, path, signers[0].Name)

	payload := []byte("payload")
	wire, err := signers[0].SignPayload(payload)
	require.NoError(t, err)

	sig := &ssh.Signature{}
	require.NoError(t, ssh.Unmarshal(wire, sig))
	require.NoError(t, signers[0].PublicKey().Verify(payload, sig))
}

func TestResolve_MissingExplicitPath(t *testing.T) {
	isolateHome(t)

	_, err := Resolve(filepath.Join(t.TempDir(), "missing"), nil)
	require.ErrorContains(t, err, "read SSH key")
}

func TestResolve_EnvTakesPrecedenceOverConfig(t *testing.T) {
	home := isolateHome(t)
	envPath := writePrivateKey(t, home, "env_key", generateKey(t), "")
	t.Setenv(EnvSSHKey, envPath)
	configDir := filepath.Join(home, ".config", "nipa")
	require.NoError(t, os.MkdirAll(configDir, 0o755))
	configPath := writePrivateKey(t, home, "config_key", generateKey(t), "")
	require.NoError(t, os.WriteFile(
		filepath.Join(configDir, "config.json"),
		[]byte(`{"sshKeyPath":"`+configPath+`"}`),
		0o600,
	))

	signers, err := Resolve("", nil)
	require.NoError(t, err)
	require.Len(t, signers, 1)
	require.Equal(t, envPath, signers[0].Name)
}

func TestResolve_ExplicitPathTakesPrecedenceOverEnv(t *testing.T) {
	home := isolateHome(t)
	envPath := writePrivateKey(t, home, "env_key", generateKey(t), "")
	explicitPath := writePrivateKey(t, home, "explicit_key", generateKey(t), "")
	t.Setenv(EnvSSHKey, envPath)

	signers, err := Resolve(explicitPath, nil)
	require.NoError(t, err)
	require.Len(t, signers, 1)
	require.Equal(t, explicitPath, signers[0].Name)
}

func TestResolve_ConfigPath(t *testing.T) {
	home := isolateHome(t)
	configDir := filepath.Join(home, ".config", "nipa")
	require.NoError(t, os.MkdirAll(configDir, 0o755))
	configPath := writePrivateKey(t, home, "config_key", generateKey(t), "")
	require.NoError(t, os.WriteFile(
		filepath.Join(configDir, "config.json"),
		[]byte(`{"sshKeyPath":"`+configPath+`"}`),
		0o600,
	))

	signers, err := Resolve("", nil)
	require.NoError(t, err)
	require.Len(t, signers, 1)
	require.Equal(t, configPath, signers[0].Name)
}

func TestResolve_EncryptedKeyPrompt(t *testing.T) {
	home := isolateHome(t)
	path := writePrivateKey(t, home, "id_ed25519", generateKey(t), "secret")

	prompted := ""
	signers, err := Resolve(path, func(keyPath string) (string, error) {
		prompted = keyPath
		return "secret", nil
	})
	require.NoError(t, err)
	require.Len(t, signers, 1)
	require.Equal(t, path, prompted)
}

func TestResolve_EncryptedKeyWrongPassphrase(t *testing.T) {
	home := isolateHome(t)
	path := writePrivateKey(t, home, "id_ed25519", generateKey(t), "secret")

	_, err := Resolve(path, func(string) (string, error) { return "wrong", nil })
	require.ErrorContains(t, err, "load SSH key")
}

func TestResolve_EncryptedKeyWithoutPrompt(t *testing.T) {
	home := isolateHome(t)
	path := writePrivateKey(t, home, "id_ed25519", generateKey(t), "secret")

	_, err := Resolve(path, nil)
	require.ErrorContains(t, err, "load SSH key")
}

func TestResolve_DefaultKeyFiles(t *testing.T) {
	home := isolateHome(t)
	sshDir := filepath.Join(home, ".ssh")
	require.NoError(t, os.MkdirAll(sshDir, 0o700))
	path := writePrivateKey(t, sshDir, "id_ed25519", generateKey(t), "")

	signers, err := Resolve("", nil)
	require.NoError(t, err)
	require.Len(t, signers, 1)
	require.Equal(t, path, signers[0].Name)
}

func TestResolve_AgentKeys(t *testing.T) {
	home := isolateHome(t)
	_ = home

	serveTestAgent(t)

	signers, err := Resolve("", nil)
	require.NoError(t, err)
	require.Len(t, signers, 1)
	require.Equal(t, "ssh-agent:test@agent", signers[0].Name)

	payload := []byte("payload")
	wire, err := signers[0].SignPayload(payload)
	require.NoError(t, err)

	sig := &ssh.Signature{}
	require.NoError(t, ssh.Unmarshal(wire, sig))
	require.NoError(t, signers[0].PublicKey().Verify(payload, sig))
}

func serveTestAgent(t *testing.T) string {
	t.Helper()
	keyring := agent.NewKeyring()
	require.NoError(t, keyring.Add(agent.AddedKey{
		PrivateKey: generateKey(t),
		Comment:    "test@agent",
	}))

	dir, err := os.MkdirTemp("", "nipa-agent")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "agent.sock")

	listener, err := net.Listen("unix", socket)
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() { _ = agent.ServeAgent(keyring, conn) }()
		}
	}()

	t.Setenv("SSH_AUTH_SOCK", socket)
	return socket
}

type failingSigner struct{}

func (failingSigner) PublicKey() ssh.PublicKey { return nil }

func (failingSigner) Sign(io.Reader, []byte) (*ssh.Signature, error) {
	return nil, errors.New("sign failed")
}

func TestSignPayload_SignError(t *testing.T) {
	_, err := Signer{Signer: failingSigner{}}.SignPayload([]byte("payload"))
	require.ErrorContains(t, err, "sign failed")
}

func TestResolve_MalformedConfig(t *testing.T) {
	home := isolateHome(t)
	configDir := filepath.Join(home, ".config", "nipa")
	require.NoError(t, os.MkdirAll(configDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config.json"), []byte("{invalid"), 0o600))

	_, err := Resolve("", nil)
	require.ErrorContains(t, err, "parse")
}

func TestResolve_PassphrasePromptError(t *testing.T) {
	home := isolateHome(t)
	path := writePrivateKey(t, home, "id_ed25519", generateKey(t), "secret")

	_, err := Resolve(path, func(string) (string, error) { return "", errors.New("no tty") })
	require.ErrorContains(t, err, "no tty")
}

func TestResolve_NoHomeDirectory(t *testing.T) {
	home := isolateHome(t)
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	signers, err := Resolve("", nil)
	require.NoError(t, err)
	require.Empty(t, signers)
}

func TestResolve_AgentSocketUnavailable(t *testing.T) {
	isolateHome(t)
	t.Setenv("SSH_AUTH_SOCK", filepath.Join(t.TempDir(), "missing.sock"))

	signers, err := Resolve("", nil)
	require.NoError(t, err)
	require.Empty(t, signers)
}

func TestResolve_AgentListError(t *testing.T) {
	isolateHome(t)

	dir, err := os.MkdirTemp("", "nipa-agent-bad")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "agent.sock")

	listener, err := net.Listen("unix", socket)
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	t.Setenv("SSH_AUTH_SOCK", socket)

	signers, err := Resolve("", nil)
	require.NoError(t, err)
	require.Empty(t, signers)
}

func TestAgentSigner_Sign_SocketUnavailable(t *testing.T) {
	priv := generateKey(t)
	pub, err := ssh.NewPublicKey(priv.Public())
	require.NoError(t, err)

	signer := agentSigner{socket: filepath.Join(t.TempDir(), "missing.sock"), pub: pub}
	_, err = signer.Sign(rand.Reader, []byte("payload"))
	require.Error(t, err)
}
