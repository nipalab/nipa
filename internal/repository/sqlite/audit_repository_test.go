package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	database "github.com/nipalab/nipa/db"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	sqlcSqlite "github.com/nipalab/nipa/internal/repository/sqlc/sqlite"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

func newAuditTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := database.OpenSQLite(filepath.Join(t.TempDir(), "nipa.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, database.MigrateUp(db, "sqlite3"))
	return db
}

func auditEntry(id int64, action string, at time.Time) domain.AuditEntry {
	return domain.AuditEntry{
		ID:          snow.ID(id),
		OccurredAt:  at,
		ActorType:   domain.AuditActorUser,
		ActorUserID: domain.IDPtr(7),
		Action:      action,
		OrgID:       domain.IDPtr(2),
		ProjectID:   domain.IDPtr(3),
		TargetType:  domain.AuditTargetUser,
		TargetID:    "t" + action,
		Metadata:    map[string]any{"old": "a", "new": "b"},
		RequestID:   "req",
		ClientIP:    "10.0.0.1",
		UserAgent:   "nipa/1",
	}
}

func TestAuditRepositorySQLite_InsertAndList(t *testing.T) {
	ctx := context.Background()
	repo := NewAuditRepository(newAuditTestDB(t))
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	require.NoError(t, repo.Insert(ctx, auditEntry(1, domain.AuditOrgMemberAdded, base)))
	require.NoError(t, repo.Insert(ctx, auditEntry(2, domain.AuditOrgMemberRemoved, base.Add(time.Hour))))
	system := domain.AuditEntry{ID: 3, OccurredAt: base.Add(2 * time.Hour), ActorType: domain.AuditActorSystem, Action: domain.AuditUserCreated}
	require.NoError(t, repo.Insert(ctx, system))

	all, err := repo.List(ctx, domain.AuditFilter{Limit: 10})
	require.NoError(t, err)
	require.Len(t, all, 3)
	require.Equal(t, snow.ID(3), all[0].ID)
	require.Nil(t, all[0].ActorUserID)
	require.Nil(t, all[0].OrgID)
	require.Equal(t, map[string]any{}, all[0].Metadata)
	first := all[2]
	require.Equal(t, snow.ID(7), *first.ActorUserID)
	require.Equal(t, snow.ID(2), *first.OrgID)
	require.Equal(t, snow.ID(3), *first.ProjectID)
	require.Equal(t, map[string]any{"old": "a", "new": "b"}, first.Metadata)
	require.Equal(t, "10.0.0.1", first.ClientIP)
	require.Equal(t, "nipa/1", first.UserAgent)
	require.Equal(t, "req", first.RequestID)
	require.True(t, base.Equal(first.OccurredAt))

	ids := func(filter domain.AuditFilter) []snow.ID {
		t.Helper()
		filter.Limit = 10
		entries, err := repo.List(ctx, filter)
		require.NoError(t, err)
		out := []snow.ID{}
		for _, e := range entries {
			out = append(out, e.ID)
		}
		return out
	}
	since := base.Add(30 * time.Minute)
	until := base.Add(90 * time.Minute)
	require.Equal(t, []snow.ID{2, 1}, ids(domain.AuditFilter{ActorUserID: domain.IDPtr(7)}))
	require.Equal(t, []snow.ID{2, 1}, ids(domain.AuditFilter{OrgID: domain.IDPtr(2)}))
	require.Equal(t, []snow.ID{2, 1}, ids(domain.AuditFilter{ProjectID: domain.IDPtr(3)}))
	require.Equal(t, []snow.ID{2, 1}, ids(domain.AuditFilter{ActionPrefix: "org.member"}))
	require.Equal(t, []snow.ID{}, ids(domain.AuditFilter{ActionPrefix: "org.member_"}))
	require.Equal(t, []snow.ID{1}, ids(domain.AuditFilter{TargetID: "t" + domain.AuditOrgMemberAdded}))
	require.Equal(t, []snow.ID{3, 2}, ids(domain.AuditFilter{Since: &since}))
	require.Equal(t, []snow.ID{2, 1}, ids(domain.AuditFilter{Until: &until}))
	require.Equal(t, []snow.ID{2}, ids(domain.AuditFilter{Since: &since, Until: &until}))
	require.Equal(t, []snow.ID{2, 1}, ids(domain.AuditFilter{After: domain.IDPtr(3)}))

	page, err := repo.List(ctx, domain.AuditFilter{Limit: 2})
	require.NoError(t, err)
	require.Len(t, page, 2)
	rest, err := repo.List(ctx, domain.AuditFilter{After: &page[1].ID, Limit: 2})
	require.NoError(t, err)
	require.Len(t, rest, 1)
	require.Equal(t, snow.ID(1), rest[0].ID)
}

func TestAuditRepositorySQLite_JoinsCallerTx(t *testing.T) {
	ctx := context.Background()
	db := newAuditTestDB(t)
	repo := NewAuditRepository(db)

	err := dbtx.NewTransactor(db).WithinTx(ctx, func(ctx context.Context) error {
		require.NoError(t, repo.Insert(ctx, auditEntry(1, domain.AuditOrgCreated, time.Now())))
		return errors.New("rollback")
	})
	require.Error(t, err)
	entries, err := repo.List(ctx, domain.AuditFilter{Limit: 10})
	require.NoError(t, err)
	require.Empty(t, entries)

	require.Error(t, repo.Insert(ctx, domain.AuditEntry{ID: 2, Metadata: map[string]any{"bad": make(chan int)}}))
}

func TestAuditRepositorySQLite_EntriesOutliveSubjects(t *testing.T) {
	ctx := context.Background()
	db := newAuditTestDB(t)
	repo := NewAuditRepository(db)
	q := sqlcSqlite.New(db)
	userID := seedUser(t, q, "bob", "bob@example.com", sql.NullString{})
	orgID := seedOrg(t, q, "Studio", "studio")
	projectID := seedProject(t, q, orgID.Int64(), "game")

	entry := auditEntry(1, domain.AuditProjectCreated, time.Now())
	entry.ActorUserID, entry.OrgID, entry.ProjectID = &userID, &orgID, &projectID
	require.NoError(t, repo.Insert(ctx, entry))

	deletes := []struct {
		stmt string
		id   snow.ID
	}{
		{"DELETE FROM projects WHERE id = ?", projectID},
		{"DELETE FROM organizations WHERE id = ?", orgID},
		{"DELETE FROM users WHERE id = ?", userID},
	}
	for _, d := range deletes {
		_, err := db.ExecContext(ctx, d.stmt, d.id.Int64())
		require.NoError(t, err)
	}
	entries, err := repo.List(ctx, domain.AuditFilter{ProjectID: &projectID, Limit: 10})
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, userID, *entries[0].ActorUserID)
}

type failingAuditRepo struct {
	*AuditRepository
}

func (f failingAuditRepo) Insert(ctx context.Context, entry domain.AuditEntry) error {
	if err := f.AuditRepository.Insert(ctx, entry); err != nil {
		return err
	}
	return errors.New("audit insert failed")
}

// A record failure rolls back both the audited write and the audit row.
func TestAuditedWrites_RollBackOnRecordFailure(t *testing.T) {
	db := newAuditTestDB(t)
	node, err := snow.NewNode(1)
	require.NoError(t, err)
	tx := dbtx.NewTransactor(db)
	auditRepo := NewAuditRepository(db)
	orgRepo := NewOrgRepository(db)
	projectRepo := NewProjectRepository(db)
	branchRepo := NewBranchRepository(db)
	webhookRepo := NewWebhookRepository(db)
	pbac := NewPBACRepository(db)
	q := sqlcSqlite.New(db)
	userID := seedUser(t, q, "bob", "bob@example.com", sql.NullString{})
	ctx := domain.ContextWithClaim(context.Background(), domain.Claims{UserID: userID, IsSuperAdmin: true})

	good := usecase.NewAudit(auditRepo, node, tx).WithProjectOrgs(pbac)
	bad := usecase.NewAudit(failingAuditRepo{auditRepo}, node, tx).WithProjectOrgs(pbac)

	org, err := usecase.NewOrg(orgRepo, node).WithAudit(good).Create(ctx, "Studio", "studio")
	require.NoError(t, err)
	_, err = usecase.NewOrg(orgRepo, node).WithAudit(bad).Create(ctx, "Other", "other")
	require.Error(t, err)
	_, err = orgRepo.GetBySlug(ctx, "other")
	requireRecordNotFound(t, err)

	member := seedPBACUser(t, db, 99)
	require.NoError(t, orgRepo.UpsertMember(ctx, org.ID, member, domain.OrgRoleMember))
	require.Error(t, usecase.NewOrg(orgRepo, node).WithAudit(bad).RemoveMember(ctx, org.ID, member))
	role, err := orgRepo.MemberRole(ctx, org.ID, member)
	require.NoError(t, err)
	require.Equal(t, domain.OrgRoleMember, role)

	permission := usecase.NewPermission(pbac, NewUserRepository(db), NewGroupRepository(db), usecase.NewOrg(orgRepo, node))
	projects := usecase.NewProject(projectRepo, node, permission, usecase.NewOrg(orgRepo, node))
	_, err = projects.WithAudit(bad).Create(ctx, org.ID, "Game", "", "game")
	require.Error(t, err)
	_, err = projectRepo.GetByOrgIDAndSlug(ctx, org.ID, "game")
	requireRecordNotFound(t, err)
	project, err := projects.WithAudit(good).Create(ctx, org.ID, "Game", "", "game")
	require.NoError(t, err)

	branches := usecase.NewBranch(permission, branchRepo, node)
	_, err = branches.WithAudit(bad).SetProtection(ctx, project.ID, "main", usecase.BranchProtectionOptions{Protected: true})
	require.Error(t, err)
	main, err := branchRepo.GetBranchByName(ctx, project.ID, "main")
	require.NoError(t, err)
	require.False(t, main.IsProtected)
	_, err = branches.WithAudit(good).SetProtection(ctx, project.ID, "main", usecase.BranchProtectionOptions{Protected: true})
	require.NoError(t, err)

	hook := seedWebhook(t, webhookRepo, project.ID, 501, "ci", []string{domain.WebhookEventPush})
	hooks := usecase.NewWebhook(webhookRepo, permission, NewUserRepository(db), nil, node)
	require.Error(t, hooks.WithAudit(bad).Delete(ctx, project.ID, hook.ID))
	_, err = webhookRepo.Get(ctx, project.ID, hook.ID)
	require.NoError(t, err)

	entries, err := auditRepo.List(context.Background(), domain.AuditFilter{Limit: 50})
	require.NoError(t, err)
	actions := map[string]domain.AuditEntry{}
	for _, entry := range entries {
		actions[entry.Action] = *entry
	}
	require.Len(t, entries, 3, "only the committed writes left audit rows")
	require.Contains(t, actions, domain.AuditOrgCreated)
	require.Contains(t, actions, domain.AuditProjectCreated)
	protection := actions[domain.AuditBranchProtectionChanged]
	require.Equal(t, userID, *protection.ActorUserID)
	require.Equal(t, org.ID, *protection.OrgID, "the org is resolved from the project")
}
