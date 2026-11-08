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

type OrgInvitationRepository struct {
	queries *sqlcPostgres.Queries
}

func NewOrgInvitationRepository(db *sql.DB) *OrgInvitationRepository {
	return &OrgInvitationRepository{queries: sqlcPostgres.New(dbtx.New(db))}
}

func (r *OrgInvitationRepository) Create(ctx context.Context, inv *domain.OrgInvitation) error {
	return handleError(r.queries.OrgInvitationInsert(ctx, sqlcPostgres.OrgInvitationInsertParams{
		ID:        inv.ID.Int64(),
		OrgID:     inv.OrgID.Int64(),
		InvitedBy: inv.InvitedBy.Int64(),
		Email:     inv.Email,
		Role:      inv.Role,
		TokenHash: inv.TokenHash,
		ExpiresAt: inv.ExpiresAt.UTC(),
	}))
}

func (r *OrgInvitationRepository) Get(ctx context.Context, id snow.ID) (*domain.OrgInvitation, error) {
	row, err := r.queries.OrgInvitationGet(ctx, id.Int64())
	if err != nil {
		return nil, handleError(err)
	}
	return toDomainOrgInvitation(sqlcPostgres.OrgInvitationListByOrgRow(row)), nil
}

func (r *OrgInvitationRepository) GetByTokenHash(ctx context.Context, hash []byte) (*domain.OrgInvitation, error) {
	row, err := r.queries.OrgInvitationGetByTokenHash(ctx, hash)
	if err != nil {
		return nil, handleError(err)
	}
	return toDomainOrgInvitation(sqlcPostgres.OrgInvitationListByOrgRow(row)), nil
}

func (r *OrgInvitationRepository) FindPending(ctx context.Context, orgID snow.ID, email string) (*domain.OrgInvitation, error) {
	row, err := r.queries.OrgInvitationFindPending(ctx, sqlcPostgres.OrgInvitationFindPendingParams{
		OrgID: orgID.Int64(),
		Email: email,
	})
	if err != nil {
		return nil, handleError(err)
	}
	return toDomainOrgInvitation(sqlcPostgres.OrgInvitationListByOrgRow(row)), nil
}

func (r *OrgInvitationRepository) ListByOrg(ctx context.Context, orgID snow.ID, status string) ([]*domain.OrgInvitation, error) {
	rows, err := r.queries.OrgInvitationListByOrg(ctx, sqlcPostgres.OrgInvitationListByOrgParams{
		OrgID:  orgID.Int64(),
		Status: status,
	})
	if err != nil {
		return nil, handleError(err)
	}
	out := make([]*domain.OrgInvitation, 0, len(rows))
	for _, row := range rows {
		out = append(out, toDomainOrgInvitation(row))
	}
	return out, nil
}

func (r *OrgInvitationRepository) ListPendingByEmail(ctx context.Context, email string, now time.Time) ([]*domain.OrgInvitation, error) {
	rows, err := r.queries.OrgInvitationListPendingByEmail(ctx, sqlcPostgres.OrgInvitationListPendingByEmailParams{
		Email: email,
		Now:   now.UTC(),
	})
	if err != nil {
		return nil, handleError(err)
	}
	out := make([]*domain.OrgInvitation, 0, len(rows))
	for _, row := range rows {
		out = append(out, toDomainOrgInvitation(sqlcPostgres.OrgInvitationListByOrgRow(row)))
	}
	return out, nil
}

func (r *OrgInvitationRepository) ExpireStale(ctx context.Context, orgID snow.ID, email string, now time.Time) error {
	return handleError(r.queries.OrgInvitationExpireStale(ctx, sqlcPostgres.OrgInvitationExpireStaleParams{
		OrgID: orgID.Int64(),
		Email: email,
		Now:   now.UTC(),
	}))
}

func (r *OrgInvitationRepository) Rotate(ctx context.Context, id snow.ID, tokenHash []byte, expiresAt time.Time) (bool, error) {
	n, err := r.queries.OrgInvitationRotate(ctx, sqlcPostgres.OrgInvitationRotateParams{
		ID:        id.Int64(),
		TokenHash: tokenHash,
		ExpiresAt: expiresAt.UTC(),
	})
	return n > 0, handleError(err)
}

func (r *OrgInvitationRepository) Resolve(ctx context.Context, id snow.ID, status string) (bool, error) {
	n, err := r.queries.OrgInvitationResolve(ctx, sqlcPostgres.OrgInvitationResolveParams{
		ID:     id.Int64(),
		Status: status,
	})
	return n > 0, handleError(err)
}

func (r *OrgInvitationRepository) Accept(ctx context.Context, id, userID snow.ID, at time.Time) (bool, error) {
	n, err := r.queries.OrgInvitationAccept(ctx, sqlcPostgres.OrgInvitationAcceptParams{
		ID:         id.Int64(),
		AcceptedBy: sql.NullInt64{Int64: userID.Int64(), Valid: true},
		AcceptedAt: sql.NullTime{Time: at.UTC(), Valid: true},
	})
	return n > 0, handleError(err)
}

func toDomainOrgInvitation(row sqlcPostgres.OrgInvitationListByOrgRow) *domain.OrgInvitation {
	inv := &domain.OrgInvitation{
		ID:          snow.ID(row.ID),
		OrgID:       snow.ID(row.OrgID),
		OrgName:     row.OrgName,
		OrgSlug:     row.OrgSlug,
		InvitedBy:   snow.ID(row.InvitedBy),
		InviterName: row.InviterName,
		Email:       row.Email,
		Role:        row.Role,
		TokenHash:   row.TokenHash,
		Status:      row.Status,
		ExpiresAt:   row.ExpiresAt,
		AcceptedAt:  nullTimePtr(row.AcceptedAt),
		CreatedAt:   row.CreatedAt,
	}
	if row.AcceptedBy.Valid {
		by := snow.ID(row.AcceptedBy.Int64)
		inv.AcceptedBy = &by
	}
	return inv
}
