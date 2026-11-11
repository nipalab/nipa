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

func (a *Auth) CreateRefreshSession(ctx context.Context, userID snow.ID, tokenHash string, startedAt, expiresAt time.Time) (int64, error) {
	err := a.queries.RefreshTokenDeleteExpiredByUser(ctx, sqlcSqlite.RefreshTokenDeleteExpiredByUserParams{
		UserID:    userID.Int64(),
		ExpiresAt: startedAt.Add(-refreshTokenPruneGrace),
	})
	if err != nil {
		return 0, handleError(err)
	}
	id, err := a.queries.RefreshSessionCreate(ctx, sqlcSqlite.RefreshSessionCreateParams{
		UserID:           userID.Int64(),
		TokenHash:        tokenHash,
		SessionStartedAt: sql.NullTime{Time: startedAt, Valid: true},
		ExpiresAt:        expiresAt,
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

func (a *Auth) GetLegacyRefreshToken(ctx context.Context, token string) (*domain.RefreshToken, error) {
	row, err := a.queries.RefreshTokenGetLegacy(ctx, token)
	if err != nil {
		return nil, handleError(err)
	}
	return toDomainRefreshToken(row), nil
}

func (a *Auth) RotateRefreshSession(ctx context.Context, r domain.RefreshRotation) (bool, error) {
	var (
		n   int64
		err error
	)
	if r.MatchLegacyToken != "" {
		n, err = a.queries.RefreshTokenUpgradeLegacy(ctx, sqlcSqlite.RefreshTokenUpgradeLegacyParams{
			NewHash:          r.NewHash,
			SessionStartedAt: sql.NullTime{Time: r.SessionStartedAt, Valid: true},
			RotatedAt:        sql.NullTime{Time: r.RotatedAt, Valid: true},
			ExpiresAt:        r.ExpiresAt,
			ID:               r.ID,
			LegacyToken:      r.MatchLegacyToken,
		})
	} else {
		n, err = a.queries.RefreshSessionRotate(ctx, sqlcSqlite.RefreshSessionRotateParams{
			NewHash:     r.NewHash,
			CurrentHash: sql.NullString{String: r.MatchHash, Valid: true},
			RotatedAt:   sql.NullTime{Time: r.RotatedAt, Valid: true},
			ExpiresAt:   r.ExpiresAt,
			ID:          r.ID,
		})
	}
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
		TokenHash:    row.TokenHash.String,
		PreviousHash: row.PreviousHash.String,
		ExpiresAt:    row.ExpiresAt,
		CreatedAt:    row.CreatedAt,
	}
	if !row.TokenHash.Valid {
		t.Token = row.Token
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
