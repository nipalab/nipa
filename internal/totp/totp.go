// Package totp implements RFC 6238 time-based one-time passwords (SHA-1, six
// digits, 30 second steps), single-use recovery codes and the encryption of
// stored secrets.
package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	Digits      = 6
	Period      = 30
	SecretBytes = 20
	// Skew is how many steps either side of the current one are accepted.
	Skew = 1
)

var secretEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

func NewSecret() ([]byte, error) {
	secret := make([]byte, SecretBytes)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	return secret, nil
}

func EncodeSecret(secret []byte) string {
	return secretEncoding.EncodeToString(secret)
}

func Step(t time.Time) int64 {
	return t.Unix() / Period
}

// Code returns the code of one time step.
func Code(secret []byte, step int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	mac := hmac.New(sha1.New, secret)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", value%1_000_000)
}

// NormalizeCode strips the spaces and dashes people type into a code.
func NormalizeCode(code string) string {
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '-' || r == '\t' {
			return -1
		}
		return r
	}, code)
}

// IsCode reports whether code has the shape of a TOTP code.
func IsCode(code string) bool {
	if len(code) != Digits {
		return false
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// Match checks code against the steps around now, skipping every step at or
// below lastStep so an accepted code cannot be replayed. It returns the
// matched step.
func Match(secret []byte, code string, now time.Time, lastStep int64) (int64, bool) {
	code = NormalizeCode(code)
	if !IsCode(code) {
		return 0, false
	}
	current := Step(now)
	matched := int64(0)
	ok := false
	for step := current - Skew; step <= current+Skew; step++ {
		if step <= lastStep {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(Code(secret, step)), []byte(code)) == 1 && !ok {
			matched, ok = step, true
		}
	}
	return matched, ok
}

// URI builds the otpauth:// URI authenticator apps import from a QR code.
func URI(issuer, account string, secret []byte) string {
	label := url.PathEscape(issuer) + ":" + url.PathEscape(account)
	q := url.Values{}
	q.Set("secret", EncodeSecret(secret))
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprint(Digits))
	q.Set("period", fmt.Sprint(Period))
	return "otpauth://totp/" + label + "?" + q.Encode()
}
