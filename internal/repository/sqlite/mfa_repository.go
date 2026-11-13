package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	sqlcSqlite "github.com/nipalab/nipa/internal/repository/sqlc/sqlite"
	"github.com/nipalab/nipa/internal/snow"
)

// MFA stores TOTP secrets, recovery codes and pending login challenges. Writes
// that span several statements rely on the caller's transaction.
type MFA struct {
	queries *sqlcSqlite.Queries
}

func NewMFARepository(db *sql.DB) *MFA {
	return &MFA{queries: sqlcSqlite.New(dbtx.New(db))}
}

func (m *MFA) GetTOTP(ctx context.Context, userID snow.ID) (*domain.UserTOTP, error) {
	row, err := m.queries.UserTOTPGet(ctx, userID.Int64())
	if err != nil {
		return nil, handleError(err)
	}
	t := &domain.UserTOTP{
		UserID:    snow.ID(row.UserID),
		SecretEnc: row.SecretEnc,
		LastStep:  row.LastStep,
		CreatedAt: row.CreatedAt,
	}
	if row.ConfirmedAt.Valid {
		t.ConfirmedAt = &row.ConfirmedAt.Time
	}
	return t, nil
}

func (m *MFA) SavePendingTOTP(ctx context.Context, userID snow.ID, secretEnc []byte, at time.Time) (bool, error) {
	n, err := m.queries.UserTOTPUpsertPending(ctx, sqlcSqlite.UserTOTPUpsertPendingParams{
		UserID:    userID.Int64(),
		SecretEnc: secretEnc,
		CreatedAt: at,
	})
	return n > 0, handleError(err)
}

func (m *MFA) ConfirmTOTP(ctx context.Context, userID snow.ID, secretEnc []byte, step int64, at time.Time) (bool, error) {
	n, err := m.queries.UserTOTPConfirm(ctx, sqlcSqlite.UserTOTPConfirmParams{
		ConfirmedAt: sql.NullTime{Time: at, Valid: true},
		LastStep:    step,
		UserID:      userID.Int64(),
		SecretEnc:   secretEnc,
	})
	return n > 0, handleError(err)
}

func (m *MFA) AdvanceTOTPStep(ctx context.Context, userID snow.ID, step int64) (bool, error) {
	n, err := m.queries.UserTOTPAdvanceStep(ctx, sqlcSqlite.UserTOTPAdvanceStepParams{Step: step, UserID: userID.Int64()})
	return n > 0, handleError(err)
}

func (m *MFA) DeleteTOTP(ctx context.Context, userID snow.ID) (bool, error) {
	n, err := m.queries.UserTOTPDelete(ctx, userID.Int64())
	return n > 0, handleError(err)
}

func (m *MFA) CountConfirmedTOTP(ctx context.Context) (int64, error) {
	n, err := m.queries.UserTOTPCountConfirmed(ctx)
	return n, handleError(err)
}

func (m *MFA) ReplaceRecoveryCodes(ctx context.Context, userID snow.ID, hashes []string) error {
	if err := m.DeleteRecoveryCodes(ctx, userID); err != nil {
		return err
	}
	for _, hash := range hashes {
		err := m.queries.MFARecoveryCodeInsert(ctx, sqlcSqlite.MFARecoveryCodeInsertParams{UserID: userID.Int64(), CodeHash: hash})
		if err != nil {
			return handleError(err)
		}
	}
	return nil
}

func (m *MFA) DeleteRecoveryCodes(ctx context.Context, userID snow.ID) error {
	return handleError(m.queries.MFARecoveryCodeDeleteByUser(ctx, userID.Int64()))
}

func (m *MFA) UseRecoveryCode(ctx context.Context, userID snow.ID, hash string, at time.Time) (bool, error) {
	n, err := m.queries.MFARecoveryCodeUse(ctx, sqlcSqlite.MFARecoveryCodeUseParams{
		UsedAt:   sql.NullTime{Time: at, Valid: true},
		UserID:   userID.Int64(),
		CodeHash: hash,
	})
	return n > 0, handleError(err)
}

func (m *MFA) CountRecoveryCodes(ctx context.Context, userID snow.ID) (int64, error) {
	n, err := m.queries.MFARecoveryCodeCountUnused(ctx, userID.Int64())
	return n, handleError(err)
}

func (m *MFA) CreateChallenge(ctx context.Context, c domain.MFAChallenge) error {
	return handleError(m.queries.MFAChallengeCreate(ctx, sqlcSqlite.MFAChallengeCreateParams{
		TokenHash: c.TokenHash,
		UserID:    c.UserID.Int64(),
		Method:    c.Method,
		ExpiresAt: c.ExpiresAt,
	}))
}

func (m *MFA) DeleteExpiredChallenges(ctx context.Context, before time.Time) error {
	_, err := m.queries.MFAChallengeDeleteExpired(ctx, before)
	return handleError(err)
}

func (m *MFA) GetChallenge(ctx context.Context, tokenHash string) (*domain.MFAChallenge, error) {
	row, err := m.queries.MFAChallengeGet(ctx, tokenHash)
	if err != nil {
		return nil, handleError(err)
	}
	return &domain.MFAChallenge{
		TokenHash: row.TokenHash,
		UserID:    snow.ID(row.UserID),
		Method:    row.Method,
		Attempts:  row.Attempts,
		ExpiresAt: row.ExpiresAt,
		CreatedAt: row.CreatedAt,
	}, nil
}

func (m *MFA) AddChallengeAttempt(ctx context.Context, tokenHash string) (int64, error) {
	n, err := m.queries.MFAChallengeAddAttempt(ctx, tokenHash)
	return n, handleError(err)
}

func (m *MFA) DeleteChallenge(ctx context.Context, tokenHash string) (bool, error) {
	n, err := m.queries.MFAChallengeDelete(ctx, tokenHash)
	return n > 0, handleError(err)
}

func (m *MFA) DeleteUserChallenges(ctx context.Context, userID snow.ID) error {
	return handleError(m.queries.MFAChallengeDeleteByUser(ctx, userID.Int64()))
}
