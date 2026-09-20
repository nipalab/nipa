package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/nipalab/nipa/internal/domain"
	sqlcSqlite "github.com/nipalab/nipa/internal/repository/sqlc/sqlite"
	"github.com/nipalab/nipa/internal/snow"
)

type Group struct {
	queries *sqlcSqlite.Queries
}

func NewGroupRepository(db *sql.DB) *Group {
	return &Group{queries: sqlcSqlite.New(db)}
}

func (g *Group) Create(ctx context.Context, group domain.Group) (*domain.Group, error) {
	row, err := g.queries.GroupCreate(ctx, sqlcSqlite.GroupCreateParams{
		ID:          group.ID.Int64(),
		OrgID:       group.OrgID.Int64(),
		Name:        group.Name,
		Description: sql.NullString{String: group.Description, Valid: group.Description != ""},
	})
	if err != nil {
		return nil, domain.NewErrorDatabase(err.Error())
	}
	return toDomainGroup(row), nil
}

func (g *Group) GetByID(ctx context.Context, id snow.ID) (*domain.Group, error) {
	row, err := g.queries.GroupGet(ctx, id.Int64())
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewErrorRecordNotFound()
		}
		return nil, domain.NewErrorDatabase(err.Error())
	}
	return toDomainGroup(row), nil
}

func (g *Group) ListByOrg(ctx context.Context, orgID snow.ID) ([]*domain.Group, error) {
	rows, err := g.queries.GroupListByOrg(ctx, orgID.Int64())
	if err != nil {
		return nil, domain.NewErrorDatabase(err.Error())
	}
	groups := make([]*domain.Group, 0, len(rows))
	for _, row := range rows {
		groups = append(groups, toDomainGroup(row))
	}
	return groups, nil
}

func (g *Group) AddMember(ctx context.Context, groupID, userID snow.ID) error {
	err := g.queries.GroupMemberAdd(ctx, sqlcSqlite.GroupMemberAddParams{
		GroupID: groupID.Int64(),
		UserID:  userID.Int64(),
	})
	if err != nil {
		return domain.NewErrorDatabase(err.Error())
	}
	return nil
}

func (g *Group) RemoveMember(ctx context.Context, groupID, userID snow.ID) error {
	err := g.queries.GroupMemberRemove(ctx, sqlcSqlite.GroupMemberRemoveParams{
		GroupID: groupID.Int64(),
		UserID:  userID.Int64(),
	})
	if err != nil {
		return domain.NewErrorDatabase(err.Error())
	}
	return nil
}

func (g *Group) ListMemberIDs(ctx context.Context, groupID snow.ID) ([]snow.ID, error) {
	rows, err := g.queries.GroupMemberList(ctx, groupID.Int64())
	if err != nil {
		return nil, domain.NewErrorDatabase(err.Error())
	}
	members := make([]snow.ID, 0, len(rows))
	for _, row := range rows {
		members = append(members, snow.ID(row.UserID))
	}
	return members, nil
}

func toDomainGroup(row sqlcSqlite.Group) *domain.Group {
	return &domain.Group{
		ID:          snow.ID(row.ID),
		OrgID:       snow.ID(row.OrgID),
		Name:        row.Name,
		Description: row.Description.String,
		CreatedAt:   row.CreatedAt.Time,
		UpdatedAt:   row.UpdatedAt,
		Deleted:     row.Deleted,
		DeletedAt:   nullTimePtr(row.DeletedAt),
	}
}
