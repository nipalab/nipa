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

type Auth struct {
	queries *sqlcSqlite.Queries
}

func NewAuthRepository(db *sql.DB) *Auth {
	return &Auth{queries: sqlcSqlite.New(dbtx.New(db))}
}

// Expired tokens stay for a grace period so a stale cookie is still answered
// with "expired" (and cleared) rather than "not found" (kept for a retry).
const refreshTokenPruneGrace = 24 * time.Hour

func (a *Auth) CreateRefreshSession(ctx context.Context, session domain.NewRefreshSession) (int64, error) {
	err := a.queries.RefreshTokenDeleteExpiredByUser(ctx, sqlcSqlite.RefreshTokenDeleteExpiredByUserParams{
		UserID:    session.UserID.Int64(),
		ExpiresAt: session.StartedAt.Add(-refreshTokenPruneGrace),
	})
	if err != nil {
		return 0, handleError(err)
	}
	id, err := a.queries.RefreshSessionCreate(ctx, sqlcSqlite.RefreshSessionCreateParams{
		UserID:           session.UserID.Int64(),
		TokenHash:        session.TokenHash,
		SessionStartedAt: sql.NullTime{Time: session.StartedAt, Valid: true},
		ExpiresAt:        session.ExpiresAt,
		AuthMethod:       sql.NullString{String: session.AuthMethod, Valid: session.AuthMethod != ""},
		Mfa:              sql.NullString{String: session.MFA, Valid: session.MFA != ""},
	})
	if err != nil {
		return 0, handleError(err)
	}
	return id, nil
}

func (a *Auth) GetRefreshSession(ctx context.Context, id int64) (*domain.RefreshToken, error) {
	row, err := a.queries.RefreshSessionGet(ctx, id)
	if err != nil {
		return nil, handleError(err)
	}
	return toDomainRefreshToken(row), nil
}

func (a *Auth) RotateRefreshSession(ctx context.Context, r domain.RefreshRotation) (bool, error) {
	n, err := a.queries.RefreshSessionRotate(ctx, sqlcSqlite.RefreshSessionRotateParams{
		NewHash:     r.NewHash,
		CurrentHash: sql.NullString{String: r.MatchHash, Valid: true},
		RotatedAt:   sql.NullTime{Time: r.RotatedAt, Valid: true},
		ExpiresAt:   r.ExpiresAt,
		ID:          r.ID,
	})
	if err != nil {
		return false, handleError(err)
	}
	return n > 0, nil
}

func (a *Auth) DeleteRefreshSession(ctx context.Context, id int64) error {
	_, err := a.queries.RefreshSessionDelete(ctx, id)
	return handleError(err)
}

func (a *Auth) CreateSSHLoginChallenge(ctx context.Context, email, nonce string, expiresAt time.Time) error {
	err := a.queries.SSHLoginChallengeCreate(ctx, sqlcSqlite.SSHLoginChallengeCreateParams{
		Email:     email,
		Nonce:     nonce,
		ExpiresAt: expiresAt,
	})
	return handleError(err)
}

func (a *Auth) DeleteExpiredSSHLoginChallenges(ctx context.Context, before time.Time) error {
	_, err := a.queries.SSHLoginChallengeDeleteExpired(ctx, before)
	return handleError(err)
}

func (a *Auth) ConsumeSSHLoginChallenge(ctx context.Context, nonce string) (*domain.SSHLoginChallenge, error) {
	row, err := a.queries.SSHLoginChallengeConsume(ctx, nonce)
	if err != nil {
		return nil, handleError(err)
	}
	return toDomainSSHLoginChallenge(row), nil
}

func toDomainRefreshToken(row sqlcSqlite.RefreshToken) *domain.RefreshToken {
	t := &domain.RefreshToken{
		ID:           row.ID,
		UserID:       snow.ID(row.UserID),
		TokenHash:    row.TokenHash,
		PreviousHash: row.PreviousHash.String,
		AuthMethod:   row.AuthMethod.String,
		MFA:          row.Mfa.String,
		ExpiresAt:    row.ExpiresAt,
		CreatedAt:    row.CreatedAt,
	}
	if row.RotatedAt.Valid {
		t.RotatedAt = &row.RotatedAt.Time
	}
	if row.SessionStartedAt.Valid {
		t.SessionStartedAt = &row.SessionStartedAt.Time
	}
	return t
}

func toDomainSSHLoginChallenge(row sqlcSqlite.SshLoginChallenge) *domain.SSHLoginChallenge {
	return &domain.SSHLoginChallenge{
		Email:     row.Email,
		Nonce:     row.Nonce,
		ExpiresAt: row.ExpiresAt,
		CreatedAt: row.CreatedAt,
	}
}
