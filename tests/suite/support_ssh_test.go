package suite_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"golang.org/x/crypto/ssh"
)

// newSSHKeyPair writes an OpenSSH ed25519 private key into dir and returns its
// path plus the authorized_keys line to register.
func newSSHKeyPair(dir string) (keyPath, publicKey string) {
	GinkgoHelper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	Expect(err).NotTo(HaveOccurred())

	block, err := ssh.MarshalPrivateKey(priv, "")
	Expect(err).NotTo(HaveOccurred())
	keyPath = filepath.Join(dir, "id_ed25519")
	Expect(os.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600)).To(Succeed())

	signer, err := ssh.NewSignerFromKey(priv)
	Expect(err).NotTo(HaveOccurred())
	publicKey = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))) + " e2e@test"
	return keyPath, publicKey
}
