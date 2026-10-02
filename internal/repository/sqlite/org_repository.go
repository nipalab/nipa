package sqlite

import (
	"context"
	"database/sql"

	"github.com/nipalab/nipa/internal/domain"
	sqlcSqlite "github.com/nipalab/nipa/internal/repository/sqlc/sqlite"
	"github.com/nipalab/nipa/internal/snow"
)

type OrgRepository struct {
	db      *sql.DB
	queries *sqlcSqlite.Queries
}

func NewOrgRepository(db *sql.DB) *OrgRepository {
	return &OrgRepository{
		db:      db,
		queries: sqlcSqlite.New(db),
	}
}

func (r *OrgRepository) CreateWithOwner(ctx context.Context, org domain.Organization, ownerID snow.ID) (*domain.Organization, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, handleError(err)
	}
	defer func() { _ = tx.Rollback() }()

	q := sqlcSqlite.New(tx)
	row, err := q.CreateOrganization(ctx, sqlcSqlite.CreateOrganizationParams{
		ID:              org.ID.Int64(),
		Name:            org.Name,
		Slug:            org.Slug,
		CreatedByUserID: ownerID.Int64(),
	})
	if err != nil {
		return nil, handleError(err)
	}
	err = q.OrgMemberUpsert(ctx, sqlcSqlite.OrgMemberUpsertParams{
		OrgID:  org.ID.Int64(),
		UserID: ownerID.Int64(),
		Role:   domain.OrgRoleOwner,
	})
	if err != nil {
		return nil, handleError(err)
	}
	if err := tx.Commit(); err != nil {
		return nil, handleError(err)
	}
	return toDomainOrganization(row), nil
}

func (r *OrgRepository) GetByID(ctx context.Context, id snow.ID) (*domain.Organization, error) {
	org, err := r.queries.GetOrganization(ctx, id.Int64())
	if err != nil {
		return nil, handleError(err)
	}
	return toDomainOrganization(org), nil
}

func (r *OrgRepository) GetBySlug(ctx context.Context, slug string) (*domain.Organization, error) {
	org, err := r.queries.GetOrganizationBySlug(ctx, slug)
	if err != nil {
		return nil, handleError(err)
	}
	return toDomainOrganization(org), nil
}

func (r *OrgRepository) ListForUser(ctx context.Context, userID snow.ID) ([]*domain.OrgMembership, error) {
	rows, err := r.queries.OrgMemberListForUser(ctx, userID.Int64())
	if err != nil {
		return nil, handleError(err)
	}
	memberships := make([]*domain.OrgMembership, 0, len(rows))
	for _, row := range rows {
		memberships = append(memberships, &domain.OrgMembership{
			Org: domain.Organization{
				ID:              snow.ID(row.ID),
				Name:            row.Name,
				Slug:            row.Slug,
				CreatedAt:       row.CreatedAt,
				UpdatedAt:       row.UpdatedAt,
				Deleted:         row.Deleted,
				DeletedAt:       nullTimePtr(row.DeletedAt),
				CreatedByUserID: snow.ID(row.CreatedByUserID),
			},
			Role: row.Role,
		})
	}
	return memberships, nil
}

func (r *OrgRepository) ListMembers(ctx context.Context, orgID snow.ID) ([]*domain.OrgMember, error) {
	rows, err := r.queries.OrgMemberList(ctx, orgID.Int64())
	if err != nil {
		return nil, handleError(err)
	}
	members := make([]*domain.OrgMember, 0, len(rows))
	for _, row := range rows {
		members = append(members, &domain.OrgMember{
			User: domain.User{
				ID:           snow.ID(row.ID),
				Name:         row.Name,
				Email:        row.Email,
				PhotoUrl:     row.PhotoUrl.String,
				IsAdmin:      row.IsAdmin,
				IsSuperAdmin: row.IsSuperAdmin,
			},
			Role:     row.Role,
			JoinedAt: row.CreatedAt,
		})
	}
	return members, nil
}

func (r *OrgRepository) MemberRole(ctx context.Context, orgID, userID snow.ID) (string, error) {
	role, err := r.queries.OrgMemberGet(ctx, sqlcSqlite.OrgMemberGetParams{
		OrgID:  orgID.Int64(),
		UserID: userID.Int64(),
	})
	if err != nil {
		return "", handleError(err)
	}
	return role, nil
}

func (r *OrgRepository) UpsertMember(ctx context.Context, orgID, userID snow.ID, role string) error {
	err := r.queries.OrgMemberUpsert(ctx, sqlcSqlite.OrgMemberUpsertParams{
		OrgID:  orgID.Int64(),
		UserID: userID.Int64(),
		Role:   role,
	})
	return handleError(err)
}

func (r *OrgRepository) RemoveMember(ctx context.Context, orgID, userID snow.ID) error {
	err := r.queries.OrgMemberDelete(ctx, sqlcSqlite.OrgMemberDeleteParams{
		OrgID:  orgID.Int64(),
		UserID: userID.Int64(),
	})
	return handleError(err)
}

func (r *OrgRepository) CountMembersByRole(ctx context.Context, orgID snow.ID, role string) (int, error) {
	count, err := r.queries.OrgMemberCountByRole(ctx, sqlcSqlite.OrgMemberCountByRoleParams{
		OrgID: orgID.Int64(),
		Role:  role,
	})
	if err != nil {
		return 0, handleError(err)
	}
	return int(count), nil
}

func toDomainOrganization(org sqlcSqlite.Organization) *domain.Organization {
	return &domain.Organization{
		ID:              snow.ID(org.ID),
		Slug:            org.Slug,
		Name:            org.Name,
		CreatedAt:       org.CreatedAt,
		UpdatedAt:       org.UpdatedAt,
		CreatedByUserID: snow.ID(org.CreatedByUserID),
	}
}
