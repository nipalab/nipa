// Package sshkey resolves client SSH private keys for the SSH key login flow.
package sshkey

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"github.com/nipalab/nipa/internal/client/config"
)

// EnvSSHKey names the environment variable holding the SSH private key path.
const EnvSSHKey = "NIPA_SSH_KEY"

// Signer is a resolved SSH private key.
type Signer struct {
	ssh.Signer
	Name string
}

// SignPayload signs the login challenge and returns the SSH wire-format signature.
func (s Signer) SignPayload(payload []byte) ([]byte, error) {
	sig, err := s.Sign(rand.Reader, payload)
	if err != nil {
		return nil, err
	}
	return ssh.Marshal(sig), nil
}

// Resolve returns the SSH signers to try for login, in priority order: an
// explicitly configured key (the explicit path, NIPA_SSH_KEY or the config
// sshKeyPath), otherwise the keys of a running ssh-agent followed by the
// default key files. A configured key that cannot be loaded is an error;
// auto-detected keys that cannot be loaded are skipped. Passphrase prompts are
// only used for configured keys.
func Resolve(explicitPath string, promptPassphrase func(keyPath string) (string, error)) ([]Signer, error) {
	configured, err := resolveConfiguredPath(explicitPath)
	if err != nil {
		return nil, err
	}
	if configured != "" {
		signer, err := loadSignerFile(configured, promptPassphrase)
		if err != nil {
			return nil, err
		}
		return []Signer{signer}, nil
	}

	signers := agentSigners()
	for _, path := range defaultKeyPaths() {
		signer, err := loadSignerFile(path, nil)
		if err != nil {
			continue
		}
		signers = append(signers, signer)
	}
	return signers, nil
}

func resolveConfiguredPath(explicitPath string) (string, error) {
	if path := strings.TrimSpace(explicitPath); path != "" {
		return path, nil
	}
	if path := strings.TrimSpace(os.Getenv(EnvSSHKey)); path != "" {
		return path, nil
	}
	cfg, err := config.Load()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(cfg.SSHKeyPath), nil
}

func loadSignerFile(path string, promptPassphrase func(keyPath string) (string, error)) (Signer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Signer{}, fmt.Errorf("read SSH key %s: %w", path, err)
	}
	signer, err := ssh.ParsePrivateKey(data)
	if err != nil {
		var missing *ssh.PassphraseMissingError
		if !errors.As(err, &missing) || promptPassphrase == nil {
			return Signer{}, fmt.Errorf("load SSH key %s: %w", path, err)
		}
		passphrase, perr := promptPassphrase(path)
		if perr != nil {
			return Signer{}, perr
		}
		signer, err = ssh.ParsePrivateKeyWithPassphrase(data, []byte(passphrase))
		if err != nil {
			return Signer{}, fmt.Errorf("load SSH key %s: %w", path, err)
		}
	}
	return Signer{Signer: signer, Name: path}, nil
}

func defaultKeyPaths() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	dir := filepath.Join(home, ".ssh")
	return []string{
		filepath.Join(dir, "id_ed25519"),
		filepath.Join(dir, "id_ecdsa"),
		filepath.Join(dir, "id_rsa"),
	}
}

func agentSigners() []Signer {
	socket := strings.TrimSpace(os.Getenv("SSH_AUTH_SOCK"))
	if socket == "" {
		return nil
	}
	conn, err := net.Dial("unix", socket)
	if err != nil {
		return nil
	}
	keys, err := agent.NewClient(conn).List()
	_ = conn.Close()
	if err != nil {
		return nil
	}
	signers := make([]Signer, 0, len(keys))
	for _, key := range keys {
		name := "ssh-agent"
		if key.Comment != "" {
			name = "ssh-agent:" + key.Comment
		}
		signers = append(signers, Signer{Signer: agentSigner{socket: socket, pub: key}, Name: name})
	}
	return signers
}

type agentSigner struct {
	socket string
	pub    ssh.PublicKey
}

func (a agentSigner) PublicKey() ssh.PublicKey {
	return a.pub
}

func (a agentSigner) Sign(_ io.Reader, data []byte) (*ssh.Signature, error) {
	conn, err := net.Dial("unix", a.socket)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	return agent.NewClient(conn).Sign(a.pub, data)
}
