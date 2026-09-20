package usecase

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

func permissionCtx(userID snow.ID, opts ...func(*domain.Claims)) context.Context {
	claims := domain.Claims{UserID: userID}
	for _, opt := range opts {
		opt(&claims)
	}
	return domain.ContextWithClaim(context.Background(), claims)
}

func withSuperAdmin() func(*domain.Claims) {
	return func(claims *domain.Claims) { claims.IsSuperAdmin = true }
}

func withAdmin() func(*domain.Claims) {
	return func(claims *domain.Claims) { claims.IsAdmin = true }
}

func newTestPermission(t *testing.T, rules []*domain.PBACRule, defaults []*domain.ProjectPathPermission) *Permission {
	t.Helper()

	ctrl := gomock.NewController(t)
	repo := NewMockpbacRepository(ctrl)
	repo.EXPECT().ListEffectiveRules(gomock.Any(), gomock.Any(), gomock.Any()).Return(rules, nil).AnyTimes()
	repo.EXPECT().ListPathPermissions(gomock.Any(), gomock.Any()).Return(defaults, nil).AnyTimes()
	return NewPermission(repo)
}

func newAllowAllPerm(ctrl *gomock.Controller) *MockpermissionUsecase {
	perm := NewMockpermissionUsecase(ctrl)
	perm.EXPECT().
		CompileFilter(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(AllowAllFilter(), nil).
		AnyTimes()
	return perm
}

func restrictedPerm(ctrl *gomock.Controller, rules []*domain.PBACRule, defaults []*domain.ProjectPathPermission) *MockpermissionUsecase {
	perm := NewMockpermissionUsecase(ctrl)
	perm.EXPECT().
		CompileFilter(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&PathFilter{
			set:        &permissionSet{rules: rules, defaults: defaults},
			permission: domain.PermissionRead,
		}, nil).
		AnyTimes()
	return perm
}

func TestPermission_HasProjectAccess(t *testing.T) {
	const projectID snow.ID = 1
	const userID snow.ID = 42

	tests := []struct {
		name    string
		ctx     context.Context
		rules   []*domain.PBACRule
		defs    []*domain.ProjectPathPermission
		wantBit domain.Permission
		want    bool
	}{
		{
			name: "no claim",
			ctx:  context.Background(),
			want: false,
		},
		{
			name: "super admin",
			ctx:  permissionCtx(userID, withSuperAdmin()),
			want: true,
		},
		{
			name: "admin",
			ctx:  permissionCtx(userID, withAdmin()),
			want: true,
		},
		{
			name: "regular user without rules",
			ctx:  permissionCtx(userID),
			want: false,
		},
		{
			name:    "regular user with read rule",
			ctx:     permissionCtx(userID),
			rules:   []*domain.PBACRule{{PathPrefix: "assets", Permission: domain.PermissionRead}},
			wantBit: domain.PermissionRead,
			want:    true,
		},
		{
			name:    "regular user with read rule asking write",
			ctx:     permissionCtx(userID),
			rules:   []*domain.PBACRule{{PathPrefix: "assets", Permission: domain.PermissionRead}},
			wantBit: domain.PermissionWrite,
			want:    false,
		},
		{
			name:    "regular user with default permission",
			ctx:     permissionCtx(userID),
			defs:    []*domain.ProjectPathPermission{{PathPrefix: "", Permission: domain.PermissionRead}},
			wantBit: domain.PermissionRead,
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			perm := newTestPermission(t, tt.rules, tt.defs)
			require.Equal(t, tt.want, perm.HasProjectAccess(tt.ctx, projectID, tt.wantBit))
		})
	}
}

func TestPermission_HasPathAccess(t *testing.T) {
	const projectID snow.ID = 1
	const userID snow.ID = 42
	ctx := permissionCtx(userID)

	rules := []*domain.PBACRule{
		{PathPrefix: "", Permission: domain.PermissionWrite},
		{PathPrefix: "assets", Permission: domain.PermissionRead | domain.PermissionWrite},
	}
	defs := []*domain.ProjectPathPermission{
		{PathPrefix: "assets", Permission: domain.PermissionRead},
		{PathPrefix: "assets/secret", Permission: 0},
	}

	tests := []struct {
		name string
		path string
		bit  domain.Permission
		want bool
	}{
		{name: "root write rule read denied", path: "src/main.go", bit: domain.PermissionRead, want: false},
		{name: "root write rule write allowed", path: "src/main.go", bit: domain.PermissionWrite, want: true},
		{name: "prefix rule read allowed", path: "assets/logo.png", bit: domain.PermissionRead, want: true},
		{name: "prefix rule write allowed", path: "assets/logo.png", bit: domain.PermissionWrite, want: true},
		{name: "prefix covers deep file", path: "assets/textures/wood.png", bit: domain.PermissionRead, want: true},
		{name: "fallback deny ignored when rule matches", path: "assets/secret/key.bin", bit: domain.PermissionRead, want: true},
		{name: "sibling prefix not covered", path: "assets2/logo.png", bit: domain.PermissionRead, want: false},
		{name: "exact path match", path: "assets", bit: domain.PermissionRead, want: true},
	}

	perm := newTestPermission(t, rules, defs)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, perm.HasPathAccess(ctx, projectID, tt.path, tt.bit))
		})
	}
}

func TestPermission_HasPathAccess_FallbackDenyWithoutRule(t *testing.T) {
	const projectID snow.ID = 1
	const userID snow.ID = 42
	ctx := permissionCtx(userID)

	defs := []*domain.ProjectPathPermission{
		{PathPrefix: "", Permission: domain.PermissionRead},
		{PathPrefix: "assets/secret", Permission: 0},
	}
	perm := newTestPermission(t, nil, defs)

	require.True(t, perm.HasPathAccess(ctx, projectID, "docs/readme.md", domain.PermissionRead))
	require.True(t, perm.HasPathAccess(ctx, projectID, "assets/logo.png", domain.PermissionRead))
	require.False(t, perm.HasPathAccess(ctx, projectID, "assets/secret/key.bin", domain.PermissionRead))
	require.False(t, perm.HasPathAccess(ctx, projectID, "assets/secret", domain.PermissionRead))
}

func TestPermission_Effective_Admin(t *testing.T) {
	perm := newTestPermission(t, nil, nil)
	require.Equal(t, domain.PermissionAll, perm.Effective(permissionCtx(42, withAdmin()), 1, "any/path"))
}

func TestPermission_Effective_NoClaim(t *testing.T) {
	perm := newTestPermission(t, nil, nil)
	require.Equal(t, domain.Permission(0), perm.Effective(context.Background(), 1, "any/path"))
}

func TestPermission_CompileFilter(t *testing.T) {
	const projectID snow.ID = 1
	const userID snow.ID = 42

	rules := []*domain.PBACRule{
		{PathPrefix: "assets/textures", Permission: domain.PermissionRead},
	}
	perm := newTestPermission(t, rules, nil)

	filter, err := perm.CompileFilter(permissionCtx(userID), projectID, domain.PermissionRead)
	require.NoError(t, err)
	require.False(t, filter.All())
	require.True(t, filter.Allow("assets/textures/wood.png"))
	require.False(t, filter.Allow("assets/logo.png"))
	require.True(t, filter.CanDescend(""))
	require.True(t, filter.CanDescend("assets"))
	require.True(t, filter.CanDescend("assets/textures"))
	require.False(t, filter.CanDescend("src"))
}

func TestPermission_CompileFilter_Defaults(t *testing.T) {
	defs := []*domain.ProjectPathPermission{
		{PathPrefix: "assets", Permission: domain.PermissionRead},
	}
	perm := newTestPermission(t, nil, defs)

	filter, err := perm.CompileFilter(permissionCtx(42), 1, domain.PermissionRead)
	require.NoError(t, err)
	require.True(t, filter.Allow("assets/logo.png"))
	require.False(t, filter.Allow("src/main.go"))
	require.True(t, filter.CanDescend("assets"))
	require.False(t, filter.CanDescend("src"))
}

func TestPermission_CompileFilter_Admin(t *testing.T) {
	perm := newTestPermission(t, nil, nil)

	filter, err := perm.CompileFilter(permissionCtx(42, withAdmin()), 1, domain.PermissionRead)
	require.NoError(t, err)
	require.True(t, filter.All())
	require.True(t, filter.Allow("any/path"))
	require.True(t, filter.CanDescend("any"))
}

func TestPermission_CompileFilter_NoClaim(t *testing.T) {
	perm := newTestPermission(t, nil, nil)

	filter, err := perm.CompileFilter(context.Background(), 1, domain.PermissionRead)
	require.True(t, domain.IsErrorNoPermission(err))
	require.Nil(t, filter)
}

func TestAllowAllFilter(t *testing.T) {
	filter := AllowAllFilter()
	require.True(t, filter.All())
	require.True(t, filter.Allow("any/path"))
	require.True(t, filter.CanDescend("any"))
}

func TestPermission_CreateRule_Validation(t *testing.T) {
	perm := newTestPermission(t, nil, nil)
	ctx := context.Background()
	userID := snow.ID(42)
	groupID := snow.ID(7)

	_, err := perm.CreateRule(ctx, domain.PBACRule{
		UserID: &userID, GroupID: &groupID, OrgID: 1,
		PathPrefix: "", Permission: domain.PermissionRead,
	})
	requireUserError(t, err)

	_, err = perm.CreateRule(ctx, domain.PBACRule{
		OrgID: 1, PathPrefix: "", Permission: domain.PermissionRead,
	})
	requireUserError(t, err)

	_, err = perm.CreateRule(ctx, domain.PBACRule{
		UserID: &userID, OrgID: 1, PathPrefix: "", Permission: 0,
	})
	requireUserError(t, err)

	_, err = perm.CreateRule(ctx, domain.PBACRule{
		UserID: &userID, OrgID: 1, PathPrefix: "../etc", Permission: domain.PermissionRead,
	})
	requireUserError(t, err)
}

func TestPermission_CreateRule_NormalizesAndInvalidatesCache(t *testing.T) {
	projectID := snow.ID(1)
	userID := snow.ID(42)
	ctx := permissionCtx(userID)

	ctrl := gomock.NewController(t)
	repo := NewMockpbacRepository(ctrl)

	gomock.InOrder(
		repo.EXPECT().ListEffectiveRules(gomock.Any(), projectID, userID).Return(nil, nil),
		repo.EXPECT().ListPathPermissions(gomock.Any(), projectID).Return(nil, nil),
	)
	repo.EXPECT().CreateRule(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, rule domain.PBACRule) (*domain.PBACRule, error) {
			require.Equal(t, "assets/textures", rule.PathPrefix)
			rule.ID = 7
			return &rule, nil
		},
	)
	gomock.InOrder(
		repo.EXPECT().ListEffectiveRules(gomock.Any(), projectID, userID).Return([]*domain.PBACRule{
			{PathPrefix: "assets/textures", Permission: domain.PermissionRead},
		}, nil),
		repo.EXPECT().ListPathPermissions(gomock.Any(), projectID).Return(nil, nil),
	)

	perm := NewPermission(repo)

	require.False(t, perm.HasPathAccess(ctx, projectID, "assets/textures/wood.png", domain.PermissionRead))

	created, err := perm.CreateRule(ctx, domain.PBACRule{
		UserID: &userID, OrgID: 1, ProjectID: projectIDPtr(projectID),
		PathPrefix: "assets/textures/", Permission: domain.PermissionRead,
	})
	require.NoError(t, err)
	require.Equal(t, int64(7), created.ID)

	require.True(t, perm.HasPathAccess(ctx, projectID, "assets/textures/wood.png", domain.PermissionRead))
}

func TestPermission_Cache_ServesRepeatedReads(t *testing.T) {
	const projectID snow.ID = 1
	const userID snow.ID = 42
	ctx := permissionCtx(userID)

	ctrl := gomock.NewController(t)
	repo := NewMockpbacRepository(ctrl)

	repo.EXPECT().ListEffectiveRules(gomock.Any(), projectID, userID).Return([]*domain.PBACRule{
		{PathPrefix: "", Permission: domain.PermissionRead},
	}, nil).Times(1)
	repo.EXPECT().ListPathPermissions(gomock.Any(), projectID).Return(nil, nil).Times(1)

	perm := NewPermission(repo)
	require.True(t, perm.HasPathAccess(ctx, projectID, "a.txt", domain.PermissionRead))
	require.True(t, perm.HasPathAccess(ctx, projectID, "b.txt", domain.PermissionRead))
	require.True(t, perm.HasProjectAccess(ctx, projectID, domain.PermissionRead))
}

func TestPermission_PathPermissionCRUD(t *testing.T) {
	const projectID snow.ID = 1

	ctrl := gomock.NewController(t)
	repo := NewMockpbacRepository(ctrl)

	repo.EXPECT().UpsertPathPermission(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, perm domain.ProjectPathPermission) (*domain.ProjectPathPermission, error) {
			require.Equal(t, "", perm.PathPrefix)
			require.Equal(t, domain.PermissionRead, perm.Permission)
			return &perm, nil
		},
	)
	repo.EXPECT().DeletePathPermission(gomock.Any(), projectID, "").Return(nil)
	repo.EXPECT().ListPathPermissions(gomock.Any(), projectID).Return(nil, nil)

	perm := NewPermission(repo)
	ctx := context.Background()

	_, err := perm.SetPathPermission(ctx, projectID, "/", domain.PermissionRead)
	require.NoError(t, err)

	_, err = perm.SetPathPermission(ctx, projectID, "../etc", domain.PermissionRead)
	requireUserError(t, err)

	require.NoError(t, perm.DeletePathPermission(ctx, projectID, "/"))

	_, err = perm.ListPathPermissions(ctx, projectID)
	require.NoError(t, err)
}

func projectIDPtr(id snow.ID) *snow.ID {
	return &id
}

func requireUserError(t *testing.T, err error) {
	t.Helper()

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 400, domErr.Code)
}
