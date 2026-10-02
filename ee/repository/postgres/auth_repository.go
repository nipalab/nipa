package postgres

import (
	"context"
	"database/sql"
	"time"

	sqlcPostgres "github.com/nipalab/nipa/ee/repository/postgres/sqlc"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type Auth struct {
	queries *sqlcPostgres.Queries
}

func NewAuthRepository(db *sql.DB) *Auth {
	return &Auth{queries: sqlcPostgres.New(db)}
}

func (a *Auth) SaveRefreshToken(ctx context.Context, userID snow.ID, refreshToken string, expiresAt time.Time) error {
	_, err := a.queries.RefreshTokenCreate(ctx, sqlcPostgres.RefreshTokenCreateParams{
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

func toDomainRefreshToken(row sqlcPostgres.RefreshToken) *domain.RefreshToken {
	return &domain.RefreshToken{
		ID:        row.ID,
		UserID:    snow.ID(row.UserID),
		Token:     row.Token,
		ExpiresAt: row.ExpiresAt,
		CreatedAt: row.CreatedAt,
	}
}
