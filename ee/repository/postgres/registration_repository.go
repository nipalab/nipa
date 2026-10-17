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

// RegistrationRepository backs the SaaS onboarding flows: email verification
// tokens, the free-tier quota seed and the social login identities plus their
// one-time handoff codes. Queries run through the transaction carried in the
// context, so registration can atomically create a user, a token, an identity
// and the quota row.
type RegistrationRepository struct {
	queries *sqlcPostgres.Queries
}

func NewRegistrationRepository(db *sql.DB) *RegistrationRepository {
	return &RegistrationRepository{queries: sqlcPostgres.New(dbtx.New(db))}
}

// CreateUnverifiedUser inserts an inactive account awaiting email
// confirmation. Generic user creation always produces verified accounts, so
// this explicit insert is the only path to a pending registration.
func (r *RegistrationRepository) CreateUnverifiedUser(ctx context.Context, user domain.User) (*domain.User, error) {
	id, err := r.queries.UserCreateUnverified(ctx, sqlcPostgres.UserCreateUnverifiedParams{
		ID:       user.ID.Int64(),
		Name:     user.Name,
		Email:    user.Email,
		Password: user.Password,
		PhotoUrl: sql.NullString{String: user.PhotoUrl, Valid: user.PhotoUrl != ""},
	})
	if err != nil {
		return nil, handleError(err)
	}
	row, err := r.queries.UserGetById(ctx, id)
	if err != nil {
		return nil, handleError(err)
	}
	return toDomainUser(row), nil
}

func (r *RegistrationRepository) UpsertEmailVerification(ctx context.Context, userID snow.ID, tokenHash string, expiresAt time.Time) error {
	err := r.queries.EmailVerificationUpsert(ctx, sqlcPostgres.EmailVerificationUpsertParams{
		UserID:    userID.Int64(),
		TokenHash: tokenHash,
		ExpiresAt: expiresAt,
	})
	return handleError(err)
}

func (r *RegistrationRepository) EmailVerificationByToken(ctx context.Context, tokenHash string) (snow.ID, time.Time, error) {
	row, err := r.queries.EmailVerificationGetByToken(ctx, tokenHash)
	if err != nil {
		return 0, time.Time{}, handleError(err)
	}
	return snow.ID(row.UserID), row.ExpiresAt, nil
}

func (r *RegistrationRepository) DeleteEmailVerification(ctx context.Context, userID snow.ID) error {
	return handleError(r.queries.EmailVerificationDelete(ctx, userID.Int64()))
}

func (r *RegistrationRepository) DeleteExpiredEmailVerifications(ctx context.Context, before time.Time) error {
	_, err := r.queries.EmailVerificationDeleteExpired(ctx, before)
	return handleError(err)
}

func (r *RegistrationRepository) SetEmailVerified(ctx context.Context, userID snow.ID) error {
	return handleError(r.queries.UserSetEmailVerified(ctx, userID.Int64()))
}

func (r *RegistrationRepository) SeedOwnerQuota(ctx context.Context, userID snow.ID, quotaBytes int64) error {
	err := r.queries.SetOwnerQuota(ctx, sqlcPostgres.SetOwnerQuotaParams{
		UserID:     userID.Int64(),
		QuotaBytes: sql.NullInt64{Int64: quotaBytes, Valid: true},
	})
	return handleError(err)
}

func (r *RegistrationRepository) IdentityByProviderAccount(ctx context.Context, provider, providerAccountID string) (*domain.UserIdentity, error) {
	row, err := r.queries.UserIdentityGet(ctx, sqlcPostgres.UserIdentityGetParams{
		Provider:          provider,
		ProviderAccountID: providerAccountID,
	})
	if err != nil {
		return nil, handleError(err)
	}
	return toDomainIdentity(row), nil
}

func (r *RegistrationRepository) IdentityByID(ctx context.Context, id snow.ID) (*domain.UserIdentity, error) {
	row, err := r.queries.UserIdentityGetByID(ctx, id.Int64())
	if err != nil {
		return nil, handleError(err)
	}
	return toDomainIdentity(row), nil
}

func (r *RegistrationRepository) ListIdentities(ctx context.Context, userID snow.ID) ([]*domain.UserIdentity, error) {
	rows, err := r.queries.UserIdentityListByUser(ctx, userID.Int64())
	if err != nil {
		return nil, handleError(err)
	}
	identities := make([]*domain.UserIdentity, 0, len(rows))
	for _, row := range rows {
		identities = append(identities, toDomainIdentity(row))
	}
	return identities, nil
}

func (r *RegistrationRepository) CountIdentities(ctx context.Context, userID snow.ID) (int, error) {
	count, err := r.queries.UserIdentityCountByUser(ctx, userID.Int64())
	if err != nil {
		return 0, handleError(err)
	}
	return int(count), nil
}

func (r *RegistrationRepository) CreateIdentity(ctx context.Context, identity domain.UserIdentity) error {
	_, err := r.queries.UserIdentityCreate(ctx, sqlcPostgres.UserIdentityCreateParams{
		ID:                identity.ID.Int64(),
		UserID:            identity.UserID.Int64(),
		Provider:          identity.Provider,
		ProviderAccountID: identity.ProviderAccountID,
		Email:             identity.Email,
	})
	return handleError(err)
}

func (r *RegistrationRepository) DeleteIdentity(ctx context.Context, id, userID snow.ID) error {
	affected, err := r.queries.UserIdentityDelete(ctx, sqlcPostgres.UserIdentityDeleteParams{
		ID:     id.Int64(),
		UserID: userID.Int64(),
	})
	if err != nil {
		return handleError(err)
	}
	if affected == 0 {
		return domain.NewErrorRecordNotFound()
	}
	return nil
}

func (r *RegistrationRepository) CreateOAuthLoginCode(ctx context.Context, code string, userID snow.ID, expiresAt time.Time) error {
	err := r.queries.OAuthLoginCodeCreate(ctx, sqlcPostgres.OAuthLoginCodeCreateParams{
		Code:      code,
		UserID:    userID.Int64(),
		ExpiresAt: expiresAt,
	})
	return handleError(err)
}

func (r *RegistrationRepository) ConsumeOAuthLoginCode(ctx context.Context, code string) (snow.ID, time.Time, error) {
	row, err := r.queries.OAuthLoginCodeGetAndDelete(ctx, code)
	if err != nil {
		return 0, time.Time{}, handleError(err)
	}
	return snow.ID(row.UserID), row.ExpiresAt, nil
}

func (r *RegistrationRepository) DeleteExpiredOAuthLoginCodes(ctx context.Context, before time.Time) error {
	_, err := r.queries.OAuthLoginCodeDeleteExpired(ctx, before)
	return handleError(err)
}

func toDomainIdentity(row sqlcPostgres.UserIdentity) *domain.UserIdentity {
	return &domain.UserIdentity{
		ID:                snow.ID(row.ID),
		UserID:            snow.ID(row.UserID),
		Provider:          row.Provider,
		ProviderAccountID: row.ProviderAccountID,
		Email:             row.Email,
		CreatedAt:         row.CreatedAt,
		UpdatedAt:         row.UpdatedAt,
	}
}
