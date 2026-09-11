package server

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

type stubUserRepo struct {
	user *domain.User
	err  error
}

func (s *stubUserRepo) GetByEmail(_ context.Context, _ string) (*domain.User, error) {
	return s.user, s.err
}

func (s *stubUserRepo) GetByID(_ context.Context, _ snow.ID) (*domain.User, error) {
	return s.user, s.err
}

type stubAuthRepo struct {
	saveErr      error
	refreshErr   error
	refreshToken *domain.RefreshToken
}

func (s *stubAuthRepo) SaveRefreshToken(_ context.Context, _ snow.ID, _ string, _ time.Time) error {
	return s.saveErr
}

func (s *stubAuthRepo) GetAndDeleteRefreshToken(_ context.Context, _ string) (*domain.RefreshToken, error) {
	return s.refreshToken, s.refreshErr
}

type stubPasswordHasher struct{}

func (stubPasswordHasher) Hash(_ string) (string, error) { return "hash", nil }
func (stubPasswordHasher) Compare(_, _ string) bool      { return true }

type loginMockContainer struct {
	auth *usecase.Auth
}

func (m *loginMockContainer) Auth() *usecase.Auth     { return m.auth }
func (m *loginMockContainer) User() *usecase.User     { return nil }
func (m *loginMockContainer) Branch() *usecase.Branch { return nil }
func (m *loginMockContainer) Common() *usecase.Common { return nil }
func (m *loginMockContainer) Push() *usecase.Push     { return nil }
func (m *loginMockContainer) Chunk() *usecase.Chunk   { return nil }

func newLoginServer(t *testing.T, userRepo *stubUserRepo, authRepo *stubAuthRepo) *nipaServer {
	t.Helper()
	auth := usecase.NewAuth("test-secret", stubPasswordHasher{}, userRepo, authRepo)
	return New(&loginMockContainer{auth: auth})
}

func TestLoginWithUsernamePassword_Success(t *testing.T) {
	user := &domain.User{
		ID:      snow.ID(42),
		Name:    "alice",
		Email:   "alice@example.com",
		IsAdmin: true,
	}
	srv := newLoginServer(t,
		&stubUserRepo{user: user},
		&stubAuthRepo{},
	)

	resp, err := srv.LoginWithUsernamePassword(context.Background(), &pb.LoginUsernamePasswordRequest{
		Username: "alice@example.com",
		Password: "secret",
	})
	require.NoError(t, err)
	require.NotEmpty(t, resp.AccessToken)
	require.NotEmpty(t, resp.RefreshToken)
	require.Equal(t, int32(30*60), resp.ExpiresIn)
}

func TestLoginWithUsernamePassword_UserNotFound(t *testing.T) {
	srv := newLoginServer(t,
		&stubUserRepo{err: domain.NewErrorRecordNotFound()},
		&stubAuthRepo{},
	)

	_, err := srv.LoginWithUsernamePassword(context.Background(), &pb.LoginUsernamePasswordRequest{
		Username: "unknown@example.com",
		Password: "wrong",
	})
	require.Error(t, err)
}

func TestLoginWithUsernamePassword_SaveRefreshTokenFails(t *testing.T) {
	user := &domain.User{ID: snow.ID(1)}
	srv := newLoginServer(t,
		&stubUserRepo{user: user},
		&stubAuthRepo{saveErr: domain.NewErrorDatabase("db error")},
	)

	_, err := srv.LoginWithUsernamePassword(context.Background(), &pb.LoginUsernamePasswordRequest{
		Username: "user@example.com",
		Password: "pass",
	})
	require.Error(t, err)
}

func TestLoginWithRefreshToken_Success(t *testing.T) {
	user := &domain.User{ID: snow.ID(7), IsSuperAdmin: true}
	srv := newLoginServer(t,
		&stubUserRepo{user: user},
		&stubAuthRepo{
			refreshToken: &domain.RefreshToken{
				UserID:    snow.ID(7),
				ExpiresAt: time.Now().Add(time.Hour),
			},
		},
	)

	resp, err := srv.LoginWithRefreshToken(context.Background(), &pb.LoginWithRefreshRequest{
		RefreshToken: "valid-refresh-token",
	})
	require.NoError(t, err)
	require.NotEmpty(t, resp.AccessToken)
	require.NotEmpty(t, resp.RefreshToken)
	require.Equal(t, int32(30*60), resp.ExpiresIn)
}

func TestLoginWithRefreshToken_Expired(t *testing.T) {
	srv := newLoginServer(t,
		&stubUserRepo{},
		&stubAuthRepo{
			refreshToken: &domain.RefreshToken{
				UserID:    snow.ID(7),
				ExpiresAt: time.Now().Add(-time.Hour),
			},
		},
	)

	_, err := srv.LoginWithRefreshToken(context.Background(), &pb.LoginWithRefreshRequest{
		RefreshToken: "expired-token",
	})
	require.Error(t, err)
}

func TestLoginWithRefreshToken_NotFound(t *testing.T) {
	srv := newLoginServer(t,
		&stubUserRepo{},
		&stubAuthRepo{refreshErr: domain.NewErrorRecordNotFound()},
	)

	_, err := srv.LoginWithRefreshToken(context.Background(), &pb.LoginWithRefreshRequest{
		RefreshToken: "nonexistent-token",
	})
	require.Error(t, err)
}

func TestLoginWithRefreshToken_UserNotFound(t *testing.T) {
	srv := newLoginServer(t,
		&stubUserRepo{err: domain.NewErrorRecordNotFound()},
		&stubAuthRepo{
			refreshToken: &domain.RefreshToken{
				UserID:    snow.ID(99),
				ExpiresAt: time.Now().Add(time.Hour),
			},
		},
	)

	_, err := srv.LoginWithRefreshToken(context.Background(), &pb.LoginWithRefreshRequest{
		RefreshToken: "valid-token",
	})
	require.Error(t, err)
}
