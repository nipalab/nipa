package sshkey

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

type fakePublicKey struct {
	typ string
}

func (f fakePublicKey) Type() string                        { return f.typ }
func (f fakePublicKey) Marshal() []byte                     { return nil }
func (f fakePublicKey) Verify([]byte, *ssh.Signature) error { return nil }

func authorizedKeyLine(t *testing.T, signer ssh.Signer, comment string) string {
	t.Helper()
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
	if comment != "" {
		line += " " + comment
	}
	return line
}

func ed25519Signer(t *testing.T) ssh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromKey(priv)
	require.NoError(t, err)
	return signer
}

func TestParseAuthorizedKey_Ed25519(t *testing.T) {
	signer := ed25519Signer(t)
	line := authorizedKeyLine(t, signer, "alice@laptop")

	parsed, err := ParseAuthorizedKey(line)
	require.NoError(t, err)
	require.Equal(t, ssh.KeyAlgoED25519, parsed.Type)
	require.Equal(t, "alice@laptop", parsed.Comment)
	require.Equal(t, ssh.FingerprintSHA256(signer.PublicKey()), parsed.Fingerprint)
	require.True(t, strings.HasPrefix(parsed.Fingerprint, "SHA256:"))
	require.NotEmpty(t, parsed.Blob)
}

func TestParseAuthorizedKey_ECDSA(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromKey(priv)
	require.NoError(t, err)

	parsed, err := ParseAuthorizedKey(authorizedKeyLine(t, signer, ""))
	require.NoError(t, err)
	require.Equal(t, ssh.KeyAlgoECDSA256, parsed.Type)
	require.Empty(t, parsed.Comment)
}

func TestParseAuthorizedKey_RSA(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromKey(priv)
	require.NoError(t, err)

	parsed, err := ParseAuthorizedKey(authorizedKeyLine(t, signer, "rsa key"))
	require.NoError(t, err)
	require.Equal(t, ssh.KeyAlgoRSA, parsed.Type)
}

func TestParseAuthorizedKey_Empty(t *testing.T) {
	_, err := ParseAuthorizedKey("   ")
	require.ErrorContains(t, err, "must not be empty")
}

func TestParseAuthorizedKey_Malformed(t *testing.T) {
	_, err := ParseAuthorizedKey("not a key")
	require.ErrorContains(t, err, "invalid public key")
}

func TestValidateSupported_RSA1024(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 1024)
	require.NoError(t, err)
	pub, err := ssh.NewPublicKey(&priv.PublicKey)
	require.NoError(t, err)

	err = ValidateSupported(pub)
	require.ErrorContains(t, err, "at least 2048 bits")
}

func TestValidateSupported_RejectsUnknownTypes(t *testing.T) {
	require.ErrorContains(t, ValidateSupported(fakePublicKey{typ: "ssh-dss"}), "unsupported key type")
	require.ErrorContains(t, ValidateSupported(fakePublicKey{typ: "ssh-edge"}), "unsupported key type")
}

func TestValidateSupported_RejectsCertificates(t *testing.T) {
	err := ValidateSupported(fakePublicKey{typ: "ssh-ed25519-cert-v01@openssh.com"})
	require.ErrorContains(t, err, "certificates are not supported")
}

func signWire(t *testing.T, signer ssh.Signer, payload []byte) []byte {
	t.Helper()
	sig, err := signer.Sign(rand.Reader, payload)
	require.NoError(t, err)
	return ssh.Marshal(sig)
}

func TestVerify_RoundTrip(t *testing.T) {
	signer := ed25519Signer(t)
	payload := []byte("nipa-login:v1:nonce")

	pub, err := ssh.ParsePublicKey(signer.PublicKey().Marshal())
	require.NoError(t, err)
	require.NoError(t, Verify(pub, payload, signWire(t, signer, payload)))
}

func TestVerify_WrongPayload(t *testing.T) {
	signer := ed25519Signer(t)

	err := Verify(signer.PublicKey(), []byte("tampered"), signWire(t, signer, []byte("expected")))
	require.ErrorContains(t, err, "verification failed")
}

func TestVerify_InvalidSignature(t *testing.T) {
	signer := ed25519Signer(t)

	err := Verify(signer.PublicKey(), []byte("payload"), []byte("junk"))
	require.ErrorContains(t, err, "invalid signature")
}

func TestVerify_WrongKey(t *testing.T) {
	alice := ed25519Signer(t)
	bob := ed25519Signer(t)

	err := Verify(bob.PublicKey(), []byte("payload"), signWire(t, alice, []byte("payload")))
	require.ErrorContains(t, err, "verification failed")
}
