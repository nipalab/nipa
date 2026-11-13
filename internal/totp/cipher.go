package totp

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"errors"
)

const keyLabel = "nipa mfa totp v1"

// MinKeyBytes is the minimum length of the configured key material.
const MinKeyBytes = 32

var ErrDecrypt = errors.New("totp secret cannot be decrypted with the configured key")

// Cipher encrypts stored secrets with AES-256-GCM under a key derived from
// the configured key material.
type Cipher struct {
	aead cipher.AEAD
}

func NewCipher(material []byte) (*Cipher, error) {
	if len(material) < MinKeyBytes {
		return nil, errors.New("MFA key must be at least 32 bytes")
	}
	key, err := hkdf.Key(sha256.New, material, nil, keyLabel, 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: aead}, nil
}

// Seal encrypts secret bound to aad (the owner's id), nonce prepended.
func (c *Cipher) Seal(secret, aad []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return c.aead.Seal(nonce, nonce, secret, aad), nil
}

func (c *Cipher) Open(sealed, aad []byte) ([]byte, error) {
	n := c.aead.NonceSize()
	if len(sealed) < n {
		return nil, ErrDecrypt
	}
	secret, err := c.aead.Open(nil, sealed[:n], sealed[n:], aad)
	if err != nil {
		return nil, ErrDecrypt
	}
	return secret, nil
}
