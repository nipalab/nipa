// Package webhook delivers signed JSON payloads to project webhook endpoints.
// It is transport only: event payload construction lives in the usecase layer.
package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// SignaturePrefix marks the signature scheme so it can be upgraded later.
const SignaturePrefix = "sha256="

// Sign returns the value for the X-Nipa-Signature header: the hex HMAC-SHA256
// of the exact request body using the webhook secret as the key.
func Sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return SignaturePrefix + hex.EncodeToString(mac.Sum(nil))
}

// Verify reports whether signature is a valid HMAC-SHA256 of body. The
// comparison is constant time.
func Verify(secret string, body []byte, signature string) bool {
	if !strings.HasPrefix(signature, SignaturePrefix) {
		return false
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(signature, SignaturePrefix))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(decoded, mac.Sum(nil))
}
