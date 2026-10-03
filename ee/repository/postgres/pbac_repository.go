package postgres

import (
	"context"
	"database/sql"
	"errors"

	sqlcPostgres "github.com/nipalab/nipa/ee/repository/postgres/sqlc"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type PBAC struct {
	queries *sqlcPostgres.Queries
}

func NewPBACRepository(db *sql.DB) *PBAC {
	return &PBAC{queries: sqlcPostgres.New(db)}
}

func (p *PBAC) ListEffectiveRules(ctx context.Context, projectID snow.ID, userID snow.ID) ([]*domain.PBACRule, error) {
	rows, err := p.queries.PBACRuleListEffective(ctx, sqlcPostgres.PBACRuleListEffectiveParams{
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
	rules := make([]*domain.PBACRule, 0, len(rows))
	for _, row := range rows {
		rules = append(rules, &domain.PBACRule{
			ID:         row.ID,
			UserID:     snowIDPtr(row.UserID),
			UserName:   row.UserName,
			UserEmail:  row.UserEmail,
			GroupID:    snowIDPtr(row.GroupID),
			GroupName:  row.GroupName,
			OrgID:      snow.ID(row.OrgID),
			ProjectID:  snowIDPtr(row.ProjectID),
			PathPrefix: row.PathPrefix,
			Permission: domain.Permission(row.Permission),
			CreatedAt:  row.CreatedAt.Time,
		})
	}
	return rules, nil
}

func (p *PBAC) CreateRule(ctx context.Context, rule domain.PBACRule) (*domain.PBACRule, error) {
	row, err := p.queries.PBACRuleCreate(ctx, sqlcPostgres.PBACRuleCreateParams{
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

func (p *PBAC) GetRuleForProject(ctx context.Context, projectID snow.ID, ruleID int64) (*domain.PBACRule, error) {
	row, err := p.queries.PBACRuleGetForProject(ctx, sqlcPostgres.PBACRuleGetForProjectParams{
		RuleID:    ruleID,
		ProjectID: sql.NullInt64{Int64: projectID.Int64(), Valid: true},
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewErrorRecordNotFound()
		}
		return nil, domain.NewErrorDatabase(err.Error())
	}
	return toDomainPBACRule(row), nil
}

func (p *PBAC) DeleteRuleForProject(ctx context.Context, projectID snow.ID, ruleID int64) error {
	affected, err := p.queries.PBACRuleDeleteForProject(ctx, sqlcPostgres.PBACRuleDeleteForProjectParams{
		RuleID:    ruleID,
		ProjectID: sql.NullInt64{Int64: projectID.Int64(), Valid: true},
	})
	if err != nil {
		return domain.NewErrorDatabase(err.Error())
	}
	if affected == 0 {
		return domain.NewErrorRecordNotFound()
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
	row, err := p.queries.ProjectPathPermissionUpsert(ctx, sqlcPostgres.ProjectPathPermissionUpsertParams{
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
	err := p.queries.ProjectPathPermissionDelete(ctx, sqlcPostgres.ProjectPathPermissionDeleteParams{
		ProjectID:  projectID.Int64(),
		PathPrefix: pathPrefix,
	})
	if err != nil {
		return domain.NewErrorDatabase(err.Error())
	}
	return nil
}

func toDomainPBACRules(rows []sqlcPostgres.PbacRule) []*domain.PBACRule {
	rules := make([]*domain.PBACRule, 0, len(rows))
	for _, row := range rows {
		rules = append(rules, toDomainPBACRule(row))
	}
	return rules
}

func toDomainPBACRule(row sqlcPostgres.PbacRule) *domain.PBACRule {
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

func toDomainProjectPathPermission(row sqlcPostgres.ProjectPathsPermission) *domain.ProjectPathPermission {
	return &domain.ProjectPathPermission{
		ID:         row.ID,
		ProjectID:  snow.ID(row.ProjectID),
		PathPrefix: row.PathPrefix,
		Permission: domain.Permission(row.Permission),
		CreatedAt:  row.CreatedAt.Time,
	}
}
