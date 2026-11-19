package postgres

import (
	"context"
	"database/sql"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"

	sqlcPostgres "github.com/nipalab/nipa/ee/repository/postgres/sqlc"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	"github.com/nipalab/nipa/internal/snow"
)

// storageServerAdvisoryNamespace keeps the per-owner storage server lock apart
// from the ledger, billing and file-lock keys.
const storageServerAdvisoryNamespace int64 = 0x4e4950533353

// BYOStorageRepository stores bring-your-own storage servers and the org and
// project assignments. Queries run through the transaction carried in the
// context.
type BYOStorageRepository struct {
	queries *sqlcPostgres.Queries
}

func NewBYOStorageRepository(db *sql.DB) *BYOStorageRepository {
	return &BYOStorageRepository{queries: sqlcPostgres.New(dbtx.New(db))}
}

// LockOwner takes the owner's transaction-scoped storage server lock.
func (r *BYOStorageRepository) LockOwner(ctx context.Context, owner snow.ID) error {
	return handleError(r.queries.LockStorageServerOwner(ctx, owner.Int64()^storageServerAdvisoryNamespace))
}

func (r *BYOStorageRepository) CountServers(ctx context.Context, owner snow.ID) (int64, error) {
	n, err := r.queries.CountStorageServersByOwner(ctx, owner.Int64())
	return n, handleError(err)
}

func (r *BYOStorageRepository) CreateServer(ctx context.Context, server domain.StorageServer) (*domain.StorageServer, error) {
	row, err := r.queries.CreateStorageServer(ctx, sqlcPostgres.CreateStorageServerParams{
		ID:           server.ID.Int64(),
		OwnerUserID:  server.OwnerUserID.Int64(),
		Name:         server.Name,
		Endpoint:     server.Endpoint,
		Region:       server.Region,
		Bucket:       server.Bucket,
		Prefix:       server.Prefix,
		AccessKeyID:  server.AccessKeyID,
		SecretSealed: server.SecretSealed,
	})
	if err != nil {
		return nil, handleError(err)
	}
	return toStorageServer(row), nil
}

func (r *BYOStorageRepository) UpdateServer(ctx context.Context, server domain.StorageServer) (*domain.StorageServer, error) {
	row, err := r.queries.UpdateStorageServer(ctx, sqlcPostgres.UpdateStorageServerParams{
		ID:           server.ID.Int64(),
		Name:         server.Name,
		Endpoint:     server.Endpoint,
		Region:       server.Region,
		AccessKeyID:  server.AccessKeyID,
		SecretSealed: server.SecretSealed,
	})
	if err != nil {
		return nil, handleError(err)
	}
	return toStorageServer(row), nil
}

// Server returns the server; a 404 domain error when it does not exist.
func (r *BYOStorageRepository) Server(ctx context.Context, id snow.ID) (*domain.StorageServer, error) {
	row, err := r.queries.GetStorageServer(ctx, id.Int64())
	if err != nil {
		return nil, handleError(err)
	}
	return toStorageServer(row), nil
}

func (r *BYOStorageRepository) ListServers(ctx context.Context, owner snow.ID) ([]domain.StorageServer, error) {
	rows, err := r.queries.ListStorageServersByOwner(ctx, owner.Int64())
	if err != nil {
		return nil, handleError(err)
	}
	servers := make([]domain.StorageServer, 0, len(rows))
	for _, row := range rows {
		servers = append(servers, *toStorageServer(row))
	}
	return servers, nil
}

// ServerInUse reports whether a project or a live organization's default
// references the server.
func (r *BYOStorageRepository) ServerInUse(ctx context.Context, id snow.ID) (bool, error) {
	inUse, err := r.queries.StorageServerInUse(ctx, id.Int64())
	return inUse, handleError(err)
}

// DeleteServer drops the defaults deleted organizations still hold, then the
// server. A reference that appeared meanwhile is a 409.
func (r *BYOStorageRepository) DeleteServer(ctx context.Context, id snow.ID) error {
	if err := r.queries.DeleteDeletedOrgStorageDefaults(ctx, id.Int64()); err != nil {
		return handleError(err)
	}
	n, err := r.queries.DeleteStorageServer(ctx, id.Int64())
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return domain.NewErrorConflict("storage server is in use")
		}
		return handleError(err)
	}
	if n == 0 {
		return domain.NewErrorNotFound("storage server not found")
	}
	return nil
}

// OrgDefault returns the organization's default server id; nil when none.
func (r *BYOStorageRepository) OrgDefault(ctx context.Context, orgID snow.ID) (*snow.ID, error) {
	id, err := r.queries.GetOrgStorageDefault(ctx, orgID.Int64())
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, handleError(err)
	}
	serverID := snow.ID(id)
	return &serverID, nil
}

func (r *BYOStorageRepository) SetOrgDefault(ctx context.Context, orgID, serverID, setBy snow.ID) error {
	return handleError(r.queries.UpsertOrgStorageDefault(ctx, sqlcPostgres.UpsertOrgStorageDefaultParams{
		OrgID:           orgID.Int64(),
		StorageServerID: serverID.Int64(),
		SetBy:           setBy.Int64(),
	}))
}

// ClearOrgDefault removes the default and reports whether there was one.
func (r *BYOStorageRepository) ClearOrgDefault(ctx context.Context, orgID snow.ID) (bool, error) {
	n, err := r.queries.DeleteOrgStorageDefault(ctx, orgID.Int64())
	return n > 0, handleError(err)
}

func (r *BYOStorageRepository) AssignProject(ctx context.Context, projectID, serverID snow.ID) error {
	return handleError(r.queries.InsertProjectStorage(ctx, sqlcPostgres.InsertProjectStorageParams{
		ProjectID:       projectID.Int64(),
		StorageServerID: serverID.Int64(),
	}))
}

// ProjectServer returns the server a project is assigned to; nil when the
// project keeps the server's own storage.
func (r *BYOStorageRepository) ProjectServer(ctx context.Context, projectID snow.ID) (*domain.StorageServer, error) {
	row, err := r.queries.GetProjectStorageServer(ctx, projectID.Int64())
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, handleError(err)
	}
	return toStorageServer(row), nil
}

func toStorageServer(row sqlcPostgres.StorageServer) *domain.StorageServer {
	return &domain.StorageServer{
		ID:           snow.ID(row.ID),
		OwnerUserID:  snow.ID(row.OwnerUserID),
		Name:         row.Name,
		Endpoint:     row.Endpoint,
		Region:       row.Region,
		Bucket:       row.Bucket,
		Prefix:       row.Prefix,
		AccessKeyID:  row.AccessKeyID,
		SecretSealed: row.SecretSealed,
		CreatedAt:    row.CreatedAt,
		UpdatedAt:    row.UpdatedAt,
	}
}
