package postgres

import (
	"context"
	"database/sql"

	sqlcPostgres "github.com/nipalab/nipa/ee/repository/postgres/sqlc"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"gopkg.in/typ.v4/slices"
)

type SSHKeyRepository struct {
	queries *sqlcPostgres.Queries
}

func NewSSHKeyRepository(db *sql.DB) *SSHKeyRepository {
	return &SSHKeyRepository{queries: sqlcPostgres.New(db)}
}

func (r *SSHKeyRepository) Create(ctx context.Context, key domain.SSHPublicKey) (*domain.SSHPublicKey, error) {
	row, err := r.queries.SSHKeyCreate(ctx, sqlcPostgres.SSHKeyCreateParams{
		ID:          key.ID.Int64(),
		UserID:      key.UserID.Int64(),
		Name:        key.Name,
		KeyType:     key.KeyType,
		KeyBlob:     key.KeyBlob,
		Fingerprint: key.Fingerprint,
		Comment:     key.Comment,
	})
	if err != nil {
		return nil, handleError(err)
	}
	return sshKeyToDomain(row), nil
}

func (r *SSHKeyRepository) GetByID(ctx context.Context, id snow.ID) (*domain.SSHPublicKey, error) {
	row, err := r.queries.SSHKeyGetByID(ctx, id.Int64())
	if err != nil {
		return nil, handleError(err)
	}
	return sshKeyToDomain(row), nil
}

func (r *SSHKeyRepository) GetByFingerprint(ctx context.Context, fingerprint string) (*domain.SSHPublicKey, error) {
	row, err := r.queries.SSHKeyGetByFingerprint(ctx, fingerprint)
	if err != nil {
		return nil, handleError(err)
	}
	return sshKeyToDomain(row), nil
}

func (r *SSHKeyRepository) ListByUser(ctx context.Context, userID snow.ID) ([]*domain.SSHPublicKey, error) {
	rows, err := r.queries.SSHKeyListByUser(ctx, userID.Int64())
	if err != nil {
		return nil, handleError(err)
	}
	return slices.Map(rows, func(k sqlcPostgres.SshPublicKey) *domain.SSHPublicKey {
		return sshKeyToDomain(k)
	}), nil
}

func (r *SSHKeyRepository) CountByUser(ctx context.Context, userID snow.ID) (int, error) {
	count, err := r.queries.SSHKeyCountByUser(ctx, userID.Int64())
	if err != nil {
		return 0, handleError(err)
	}
	return int(count), nil
}

func (r *SSHKeyRepository) Delete(ctx context.Context, userID, id snow.ID) error {
	affected, err := r.queries.SSHKeyDelete(ctx, sqlcPostgres.SSHKeyDeleteParams{
		UserID: userID.Int64(),
		ID:     id.Int64(),
	})
	if err != nil {
		return handleError(err)
	}
	if affected == 0 {
		return domain.NewErrorRecordNotFound()
	}
	return nil
}

func sshKeyToDomain(k sqlcPostgres.SshPublicKey) *domain.SSHPublicKey {
	return &domain.SSHPublicKey{
		ID:          snow.ID(k.ID),
		UserID:      snow.ID(k.UserID),
		Name:        k.Name,
		KeyType:     k.KeyType,
		KeyBlob:     k.KeyBlob,
		Fingerprint: k.Fingerprint,
		Comment:     k.Comment,
		CreatedAt:   k.CreatedAt,
	}
}
