package postgres

import (
	"context"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/nipalab/nipa/internal/domain"
)

type PBACRepositorySuite struct {
	baseSuite
}

func TestPBACRepositorySuite(t *testing.T) {
	suite.Run(t, new(PBACRepositorySuite))
}

func (s *PBACRepositorySuite) TestListEffectiveRules() {
	ctx := context.Background()
	repo := NewPBACRepository(s.db)

	projectID := seedProject(s.T(), s.q, 1, "game")
	otherProjectID := seedProject(s.T(), s.q, 1, "other")
	userID := seedPBACUser(s.T(), s.db, 42)
	otherUserID := seedPBACUser(s.T(), s.db, 43)
	groupID := seedPBACGroup(s.T(), s.db, 5001, 1, "artists", 42)
	otherGroupID := seedPBACGroup(s.T(), s.db, 5002, 1, "engineers", 43)

	ruleOnProject, err := repo.CreateRule(ctx, domain.PBACRule{
		UserID: pbacIDPtr(userID), OrgID: 1, ProjectID: pbacIDPtr(projectID),
		PathPrefix: "", Permission: domain.PermissionRead,
	})
	s.Require().NoError(err)

	ruleOnGroup, err := repo.CreateRule(ctx, domain.PBACRule{
		GroupID: pbacIDPtr(groupID), OrgID: 1, ProjectID: pbacIDPtr(projectID),
		PathPrefix: "assets", Permission: domain.PermissionRead | domain.PermissionWrite,
	})
	s.Require().NoError(err)

	_, err = repo.CreateRule(ctx, domain.PBACRule{
		UserID: pbacIDPtr(otherUserID), OrgID: 1, ProjectID: pbacIDPtr(projectID),
		PathPrefix: "", Permission: domain.PermissionRead,
	})
	s.Require().NoError(err)

	_, err = repo.CreateRule(ctx, domain.PBACRule{
		UserID: pbacIDPtr(userID), OrgID: 1, ProjectID: pbacIDPtr(otherProjectID),
		PathPrefix: "", Permission: domain.PermissionRead,
	})
	s.Require().NoError(err)

	ruleOrgWide, err := repo.CreateRule(ctx, domain.PBACRule{
		UserID: pbacIDPtr(userID), OrgID: 1,
		PathPrefix: "docs", Permission: domain.PermissionRead,
	})
	s.Require().NoError(err)

	_, err = repo.CreateRule(ctx, domain.PBACRule{
		GroupID: pbacIDPtr(otherGroupID), OrgID: 1, ProjectID: pbacIDPtr(projectID),
		PathPrefix: "", Permission: domain.PermissionAdmin,
	})
	s.Require().NoError(err)

	rules, err := repo.ListEffectiveRules(ctx, projectID, userID)
	s.Require().NoError(err)
	s.Len(rules, 3)

	s.Equal(ruleOnProject.ID, rules[0].ID)
	s.Equal(domain.PermissionRead, rules[0].Permission)
	s.Equal("", rules[0].PathPrefix)
	s.Equal(userID, *rules[0].UserID)
	s.Equal(projectID, *rules[0].ProjectID)

	s.Equal(ruleOnGroup.ID, rules[1].ID)
	s.Equal(groupID, *rules[1].GroupID)
	s.Equal(domain.PermissionRead|domain.PermissionWrite, rules[1].Permission)
	s.Equal("assets", rules[1].PathPrefix)

	s.Equal(ruleOrgWide.ID, rules[2].ID)
	s.Nil(rules[2].ProjectID)
}

func (s *PBACRepositorySuite) TestListEffectiveRules_RejectsForeignGroup() {
	ctx := context.Background()
	repo := NewPBACRepository(s.db)

	projectID := seedProject(s.T(), s.q, 1, "game")
	userID := seedPBACUser(s.T(), s.db, 42)
	_, err := s.db.ExecContext(ctx, `INSERT INTO organizations (id, slug, name, created_by_user_id) VALUES (2, 'other', 'Other', 1)`)
	s.Require().NoError(err)
	foreignGroupID := seedPBACGroup(s.T(), s.db, 5003, 2, "foreign", 42)

	_, err = repo.CreateRule(ctx, domain.PBACRule{
		GroupID: pbacIDPtr(foreignGroupID), OrgID: 1, ProjectID: pbacIDPtr(projectID),
		PathPrefix: "", Permission: domain.PermissionRead,
	})
	s.Require().NoError(err)

	rules, err := repo.ListEffectiveRules(ctx, projectID, userID)
	s.Require().NoError(err)
	s.Empty(rules, "a rule must not apply to a group from another org")
}

func (s *PBACRepositorySuite) TestListEffectiveRules_NoRules() {
	ctx := context.Background()
	repo := NewPBACRepository(s.db)

	projectID := seedProject(s.T(), s.q, 1, "game")
	userID := seedPBACUser(s.T(), s.db, 42)

	rules, err := repo.ListEffectiveRules(ctx, projectID, userID)
	s.Require().NoError(err)
	s.Empty(rules)
}

func (s *PBACRepositorySuite) TestListRulesByProject() {
	ctx := context.Background()
	repo := NewPBACRepository(s.db)

	projectID := seedProject(s.T(), s.q, 1, "game")
	otherProjectID := seedProject(s.T(), s.q, 1, "other")
	userID := seedPBACUser(s.T(), s.db, 42)

	onProject, err := repo.CreateRule(ctx, domain.PBACRule{
		UserID: pbacIDPtr(userID), OrgID: 1, ProjectID: pbacIDPtr(projectID),
		PathPrefix: "", Permission: domain.PermissionRead,
	})
	s.Require().NoError(err)

	_, err = repo.CreateRule(ctx, domain.PBACRule{
		UserID: pbacIDPtr(userID), OrgID: 1, ProjectID: pbacIDPtr(otherProjectID),
		PathPrefix: "", Permission: domain.PermissionRead,
	})
	s.Require().NoError(err)

	_, err = repo.CreateRule(ctx, domain.PBACRule{
		UserID: pbacIDPtr(userID), OrgID: 1,
		PathPrefix: "", Permission: domain.PermissionRead,
	})
	s.Require().NoError(err)

	rules, err := repo.ListRulesByProject(ctx, projectID)
	s.Require().NoError(err)
	s.Len(rules, 1)
	s.Equal(onProject.ID, rules[0].ID)
	s.Equal("user42", rules[0].UserName)
	s.Equal("user42@example.com", rules[0].UserEmail)
	s.Empty(rules[0].GroupName)
}

func (s *PBACRepositorySuite) TestListRulesByProjectResolvesGroupName() {
	ctx := context.Background()
	repo := NewPBACRepository(s.db)

	projectID := seedProject(s.T(), s.q, 1, "game")
	groupID := seedPBACGroup(s.T(), s.db, 77, 1, "artists")

	_, err := repo.CreateRule(ctx, domain.PBACRule{
		GroupID: pbacIDPtr(groupID), OrgID: 1, ProjectID: pbacIDPtr(projectID),
		PathPrefix: "art", Permission: domain.PermissionWrite,
	})
	s.Require().NoError(err)

	rules, err := repo.ListRulesByProject(ctx, projectID)
	s.Require().NoError(err)
	s.Len(rules, 1)
	s.Equal(groupID, *rules[0].GroupID)
	s.Equal("artists", rules[0].GroupName)
	s.Empty(rules[0].UserName)
	s.Empty(rules[0].UserEmail)
}

func (s *PBACRepositorySuite) TestDeleteRule() {
	ctx := context.Background()
	repo := NewPBACRepository(s.db)

	projectID := seedProject(s.T(), s.q, 1, "game")
	userID := seedPBACUser(s.T(), s.db, 42)

	rule, err := repo.CreateRule(ctx, domain.PBACRule{
		UserID: pbacIDPtr(userID), OrgID: 1, ProjectID: pbacIDPtr(projectID),
		PathPrefix: "", Permission: domain.PermissionRead,
	})
	s.Require().NoError(err)

	s.Require().NoError(repo.DeleteRuleForProject(ctx, projectID, rule.ID))

	rules, err := repo.ListRulesByProject(ctx, projectID)
	s.Require().NoError(err)
	s.Empty(rules)
}

func (s *PBACRepositorySuite) TestGetRuleForProject() {
	ctx := context.Background()
	repo := NewPBACRepository(s.db)

	projectID := seedProject(s.T(), s.q, 1, "game")
	otherProjectID := seedProject(s.T(), s.q, 1, "other")
	userID := seedPBACUser(s.T(), s.db, 42)

	projectRule, err := repo.CreateRule(ctx, domain.PBACRule{
		UserID: pbacIDPtr(userID), OrgID: 1, ProjectID: pbacIDPtr(projectID),
		PathPrefix: "", Permission: domain.PermissionRead,
	})
	s.Require().NoError(err)

	orgRule, err := repo.CreateRule(ctx, domain.PBACRule{
		UserID: pbacIDPtr(userID), OrgID: 1,
		PathPrefix: "", Permission: domain.PermissionRead,
	})
	s.Require().NoError(err)

	got, err := repo.GetRuleForProject(ctx, projectID, projectRule.ID)
	s.Require().NoError(err)
	s.Equal(projectRule.ID, got.ID)
	s.Require().NotNil(got.ProjectID)
	s.Equal(projectID, *got.ProjectID)

	got, err = repo.GetRuleForProject(ctx, projectID, orgRule.ID)
	s.Require().NoError(err)
	s.Nil(got.ProjectID)

	_, err = repo.GetRuleForProject(ctx, otherProjectID, projectRule.ID)
	requireRecordNotFound(s.T(), err)

	_, err = repo.GetRuleForProject(ctx, projectID, 999999)
	requireRecordNotFound(s.T(), err)
}

func (s *PBACRepositorySuite) TestPathPermissions() {
	ctx := context.Background()
	repo := NewPBACRepository(s.db)

	projectID := seedProject(s.T(), s.q, 1, "game")

	root, err := repo.UpsertPathPermission(ctx, domain.ProjectPathPermission{
		ProjectID: projectID, PathPrefix: "", Permission: domain.PermissionRead,
	})
	s.Require().NoError(err)
	s.NotZero(root.ID)

	_, err = repo.UpsertPathPermission(ctx, domain.ProjectPathPermission{
		ProjectID: projectID, PathPrefix: "assets", Permission: domain.PermissionRead | domain.PermissionWrite,
	})
	s.Require().NoError(err)

	perms, err := repo.ListPathPermissions(ctx, projectID)
	s.Require().NoError(err)
	s.Len(perms, 2)
	s.Equal("", perms[0].PathPrefix)
	s.Equal(projectID, perms[0].ProjectID)
	s.Equal(domain.PermissionRead, perms[0].Permission)
	s.Equal("assets", perms[1].PathPrefix)
	s.Equal(domain.PermissionRead|domain.PermissionWrite, perms[1].Permission)

	updated, err := repo.UpsertPathPermission(ctx, domain.ProjectPathPermission{
		ProjectID: projectID, PathPrefix: "assets", Permission: domain.PermissionWrite,
	})
	s.Require().NoError(err)
	s.Equal(perms[1].ID, updated.ID)
	s.Equal(domain.PermissionWrite, updated.Permission)

	s.Require().NoError(repo.DeletePathPermission(ctx, projectID, "assets"))
	perms, err = repo.ListPathPermissions(ctx, projectID)
	s.Require().NoError(err)
	s.Len(perms, 1)
	s.Equal("", perms[0].PathPrefix)
}
