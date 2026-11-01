// Package chunkproof implements proof of possession for deduplicated chunks.
// A chunk is stored once per content hash, so the hash alone must never be
// enough to reference it: a project proves it holds the bytes by answering a
// server-issued challenge with BLAKE3(challenge || bytes).
package chunkproof

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/zeebo/blake3"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

const (
	nonceBytes  = 16
	macDomain   = "nipa-chunk-proof:v1"
	expiryBytes = 8
)

// ErrInvalidChallenge is returned for a malformed, forged, expired or misbound
// challenge.
var ErrInvalidChallenge = errors.New("invalid chunk proof challenge")

// NewChallenge issues a challenge bound to one project and chunk hash. It is
// stateless: the signature carries everything needed to verify it later.
func NewChallenge(key string, projectID snow.ID, hash domain.Hash, expires time.Time) (string, error) {
	nonce := make([]byte, nonceBytes)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	payload := make([]byte, expiryBytes+nonceBytes)
	binary.BigEndian.PutUint64(payload, uint64(expires.Unix()))
	copy(payload[expiryBytes:], nonce)
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	return encoded + "." + sign(key, projectID, hash, encoded), nil
}

// VerifyChallenge checks the signature, the project and hash binding and the
// expiry of a challenge.
func VerifyChallenge(key string, projectID snow.ID, hash domain.Hash, challenge string, now time.Time) error {
	encoded, sig, ok := strings.Cut(challenge, ".")
	if !ok || encoded == "" || sig == "" {
		return ErrInvalidChallenge
	}
	if !hmac.Equal([]byte(sig), []byte(sign(key, projectID, hash, encoded))) {
		return ErrInvalidChallenge
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(payload) != expiryBytes+nonceBytes {
		return ErrInvalidChallenge
	}
	if now.Unix() > int64(binary.BigEndian.Uint64(payload)) {
		return ErrInvalidChallenge
	}
	return nil
}

// Proof answers a challenge for the given stored chunk bytes.
func Proof(challenge string, data []byte) string {
	h := blake3.New()
	_, _ = h.Write([]byte(challenge))
	_, _ = h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

// Equal compares two proofs in constant time.
func Equal(a, b string) bool {
	return hmac.Equal([]byte(a), []byte(b))
}

func sign(key string, projectID snow.ID, hash domain.Hash, encoded string) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(macDomain))
	var id [8]byte
	binary.BigEndian.PutUint64(id[:], uint64(projectID.Int64()))
	mac.Write(id[:])
	mac.Write(hash[:])
	mac.Write([]byte(encoded))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
