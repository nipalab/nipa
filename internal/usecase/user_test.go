package usecase

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/ratelimit"
	"github.com/nipalab/nipa/internal/snow"
)

func newTestUser(t *testing.T, match bool) (*User, *MockuserAdminRepository) {
	t.Helper()

	ctrl := gomock.NewController(t)
	repo := NewMockuserAdminRepository(ctrl)
	node, err := snow.NewNode(1)
	require.NoError(t, err)
	return NewUser(node, repo, stubPasswordHasher{match: match}), repo
}

func TestNewUser(t *testing.T) {
	node, err := snow.NewNode(1)
	require.NoError(t, err)
	require.NotNil(t, NewUser(node, nil, nil))
}

func TestUser_Get(t *testing.T) {
	user, repo := newTestUser(t, true)
	want := &domain.User{ID: 7, Name: "alice", Email: "alice@example.com"}
	repo.EXPECT().GetByID(gomock.Any(), snow.ID(7)).Return(want, nil)

	got, err := user.Get(context.Background(), snow.ID(7))
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestUser_Get_Error(t *testing.T) {
	wantErr := errors.New("db down")
	user, repo := newTestUser(t, true)
	repo.EXPECT().GetByID(gomock.Any(), snow.ID(9)).Return(nil, wantErr)

	_, err := user.Get(context.Background(), snow.ID(9))
	require.ErrorIs(t, err, wantErr)
}

func TestUser_GetByEmail(t *testing.T) {
	user, repo := newTestUser(t, true)
	want := &domain.User{ID: 7, Email: "alice@example.com"}
	repo.EXPECT().GetByEmail(gomock.Any(), "alice@example.com").Return(want, nil)

	got, err := user.GetByEmail(context.Background(), " alice@example.com ")
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestUser_List_NoPermission(t *testing.T) {
	user, _ := newTestUser(t, true)

	_, err := user.List(permissionCtx(42))
	require.True(t, domain.IsErrorNoPermission(err))
}

func TestUser_List_Success(t *testing.T) {
	user, repo := newTestUser(t, true)
	want := []*domain.User{{ID: 7, Name: "alice"}}
	repo.EXPECT().List(gomock.Any()).Return(want, nil)

	got, err := user.List(permissionCtx(42, withAdmin()))
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestUser_Create_NoPermission(t *testing.T) {
	user, _ := newTestUser(t, true)

	_, err := user.Create(permissionCtx(42), "bob", "bob@example.com", "password123")
	require.True(t, domain.IsErrorNoPermission(err))
}

func TestUser_Create_Validation(t *testing.T) {
	user, _ := newTestUser(t, true)
	ctx := permissionCtx(42, withAdmin())

	_, err := user.Create(ctx, "  ", "bob@example.com", "password123")
	requireUserError(t, err)

	_, err = user.Create(ctx, "bob", "not-an-email", "password123")
	requireUserError(t, err)

	_, err = user.Create(ctx, "bob", "bob@example.com", "short")
	requireUserError(t, err)
}

func TestUser_Create_DuplicateEmail(t *testing.T) {
	user, repo := newTestUser(t, true)
	repo.EXPECT().GetByEmail(gomock.Any(), "bob@example.com").Return(&domain.User{ID: 9}, nil)

	_, err := user.Create(permissionCtx(42, withAdmin()), "bob", "bob@example.com", "password123")
	require.True(t, domain.IsErrorConflict(err))
}

func TestUser_Create_Success(t *testing.T) {
	user, repo := newTestUser(t, true)
	ctx := permissionCtx(42, withAdmin())

	repo.EXPECT().GetByEmail(gomock.Any(), "bob@example.com").Return(nil, domain.NewErrorRecordNotFound())
	repo.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, created domain.User) (*domain.User, error) {
			require.NotZero(t, created.ID)
			require.Equal(t, "bob", created.Name)
			require.Equal(t, "bob@example.com", created.Email)
			require.Equal(t, "hash", created.Password)
			return &created, nil
		},
	)

	got, err := user.Create(ctx, " bob ", " bob@example.com ", "password123")
	require.NoError(t, err)
	require.Equal(t, "bob", got.Name)
}

func TestUser_Create_LookupError(t *testing.T) {
	wantErr := errors.New("db down")
	user, repo := newTestUser(t, true)
	repo.EXPECT().GetByEmail(gomock.Any(), "bob@example.com").Return(nil, wantErr)

	_, err := user.Create(permissionCtx(42, withAdmin()), "bob", "bob@example.com", "password123")
	require.ErrorIs(t, err, wantErr)
}

func TestUser_UpdateProfile_Self(t *testing.T) {
	user, repo := newTestUser(t, true)
	ctx := permissionCtx(42)

	repo.EXPECT().UpdateProfile(gomock.Any(), snow.ID(42), "alice", "https://example.com/a.png", nil).Return(nil)
	repo.EXPECT().GetByID(gomock.Any(), snow.ID(42)).Return(&domain.User{ID: 42, Name: "alice"}, nil)

	got, err := user.UpdateProfile(ctx, snow.ID(42), " alice ", " https://example.com/a.png ", nil)
	require.NoError(t, err)
	require.Equal(t, "alice", got.Name)
}

func TestUser_UpdateProfile_NotifyEmail(t *testing.T) {
	user, repo := newTestUser(t, true)
	ctx := permissionCtx(42)

	disabled := false
	repo.EXPECT().UpdateProfile(gomock.Any(), snow.ID(42), "alice", "", &disabled).Return(nil)
	repo.EXPECT().GetByID(gomock.Any(), snow.ID(42)).Return(&domain.User{ID: 42, Name: "alice", NotifyEmail: false}, nil)

	got, err := user.UpdateProfile(ctx, snow.ID(42), "alice", "", &disabled)
	require.NoError(t, err)
	require.False(t, got.NotifyEmail)
}

func TestUser_UpdateProfile_OtherUserForbidden(t *testing.T) {
	user, _ := newTestUser(t, true)

	_, err := user.UpdateProfile(permissionCtx(42), snow.ID(7), "bob", "", nil)
	require.True(t, domain.IsErrorNoPermission(err))
}

func TestUser_UpdateProfile_AdminCanEditOther(t *testing.T) {
	user, repo := newTestUser(t, true)
	ctx := permissionCtx(42, withAdmin())

	repo.EXPECT().UpdateProfile(gomock.Any(), snow.ID(7), "bob", "", nil).Return(nil)
	repo.EXPECT().GetByID(gomock.Any(), snow.ID(7)).Return(&domain.User{ID: 7, Name: "bob"}, nil).Times(2)

	_, err := user.UpdateProfile(ctx, snow.ID(7), "bob", "", nil)
	require.NoError(t, err)
}

func TestUser_UpdateProfile_EmptyName(t *testing.T) {
	user, _ := newTestUser(t, true)

	_, err := user.UpdateProfile(permissionCtx(42), snow.ID(42), "  ", "", nil)
	requireUserError(t, err)
}

func TestUser_UpdateEmail(t *testing.T) {
	user, repo := newTestUser(t, true)
	ctx := permissionCtx(42, withAdmin())

	repo.EXPECT().GetByEmail(gomock.Any(), "new@example.com").Return(nil, domain.NewErrorRecordNotFound())
	repo.EXPECT().UpdateEmailAndRevokeTokens(gomock.Any(), snow.ID(7), "new@example.com").Return(nil)
	repo.EXPECT().GetByID(gomock.Any(), snow.ID(7)).Return(&domain.User{ID: 7, Email: "new@example.com"}, nil).Times(2)

	got, err := user.UpdateEmail(ctx, snow.ID(7), "New@Example.com")
	require.NoError(t, err)
	require.Equal(t, "new@example.com", got.Email)
}

func TestUser_UpdateEmail_RevokeError(t *testing.T) {
	user, repo := newTestUser(t, true)
	wantErr := errors.New("db down")
	ctx := permissionCtx(42, withAdmin())

	repo.EXPECT().GetByID(gomock.Any(), snow.ID(7)).Return(&domain.User{ID: 7}, nil)
	repo.EXPECT().GetByEmail(gomock.Any(), "new@example.com").Return(nil, domain.NewErrorRecordNotFound())
	repo.EXPECT().UpdateEmailAndRevokeTokens(gomock.Any(), snow.ID(7), "new@example.com").Return(wantErr)

	_, err := user.UpdateEmail(ctx, snow.ID(7), "new@example.com")
	require.ErrorIs(t, err, wantErr)
}

func TestUser_UpdateEmail_Invalid(t *testing.T) {
	user, repo := newTestUser(t, true)
	repo.EXPECT().GetByID(gomock.Any(), snow.ID(7)).Return(&domain.User{ID: 7}, nil)

	_, err := user.UpdateEmail(permissionCtx(42, withAdmin()), snow.ID(7), "nope")
	requireUserError(t, err)
}

func TestUser_UpdateEmail_Duplicate(t *testing.T) {
	user, repo := newTestUser(t, true)
	repo.EXPECT().GetByID(gomock.Any(), snow.ID(7)).Return(&domain.User{ID: 7}, nil)
	repo.EXPECT().GetByEmail(gomock.Any(), "taken@example.com").Return(&domain.User{ID: 9}, nil)

	_, err := user.UpdateEmail(permissionCtx(42, withAdmin()), snow.ID(7), "taken@example.com")
	require.True(t, domain.IsErrorConflict(err))
}

func TestUser_ChangePassword(t *testing.T) {
	user, repo := newTestUser(t, true)
	ctx := permissionCtx(42)

	repo.EXPECT().GetByID(gomock.Any(), snow.ID(42)).Return(&domain.User{ID: 42, Password: "old-hash"}, nil)
	repo.EXPECT().UpdatePasswordAndRevokeTokens(gomock.Any(), snow.ID(42), "hash").Return(nil)

	require.NoError(t, user.ChangePassword(ctx, snow.ID(42), "old-password", "new-password"))
}

func TestUser_ChangePassword_UpdateError(t *testing.T) {
	user, repo := newTestUser(t, true)
	wantErr := errors.New("db down")
	ctx := permissionCtx(42)

	repo.EXPECT().GetByID(gomock.Any(), snow.ID(42)).Return(&domain.User{ID: 42, Password: "old-hash"}, nil)
	repo.EXPECT().UpdatePasswordAndRevokeTokens(gomock.Any(), snow.ID(42), "hash").Return(wantErr)

	err := user.ChangePassword(ctx, snow.ID(42), "old-password", "new-password")
	require.ErrorIs(t, err, wantErr)
}

func TestUser_ChangePassword_WrongOldPassword(t *testing.T) {
	user, repo := newTestUser(t, false)
	ctx := permissionCtx(42)

	repo.EXPECT().GetByID(gomock.Any(), snow.ID(42)).Return(&domain.User{ID: 42, Password: "old-hash"}, nil)

	err := user.ChangePassword(ctx, snow.ID(42), "wrong", "new-password")
	requireUserError(t, err)
}

func TestUser_ChangePassword_OtherUser(t *testing.T) {
	user, _ := newTestUser(t, true)

	err := user.ChangePassword(permissionCtx(42), snow.ID(7), "old", "new-password")
	require.True(t, domain.IsErrorNoPermission(err))
}

func TestUser_ChangePassword_ShortNewPassword(t *testing.T) {
	user, repo := newTestUser(t, true)
	ctx := permissionCtx(42)

	repo.EXPECT().GetByID(gomock.Any(), snow.ID(42)).Return(&domain.User{ID: 42, Password: "old-hash"}, nil)

	err := user.ChangePassword(ctx, snow.ID(42), "old", "short")
	requireUserError(t, err)
}

func TestUser_ResetPassword(t *testing.T) {
	user, repo := newTestUser(t, true)
	repo.EXPECT().GetByID(gomock.Any(), snow.ID(7)).Return(&domain.User{ID: 7}, nil)
	repo.EXPECT().UpdatePasswordAndRevokeTokens(gomock.Any(), snow.ID(7), "hash").Return(nil)

	require.NoError(t, user.ResetPassword(permissionCtx(42, withAdmin()), snow.ID(7), "new-password"))
}

func TestUser_ResetPassword_UpdateError(t *testing.T) {
	user, repo := newTestUser(t, true)
	wantErr := errors.New("db down")
	repo.EXPECT().GetByID(gomock.Any(), snow.ID(7)).Return(&domain.User{ID: 7}, nil)
	repo.EXPECT().UpdatePasswordAndRevokeTokens(gomock.Any(), snow.ID(7), "hash").Return(wantErr)

	err := user.ResetPassword(permissionCtx(42, withAdmin()), snow.ID(7), "new-password")
	require.ErrorIs(t, err, wantErr)
}

func TestUser_ResetPassword_TargetLookupError(t *testing.T) {
	user, repo := newTestUser(t, true)
	wantErr := errors.New("db down")
	repo.EXPECT().GetByID(gomock.Any(), snow.ID(7)).Return(nil, wantErr)

	err := user.ResetPassword(permissionCtx(42, withAdmin()), snow.ID(7), "new-password")
	require.ErrorIs(t, err, wantErr)
}

func TestUser_AdminCannotManagePrivilegedUsers(t *testing.T) {
	for _, target := range []*domain.User{{ID: 7, IsAdmin: true}, {ID: 7, IsSuperAdmin: true}} {
		user, repo := newTestUser(t, true)
		repo.EXPECT().GetByID(gomock.Any(), snow.ID(7)).Return(target, nil).Times(4)
		ctx := permissionCtx(42, withAdmin())

		require.True(t, domain.IsErrorNoPermission(user.ResetPassword(ctx, snow.ID(7), "new-password")))
		_, err := user.UpdateEmail(ctx, snow.ID(7), "new@example.com")
		require.True(t, domain.IsErrorNoPermission(err))
		require.True(t, domain.IsErrorNoPermission(user.Deactivate(ctx, snow.ID(7))))
		_, err = user.UpdateProfile(ctx, snow.ID(7), "bob", "", nil)
		require.True(t, domain.IsErrorNoPermission(err))
	}
}

func TestUser_SuperAdminCanManagePrivilegedUsers(t *testing.T) {
	user, repo := newTestUser(t, true)
	repo.EXPECT().UpdatePasswordAndRevokeTokens(gomock.Any(), snow.ID(7), "hash").Return(nil)

	require.NoError(t, user.ResetPassword(permissionCtx(42, withSuperAdmin()), snow.ID(7), "new-password"))
}

func TestUser_ResetPassword_NoPermission(t *testing.T) {
	user, _ := newTestUser(t, true)

	err := user.ResetPassword(permissionCtx(42), snow.ID(7), "new-password")
	require.True(t, domain.IsErrorNoPermission(err))
}

func TestUser_SetAdminFlags(t *testing.T) {
	user, repo := newTestUser(t, true)
	ctx := permissionCtx(42, withSuperAdmin())

	repo.EXPECT().UpdateAdminFlags(gomock.Any(), snow.ID(7), true, false).Return(nil)
	repo.EXPECT().GetByID(gomock.Any(), snow.ID(7)).Return(&domain.User{ID: 7, IsAdmin: true}, nil)

	got, err := user.SetAdminFlags(ctx, snow.ID(7), true, false)
	require.NoError(t, err)
	require.True(t, got.IsAdmin)
}

func TestUser_SetAdminFlags_NoPermission(t *testing.T) {
	user, _ := newTestUser(t, true)

	_, err := user.SetAdminFlags(permissionCtx(42, withAdmin()), snow.ID(7), true, false)
	require.True(t, domain.IsErrorNoPermission(err))
}

func TestUser_SetAdminFlags_CannotDemoteSelf(t *testing.T) {
	user, _ := newTestUser(t, true)

	_, err := user.SetAdminFlags(permissionCtx(42, withSuperAdmin()), snow.ID(42), true, false)
	requireUserError(t, err)
}

func TestUser_Deactivate(t *testing.T) {
	user, repo := newTestUser(t, true)
	repo.EXPECT().GetByID(gomock.Any(), snow.ID(7)).Return(&domain.User{ID: 7}, nil)
	repo.EXPECT().DeactivateAndRevokeTokens(gomock.Any(), snow.ID(7)).Return(nil)

	require.NoError(t, user.Deactivate(permissionCtx(42, withAdmin()), snow.ID(7)))
}

func TestUser_Deactivate_RepositoryError(t *testing.T) {
	user, repo := newTestUser(t, true)
	wantErr := errors.New("db down")
	repo.EXPECT().GetByID(gomock.Any(), snow.ID(7)).Return(&domain.User{ID: 7}, nil)
	repo.EXPECT().DeactivateAndRevokeTokens(gomock.Any(), snow.ID(7)).Return(wantErr)

	err := user.Deactivate(permissionCtx(42, withAdmin()), snow.ID(7))
	require.ErrorIs(t, err, wantErr)
}

func TestUser_Deactivate_NoPermission(t *testing.T) {
	user, _ := newTestUser(t, true)

	err := user.Deactivate(permissionCtx(42), snow.ID(7))
	require.True(t, domain.IsErrorNoPermission(err))
}

func TestUser_Deactivate_Self(t *testing.T) {
	user, _ := newTestUser(t, true)

	err := user.Deactivate(permissionCtx(42, withAdmin()), snow.ID(42))
	requireUserError(t, err)
}

func TestValidatePassword(t *testing.T) {
	cases := []struct {
		name     string
		password string
		ok       bool
	}{
		{"too short", "1234567", false},
		{"minimum", "12345678", true},
		{"72 ascii bytes", strings.Repeat("a", 72), true},
		{"73 ascii bytes", strings.Repeat("a", 73), false},
		{"72 bytes of 3-byte runes", strings.Repeat("€", 24), true},
		{"73 bytes with a multi-byte rune", strings.Repeat("a", 71) + "é", false},
		{"few runes over 72 bytes", strings.Repeat("😀", 19), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePassword(tc.password)
			if tc.ok {
				require.NoError(t, err)
				return
			}
			requireUserError(t, err)
		})
	}
	require.EqualError(t, ValidatePassword(strings.Repeat("a", 73)), "password must be at most 72 bytes")
}

func TestUser_ChangePasswordThrottledPerUser(t *testing.T) {
	user, repo := newTestUser(t, false)
	user.WithLimiter(ratelimit.NewLimiter(ratelimit.NewMemory(), map[string]ratelimit.Rule{
		ratelimit.ScopeAuthID: {Rate: 0.0001, Burst: 2},
	}))
	repo.EXPECT().GetByID(gomock.Any(), snow.ID(42)).Return(&domain.User{ID: 42, Password: "hash"}, nil).Times(2)

	for range 2 {
		requireUserError(t, user.ChangePassword(permissionCtx(42), 42, "wrong", "new-password"))
	}
	var derr *domain.Error
	require.ErrorAs(t, user.ChangePassword(permissionCtx(42), 42, "wrong", "new-password"), &derr)
	require.Equal(t, 429, derr.Code)

	repo.EXPECT().GetByID(gomock.Any(), snow.ID(7)).Return(&domain.User{ID: 7, Password: "hash"}, nil)
	requireUserError(t, user.ChangePassword(permissionCtx(7), 7, "wrong", "new-password"))
}

func TestUser_ChangePasswordAudited(t *testing.T) {
	user, repo := newTestUser(t, true)
	rec := &captureRecorder{}
	user.WithAudit(rec)
	repo.EXPECT().GetByID(gomock.Any(), snow.ID(42)).Return(&domain.User{ID: 42, Password: "hash"}, nil).Times(2)
	repo.EXPECT().UpdatePasswordAndRevokeTokens(gomock.Any(), snow.ID(42), "hash").Return(nil)

	require.NoError(t, user.ChangePassword(permissionCtx(42), 42, "old-password", "new-password"))
	entry := rec.only(t)
	requireActor(t, entry, domain.AuditUserPasswordChanged)
	require.Equal(t, snow.ID(42).Base36(), entry.TargetID)
	require.Empty(t, entry.Metadata)
	require.Equal(t, 1, rec.txs)

	requireUserError(t, user.ChangePassword(permissionCtx(42), 42, "old-password", "short"))
	require.Len(t, rec.entries, 1)

	rec.err = errAudit
	repo.EXPECT().GetByID(gomock.Any(), snow.ID(42)).Return(&domain.User{ID: 42, Password: "hash"}, nil)
	repo.EXPECT().UpdatePasswordAndRevokeTokens(gomock.Any(), snow.ID(42), "hash").Return(nil)
	require.ErrorIs(t, user.ChangePassword(permissionCtx(42), 42, "old-password", "new-password"), errAudit)
}
