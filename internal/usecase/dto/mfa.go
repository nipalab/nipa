package dto

import "time"

type MFAStatus struct {
	Available         bool
	TOTPEnabled       bool
	ConfirmedAt       *time.Time
	RecoveryCodesLeft int64
}

type TOTPEnrollment struct {
	Secret string
	URI    string
}

type RecoveryCodes struct {
	Codes []string
}
