package postgres

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

type AuditRepositorySuite struct {
	baseSuite
}

func TestAuditRepositorySuite(t *testing.T) {
	suite.Run(t, new(AuditRepositorySuite))
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

func (s *AuditRepositorySuite) TestInsertAndList() {
	ctx := context.Background()
	repo := NewAuditRepository(s.db)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	s.Require().NoError(repo.Insert(ctx, auditEntry(1, domain.AuditOrgMemberAdded, base)))
	s.Require().NoError(repo.Insert(ctx, auditEntry(2, domain.AuditOrgMemberRemoved, base.Add(time.Hour))))
	s.Require().NoError(repo.Insert(ctx, domain.AuditEntry{ID: 3, OccurredAt: base.Add(2 * time.Hour), ActorType: domain.AuditActorSystem, Action: domain.AuditUserCreated}))

	all, err := repo.List(ctx, domain.AuditFilter{Limit: 10})
	s.Require().NoError(err)
	s.Require().Len(all, 3)
	s.Nil(all[0].ActorUserID)
	s.Equal(map[string]any{}, all[0].Metadata)
	first := all[2]
	s.Equal(snow.ID(7), *first.ActorUserID)
	s.Equal(snow.ID(3), *first.ProjectID)
	s.Equal(map[string]any{"old": "a", "new": "b"}, first.Metadata)
	s.Equal("10.0.0.1", first.ClientIP)
	s.True(base.Equal(first.OccurredAt))

	ids := func(filter domain.AuditFilter) []snow.ID {
		filter.Limit = 10
		entries, err := repo.List(ctx, filter)
		s.Require().NoError(err)
		out := []snow.ID{}
		for _, e := range entries {
			out = append(out, e.ID)
		}
		return out
	}
	s.Equal([]snow.ID{2, 1}, ids(domain.AuditFilter{ActorUserID: domain.IDPtr(7)}))
	s.Equal([]snow.ID{2, 1}, ids(domain.AuditFilter{OrgID: domain.IDPtr(2)}))
	s.Equal([]snow.ID{2, 1}, ids(domain.AuditFilter{ProjectID: domain.IDPtr(3)}))
	s.Equal([]snow.ID{2, 1}, ids(domain.AuditFilter{ActionPrefix: "org.member"}))
	s.Equal([]snow.ID{}, ids(domain.AuditFilter{ActionPrefix: "org.member_"}))
	s.Equal([]snow.ID{1}, ids(domain.AuditFilter{TargetID: "t" + domain.AuditOrgMemberAdded}))
	s.Equal([]snow.ID{2, 1}, ids(domain.AuditFilter{After: domain.IDPtr(3)}))

	page, err := repo.List(ctx, domain.AuditFilter{Limit: 2})
	s.Require().NoError(err)
	s.Require().Len(page, 2)
	rest, err := repo.List(ctx, domain.AuditFilter{After: &page[1].ID, Limit: 2})
	s.Require().NoError(err)
	s.Require().Len(rest, 1)
	s.Equal(snow.ID(1), rest[0].ID)
}

func (s *AuditRepositorySuite) TestPrefixAndTimeFilters() {
	ctx := context.Background()
	repo := NewAuditRepository(s.db)
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	insert := func(action string, at time.Time, idAt time.Time) snow.ID {
		id := snow.FloorAt(idAt) + 1
		s.Require().NoError(repo.Insert(ctx, auditEntry(int64(id), action, at)))
		return id
	}
	added := insert("org.member.added", base, base)
	removed := insert("org.member.removed", base.Add(time.Hour), base.Add(time.Hour))
	membership := insert("org.membership", base.Add(2*time.Hour), base.Add(2*time.Hour))
	org := insert("org", base.Add(3*time.Hour), base.Add(3*time.Hour))
	skewed := insert("ünïcode.ëvent", base.Add(4*time.Hour), base.Add(4*time.Hour-30*time.Second))

	ids := func(filter domain.AuditFilter) []snow.ID {
		filter.Limit = 10
		entries, err := repo.List(ctx, filter)
		s.Require().NoError(err)
		out := []snow.ID{}
		for _, e := range entries {
			out = append(out, e.ID)
		}
		return out
	}
	s.Equal([]snow.ID{membership, removed, added}, ids(domain.AuditFilter{ActionPrefix: "org.member"}))
	s.Equal([]snow.ID{removed, added}, ids(domain.AuditFilter{ActionPrefix: "org.member."}))
	s.Equal([]snow.ID{added}, ids(domain.AuditFilter{ActionPrefix: "org.member.added"}))
	s.Equal([]snow.ID{org, membership, removed, added}, ids(domain.AuditFilter{ActionPrefix: "org"}))
	s.Equal([]snow.ID{}, ids(domain.AuditFilter{ActionPrefix: "org.x"}))
	s.Equal([]snow.ID{}, ids(domain.AuditFilter{ActionPrefix: "org.member.added.more"}))
	s.Equal([]snow.ID{skewed}, ids(domain.AuditFilter{ActionPrefix: "ünï"}))
	s.Equal([]snow.ID{}, ids(domain.AuditFilter{ActionPrefix: "üx"}))

	since := base.Add(time.Hour)
	until := base.Add(3 * time.Hour)
	s.Equal([]snow.ID{membership, removed}, ids(domain.AuditFilter{Since: &since, Until: &until}))
	s.Equal([]snow.ID{skewed, org, membership, removed}, ids(domain.AuditFilter{Since: &since}))
	s.Equal([]snow.ID{membership, removed, added}, ids(domain.AuditFilter{Until: &until}))
	skewedAt := base.Add(4 * time.Hour)
	s.Equal([]snow.ID{skewed}, ids(domain.AuditFilter{Since: &skewedAt}))
	justBefore := skewedAt.Add(-time.Microsecond)
	s.Equal([]snow.ID{org, membership, removed, added}, ids(domain.AuditFilter{Until: &justBefore}))
}

func (s *AuditRepositorySuite) TestJoinsCallerTxAndOutlivesSubjects() {
	ctx := context.Background()
	repo := NewAuditRepository(s.db)

	err := dbtx.NewTransactor(s.db).WithinTx(ctx, func(ctx context.Context) error {
		s.Require().NoError(repo.Insert(ctx, auditEntry(1, domain.AuditOrgCreated, time.Now())))
		return errors.New("rollback")
	})
	s.Require().Error(err)
	entries, err := repo.List(ctx, domain.AuditFilter{Limit: 10})
	s.Require().NoError(err)
	s.Empty(entries)

	projectID := seedProject(s.T(), s.q, 1, "game")
	entry := auditEntry(2, domain.AuditProjectDeleted, time.Now())
	entry.ProjectID = &projectID
	entry.ActorUserID = domain.IDPtr(987654)
	s.Require().NoError(repo.Insert(ctx, entry), "no foreign keys: unknown users are accepted")
	_, err = s.db.ExecContext(ctx, `DELETE FROM branches WHERE project_id = $1`, projectID.Int64())
	s.Require().NoError(err)
	_, err = s.db.ExecContext(ctx, `DELETE FROM projects WHERE id = $1`, projectID.Int64())
	s.Require().NoError(err)
	entries, err = repo.List(ctx, domain.AuditFilter{ProjectID: &projectID, Limit: 10})
	s.Require().NoError(err)
	s.Len(entries, 1)

	s.Require().Error(repo.Insert(ctx, domain.AuditEntry{ID: 3, Metadata: map[string]any{"bad": make(chan int)}}))
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

func (s *AuditRepositorySuite) TestAuditedWritesRollBackOnRecordFailure() {
	db := s.db
	node, err := snow.NewNode(1)
	s.Require().NoError(err)
	tx := dbtx.NewTransactor(db)
	auditRepo := NewAuditRepository(db)
	orgRepo := NewOrgRepository(db)
	projectRepo := NewProjectRepository(db)
	branchRepo := NewBranchRepository(db)
	webhookRepo := NewWebhookRepository(db)
	pbac := NewPBACRepository(db)
	userID := seedUser(s.T(), s.q, "bob", "bob@example.com", sql.NullString{})
	ctx := domain.ContextWithClaim(context.Background(), domain.Claims{UserID: userID, IsSuperAdmin: true})

	good := usecase.NewAudit(auditRepo, node, tx).WithProjectOrgs(pbac)
	bad := usecase.NewAudit(failingAuditRepo{auditRepo}, node, tx).WithProjectOrgs(pbac)

	org, err := usecase.NewOrg(orgRepo, node).WithAudit(good).Create(ctx, "Studio", "studio")
	s.Require().NoError(err)
	_, err = usecase.NewOrg(orgRepo, node).WithAudit(bad).Create(ctx, "Other", "other")
	s.Require().Error(err)
	_, err = orgRepo.GetBySlug(ctx, "other")
	requireRecordNotFound(s.T(), err)

	member := seedPBACUser(s.T(), db, 99)
	s.Require().NoError(orgRepo.UpsertMember(ctx, org.ID, member, domain.OrgRoleMember))
	s.Require().Error(usecase.NewOrg(orgRepo, node).WithAudit(bad).RemoveMember(ctx, org.ID, member))
	role, err := orgRepo.MemberRole(ctx, org.ID, member)
	s.Require().NoError(err)
	s.Equal(domain.OrgRoleMember, role)

	permission := usecase.NewPermission(pbac, NewUserRepository(db), NewGroupRepository(db), usecase.NewOrg(orgRepo, node))
	projects := usecase.NewProject(projectRepo, node, permission, usecase.NewOrg(orgRepo, node))
	_, err = projects.WithAudit(bad).Create(ctx, org.ID, "Game", "", "game")
	s.Require().Error(err)
	_, err = projectRepo.GetByOrgIDAndSlug(ctx, org.ID, "game")
	requireRecordNotFound(s.T(), err)
	project, err := projects.WithAudit(good).Create(ctx, org.ID, "Game", "", "game")
	s.Require().NoError(err)

	branches := usecase.NewBranch(permission, branchRepo, node)
	_, err = branches.WithAudit(bad).SetProtection(ctx, project.ID, "main", usecase.BranchProtectionOptions{Protected: true})
	s.Require().Error(err)
	main, err := branchRepo.GetBranchByName(ctx, project.ID, "main")
	s.Require().NoError(err)
	s.False(main.IsProtected)
	_, err = branches.WithAudit(good).SetProtection(ctx, project.ID, "main", usecase.BranchProtectionOptions{Protected: true})
	s.Require().NoError(err)

	hook := seedWebhook(s.T(), webhookRepo, project.ID, 501, "ci", []string{domain.WebhookEventPush})
	hooks := usecase.NewWebhook(webhookRepo, permission, NewUserRepository(db), nil, node)
	s.Require().Error(hooks.WithAudit(bad).Delete(ctx, project.ID, hook.ID))
	_, err = webhookRepo.Get(ctx, project.ID, hook.ID)
	s.Require().NoError(err)

	entries, err := auditRepo.List(context.Background(), domain.AuditFilter{Limit: 50})
	s.Require().NoError(err)
	s.Require().Len(entries, 3, "only the committed writes left audit rows")
	s.Equal(domain.AuditBranchProtectionChanged, entries[0].Action)
	s.Equal(org.ID, *entries[0].OrgID)
	s.Equal(userID, *entries[0].ActorUserID)
}
