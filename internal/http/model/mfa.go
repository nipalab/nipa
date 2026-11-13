package model

import "time"

// MFAChallengeResponse answers a login whose account must still present a
// second factor; no session or refresh cookie is issued yet.
type MFAChallengeResponse struct {
	MFARequired  bool   `json:"mfa_required"`
	MFAChallenge string `json:"mfa_challenge"`
	ExpiresIn    int    `json:"expires_in"`
}

type MFAVerifyRequest struct {
	Challenge string `json:"challenge"`
	Code      string `json:"code"`
}

type MFACodeRequest struct {
	Code string `json:"code"`
}

type MFAStartRequest struct {
	Password string `json:"password"`
}

type TOTPStatusResponse struct {
	Enabled     bool       `json:"enabled"`
	ConfirmedAt *time.Time `json:"confirmed_at,omitempty"`
}

type MFAStatusResponse struct {
	Available         bool               `json:"available"`
	TOTP              TOTPStatusResponse `json:"totp"`
	RecoveryCodesLeft int64              `json:"recovery_codes_left"`
}

type TOTPEnrollmentResponse struct {
	Secret string `json:"secret"`
	URI    string `json:"uri"`
}

type RecoveryCodesResponse struct {
	RecoveryCodes []string `json:"recovery_codes"`
}
