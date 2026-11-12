package postgres

import (
	"context"
	"database/sql"
	"time"

	sqlcPostgres "github.com/nipalab/nipa/ee/repository/postgres/sqlc"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	"github.com/nipalab/nipa/internal/snow"
)

type Auth struct {
	queries *sqlcPostgres.Queries
}

func NewAuthRepository(db *sql.DB) *Auth {
	return &Auth{queries: sqlcPostgres.New(dbtx.New(db))}
}

// Expired tokens stay for a grace period so a stale cookie is still answered
// with "expired" (and cleared) rather than "not found" (kept for a retry).
const refreshTokenPruneGrace = 24 * time.Hour

func (a *Auth) CreateRefreshSession(ctx context.Context, userID snow.ID, tokenHash string, startedAt, expiresAt time.Time) (int64, error) {
	err := a.queries.RefreshTokenDeleteExpiredByUser(ctx, sqlcPostgres.RefreshTokenDeleteExpiredByUserParams{
		UserID:    userID.Int64(),
		ExpiresAt: startedAt.Add(-refreshTokenPruneGrace),
	})
	if err != nil {
		return 0, handleError(err)
	}
	id, err := a.queries.RefreshSessionCreate(ctx, sqlcPostgres.RefreshSessionCreateParams{
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
		n, err = a.queries.RefreshTokenUpgradeLegacy(ctx, sqlcPostgres.RefreshTokenUpgradeLegacyParams{
			NewHash:          r.NewHash,
			SessionStartedAt: sql.NullTime{Time: r.SessionStartedAt, Valid: true},
			RotatedAt:        sql.NullTime{Time: r.RotatedAt, Valid: true},
			ExpiresAt:        r.ExpiresAt,
			ID:               r.ID,
			LegacyToken:      r.MatchLegacyToken,
		})
	} else {
		n, err = a.queries.RefreshSessionRotate(ctx, sqlcPostgres.RefreshSessionRotateParams{
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
	err := a.queries.SSHLoginChallengeCreate(ctx, sqlcPostgres.SSHLoginChallengeCreateParams{
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

func toDomainRefreshToken(row sqlcPostgres.RefreshToken) *domain.RefreshToken {
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

func toDomainSSHLoginChallenge(row sqlcPostgres.SshLoginChallenge) *domain.SSHLoginChallenge {
	return &domain.SSHLoginChallenge{
		Email:     row.Email,
		Nonce:     row.Nonce,
		ExpiresAt: row.ExpiresAt,
		CreatedAt: row.CreatedAt,
	}
}
