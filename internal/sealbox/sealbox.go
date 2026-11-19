// Package sealbox encrypts small secrets with AES-256-GCM under a key derived
// from configured key material with HKDF-SHA256 and a per-use label.
package sealbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"errors"
)

// MinKeyBytes is the minimum length of the key material.
const MinKeyBytes = 32

var (
	ErrShortKey = errors.New("key material must be at least 32 bytes")
	ErrOpen     = errors.New("sealed value cannot be decrypted with the configured key")
)

type Box struct {
	aead cipher.AEAD
}

func New(material []byte, label string) (*Box, error) {
	if len(material) < MinKeyBytes {
		return nil, ErrShortKey
	}
	key, err := hkdf.Key(sha256.New, material, nil, label, 32)
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
	return &Box{aead: aead}, nil
}

// Seal encrypts plain bound to aad, with the nonce prepended.
func (b *Box) Seal(plain, aad []byte) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return b.aead.Seal(nonce, nonce, plain, aad), nil
}

func (b *Box) Open(sealed, aad []byte) ([]byte, error) {
	n := b.aead.NonceSize()
	if len(sealed) < n {
		return nil, ErrOpen
	}
	plain, err := b.aead.Open(nil, sealed[:n], sealed[n:], aad)
	if err != nil {
		return nil, ErrOpen
	}
	return plain, nil
}
