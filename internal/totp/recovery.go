package totp

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const (
	RecoveryCodeCount  = 10
	RecoveryCodeLength = 10
)

// recoveryAlphabet leaves out characters that are easy to misread (0/O, 1/I/L).
const recoveryAlphabet = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"

// NewRecoveryCodes returns n codes formatted as XXXXX-XXXXX.
func NewRecoveryCodes(n int) ([]string, error) {
	codes := make([]string, 0, n)
	seen := make(map[string]bool, n)
	for len(codes) < n {
		code, err := newRecoveryCode()
		if err != nil {
			return nil, err
		}
		if seen[code] {
			continue
		}
		seen[code] = true
		codes = append(codes, code[:RecoveryCodeLength/2]+"-"+code[RecoveryCodeLength/2:])
	}
	return codes, nil
}

func newRecoveryCode() (string, error) {
	limit := byte(256 - 256%len(recoveryAlphabet))
	out := make([]byte, 0, RecoveryCodeLength)
	buf := make([]byte, RecoveryCodeLength*2)
	for len(out) < RecoveryCodeLength {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		for _, b := range buf {
			if b >= limit {
				continue
			}
			out = append(out, recoveryAlphabet[int(b)%len(recoveryAlphabet)])
			if len(out) == RecoveryCodeLength {
				break
			}
		}
	}
	return string(out), nil
}

// NormalizeRecoveryCode uppercases a typed code and drops separators; it
// returns "" when the result cannot be a recovery code.
func NormalizeRecoveryCode(code string) string {
	code = strings.ToUpper(NormalizeCode(code))
	if len(code) != RecoveryCodeLength {
		return ""
	}
	for _, r := range code {
		if !strings.ContainsRune(recoveryAlphabet, r) {
			return ""
		}
	}
	return code
}

// HashRecoveryCode hashes a code for storage; it normalizes first, so the
// displayed and the typed form hash the same.
func HashRecoveryCode(code string) string {
	sum := sha256.Sum256([]byte(NormalizeRecoveryCode(code)))
	return hex.EncodeToString(sum[:])
}
