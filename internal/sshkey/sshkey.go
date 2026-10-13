// Package sshkey parses and validates OpenSSH public keys and verifies SSH
// signatures. It is shared by the server key registry and the SSH login flow.
package sshkey

import (
	"crypto/rsa"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"
)

// MinRSABits is the smallest accepted RSA modulus.
const MinRSABits = 2048

const loginPayloadPrefix = "nipa-login:v1:"

// LoginPayload returns the message a client signs to complete an SSH key login.
func LoginPayload(nonce string) []byte {
	return []byte(loginPayloadPrefix + nonce)
}

// ParsedKey is a normalized OpenSSH public key.
type ParsedKey struct {
	Type        string
	Blob        string
	Fingerprint string
	Comment     string
	PublicKey   ssh.PublicKey
}

// ParseAuthorizedKey parses one OpenSSH authorized_keys line and rejects key
// types the server does not support.
func ParseAuthorizedKey(line string) (*ParsedKey, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil, errors.New("public key must not be empty")
	}
	pub, comment, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil {
		return nil, fmt.Errorf("invalid public key: %w", err)
	}
	if err := ValidateSupported(pub); err != nil {
		return nil, err
	}
	return &ParsedKey{
		Type:        pub.Type(),
		Blob:        base64.StdEncoding.EncodeToString(pub.Marshal()),
		Fingerprint: ssh.FingerprintSHA256(pub),
		Comment:     strings.TrimSpace(comment),
		PublicKey:   pub,
	}, nil
}

// ValidateSupported accepts ed25519, ECDSA (nistp256/384/521) and RSA keys of
// at least MinRSABits bits. DSA, certificates and any other type are rejected.
func ValidateSupported(pub ssh.PublicKey) error {
	switch pub.Type() {
	case ssh.KeyAlgoED25519, ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521:
		return nil
	case ssh.KeyAlgoRSA:
		cryptoKey, ok := pub.(ssh.CryptoPublicKey)
		if !ok {
			return errors.New("unsupported RSA public key")
		}
		rsaKey, ok := cryptoKey.CryptoPublicKey().(*rsa.PublicKey)
		if !ok {
			return errors.New("unsupported RSA public key")
		}
		if rsaKey.N.BitLen() < MinRSABits {
			return fmt.Errorf("RSA key must be at least %d bits", MinRSABits)
		}
		return nil
	default:
		if strings.HasSuffix(pub.Type(), "-cert-v01@openssh.com") {
			return errors.New("SSH certificates are not supported, add the plain public key instead")
		}
		return fmt.Errorf("unsupported key type %q", pub.Type())
	}
}

// Verify checks an SSH signature (wire format) over payload with pub.
func Verify(pub ssh.PublicKey, payload, signature []byte) error {
	sig := &ssh.Signature{}
	if err := ssh.Unmarshal(signature, sig); err != nil {
		return fmt.Errorf("invalid signature: %w", err)
	}
	if err := pub.Verify(payload, sig); err != nil {
		return errors.New("signature verification failed")
	}
	return nil
}
