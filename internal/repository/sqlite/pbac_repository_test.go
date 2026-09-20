package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

func seedPBACUser(t *testing.T, db *sql.DB, id int64) snow.ID {
	t.Helper()

	_, err := db.ExecContext(context.Background(),
		`INSERT INTO users (id, name, email, password) VALUES (?, ?, ?, 'x')`,
		id, fmt.Sprintf("user%d", id), fmt.Sprintf("user%d@example.com", id),
	)
	require.NoError(t, err)
	return snow.ID(id)
}

func seedPBACGroup(t *testing.T, db *sql.DB, id, orgID int64, name string, members ...int64) snow.ID {
	t.Helper()

	_, err := db.ExecContext(context.Background(),
		`INSERT INTO groups (id, org_id, name) VALUES (?, ?, ?)`,
		id, orgID, name,
	)
	require.NoError(t, err)
	for _, member := range members {
		_, err := db.ExecContext(context.Background(),
			`INSERT INTO group_members (group_id, user_id) VALUES (?, ?)`,
			id, member,
		)
		require.NoError(t, err)
	}
	return snow.ID(id)
}

func pbacIDPtr(id snow.ID) *snow.ID {
	return &id
}

func TestPBACRepositorySQLite_ListEffectiveRules(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewPBACRepository(db)

	projectID := seedProject(t, q, 1, "game")
	otherProjectID := seedProject(t, q, 1, "other")
	userID := seedPBACUser(t, db, 42)
	otherUserID := seedPBACUser(t, db, 43)
	groupID := seedPBACGroup(t, db, 5001, 1, "artists", 42)
	otherGroupID := seedPBACGroup(t, db, 5002, 1, "engineers", 43)

	ruleOnProject, err := repo.CreateRule(ctx, domain.PBACRule{
		UserID: pbacIDPtr(userID), OrgID: 1, ProjectID: pbacIDPtr(projectID),
		PathPrefix: "", Permission: domain.PermissionRead,
	})
	require.NoError(t, err)

	ruleOnGroup, err := repo.CreateRule(ctx, domain.PBACRule{
		GroupID: pbacIDPtr(groupID), OrgID: 1, ProjectID: pbacIDPtr(projectID),
		PathPrefix: "assets", Permission: domain.PermissionRead | domain.PermissionWrite,
	})
	require.NoError(t, err)

	_, err = repo.CreateRule(ctx, domain.PBACRule{
		UserID: pbacIDPtr(otherUserID), OrgID: 1, ProjectID: pbacIDPtr(projectID),
		PathPrefix: "", Permission: domain.PermissionRead,
	})
	require.NoError(t, err)

	_, err = repo.CreateRule(ctx, domain.PBACRule{
		UserID: pbacIDPtr(userID), OrgID: 1, ProjectID: pbacIDPtr(otherProjectID),
		PathPrefix: "", Permission: domain.PermissionRead,
	})
	require.NoError(t, err)

	ruleOrgWide, err := repo.CreateRule(ctx, domain.PBACRule{
		UserID: pbacIDPtr(userID), OrgID: 1,
		PathPrefix: "docs", Permission: domain.PermissionRead,
	})
	require.NoError(t, err)

	_, err = repo.CreateRule(ctx, domain.PBACRule{
		GroupID: pbacIDPtr(otherGroupID), OrgID: 1, ProjectID: pbacIDPtr(projectID),
		PathPrefix: "", Permission: domain.PermissionAdmin,
	})
	require.NoError(t, err)

	rules, err := repo.ListEffectiveRules(ctx, projectID, userID)
	require.NoError(t, err)
	require.Len(t, rules, 3)

	require.Equal(t, ruleOnProject.ID, rules[0].ID)
	require.Equal(t, domain.PermissionRead, rules[0].Permission)
	require.Equal(t, "", rules[0].PathPrefix)
	require.Equal(t, userID, *rules[0].UserID)
	require.Equal(t, projectID, *rules[0].ProjectID)

	require.Equal(t, ruleOnGroup.ID, rules[1].ID)
	require.Equal(t, groupID, *rules[1].GroupID)
	require.Equal(t, domain.PermissionRead|domain.PermissionWrite, rules[1].Permission)
	require.Equal(t, "assets", rules[1].PathPrefix)

	require.Equal(t, ruleOrgWide.ID, rules[2].ID)
	require.Nil(t, rules[2].ProjectID)
}

func TestPBACRepositorySQLite_ListEffectiveRules_NoRules(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewPBACRepository(db)

	projectID := seedProject(t, q, 1, "game")
	userID := seedPBACUser(t, db, 42)

	rules, err := repo.ListEffectiveRules(ctx, projectID, userID)
	require.NoError(t, err)
	require.Empty(t, rules)
}

func TestPBACRepositorySQLite_ListRulesByProject(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewPBACRepository(db)

	projectID := seedProject(t, q, 1, "game")
	otherProjectID := seedProject(t, q, 1, "other")
	userID := seedPBACUser(t, db, 42)

	onProject, err := repo.CreateRule(ctx, domain.PBACRule{
		UserID: pbacIDPtr(userID), OrgID: 1, ProjectID: pbacIDPtr(projectID),
		PathPrefix: "", Permission: domain.PermissionRead,
	})
	require.NoError(t, err)

	_, err = repo.CreateRule(ctx, domain.PBACRule{
		UserID: pbacIDPtr(userID), OrgID: 1, ProjectID: pbacIDPtr(otherProjectID),
		PathPrefix: "", Permission: domain.PermissionRead,
	})
	require.NoError(t, err)

	_, err = repo.CreateRule(ctx, domain.PBACRule{
		UserID: pbacIDPtr(userID), OrgID: 1,
		PathPrefix: "", Permission: domain.PermissionRead,
	})
	require.NoError(t, err)

	rules, err := repo.ListRulesByProject(ctx, projectID)
	require.NoError(t, err)
	require.Len(t, rules, 1)
	require.Equal(t, onProject.ID, rules[0].ID)
}

func TestPBACRepositorySQLite_DeleteRule(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewPBACRepository(db)

	projectID := seedProject(t, q, 1, "game")
	userID := seedPBACUser(t, db, 42)

	rule, err := repo.CreateRule(ctx, domain.PBACRule{
		UserID: pbacIDPtr(userID), OrgID: 1, ProjectID: pbacIDPtr(projectID),
		PathPrefix: "", Permission: domain.PermissionRead,
	})
	require.NoError(t, err)

	require.NoError(t, repo.DeleteRule(ctx, rule.ID))

	rules, err := repo.ListRulesByProject(ctx, projectID)
	require.NoError(t, err)
	require.Empty(t, rules)
}

func TestPBACRepositorySQLite_PathPermissions(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewPBACRepository(db)

	projectID := seedProject(t, q, 1, "game")

	root, err := repo.UpsertPathPermission(ctx, domain.ProjectPathPermission{
		ProjectID: projectID, PathPrefix: "", Permission: domain.PermissionRead,
	})
	require.NoError(t, err)
	require.NotZero(t, root.ID)

	_, err = repo.UpsertPathPermission(ctx, domain.ProjectPathPermission{
		ProjectID: projectID, PathPrefix: "assets", Permission: domain.PermissionRead | domain.PermissionWrite,
	})
	require.NoError(t, err)

	perms, err := repo.ListPathPermissions(ctx, projectID)
	require.NoError(t, err)
	require.Len(t, perms, 2)
	require.Equal(t, "", perms[0].PathPrefix)
	require.Equal(t, projectID, perms[0].ProjectID)
	require.Equal(t, domain.PermissionRead, perms[0].Permission)
	require.Equal(t, "assets", perms[1].PathPrefix)
	require.Equal(t, domain.PermissionRead|domain.PermissionWrite, perms[1].Permission)

	updated, err := repo.UpsertPathPermission(ctx, domain.ProjectPathPermission{
		ProjectID: projectID, PathPrefix: "assets", Permission: domain.PermissionWrite,
	})
	require.NoError(t, err)
	require.Equal(t, perms[1].ID, updated.ID)
	require.Equal(t, domain.PermissionWrite, updated.Permission)

	require.NoError(t, repo.DeletePathPermission(ctx, projectID, "assets"))
	perms, err = repo.ListPathPermissions(ctx, projectID)
	require.NoError(t, err)
	require.Len(t, perms, 1)
	require.Equal(t, "", perms[0].PathPrefix)
}
