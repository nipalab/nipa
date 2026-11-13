package domain

import (
	"time"

	"github.com/nipalab/nipa/internal/snow"
)

// Second factors recorded on sessions and login audit entries.
const (
	MFATOTP         = "totp"
	MFARecoveryCode = "recovery_code"
)

type UserTOTP struct {
	UserID      snow.ID
	SecretEnc   []byte
	ConfirmedAt *time.Time
	LastStep    int64
	CreatedAt   time.Time
}

func (t *UserTOTP) Confirmed() bool {
	return t != nil && t.ConfirmedAt != nil
}

// MFAChallenge is a login that passed its first factor and waits for a code.
type MFAChallenge struct {
	TokenHash string
	UserID    snow.ID
	Method    string
	Attempts  int64
	ExpiresAt time.Time
	CreatedAt time.Time
}
