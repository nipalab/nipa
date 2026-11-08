package usecase

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

var errAudit = errors.New("audit down")

func superCtx() context.Context {
	return permissionCtx(1, withSuperAdmin())
}

func requireActor(t *testing.T, entry domain.AuditEntry, action string) {
	t.Helper()
	require.Equal(t, action, entry.Action)
	require.Empty(t, entry.ActorType, "the recorder fills the actor from the claim")
}

func TestOrgAudit(t *testing.T) {
	t.Run("created", func(t *testing.T) {
		org, repo := newTestOrg(t)
		rec := &captureRecorder{}
		org.WithAudit(rec)
		repo.EXPECT().GetBySlug(gomock.Any(), "studio").Return(nil, domain.NewErrorNotFound("x"))
		repo.EXPECT().CreateWithOwner(gomock.Any(), gomock.Any(), snow.ID(7)).DoAndReturn(
			func(_ context.Context, o domain.Organization, _ snow.ID) (*domain.Organization, error) {
				return &o, nil
			})

		created, err := org.Create(permissionCtx(7), "Studio", "studio")
		require.NoError(t, err)
		entry := rec.only(t)
		requireActor(t, entry, domain.AuditOrgCreated)
		require.Equal(t, created.ID, *entry.OrgID)
		require.Equal(t, "studio", entry.Metadata["slug"])
	})

	t.Run("member added, role changed, unchanged, removed", func(t *testing.T) {
		org, repo := newTestOrg(t)
		rec := &captureRecorder{}
		org.WithAudit(rec)

		repo.EXPECT().MemberRole(gomock.Any(), snow.ID(2), snow.ID(9)).Return("", domain.NewErrorNotFound("x"))
		repo.EXPECT().UpsertMember(gomock.Any(), snow.ID(2), snow.ID(9), domain.OrgRoleOwner).Return(nil)
		require.NoError(t, org.AddMember(superCtx(), 2, 9, domain.OrgRoleOwner))

		repo.EXPECT().MemberRole(gomock.Any(), snow.ID(2), snow.ID(9)).Return(domain.OrgRoleOwner, nil)
		repo.EXPECT().CountMembersByRole(gomock.Any(), snow.ID(2), domain.OrgRoleOwner).Return(2, nil)
		repo.EXPECT().UpsertMember(gomock.Any(), snow.ID(2), snow.ID(9), domain.OrgRoleMember).Return(nil)
		require.NoError(t, org.UpdateMemberRole(superCtx(), 2, 9, domain.OrgRoleMember))

		repo.EXPECT().MemberRole(gomock.Any(), snow.ID(2), snow.ID(9)).Return(domain.OrgRoleOwner, nil)
		repo.EXPECT().UpsertMember(gomock.Any(), snow.ID(2), snow.ID(9), domain.OrgRoleOwner).Return(nil)
		require.NoError(t, org.UpdateMemberRole(superCtx(), 2, 9, domain.OrgRoleOwner))

		repo.EXPECT().MemberRole(gomock.Any(), snow.ID(2), snow.ID(9)).Return(domain.OrgRoleMember, nil).Times(2)
		repo.EXPECT().RemoveMember(gomock.Any(), snow.ID(2), snow.ID(9)).Return(nil)
		require.NoError(t, org.RemoveMember(superCtx(), 2, 9))

		require.Len(t, rec.entries, 3)
		requireActor(t, rec.entries[0], domain.AuditOrgMemberAdded)
		require.Equal(t, domain.OrgRoleOwner, rec.entries[0].Metadata["role"])
		require.Equal(t, snow.ID(9).Base36(), rec.entries[0].TargetID)
		requireActor(t, rec.entries[1], domain.AuditOrgMemberRoleChanged)
		require.Equal(t, domain.OrgRoleOwner, rec.entries[1].Metadata["old_role"])
		require.Equal(t, domain.OrgRoleMember, rec.entries[1].Metadata["new_role"])
		requireActor(t, rec.entries[2], domain.AuditOrgMemberRemoved)
		require.Equal(t, snow.ID(2), *rec.entries[2].OrgID)
	})

	t.Run("record failure fails the change", func(t *testing.T) {
		org, repo := newTestOrg(t)
		org.WithAudit(&captureRecorder{err: errAudit})
		repo.EXPECT().MemberRole(gomock.Any(), snow.ID(2), snow.ID(9)).Return("", domain.NewErrorNotFound("x")).Times(2)
		repo.EXPECT().UpsertMember(gomock.Any(), snow.ID(2), snow.ID(9), domain.OrgRoleMember).Return(nil)
		require.ErrorIs(t, org.AddMember(superCtx(), 2, 9, domain.OrgRoleMember), errAudit)

		repo.EXPECT().MemberRole(gomock.Any(), snow.ID(2), snow.ID(9)).Return("", errors.New("db"))
		require.Error(t, org.UpdateMemberRole(superCtx(), 2, 9, domain.OrgRoleOwner))
	})
}

func TestGroupAudit(t *testing.T) {
	group, repo, _, _ := newTestGroup(t)
	rec := &captureRecorder{}
	group.WithAudit(rec)
	g := &domain.Group{ID: 4, OrgID: 2, Name: "artists"}

	repo.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, created domain.Group) (*domain.Group, error) {
		return &created, nil
	})
	_, err := group.Create(superCtx(), 2, "artists", "")
	require.NoError(t, err)

	repo.EXPECT().GetByID(gomock.Any(), snow.ID(4)).Return(g, nil).Times(2)
	repo.EXPECT().AddMember(gomock.Any(), snow.ID(4), snow.ID(9)).Return(nil)
	repo.EXPECT().RemoveMember(gomock.Any(), snow.ID(4), snow.ID(9)).Return(nil)
	require.NoError(t, group.AddMember(superCtx(), 2, 4, 9))
	require.NoError(t, group.RemoveMember(superCtx(), 2, 4, 9))

	require.Len(t, rec.entries, 3)
	requireActor(t, rec.entries[0], domain.AuditGroupCreated)
	require.Equal(t, "artists", rec.entries[0].Metadata["name"])
	requireActor(t, rec.entries[1], domain.AuditGroupMemberAdded)
	require.Equal(t, snow.ID(9), rec.entries[1].Metadata["user_id"])
	require.Equal(t, snow.ID(2), *rec.entries[1].OrgID)
	requireActor(t, rec.entries[2], domain.AuditGroupMemberRemoved)

	group.WithAudit(&captureRecorder{err: errAudit})
	repo.EXPECT().GetByID(gomock.Any(), snow.ID(4)).Return(g, nil)
	repo.EXPECT().RemoveMember(gomock.Any(), snow.ID(4), snow.ID(9)).Return(nil)
	require.ErrorIs(t, group.RemoveMember(superCtx(), 2, 4, 9), errAudit)
}

func newAuditPermission(t *testing.T) (*Permission, *MockpbacRepository, *MockuserLookup, *captureRecorder) {
	t.Helper()
	ctrl := gomock.NewController(t)
	repo := NewMockpbacRepository(ctrl)
	users := NewMockuserLookup(ctrl)
	rec := &captureRecorder{}
	perm := NewPermission(repo, users, NewMockgroupLookup(ctrl), NewMockorgAuthorizer(ctrl)).WithAudit(rec)
	return perm, repo, users, rec
}

func TestPermissionAudit_Rules(t *testing.T) {
	perm, repo, users, rec := newAuditPermission(t)
	user := snow.ID(9)
	project := snow.ID(5)

	users.EXPECT().GetByID(gomock.Any(), user).Return(&domain.User{ID: user}, nil)
	repo.EXPECT().CreateRule(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, rule domain.PBACRule) (*domain.PBACRule, error) {
		rule.ID = 11
		return &rule, nil
	})
	_, err := perm.CreateRule(superCtx(), domain.PBACRule{UserID: &user, OrgID: 2, ProjectID: &project, PathPrefix: "art", Permission: domain.PermissionRead})
	require.NoError(t, err)

	repo.EXPECT().GetRuleForProject(gomock.Any(), project, int64(12)).Return(&domain.PBACRule{ID: 12, GroupID: &user, OrgID: 2, Permission: domain.PermissionWrite}, nil)
	repo.EXPECT().DeleteRuleForProject(gomock.Any(), project, int64(12)).Return(nil)
	require.NoError(t, perm.DeleteRule(superCtx(), project, 12))

	require.Len(t, rec.entries, 2)
	created := rec.entries[0]
	requireActor(t, created, domain.AuditAccessRuleCreated)
	require.Equal(t, "11", created.TargetID)
	require.Equal(t, project, *created.ProjectID)
	require.Equal(t, snow.ID(2), *created.OrgID)
	require.Equal(t, user, created.Metadata["user_id"])
	require.Equal(t, "art", created.Metadata["path_prefix"])
	require.Equal(t, int64(domain.PermissionRead), created.Metadata["permission"])
	deleted := rec.entries[1]
	requireActor(t, deleted, domain.AuditAccessRuleDeleted)
	require.Equal(t, user, deleted.Metadata["group_id"])
	require.Nil(t, deleted.ProjectID)
}

func TestPermissionAudit_PathPermissions(t *testing.T) {
	perm, repo, _, rec := newAuditPermission(t)
	project := snow.ID(5)
	existing := []*domain.ProjectPathPermission{{ProjectID: project, PathPrefix: "art", Permission: domain.PermissionRead}}

	repo.EXPECT().ListPathPermissions(gomock.Any(), project).Return(existing, nil)
	repo.EXPECT().UpsertPathPermission(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p domain.ProjectPathPermission) (*domain.ProjectPathPermission, error) {
		return &p, nil
	})
	_, err := perm.SetPathPermission(superCtx(), project, "art", domain.PermissionWrite)
	require.NoError(t, err)

	repo.EXPECT().ListPathPermissions(gomock.Any(), project).Return(nil, nil)
	repo.EXPECT().UpsertPathPermission(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p domain.ProjectPathPermission) (*domain.ProjectPathPermission, error) {
		return &p, nil
	})
	_, err = perm.SetPathPermission(superCtx(), project, "code", domain.PermissionRead)
	require.NoError(t, err)

	repo.EXPECT().ListPathPermissions(gomock.Any(), project).Return(existing, nil)
	repo.EXPECT().DeletePathPermission(gomock.Any(), project, "art").Return(nil)
	require.NoError(t, perm.DeletePathPermission(superCtx(), project, "art"))

	require.Len(t, rec.entries, 3)
	set := rec.entries[0]
	requireActor(t, set, domain.AuditAccessPathPermissionSet)
	require.Equal(t, int64(domain.PermissionRead), set.Metadata["old_permission"])
	require.Equal(t, int64(domain.PermissionWrite), set.Metadata["new_permission"])
	require.Equal(t, project, *set.ProjectID)
	require.NotContains(t, rec.entries[1].Metadata, "old_permission")
	requireActor(t, rec.entries[2], domain.AuditAccessPathPermissionDeleted)
	require.Equal(t, int64(domain.PermissionRead), rec.entries[2].Metadata["old_permission"])

	repo.EXPECT().ListPathPermissions(gomock.Any(), project).Return(nil, errors.New("db"))
	require.Error(t, perm.DeletePathPermission(superCtx(), project, "art"))
}

func TestBranchAudit_Protection(t *testing.T) {
	uc, perm, repo := newManageBranchFixture(t)
	rec := &captureRecorder{}
	uc.WithAudit(rec)
	approvals := int64(2)

	perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true).Times(2)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "release").
		Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "release", RequiredApprovals: 1}, nil).Times(2)
	repo.EXPECT().SetBranchProtection(gomock.Any(), snow.ID(1), snow.ID(3), gomock.Any()).Return(nil)
	repo.EXPECT().GetByProjectIDAndID(gomock.Any(), snow.ID(1), snow.ID(3)).
		Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "release", IsProtected: true, RequiredApprovals: 2}, nil)

	_, err := uc.SetProtection(permissionCtx(42), 1, "release", BranchProtectionOptions{Protected: true, RequiredApprovals: &approvals})
	require.NoError(t, err)
	_, err = uc.SetProtection(permissionCtx(42), 1, "release", BranchProtectionOptions{})
	require.NoError(t, err, "no change records nothing")

	entry := rec.only(t)
	requireActor(t, entry, domain.AuditBranchProtectionChanged)
	require.Equal(t, snow.ID(3).Base36(), entry.TargetID)
	require.Equal(t, "release", entry.Metadata["branch"])
	old := entry.Metadata["old"].(map[string]any)
	next := entry.Metadata["new"].(map[string]any)
	require.Equal(t, false, old["protected"])
	require.Equal(t, int64(1), old["required_approvals"])
	require.Equal(t, true, next["protected"])
	require.Equal(t, int64(2), next["required_approvals"])
}

func TestBranchProtectionAuditValue(t *testing.T) {
	value := protectionAuditValue(branchProtection(&domain.Branch{
		RequiredReviewers: []domain.ReviewActor{{UserID: 4}},
		RequiredChecks:    []string{"ci"},
	}))
	require.Equal(t, []snow.ID{4}, value["required_reviewers"])
	require.Equal(t, []string{"ci"}, value["required_checks"])
	empty := protectionAuditValue(domain.BranchProtection{})
	require.Equal(t, []snow.ID{}, empty["required_reviewers"])
	require.Equal(t, []string{}, empty["required_checks"])
}

func TestFileLockAudit_ForceRelease(t *testing.T) {
	t.Run("own lock is not audited", func(t *testing.T) {
		uc, repo, branchRepo, perm := newTestFileLock(t)
		rec := &captureRecorder{}
		uc.WithAudit(rec)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		branchRepo.EXPECT().GetDefaultBranch(gomock.Any(), snow.ID(1)).Return(defaultBranchFixture(), nil)
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), "a.png", nil).Return(&domain.FileLock{ID: 9, HeldBy: 7}, nil)
		repo.EXPECT().Delete(gomock.Any(), snow.ID(9)).Return(nil)
		require.NoError(t, uc.Release(permissionCtx(7), 1, "", "a.png"))
		require.Empty(t, rec.entries)
	})

	t.Run("admin release of another holder's lock", func(t *testing.T) {
		uc, repo, branchRepo, perm := newTestFileLock(t)
		rec := &captureRecorder{}
		uc.WithAudit(rec)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(devBranchFixture(), nil)
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), "a.png", snowPtr(3)).Return(&domain.FileLock{ID: 9, Path: "a.png", HeldBy: 8}, nil)
		perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true)
		repo.EXPECT().Delete(gomock.Any(), snow.ID(9)).Return(nil)

		require.NoError(t, uc.Release(permissionCtx(7), 1, "feature", "a.png"))
		entry := rec.only(t)
		requireActor(t, entry, domain.AuditLockForceReleased)
		require.Equal(t, snow.ID(1), *entry.ProjectID)
		require.Equal(t, snow.ID(8), entry.Metadata["holder_id"])
		require.Equal(t, "a.png", entry.Metadata["path"])
		require.Equal(t, "feature", entry.Metadata["branch"])
	})

	t.Run("record failure keeps the lock", func(t *testing.T) {
		uc, repo, branchRepo, perm := newTestFileLock(t)
		uc.WithAudit(&captureRecorder{err: errAudit})
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		branchRepo.EXPECT().GetDefaultBranch(gomock.Any(), snow.ID(1)).Return(defaultBranchFixture(), nil)
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), "a.png", nil).Return(&domain.FileLock{ID: 9, HeldBy: 8}, nil)
		perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true)
		repo.EXPECT().Delete(gomock.Any(), snow.ID(9)).Return(nil)
		require.ErrorIs(t, uc.Release(permissionCtx(7), 1, "", "a.png"), errAudit)
	})
}

func TestUserAudit(t *testing.T) {
	user, repo := newTestUser(t, true)
	rec := &captureRecorder{}
	user.WithAudit(rec)
	ctx := superCtx()

	repo.EXPECT().GetByEmail(gomock.Any(), "bob@example.com").Return(nil, domain.NewErrorNotFound("x"))
	repo.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, u domain.User) (*domain.User, error) {
		return &u, nil
	})
	created, err := user.Create(ctx, "bob", "bob@example.com", "password123")
	require.NoError(t, err)

	repo.EXPECT().GetByID(gomock.Any(), created.ID).Return(&domain.User{ID: created.ID}, nil).Times(2)
	repo.EXPECT().UpdateAdminFlags(gomock.Any(), created.ID, true, false).Return(nil)
	_, err = user.SetAdminFlags(ctx, created.ID, true, false)
	require.NoError(t, err)

	repo.EXPECT().UpdatePasswordAndRevokeTokens(gomock.Any(), created.ID, gomock.Any()).Return(nil)
	require.NoError(t, user.ResetPassword(ctx, created.ID, "newpassword"))

	repo.EXPECT().GetByEmail(gomock.Any(), "robert@example.com").Return(nil, domain.NewErrorNotFound("x"))
	repo.EXPECT().GetByID(gomock.Any(), created.ID).Return(&domain.User{ID: created.ID, Email: "bob@example.com"}, nil).Times(2)
	repo.EXPECT().UpdateEmailAndRevokeTokens(gomock.Any(), created.ID, "robert@example.com").Return(nil)
	_, err = user.UpdateEmail(ctx, created.ID, "robert@example.com")
	require.NoError(t, err)

	repo.EXPECT().DeactivateAndRevokeTokens(gomock.Any(), created.ID).Return(nil)
	require.NoError(t, user.Deactivate(ctx, created.ID))

	require.Len(t, rec.entries, 5)
	requireActor(t, rec.entries[0], domain.AuditUserCreated)
	require.Equal(t, "admin", rec.entries[0].Metadata["source"])
	require.NotContains(t, rec.entries[0].Metadata, "password")
	requireActor(t, rec.entries[1], domain.AuditUserAdminFlagsChanged)
	require.Equal(t, map[string]any{"is_admin": false, "is_superadmin": false}, rec.entries[1].Metadata["old"])
	require.Equal(t, map[string]any{"is_admin": true, "is_superadmin": false}, rec.entries[1].Metadata["new"])
	requireActor(t, rec.entries[2], domain.AuditUserPasswordResetByAdmin)
	require.Empty(t, rec.entries[2].Metadata)
	requireActor(t, rec.entries[3], domain.AuditUserEmailChanged)
	require.Equal(t, "bob@example.com", rec.entries[3].Metadata["old_email"])
	require.Equal(t, "robert@example.com", rec.entries[3].Metadata["new_email"])
	requireActor(t, rec.entries[4], domain.AuditUserDeactivated)
	require.Equal(t, created.ID.Base36(), rec.entries[4].TargetID)
}

func TestUserAudit_UnchangedAndFailures(t *testing.T) {
	user, repo := newTestUser(t, true)
	rec := &captureRecorder{}
	user.WithAudit(rec)

	repo.EXPECT().GetByID(gomock.Any(), snow.ID(5)).Return(&domain.User{ID: 5, IsAdmin: true}, nil).Times(2)
	repo.EXPECT().UpdateAdminFlags(gomock.Any(), snow.ID(5), true, false).Return(nil)
	_, err := user.SetAdminFlags(superCtx(), 5, true, false)
	require.NoError(t, err)
	require.Empty(t, rec.entries)

	repo.EXPECT().GetByID(gomock.Any(), snow.ID(5)).Return(nil, errors.New("db"))
	_, err = user.SetAdminFlags(superCtx(), 5, false, false)
	require.Error(t, err)

	repo.EXPECT().GetByEmail(gomock.Any(), "a@example.com").Return(nil, domain.NewErrorNotFound("x"))
	repo.EXPECT().GetByID(gomock.Any(), snow.ID(5)).Return(nil, errors.New("db"))
	_, err = user.UpdateEmail(superCtx(), 5, "a@example.com")
	require.Error(t, err)

	user.WithAudit(&captureRecorder{err: errAudit})
	repo.EXPECT().DeactivateAndRevokeTokens(gomock.Any(), snow.ID(5)).Return(nil)
	require.ErrorIs(t, user.Deactivate(superCtx(), 5), errAudit)
}

func TestProjectAudit(t *testing.T) {
	uc, repo, perm, _ := newTestProject(t)
	rec := &captureRecorder{}
	uc.WithAudit(rec)
	ctx := superCtx()

	repo.EXPECT().GetByOrgIDAndSlug(gomock.Any(), snow.ID(2), "game").Return(nil, domain.NewErrorNotFound("x"))
	repo.EXPECT().CreateWithDefaultBranch(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, p domain.Project, _ domain.Branch) (*domain.Project, error) { return &p, nil })
	created, err := uc.Create(ctx, 2, "Game", "", "game")
	require.NoError(t, err)

	current := &domain.Project{ID: created.ID, OrgID: 2, Name: "Game", Slug: "game"}
	repo.EXPECT().Get(gomock.Any(), created.ID).Return(current, nil).Times(3)
	perm.EXPECT().AdminHasProject(gomock.Any(), created.ID).Return(true).Times(2)
	repo.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p domain.Project) (*domain.Project, error) {
		return &p, nil
	}).Times(2)
	_, err = uc.Update(ctx, created.ID, "Game 2", "desc")
	require.NoError(t, err)
	_, err = uc.Update(ctx, created.ID, "Game", "")
	require.NoError(t, err, "unchanged edit records nothing")

	repo.EXPECT().Delete(gomock.Any(), created.ID).Return(nil)
	require.NoError(t, uc.Delete(ctx, created.ID))

	require.Len(t, rec.entries, 3)
	requireActor(t, rec.entries[0], domain.AuditProjectCreated)
	require.Equal(t, snow.ID(2), *rec.entries[0].OrgID)
	require.Equal(t, created.ID, *rec.entries[0].ProjectID)
	requireActor(t, rec.entries[1], domain.AuditProjectUpdated)
	require.Equal(t, map[string]any{"name": "Game", "description": ""}, rec.entries[1].Metadata["old"])
	require.Equal(t, map[string]any{"name": "Game 2", "description": "desc"}, rec.entries[1].Metadata["new"])
	requireActor(t, rec.entries[2], domain.AuditProjectDeleted)

	uc.WithAudit(&captureRecorder{err: errAudit})
	repo.EXPECT().Get(gomock.Any(), created.ID).Return(current, nil)
	repo.EXPECT().Delete(gomock.Any(), created.ID).Return(nil)
	require.ErrorIs(t, uc.Delete(ctx, created.ID), errAudit)
}

func TestWebhookAudit(t *testing.T) {
	uc, repo, perm := newTestWebhook(t)
	rec := &captureRecorder{}
	uc.WithAudit(rec)
	perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true).AnyTimes()
	ctx := permissionCtx(7)

	var stored *domain.Webhook
	repo.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, hook domain.Webhook) (*domain.Webhook, error) {
		stored = &hook
		return &hook, nil
	})
	_, err := uc.Create(ctx, 1, WebhookInput{Name: "ci", URL: "https://ci.example.com/hook?token=abc", Events: []string{domain.WebhookEventPush}, IsActive: true})
	require.NoError(t, err)

	repo.EXPECT().Get(gomock.Any(), snow.ID(1), stored.ID).DoAndReturn(func(context.Context, snow.ID, snow.ID) (*domain.Webhook, error) {
		copied := *stored
		return &copied, nil
	}).Times(4)
	repo.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, hook domain.Webhook) (*domain.Webhook, error) {
		return &hook, nil
	}).Times(2)
	inactive := false
	_, err = uc.Update(ctx, 1, stored.ID, WebhookUpdate{IsActive: &inactive})
	require.NoError(t, err)
	_, err = uc.Update(ctx, 1, stored.ID, WebhookUpdate{})
	require.NoError(t, err, "unchanged webhook records nothing")

	repo.EXPECT().RotateSecret(gomock.Any(), snow.ID(1), stored.ID, gomock.Any()).DoAndReturn(
		func(_ context.Context, _, _ snow.ID, secret string) (*domain.Webhook, error) {
			rotated := *stored
			rotated.Secret = secret
			return &rotated, nil
		})
	_, err = uc.RotateSecret(ctx, 1, stored.ID)
	require.NoError(t, err)

	repo.EXPECT().Delete(gomock.Any(), snow.ID(1), stored.ID).Return(nil)
	require.NoError(t, uc.Delete(ctx, 1, stored.ID))

	require.Len(t, rec.entries, 4)
	actions := []string{domain.AuditWebhookCreated, domain.AuditWebhookUpdated, domain.AuditWebhookSecretRotated, domain.AuditWebhookDeleted}
	for i, entry := range rec.entries {
		requireActor(t, entry, actions[i])
		require.Equal(t, snow.ID(1), *entry.ProjectID)
		require.NotContains(t, entry.Metadata, "secret")
		require.NotContains(t, stringify(entry.Metadata), stored.Secret)
		require.NotContains(t, stringify(entry.Metadata), "token=abc")
	}
	require.Equal(t, "https://ci.example.com/hook", rec.entries[0].Metadata["url"])
	require.Equal(t, true, rec.entries[1].Metadata["old"].(map[string]any)["is_active"])
	require.Equal(t, false, rec.entries[1].Metadata["new"].(map[string]any)["is_active"])

	uc.WithAudit(&captureRecorder{err: errAudit})
	repo.EXPECT().Delete(gomock.Any(), snow.ID(1), stored.ID).Return(nil)
	require.ErrorIs(t, uc.Delete(ctx, 1, stored.ID), errAudit)

	uc.WithAudit(nil)
	repo.EXPECT().Delete(gomock.Any(), snow.ID(1), stored.ID).Return(nil)
	require.NoError(t, uc.Delete(ctx, 1, stored.ID))

	require.Empty(t, auditWebhookURL("://bad"))
	require.Equal(t, "https://h/p", auditWebhookURL("https://u:p@h/p?x=1"))
}

func stringify(v any) string {
	return fmt.Sprint(v)
}
