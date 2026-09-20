package sqlite

import (
	"context"
	"database/sql"

	"github.com/nipalab/nipa/internal/domain"
	sqlcSqlite "github.com/nipalab/nipa/internal/repository/sqlc/sqlite"
	"github.com/nipalab/nipa/internal/snow"
)

type PBAC struct {
	queries *sqlcSqlite.Queries
}

func NewPBACRepository(db *sql.DB) *PBAC {
	return &PBAC{queries: sqlcSqlite.New(db)}
}

func (p *PBAC) ListEffectiveRules(ctx context.Context, projectID snow.ID, userID snow.ID) ([]*domain.PBACRule, error) {
	rows, err := p.queries.PBACRuleListEffective(ctx, sqlcSqlite.PBACRuleListEffectiveParams{
		UserID:    userID.Int64(),
		ProjectID: sql.NullInt64{Int64: projectID.Int64(), Valid: true},
	})
	if err != nil {
		return nil, domain.NewErrorDatabase(err.Error())
	}
	return toDomainPBACRules(rows), nil
}

func (p *PBAC) ListRulesByProject(ctx context.Context, projectID snow.ID) ([]*domain.PBACRule, error) {
	rows, err := p.queries.PBACRuleListByProject(ctx, sql.NullInt64{Int64: projectID.Int64(), Valid: true})
	if err != nil {
		return nil, domain.NewErrorDatabase(err.Error())
	}
	return toDomainPBACRules(rows), nil
}

func (p *PBAC) CreateRule(ctx context.Context, rule domain.PBACRule) (*domain.PBACRule, error) {
	row, err := p.queries.PBACRuleCreate(ctx, sqlcSqlite.PBACRuleCreateParams{
		UserID:     nullSnowID(rule.UserID),
		GroupID:    nullSnowID(rule.GroupID),
		OrgID:      rule.OrgID.Int64(),
		ProjectID:  nullSnowID(rule.ProjectID),
		PathPrefix: rule.PathPrefix,
		Permission: int64(rule.Permission),
	})
	if err != nil {
		return nil, domain.NewErrorDatabase(err.Error())
	}
	return toDomainPBACRule(row), nil
}

func (p *PBAC) DeleteRule(ctx context.Context, id int64) error {
	if err := p.queries.PBACRuleDelete(ctx, id); err != nil {
		return domain.NewErrorDatabase(err.Error())
	}
	return nil
}

func (p *PBAC) ListPathPermissions(ctx context.Context, projectID snow.ID) ([]*domain.ProjectPathPermission, error) {
	rows, err := p.queries.ProjectPathPermissionList(ctx, projectID.Int64())
	if err != nil {
		return nil, domain.NewErrorDatabase(err.Error())
	}
	perms := make([]*domain.ProjectPathPermission, 0, len(rows))
	for _, row := range rows {
		perms = append(perms, toDomainProjectPathPermission(row))
	}
	return perms, nil
}

func (p *PBAC) UpsertPathPermission(ctx context.Context, perm domain.ProjectPathPermission) (*domain.ProjectPathPermission, error) {
	row, err := p.queries.ProjectPathPermissionUpsert(ctx, sqlcSqlite.ProjectPathPermissionUpsertParams{
		ProjectID:  perm.ProjectID.Int64(),
		PathPrefix: perm.PathPrefix,
		Permission: int64(perm.Permission),
	})
	if err != nil {
		return nil, domain.NewErrorDatabase(err.Error())
	}
	return toDomainProjectPathPermission(row), nil
}

func (p *PBAC) DeletePathPermission(ctx context.Context, projectID snow.ID, pathPrefix string) error {
	err := p.queries.ProjectPathPermissionDelete(ctx, sqlcSqlite.ProjectPathPermissionDeleteParams{
		ProjectID:  projectID.Int64(),
		PathPrefix: pathPrefix,
	})
	if err != nil {
		return domain.NewErrorDatabase(err.Error())
	}
	return nil
}

func toDomainPBACRules(rows []sqlcSqlite.PbacRule) []*domain.PBACRule {
	rules := make([]*domain.PBACRule, 0, len(rows))
	for _, row := range rows {
		rules = append(rules, toDomainPBACRule(row))
	}
	return rules
}

func toDomainPBACRule(row sqlcSqlite.PbacRule) *domain.PBACRule {
	return &domain.PBACRule{
		ID:         row.ID,
		UserID:     snowIDPtr(row.UserID),
		GroupID:    snowIDPtr(row.GroupID),
		OrgID:      snow.ID(row.OrgID),
		ProjectID:  snowIDPtr(row.ProjectID),
		PathPrefix: row.PathPrefix,
		Permission: domain.Permission(row.Permission),
		CreatedAt:  row.CreatedAt.Time,
	}
}

func toDomainProjectPathPermission(row sqlcSqlite.ProjectPathsPermission) *domain.ProjectPathPermission {
	return &domain.ProjectPathPermission{
		ID:         row.ID,
		ProjectID:  snow.ID(row.ProjectID),
		PathPrefix: row.PathPrefix,
		Permission: domain.Permission(row.Permission),
		CreatedAt:  row.CreatedAt.Time,
	}
}

func snowIDPtr(id sql.NullInt64) *snow.ID {
	if !id.Valid {
		return nil
	}
	value := snow.ID(id.Int64)
	return &value
}
