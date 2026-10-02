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
	ctx := permissionCtx(42)

	repo.EXPECT().MemberRole(gomock.Any(), snow.ID(1), snow.ID(42)).Return(domain.OrgRoleOwner, nil)
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
	org, repo := newTestOrg(t)

	repo.EXPECT().MemberRole(gomock.Any(), snow.ID(1), snow.ID(42)).Return(domain.OrgRoleOwner, nil)

	err := org.AddMember(permissionCtx(42), snow.ID(1), snow.ID(7), "viewer")
	requireUserError(t, err)
}

func TestOrg_AddMember_NoPermission(t *testing.T) {
	org, repo := newTestOrg(t)
	repo.EXPECT().MemberRole(gomock.Any(), snow.ID(1), snow.ID(42)).Return(domain.OrgRoleMember, nil)

	err := org.AddMember(permissionCtx(42), snow.ID(1), snow.ID(7), domain.OrgRoleMember)
	require.True(t, domain.IsErrorNoPermission(err))
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

	repo.EXPECT().UpsertMember(gomock.Any(), snow.ID(1), snow.ID(7), domain.OrgRoleOwner).Return(nil)

	require.NoError(t, org.UpdateMemberRole(ctx, snow.ID(1), snow.ID(7), domain.OrgRoleOwner))
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
