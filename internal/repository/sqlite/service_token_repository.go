package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	sqlcSqlite "github.com/nipalab/nipa/internal/repository/sqlc/sqlite"
	"github.com/nipalab/nipa/internal/snow"
)

// ServiceTokenRepository persists project service tokens. Only the token hash
// is stored.
type ServiceTokenRepository struct {
	queries *sqlcSqlite.Queries
}

func NewServiceTokenRepository(db *sql.DB) *ServiceTokenRepository {
	return &ServiceTokenRepository{queries: sqlcSqlite.New(dbtx.New(db))}
}

func (r *ServiceTokenRepository) Create(ctx context.Context, token domain.ServiceToken) (*domain.ServiceToken, error) {
	row, err := r.queries.ServiceTokenCreate(ctx, sqlcSqlite.ServiceTokenCreateParams{
		ID:        token.ID.Int64(),
		ProjectID: token.ProjectID.Int64(),
		Name:      token.Name,
		TokenHash: token.TokenHash,
		Hint:      token.Hint,
		Scopes:    strings.Join(token.Scopes, ","),
		CreatedBy: token.CreatedBy.Int64(),
		ExpiresAt: timePtrToNullTime(token.ExpiresAt),
	})
	if err != nil {
		return nil, handleError(err)
	}
	return serviceTokenToDomain(row), nil
}

func (r *ServiceTokenRepository) Get(ctx context.Context, projectID, id snow.ID) (*domain.ServiceToken, error) {
	row, err := r.queries.ServiceTokenGet(ctx, sqlcSqlite.ServiceTokenGetParams{ProjectID: projectID.Int64(), ID: id.Int64()})
	if err != nil {
		return nil, handleError(err)
	}
	return serviceTokenToDomain(row), nil
}

func (r *ServiceTokenRepository) GetByHash(ctx context.Context, hash string) (*domain.ServiceToken, error) {
	row, err := r.queries.ServiceTokenGetByHash(ctx, hash)
	if err != nil {
		return nil, handleError(err)
	}
	return serviceTokenToDomain(row), nil
}

func (r *ServiceTokenRepository) ListByProject(ctx context.Context, projectID snow.ID) ([]*domain.ServiceToken, error) {
	rows, err := r.queries.ServiceTokenListByProject(ctx, projectID.Int64())
	if err != nil {
		return nil, handleError(err)
	}
	tokens := make([]*domain.ServiceToken, 0, len(rows))
	for _, row := range rows {
		tokens = append(tokens, serviceTokenToDomain(row))
	}
	return tokens, nil
}

func (r *ServiceTokenRepository) Revoke(ctx context.Context, projectID, id snow.ID, at time.Time) (*domain.ServiceToken, error) {
	row, err := r.queries.ServiceTokenRevoke(ctx, sqlcSqlite.ServiceTokenRevokeParams{
		RevokedAt: sql.NullTime{Time: at, Valid: true},
		ProjectID: projectID.Int64(),
		ID:        id.Int64(),
	})
	if err != nil {
		return nil, handleError(err)
	}
	return serviceTokenToDomain(row), nil
}

// LockProject is a no-op: sqlite write transactions are immediate and already
// serialize token creation.
func (r *ServiceTokenRepository) LockProject(context.Context, snow.ID) error {
	return nil
}

func (r *ServiceTokenRepository) TouchLastUsed(ctx context.Context, id snow.ID, at time.Time) error {
	return handleError(r.queries.ServiceTokenTouch(ctx, sqlcSqlite.ServiceTokenTouchParams{
		LastUsedAt: sql.NullTime{Time: at, Valid: true},
		ID:         id.Int64(),
	}))
}

func serviceTokenToDomain(row sqlcSqlite.ProjectServiceToken) *domain.ServiceToken {
	var scopes []string
	if row.Scopes != "" {
		scopes = strings.Split(row.Scopes, ",")
	}
	return &domain.ServiceToken{
		ID:         snow.ID(row.ID),
		ProjectID:  snow.ID(row.ProjectID),
		Name:       row.Name,
		TokenHash:  row.TokenHash,
		Hint:       row.Hint,
		Scopes:     scopes,
		CreatedBy:  snow.ID(row.CreatedBy),
		ExpiresAt:  nullTimePtr(row.ExpiresAt),
		LastUsedAt: nullTimePtr(row.LastUsedAt),
		RevokedAt:  nullTimePtr(row.RevokedAt),
		CreatedAt:  row.CreatedAt,
	}
}
