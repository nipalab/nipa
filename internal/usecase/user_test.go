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

	repo.EXPECT().UpdateProfile(gomock.Any(), snow.ID(42), "alice", "https://example.com/a.png").Return(nil)
	repo.EXPECT().GetByID(gomock.Any(), snow.ID(42)).Return(&domain.User{ID: 42, Name: "alice"}, nil)

	got, err := user.UpdateProfile(ctx, snow.ID(42), " alice ", " https://example.com/a.png ")
	require.NoError(t, err)
	require.Equal(t, "alice", got.Name)
}

func TestUser_UpdateProfile_OtherUserForbidden(t *testing.T) {
	user, _ := newTestUser(t, true)

	_, err := user.UpdateProfile(permissionCtx(42), snow.ID(7), "bob", "")
	require.True(t, domain.IsErrorNoPermission(err))
}

func TestUser_UpdateProfile_AdminCanEditOther(t *testing.T) {
	user, repo := newTestUser(t, true)
	ctx := permissionCtx(42, withAdmin())

	repo.EXPECT().UpdateProfile(gomock.Any(), snow.ID(7), "bob", "").Return(nil)
	repo.EXPECT().GetByID(gomock.Any(), snow.ID(7)).Return(&domain.User{ID: 7, Name: "bob"}, nil)

	_, err := user.UpdateProfile(ctx, snow.ID(7), "bob", "")
	require.NoError(t, err)
}

func TestUser_UpdateProfile_EmptyName(t *testing.T) {
	user, _ := newTestUser(t, true)

	_, err := user.UpdateProfile(permissionCtx(42), snow.ID(42), "  ", "")
	requireUserError(t, err)
}

func TestUser_UpdateEmail(t *testing.T) {
	user, repo := newTestUser(t, true)
	ctx := permissionCtx(42, withAdmin())

	repo.EXPECT().GetByEmail(gomock.Any(), "new@example.com").Return(nil, domain.NewErrorRecordNotFound())
	repo.EXPECT().UpdateEmail(gomock.Any(), snow.ID(7), "new@example.com").Return(nil)
	repo.EXPECT().GetByID(gomock.Any(), snow.ID(7)).Return(&domain.User{ID: 7, Email: "new@example.com"}, nil)

	got, err := user.UpdateEmail(ctx, snow.ID(7), "new@example.com")
	require.NoError(t, err)
	require.Equal(t, "new@example.com", got.Email)
}

func TestUser_UpdateEmail_Invalid(t *testing.T) {
	user, _ := newTestUser(t, true)

	_, err := user.UpdateEmail(permissionCtx(42, withAdmin()), snow.ID(7), "nope")
	requireUserError(t, err)
}

func TestUser_UpdateEmail_Duplicate(t *testing.T) {
	user, repo := newTestUser(t, true)
	repo.EXPECT().GetByEmail(gomock.Any(), "taken@example.com").Return(&domain.User{ID: 9}, nil)

	_, err := user.UpdateEmail(permissionCtx(42, withAdmin()), snow.ID(7), "taken@example.com")
	require.True(t, domain.IsErrorConflict(err))
}

func TestUser_ChangePassword(t *testing.T) {
	user, repo := newTestUser(t, true)
	ctx := permissionCtx(42)

	repo.EXPECT().GetByID(gomock.Any(), snow.ID(42)).Return(&domain.User{ID: 42, Password: "old-hash"}, nil)
	repo.EXPECT().UpdatePassword(gomock.Any(), snow.ID(42), "hash").Return(nil)

	require.NoError(t, user.ChangePassword(ctx, snow.ID(42), "old-password", "new-password"))
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
	repo.EXPECT().UpdatePassword(gomock.Any(), snow.ID(7), "hash").Return(nil)

	require.NoError(t, user.ResetPassword(permissionCtx(42, withAdmin()), snow.ID(7), "new-password"))
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
	repo.EXPECT().Deactivate(gomock.Any(), snow.ID(7)).Return(nil)

	require.NoError(t, user.Deactivate(permissionCtx(42, withAdmin()), snow.ID(7)))
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
