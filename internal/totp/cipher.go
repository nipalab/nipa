package totp

import (
	"errors"

	"github.com/nipalab/nipa/internal/sealbox"
)

const keyLabel = "nipa mfa totp v1"

// MinKeyBytes is the minimum length of the configured key material.
const MinKeyBytes = sealbox.MinKeyBytes

var ErrDecrypt = errors.New("totp secret cannot be decrypted with the configured key")

// Cipher encrypts stored secrets with AES-256-GCM under a key derived from
// the configured key material.
type Cipher struct {
	box *sealbox.Box
}

func NewCipher(material []byte) (*Cipher, error) {
	if len(material) < MinKeyBytes {
		return nil, errors.New("MFA key must be at least 32 bytes")
	}
	box, err := sealbox.New(material, keyLabel)
	if err != nil {
		return nil, err
	}
	return &Cipher{box: box}, nil
}

// Seal encrypts secret bound to aad (the owner's id), nonce prepended.
func (c *Cipher) Seal(secret, aad []byte) ([]byte, error) {
	return c.box.Seal(secret, aad)
}

func (c *Cipher) Open(sealed, aad []byte) ([]byte, error) {
	secret, err := c.box.Open(sealed, aad)
	if err != nil {
		return nil, ErrDecrypt
	}
	return secret, nil
}
