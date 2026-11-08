package usecase

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type serviceTokenFixture struct {
	uc       *ServiceToken
	repo     *MockserviceTokenRepository
	perm     *MockpermissionUsecase
	users    *MockuserLookup
	projects *MockserviceTokenProjectResolver
	rec      *captureRecorder
	now      time.Time
}

func newServiceTokenFixture(t *testing.T) serviceTokenFixture {
	ctrl := gomock.NewController(t)
	f := serviceTokenFixture{
		repo:     NewMockserviceTokenRepository(ctrl),
		perm:     NewMockpermissionUsecase(ctrl),
		users:    NewMockuserLookup(ctrl),
		projects: NewMockserviceTokenProjectResolver(ctrl),
		rec:      &captureRecorder{},
		now:      time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC),
	}
	f.uc = NewServiceToken(f.repo, f.perm, f.users, f.projects, newTestBranchNode(t)).WithAudit(f.rec)
	f.uc.now = func() time.Time { return f.now }
	return f
}

func (f serviceTokenFixture) admin(ok bool) {
	f.perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(ok)
}

func TestServiceToken_Create(t *testing.T) {
	f := newServiceTokenFixture(t)
	f.admin(true)
	f.repo.EXPECT().LockProject(gomock.Any(), snow.ID(1)).Return(nil)
	f.repo.EXPECT().ListByProject(gomock.Any(), snow.ID(1)).Return([]*domain.ServiceToken{{RevokedAt: &f.now}}, nil)
	expires := f.now.Add(24 * time.Hour)
	var stored domain.ServiceToken
	f.repo.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, tok domain.ServiceToken) (*domain.ServiceToken, error) {
		stored = tok
		return &tok, nil
	})

	created, plaintext, err := f.uc.Create(permissionCtx(7), 1, ServiceTokenInput{
		Name:      "  BugBase ",
		Scopes:    []string{"webhooks:write", "read", "read"},
		ExpiresAt: &expires,
	})
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(plaintext, domain.ServiceTokenPrefix))
	require.Equal(t, "BugBase", created.Name)
	require.Equal(t, []string{"read", "webhooks:write"}, created.Scopes)
	require.Equal(t, snow.ID(7), created.CreatedBy)
	require.Equal(t, hashServiceToken(plaintext), stored.TokenHash)
	require.NotContains(t, stored.TokenHash, plaintext)
	require.Equal(t, plaintext[:serviceTokenHintLen], created.Hint)

	entry := f.rec.only(t)
	require.Equal(t, domain.AuditServiceTokenCreated, entry.Action)
	require.Equal(t, domain.AuditTargetServiceToken, entry.TargetType)
	require.Equal(t, "BugBase", entry.Metadata["name"])
	require.NotNil(t, entry.Metadata["expires_at"])
	for _, v := range entry.Metadata {
		if s, ok := v.(string); ok {
			require.NotEqual(t, plaintext, s)
		}
	}
}

func TestServiceToken_CreateValidation(t *testing.T) {
	f := newServiceTokenFixture(t)
	ctx := permissionCtx(7)

	f.admin(false)
	_, _, err := f.uc.Create(ctx, 1, ServiceTokenInput{Name: "x", Scopes: []string{"read"}})
	require.True(t, domain.IsErrorNoPermission(err))

	_, _, err = f.uc.Create(context.Background(), 1, ServiceTokenInput{Name: "x", Scopes: []string{"read"}})
	require.True(t, domain.IsErrorNoPermission(err), "no claim")

	past := f.now.Add(-time.Hour)
	for _, in := range []ServiceTokenInput{
		{Name: " ", Scopes: []string{"read"}},
		{Name: strings.Repeat("n", 101), Scopes: []string{"read"}},
		{Name: "x"},
		{Name: "x", Scopes: []string{"admin"}},
		{Name: "x", Scopes: []string{"read"}, ExpiresAt: &past},
	} {
		f.admin(true)
		_, _, err := f.uc.Create(ctx, 1, in)
		var domErr *domain.Error
		require.ErrorAs(t, err, &domErr)
		require.Equal(t, 400, domErr.Code, in.Name)
	}

	boom := errors.New("boom")
	f.admin(true)
	f.repo.EXPECT().LockProject(gomock.Any(), snow.ID(1)).Return(boom)
	_, _, err = f.uc.Create(ctx, 1, ServiceTokenInput{Name: "x", Scopes: []string{"read"}})
	require.ErrorIs(t, err, boom)

	f.admin(true)
	f.repo.EXPECT().LockProject(gomock.Any(), snow.ID(1)).Return(nil)
	f.repo.EXPECT().ListByProject(gomock.Any(), snow.ID(1)).Return(nil, boom)
	_, _, err = f.uc.Create(ctx, 1, ServiceTokenInput{Name: "x", Scopes: []string{"read"}})
	require.ErrorIs(t, err, boom)

	many := make([]*domain.ServiceToken, maxServiceTokensActive)
	for i := range many {
		many[i] = &domain.ServiceToken{}
	}
	f.admin(true)
	f.repo.EXPECT().LockProject(gomock.Any(), snow.ID(1)).Return(nil)
	f.repo.EXPECT().ListByProject(gomock.Any(), snow.ID(1)).Return(many, nil)
	_, _, err = f.uc.Create(ctx, 1, ServiceTokenInput{Name: "x", Scopes: []string{"read"}})
	require.ErrorContains(t, err, "at most")

	f.admin(true)
	f.repo.EXPECT().LockProject(gomock.Any(), snow.ID(1)).Return(nil)
	f.repo.EXPECT().ListByProject(gomock.Any(), snow.ID(1)).Return(nil, nil)
	f.repo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil, boom)
	_, _, err = f.uc.Create(ctx, 1, ServiceTokenInput{Name: "x", Scopes: []string{"read"}})
	require.ErrorIs(t, err, boom)
	require.Empty(t, f.rec.entries)
}

func TestServiceToken_ListAndRevoke(t *testing.T) {
	f := newServiceTokenFixture(t)
	ctx := permissionCtx(7)

	f.admin(true)
	f.repo.EXPECT().ListByProject(gomock.Any(), snow.ID(1)).Return([]*domain.ServiceToken{{ID: 3}}, nil)
	list, err := f.uc.List(ctx, 1)
	require.NoError(t, err)
	require.Len(t, list, 1)

	f.admin(false)
	_, err = f.uc.List(ctx, 1)
	require.True(t, domain.IsErrorNoPermission(err))

	f.admin(true)
	f.repo.EXPECT().Revoke(gomock.Any(), snow.ID(1), snow.ID(3), f.now).Return(&domain.ServiceToken{ID: 3, Name: "ci", RevokedAt: &f.now}, nil)
	revoked, err := f.uc.Revoke(ctx, 1, 3)
	require.NoError(t, err)
	require.NotNil(t, revoked.RevokedAt)
	require.Equal(t, domain.AuditServiceTokenRevoked, f.rec.only(t).Action)

	f.admin(true)
	f.repo.EXPECT().Revoke(gomock.Any(), snow.ID(1), snow.ID(4), f.now).Return(nil, domain.NewErrorRecordNotFound())
	_, err = f.uc.Revoke(ctx, 1, 4)
	require.ErrorContains(t, err, "service token not found")

	f.admin(false)
	_, err = f.uc.Revoke(ctx, 1, 3)
	require.True(t, domain.IsErrorNoPermission(err))
}

func (f serviceTokenFixture) token(plaintext string, tok *domain.ServiceToken, err error) {
	f.repo.EXPECT().GetByHash(gomock.Any(), hashServiceToken(plaintext)).Return(tok, err)
}

func TestServiceToken_Authenticate(t *testing.T) {
	f := newServiceTokenFixture(t)
	ctx := context.Background()
	const plain = "nipa_st_abcdef"
	tok := &domain.ServiceToken{ID: 3, ProjectID: 1, CreatedBy: 7, Scopes: []string{"read", "checks:write"}}

	f.token(plain, tok, nil)
	f.projects.EXPECT().ResolveBySlug(gomock.Any(), "acme", "rocket").Return(&domain.Organization{}, &domain.Project{ID: 1}, nil)
	f.users.EXPECT().GetByID(gomock.Any(), snow.ID(7)).Return(&domain.User{ID: 7, IsAdmin: true, EmailVerified: true}, nil)
	f.perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).DoAndReturn(func(ctx context.Context, _ snow.ID) bool {
		claim, ok := domain.ClaimFromContext(ctx)
		require.True(t, ok)
		require.Equal(t, snow.ID(7), claim.UserID)
		require.Equal(t, snow.ID(3), claim.ServiceTokenID)
		return true
	})
	f.repo.EXPECT().TouchLastUsed(gomock.Any(), snow.ID(3), f.now).Return(nil)
	claims, err := f.uc.Authenticate(ctx, plain, "acme", "rocket", "read")
	require.NoError(t, err)
	require.Equal(t, snow.ID(7), claims.UserID)
	require.Equal(t, snow.ID(3), claims.ServiceTokenID)
	require.True(t, claims.IsAdmin)

	recent := f.now.Add(-time.Second)
	tok.LastUsedAt = &recent
	f.token(plain, tok, nil)
	f.projects.EXPECT().ResolveBySlug(gomock.Any(), "acme", "rocket").Return(&domain.Organization{}, &domain.Project{ID: 1}, nil)
	f.users.EXPECT().GetByID(gomock.Any(), snow.ID(7)).Return(&domain.User{ID: 7, EmailVerified: true}, nil)
	f.admin(true)
	_, err = f.uc.Authenticate(ctx, plain, "acme", "rocket", "checks:write")
	require.NoError(t, err, "a recently used token is not touched again")
}

func TestServiceToken_AuthenticateRejects(t *testing.T) {
	f := newServiceTokenFixture(t)
	ctx := context.Background()
	const plain = "nipa_st_abcdef"
	code := func(err error) int {
		var domErr *domain.Error
		require.ErrorAs(t, err, &domErr)
		return domErr.Code
	}

	_, err := f.uc.Authenticate(ctx, "eyJhbGciOi", "acme", "rocket", "read")
	require.Equal(t, 401, code(err))

	f.token(plain, nil, domain.NewErrorRecordNotFound())
	_, err = f.uc.Authenticate(ctx, plain, "acme", "rocket", "read")
	require.Equal(t, 401, code(err))

	boom := errors.New("boom")
	f.token(plain, nil, boom)
	_, err = f.uc.Authenticate(ctx, plain, "acme", "rocket", "read")
	require.ErrorIs(t, err, boom)

	f.token(plain, &domain.ServiceToken{RevokedAt: &f.now}, nil)
	_, err = f.uc.Authenticate(ctx, plain, "acme", "rocket", "read")
	require.Equal(t, 401, code(err))

	expired := f.now
	f.token(plain, &domain.ServiceToken{ExpiresAt: &expired}, nil)
	_, err = f.uc.Authenticate(ctx, plain, "acme", "rocket", "read")
	require.Equal(t, 401, code(err))

	tok := &domain.ServiceToken{ID: 3, ProjectID: 1, CreatedBy: 7, Scopes: []string{"read"}}
	f.token(plain, tok, nil)
	f.projects.EXPECT().ResolveBySlug(gomock.Any(), "acme", "other").Return(&domain.Organization{}, &domain.Project{ID: 2}, nil)
	_, err = f.uc.Authenticate(ctx, plain, "acme", "other", "read")
	require.Equal(t, 403, code(err))

	f.token(plain, tok, nil)
	f.projects.EXPECT().ResolveBySlug(gomock.Any(), "acme", "gone").Return(nil, nil, domain.NewErrorNotFound("project"))
	_, err = f.uc.Authenticate(ctx, plain, "acme", "gone", "read")
	require.Equal(t, 403, code(err))

	f.token(plain, tok, nil)
	f.projects.EXPECT().ResolveBySlug(gomock.Any(), "acme", "rocket").Return(nil, nil, boom)
	_, err = f.uc.Authenticate(ctx, plain, "acme", "rocket", "read")
	require.ErrorIs(t, err, boom)

	f.token(plain, tok, nil)
	f.projects.EXPECT().ResolveBySlug(gomock.Any(), "acme", "rocket").Return(&domain.Organization{}, &domain.Project{ID: 1}, nil)
	_, err = f.uc.Authenticate(ctx, plain, "acme", "rocket", "webhooks:write")
	require.Equal(t, 403, code(err))

	for _, tc := range []struct {
		user *domain.User
		err  error
		want int
	}{
		{nil, domain.NewErrorRecordNotFound(), 401},
		{&domain.User{ID: 7, Deleted: true, EmailVerified: true}, nil, 401},
		{&domain.User{ID: 7}, nil, 401},
		{nil, boom, 500},
	} {
		f.token(plain, tok, nil)
		f.projects.EXPECT().ResolveBySlug(gomock.Any(), "acme", "rocket").Return(&domain.Organization{}, &domain.Project{ID: 1}, nil)
		f.users.EXPECT().GetByID(gomock.Any(), snow.ID(7)).Return(tc.user, tc.err)
		_, err = f.uc.Authenticate(ctx, plain, "acme", "rocket", "read")
		if tc.want == 500 {
			require.ErrorIs(t, err, boom)
			continue
		}
		require.Equal(t, tc.want, code(err))
	}

	f.token(plain, tok, nil)
	f.projects.EXPECT().ResolveBySlug(gomock.Any(), "acme", "rocket").Return(&domain.Organization{}, &domain.Project{ID: 1}, nil)
	f.users.EXPECT().GetByID(gomock.Any(), snow.ID(7)).Return(&domain.User{ID: 7, EmailVerified: true}, nil)
	f.admin(false)
	_, err = f.uc.Authenticate(ctx, plain, "acme", "rocket", "read")
	require.Equal(t, 403, code(err), "a creator who lost admin cannot use the token")
}
