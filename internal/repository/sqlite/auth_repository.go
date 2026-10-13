package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/nipalab/nipa/internal/domain"
	sqlcSqlite "github.com/nipalab/nipa/internal/repository/sqlc/sqlite"
	"github.com/nipalab/nipa/internal/snow"
)

type Auth struct {
	queries *sqlcSqlite.Queries
}

func NewAuthRepository(db *sql.DB) *Auth {
	return &Auth{queries: sqlcSqlite.New(db)}
}

func (a *Auth) SaveRefreshToken(ctx context.Context, userID snow.ID, refreshToken string, expiresAt time.Time) error {
	_, err := a.queries.RefreshTokenCreate(ctx, sqlcSqlite.RefreshTokenCreateParams{
		UserID:    userID.Int64(),
		Token:     refreshToken,
		ExpiresAt: expiresAt,
	})
	return handleError(err)
}

func (a *Auth) GetAndDeleteRefreshToken(ctx context.Context, refreshToken string) (*domain.RefreshToken, error) {
	row, err := a.queries.RefreshTokenDeleteByToken(ctx, refreshToken)
	if err != nil {
		return nil, handleError(err)
	}
	return toDomainRefreshToken(row), nil
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
	return &domain.RefreshToken{
		ID:        row.ID,
		UserID:    snow.ID(row.UserID),
		Token:     row.Token,
		ExpiresAt: row.ExpiresAt,
		CreatedAt: row.CreatedAt,
	}
}

func toDomainSSHLoginChallenge(row sqlcSqlite.SshLoginChallenge) *domain.SSHLoginChallenge {
	return &domain.SSHLoginChallenge{
		Email:     row.Email,
		Nonce:     row.Nonce,
		ExpiresAt: row.ExpiresAt,
		CreatedAt: row.CreatedAt,
	}
}
