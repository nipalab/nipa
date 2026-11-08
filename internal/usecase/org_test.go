package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

func newTestOrg(t *testing.T) (*Org, *MockorgMemberRepository) {
	t.Helper()

	ctrl := gomock.NewController(t)
	repo := NewMockorgMemberRepository(ctrl)
	node, err := snow.NewNode(1)
	require.NoError(t, err)
	return NewOrg(repo, node), repo
}

func TestOrg_ListForUser(t *testing.T) {
	org, repo := newTestOrg(t)
	want := []*domain.OrgMembership{{Org: domain.Organization{ID: 1, Slug: "default"}, Role: domain.OrgRoleOwner}}
	repo.EXPECT().ListForUser(gomock.Any(), snow.ID(42)).Return(want, nil)

	got, err := org.ListForUser(context.Background(), snow.ID(42))
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestOrg_Create(t *testing.T) {
	org, repo := newTestOrg(t)

	repo.EXPECT().GetBySlug(gomock.Any(), "acme-corp").Return(nil, domain.NewErrorNotFound("org not found"))
	repo.EXPECT().CreateWithOwner(gomock.Any(), gomock.Any(), snow.ID(42)).DoAndReturn(
		func(_ context.Context, created domain.Organization, _ snow.ID) (*domain.Organization, error) {
			require.NotZero(t, created.ID)
			require.Equal(t, "Acme Corp", created.Name)
			require.Equal(t, "acme-corp", created.Slug)
			return &created, nil
		})

	got, err := org.Create(permissionCtx(42), "Acme Corp", "")
	require.NoError(t, err)
	require.Equal(t, "Acme Corp", got.Name)
	require.Equal(t, "acme-corp", got.Slug)
}

func TestOrg_Create_ExplicitSlug(t *testing.T) {
	org, repo := newTestOrg(t)

	repo.EXPECT().GetBySlug(gomock.Any(), "acme").Return(nil, domain.NewErrorNotFound("org not found"))
	repo.EXPECT().CreateWithOwner(gomock.Any(), gomock.Any(), snow.ID(42)).DoAndReturn(
		func(_ context.Context, created domain.Organization, _ snow.ID) (*domain.Organization, error) {
			return &created, nil
		})

	got, err := org.Create(permissionCtx(42), "Acme Corp", " acme ")
	require.NoError(t, err)
	require.Equal(t, "acme", got.Slug)
}

func TestOrg_Create_SlugConflict(t *testing.T) {
	org, repo := newTestOrg(t)

	repo.EXPECT().GetBySlug(gomock.Any(), "acme").Return(&domain.Organization{Slug: "acme"}, nil)

	_, err := org.Create(permissionCtx(42), "Acme", "acme")
	require.True(t, domain.IsErrorConflict(err))
}

func TestOrg_Create_InvalidSlug(t *testing.T) {
	org, _ := newTestOrg(t)

	_, err := org.Create(permissionCtx(42), "Acme", "Bad Slug!")
	requireUserError(t, err)
}

func TestOrg_Create_ReservedSlug(t *testing.T) {
	org, _ := newTestOrg(t)

	for _, slug := range []string{"web", "login", "dashboard", "settings", "api", "docs", "debug", "pricing", "screenshots", "version"} {
		_, err := org.Create(permissionCtx(42), "Acme", slug)
		require.Error(t, err, "slug %q must be reserved", slug)
		requireUserError(t, err)
	}

	_, err := org.Create(permissionCtx(42), "Web", "")
	requireUserError(t, err)
}

func TestOrg_Create_NameRequired(t *testing.T) {
	org, _ := newTestOrg(t)

	_, err := org.Create(permissionCtx(42), "   ", "acme")
	requireUserError(t, err)
}

func TestOrg_Create_Unauthenticated(t *testing.T) {
	org, _ := newTestOrg(t)

	_, err := org.Create(context.Background(), "Acme", "acme")
	var domainErr *domain.Error
	require.ErrorAs(t, err, &domainErr)
	require.Equal(t, 401, domainErr.Code)
}

func TestOrg_Create_LookupError(t *testing.T) {
	org, repo := newTestOrg(t)
	wantErr := errors.New("db down")

	repo.EXPECT().GetBySlug(gomock.Any(), "acme").Return(nil, wantErr)

	_, err := org.Create(permissionCtx(42), "Acme", "acme")
	require.ErrorIs(t, err, wantErr)
}

func TestOrg_Create_RepositoryError(t *testing.T) {
	org, repo := newTestOrg(t)
	wantErr := errors.New("insert failed")

	repo.EXPECT().GetBySlug(gomock.Any(), "acme").Return(nil, domain.NewErrorNotFound("org not found"))
	repo.EXPECT().CreateWithOwner(gomock.Any(), gomock.Any(), snow.ID(42)).Return(nil, wantErr)

	_, err := org.Create(permissionCtx(42), "Acme", "acme")
	require.ErrorIs(t, err, wantErr)
}

func TestOrg_ListMembers_GlobalAdmin(t *testing.T) {
	org, repo := newTestOrg(t)
	want := []*domain.OrgMember{{User: domain.User{ID: 7, Name: "alice"}, Role: domain.OrgRoleMember}}
	repo.EXPECT().ListMembers(gomock.Any(), snow.ID(1)).Return(want, nil)

	got, err := org.ListMembers(permissionCtx(42, withAdmin()), snow.ID(1))
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestOrg_ListMembers_OrgOwner(t *testing.T) {
	org, repo := newTestOrg(t)
	repo.EXPECT().MemberRole(gomock.Any(), snow.ID(1), snow.ID(42)).Return(domain.OrgRoleOwner, nil)
	repo.EXPECT().ListMembers(gomock.Any(), snow.ID(1)).Return(nil, nil)

	_, err := org.ListMembers(permissionCtx(42), snow.ID(1))
	require.NoError(t, err)
}

func TestOrg_ListMembers_NoPermission(t *testing.T) {
	org, repo := newTestOrg(t)
	repo.EXPECT().MemberRole(gomock.Any(), snow.ID(1), snow.ID(42)).Return(domain.OrgRoleMember, nil)

	_, err := org.ListMembers(permissionCtx(42), snow.ID(1))
	require.True(t, domain.IsErrorNoPermission(err))
}

func TestOrg_ListMembers_NotAMember(t *testing.T) {
	org, repo := newTestOrg(t)
	repo.EXPECT().MemberRole(gomock.Any(), snow.ID(1), snow.ID(42)).Return("", domain.NewErrorRecordNotFound())

	_, err := org.ListMembers(permissionCtx(42), snow.ID(1))
	require.True(t, domain.IsErrorNoPermission(err))
}

func TestOrg_ListMembers_AuthorizerError(t *testing.T) {
	wantErr := errors.New("db down")
	org, repo := newTestOrg(t)
	repo.EXPECT().MemberRole(gomock.Any(), snow.ID(1), snow.ID(42)).Return("", wantErr)

	_, err := org.ListMembers(permissionCtx(42), snow.ID(1))
	require.ErrorIs(t, err, wantErr)
}

func TestOrg_AddMember(t *testing.T) {
	org, repo := newTestOrg(t)
	ctx := permissionCtx(42, withAdmin())

	repo.EXPECT().MemberRole(gomock.Any(), snow.ID(1), snow.ID(7)).Return("", domain.NewErrorNotFound("member not found"))
	repo.EXPECT().UpsertMember(gomock.Any(), snow.ID(1), snow.ID(7), domain.OrgRoleMember).Return(nil)

	require.NoError(t, org.AddMember(ctx, snow.ID(1), snow.ID(7), domain.OrgRoleMember))
}

func TestOrg_AddMember_DemoteLastOwner(t *testing.T) {
	org, repo := newTestOrg(t)
	ctx := permissionCtx(42, withAdmin())

	repo.EXPECT().MemberRole(gomock.Any(), snow.ID(1), snow.ID(7)).Return(domain.OrgRoleOwner, nil)
	repo.EXPECT().CountMembersByRole(gomock.Any(), snow.ID(1), domain.OrgRoleOwner).Return(1, nil)

	err := org.AddMember(ctx, snow.ID(1), snow.ID(7), domain.OrgRoleMember)
	require.True(t, domain.IsErrorConflict(err))
}

func TestOrg_AddMember_DemoteWithAnotherOwner(t *testing.T) {
	org, repo := newTestOrg(t)
	ctx := permissionCtx(42, withAdmin())

	repo.EXPECT().MemberRole(gomock.Any(), snow.ID(1), snow.ID(7)).Return(domain.OrgRoleOwner, nil)
	repo.EXPECT().CountMembersByRole(gomock.Any(), snow.ID(1), domain.OrgRoleOwner).Return(2, nil)
	repo.EXPECT().UpsertMember(gomock.Any(), snow.ID(1), snow.ID(7), domain.OrgRoleMember).Return(nil)

	require.NoError(t, org.AddMember(ctx, snow.ID(1), snow.ID(7), domain.OrgRoleMember))
}

func TestOrg_AddMember_InvalidRole(t *testing.T) {
	org, _ := newTestOrg(t)

	err := org.AddMember(permissionCtx(42, withAdmin()), snow.ID(1), snow.ID(7), "viewer")
	requireUserError(t, err)
}

func TestOrg_AddMember_NoPermission(t *testing.T) {
	org, _ := newTestOrg(t)

	err := org.AddMember(permissionCtx(42), snow.ID(1), snow.ID(7), domain.OrgRoleMember)
	require.True(t, domain.IsErrorNoPermission(err), "org owners invite instead of adding directly")
}

func TestOrg_UpdateMemberRole_DemoteLastOwner(t *testing.T) {
	org, repo := newTestOrg(t)
	ctx := permissionCtx(42, withAdmin())

	repo.EXPECT().MemberRole(gomock.Any(), snow.ID(1), snow.ID(7)).Return(domain.OrgRoleOwner, nil)
	repo.EXPECT().CountMembersByRole(gomock.Any(), snow.ID(1), domain.OrgRoleOwner).Return(1, nil)

	err := org.UpdateMemberRole(ctx, snow.ID(1), snow.ID(7), domain.OrgRoleMember)
	require.True(t, domain.IsErrorConflict(err))
}

func TestOrg_UpdateMemberRole_DemoteWithAnotherOwner(t *testing.T) {
	org, repo := newTestOrg(t)
	ctx := permissionCtx(42, withAdmin())

	repo.EXPECT().MemberRole(gomock.Any(), snow.ID(1), snow.ID(7)).Return(domain.OrgRoleOwner, nil)
	repo.EXPECT().CountMembersByRole(gomock.Any(), snow.ID(1), domain.OrgRoleOwner).Return(2, nil)
	repo.EXPECT().UpsertMember(gomock.Any(), snow.ID(1), snow.ID(7), domain.OrgRoleMember).Return(nil)

	require.NoError(t, org.UpdateMemberRole(ctx, snow.ID(1), snow.ID(7), domain.OrgRoleMember))
}

func TestOrg_UpdateMemberRole_Promote(t *testing.T) {
	org, repo := newTestOrg(t)
	ctx := permissionCtx(42, withAdmin())

	repo.EXPECT().MemberRole(gomock.Any(), snow.ID(1), snow.ID(7)).Return(domain.OrgRoleMember, nil)
	repo.EXPECT().UpsertMember(gomock.Any(), snow.ID(1), snow.ID(7), domain.OrgRoleOwner).Return(nil)

	require.NoError(t, org.UpdateMemberRole(ctx, snow.ID(1), snow.ID(7), domain.OrgRoleOwner))
}

func TestOrg_UpdateMemberRole_NotAMember(t *testing.T) {
	org, repo := newTestOrg(t)
	ctx := permissionCtx(42, withAdmin())

	repo.EXPECT().MemberRole(gomock.Any(), snow.ID(1), snow.ID(7)).Return("", domain.NewErrorNotFound("x"))

	err := org.UpdateMemberRole(ctx, snow.ID(1), snow.ID(7), domain.OrgRoleOwner)
	require.True(t, domain.IsErrorNotFound(err), "a role update must never add a user")
}

func TestOrg_UpdateMemberRole_LookupError(t *testing.T) {
	org, repo := newTestOrg(t)
	ctx := permissionCtx(42, withAdmin())

	repo.EXPECT().MemberRole(gomock.Any(), snow.ID(1), snow.ID(7)).Return("", errors.New("boom"))

	require.Error(t, org.UpdateMemberRole(ctx, snow.ID(1), snow.ID(7), domain.OrgRoleOwner))
}

func TestOrg_RemoveMember(t *testing.T) {
	org, repo := newTestOrg(t)
	ctx := permissionCtx(42, withAdmin())

	repo.EXPECT().MemberRole(gomock.Any(), snow.ID(1), snow.ID(7)).Return(domain.OrgRoleMember, nil)
	repo.EXPECT().RemoveMember(gomock.Any(), snow.ID(1), snow.ID(7)).Return(nil)

	require.NoError(t, org.RemoveMember(ctx, snow.ID(1), snow.ID(7)))
}

func TestOrg_RemoveMember_LastOwner(t *testing.T) {
	org, repo := newTestOrg(t)
	ctx := permissionCtx(42, withAdmin())

	repo.EXPECT().MemberRole(gomock.Any(), snow.ID(1), snow.ID(7)).Return(domain.OrgRoleOwner, nil)
	repo.EXPECT().CountMembersByRole(gomock.Any(), snow.ID(1), domain.OrgRoleOwner).Return(1, nil)

	err := org.RemoveMember(ctx, snow.ID(1), snow.ID(7))
	require.True(t, domain.IsErrorConflict(err))
}

func TestOrg_IsOrgOwner(t *testing.T) {
	t.Run("no claims", func(t *testing.T) {
		org, _ := newTestOrg(t)
		owner, err := org.IsOrgOwner(context.Background(), snow.ID(1))
		require.NoError(t, err)
		require.False(t, owner)
	})

	t.Run("global admin", func(t *testing.T) {
		org, _ := newTestOrg(t)
		owner, err := org.IsOrgOwner(permissionCtx(42, withAdmin()), snow.ID(1))
		require.NoError(t, err)
		require.True(t, owner)
	})

	t.Run("owner role", func(t *testing.T) {
		org, repo := newTestOrg(t)
		repo.EXPECT().MemberRole(gomock.Any(), snow.ID(1), snow.ID(42)).Return(domain.OrgRoleOwner, nil)
		owner, err := org.IsOrgOwner(permissionCtx(42), snow.ID(1))
		require.NoError(t, err)
		require.True(t, owner)
	})

	t.Run("member role", func(t *testing.T) {
		org, repo := newTestOrg(t)
		repo.EXPECT().MemberRole(gomock.Any(), snow.ID(1), snow.ID(42)).Return(domain.OrgRoleMember, nil)
		owner, err := org.IsOrgOwner(permissionCtx(42), snow.ID(1))
		require.NoError(t, err)
		require.False(t, owner)
	})

	t.Run("not a member", func(t *testing.T) {
		org, repo := newTestOrg(t)
		repo.EXPECT().MemberRole(gomock.Any(), snow.ID(1), snow.ID(42)).Return("", domain.NewErrorRecordNotFound())
		owner, err := org.IsOrgOwner(permissionCtx(42), snow.ID(1))
		require.NoError(t, err)
		require.False(t, owner)
	})

	t.Run("repository error", func(t *testing.T) {
		wantErr := errors.New("db down")
		org, repo := newTestOrg(t)
		repo.EXPECT().MemberRole(gomock.Any(), snow.ID(1), snow.ID(42)).Return("", wantErr)
		_, err := org.IsOrgOwner(permissionCtx(42), snow.ID(1))
		require.ErrorIs(t, err, wantErr)
	})
}

type fakePermissionCache struct{ invalidated int }

func (f *fakePermissionCache) InvalidateAll() { f.invalidated++ }

func TestOrg_AddAndUpdateMember_InvalidatePermissionCache(t *testing.T) {
	org, repo := newTestOrg(t)
	cache := &fakePermissionCache{}
	org.WithPermissionCache(cache)
	ctx := permissionCtx(42, withAdmin())

	repo.EXPECT().UpsertMember(gomock.Any(), snow.ID(1), snow.ID(7), domain.OrgRoleOwner).Return(nil)
	require.NoError(t, org.AddMember(ctx, snow.ID(1), snow.ID(7), domain.OrgRoleOwner))
	require.Equal(t, 1, cache.invalidated)

	repo.EXPECT().MemberRole(gomock.Any(), snow.ID(1), snow.ID(7)).Return(domain.OrgRoleMember, nil)
	repo.EXPECT().UpsertMember(gomock.Any(), snow.ID(1), snow.ID(7), domain.OrgRoleOwner).Return(nil)
	require.NoError(t, org.UpdateMemberRole(ctx, snow.ID(1), snow.ID(7), domain.OrgRoleOwner))
	require.Equal(t, 2, cache.invalidated)

	repo.EXPECT().UpsertMember(gomock.Any(), snow.ID(1), snow.ID(7), domain.OrgRoleOwner).Return(errors.New("boom"))
	require.Error(t, org.AddMember(ctx, snow.ID(1), snow.ID(7), domain.OrgRoleOwner))
	repo.EXPECT().MemberRole(gomock.Any(), snow.ID(1), snow.ID(7)).Return(domain.OrgRoleMember, nil)
	repo.EXPECT().UpsertMember(gomock.Any(), snow.ID(1), snow.ID(7), domain.OrgRoleOwner).Return(errors.New("boom"))
	require.Error(t, org.UpdateMemberRole(ctx, snow.ID(1), snow.ID(7), domain.OrgRoleOwner))
	require.Equal(t, 2, cache.invalidated)
}

func TestOrg_RemoveMember_InvalidatesPermissionCache(t *testing.T) {
	org, repo := newTestOrg(t)
	cache := &fakePermissionCache{}
	org.WithPermissionCache(cache)
	ctx := permissionCtx(42, withAdmin())

	repo.EXPECT().MemberRole(gomock.Any(), snow.ID(1), snow.ID(7)).Return(domain.OrgRoleMember, nil).Times(2)
	repo.EXPECT().RemoveMember(gomock.Any(), snow.ID(1), snow.ID(7)).Return(nil)
	require.NoError(t, org.RemoveMember(ctx, snow.ID(1), snow.ID(7)))
	require.Equal(t, 1, cache.invalidated)

	repo.EXPECT().RemoveMember(gomock.Any(), snow.ID(1), snow.ID(7)).Return(errors.New("boom"))
	require.Error(t, org.RemoveMember(ctx, snow.ID(1), snow.ID(7)))
	require.Equal(t, 1, cache.invalidated, "a failed removal keeps the cache")
}

func TestOrg_EnsureOwner(t *testing.T) {
	org, repo := newTestOrg(t)

	require.NoError(t, org.EnsureOwner(permissionCtx(42, withAdmin()), snow.ID(1)))

	repo.EXPECT().MemberRole(gomock.Any(), snow.ID(1), snow.ID(9)).Return(domain.OrgRoleMember, nil)
	err := org.EnsureOwner(permissionCtx(9), snow.ID(1))
	require.True(t, domain.IsErrorNoPermission(err))

	repo.EXPECT().MemberRole(gomock.Any(), snow.ID(1), snow.ID(8)).Return(domain.OrgRoleOwner, nil)
	require.NoError(t, org.EnsureOwner(permissionCtx(8), snow.ID(1)))
}

func TestOrg_AddMember_SeatGate(t *testing.T) {
	ctx := permissionCtx(42, withAdmin())
	member := func(repo *MockorgMemberRepository) {
		repo.EXPECT().MemberRole(gomock.Any(), snow.ID(1), snow.ID(7)).Return("", domain.NewErrorNotFound("member not found"))
	}

	t.Run("refuses", func(t *testing.T) {
		org, repo := newTestOrg(t)
		gate := NewMockSeatGate(gomock.NewController(t))
		org.WithSeatGate(gate)
		member(repo)
		wantErr := domain.NewErrorQuotaExceeded("seat limit reached")
		gate.EXPECT().Admit(gomock.Any(), snow.ID(1), snow.ID(7), gomock.Any()).Return(wantErr)

		require.ErrorIs(t, org.AddMember(ctx, 1, 7, domain.OrgRoleMember), wantErr)
	})

	t.Run("admits", func(t *testing.T) {
		org, repo := newTestOrg(t)
		gate := NewMockSeatGate(gomock.NewController(t))
		org.WithSeatGate(gate)
		member(repo)
		gate.EXPECT().Admit(gomock.Any(), snow.ID(1), snow.ID(7), gomock.Any()).DoAndReturn(
			func(ctx context.Context, _, _ snow.ID, insert func(context.Context) error) error {
				return insert(ctx)
			})
		repo.EXPECT().UpsertMember(gomock.Any(), snow.ID(1), snow.ID(7), domain.OrgRoleMember).Return(nil)

		require.NoError(t, org.AddMember(ctx, 1, 7, domain.OrgRoleMember))
	})

	t.Run("role update is not gated", func(t *testing.T) {
		org, repo := newTestOrg(t)
		org.WithSeatGate(NewMockSeatGate(gomock.NewController(t)))
		repo.EXPECT().MemberRole(gomock.Any(), snow.ID(1), snow.ID(7)).Return(domain.OrgRoleMember, nil)
		repo.EXPECT().UpsertMember(gomock.Any(), snow.ID(1), snow.ID(7), domain.OrgRoleOwner).Return(nil)

		require.NoError(t, org.UpdateMemberRole(ctx, 1, 7, domain.OrgRoleOwner))
	})
}
