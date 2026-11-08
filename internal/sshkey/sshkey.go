// Package sshkey parses and validates OpenSSH public keys and verifies SSH
// signatures. It is shared by the server key registry and the SSH login flow.
package sshkey

import (
	"crypto/rsa"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"golang.org/x/crypto/ssh"
)

// MinRSABits is the smallest accepted RSA modulus.
const MinRSABits = 2048

const loginPayloadPrefix = "nipa-login:v2:"

// LoginPayload returns the message a client signs to complete an SSH key
// login: it binds the challenge to the canonical origin of the server.
func LoginPayload(origin, nonce string) []byte {
	return []byte(loginPayloadPrefix + origin + ":" + nonce)
}

// CanonicalOrigin reduces a host, host:port or http(s) URL to the form both
// sides sign: lowercase host without trailing dot, port only when it is not 80
// or 443, no scheme, userinfo or path.
func CanonicalOrigin(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("origin must not be empty")
	}
	if idx := strings.Index(raw, "://"); idx >= 0 {
		scheme := strings.ToLower(raw[:idx])
		if scheme != "http" && scheme != "https" {
			return "", fmt.Errorf("unsupported scheme %q", scheme)
		}
	} else {
		raw = "//" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil {
		return "", fmt.Errorf("invalid origin %q", raw)
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" {
		return "", fmt.Errorf("invalid origin %q", raw)
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	port := u.Port()
	if port == "" || port == "80" || port == "443" {
		return host, nil
	}
	return host + ":" + port, nil
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
	if pub.Type() == ssh.KeyAlgoRSA && sig.Format != ssh.KeyAlgoRSASHA256 && sig.Format != ssh.KeyAlgoRSASHA512 {
		return errors.New("signature verification failed")
	}
	if err := pub.Verify(payload, sig); err != nil {
		return errors.New("signature verification failed")
	}
	return nil
}
