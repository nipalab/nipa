package server

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/totp"
	"github.com/nipalab/nipa/internal/usecase"
)

func newMFALoginServer(t *testing.T, user *domain.User) (*nipaServer, *MockmfaRepository) {
	t.Helper()
	cipher, err := totp.NewCipher([]byte("0123456789abcdef0123456789abcdef"))
	require.NoError(t, err)
	repo := NewMockmfaRepository(gomock.NewController(t))
	users := &stubUserRepo{user: user}
	mfa := usecase.NewMFA(repo, users, stubPasswordHasher{}, cipher, nil)
	auth := usecase.NewAuth("test-secret", stubPasswordHasher{}, users, &stubAuthRepo{}).WithMFA(mfa)
	return New(&loginMockContainer{auth: auth}), repo
}

func enrolledUser() *domain.User {
	return &domain.User{ID: snow.ID(42), Email: "alice@example.com", EmailVerified: true, Password: "hash"}
}

func expectEnrolled(repo *MockmfaRepository) {
	confirmed := time.Now()
	repo.EXPECT().GetTOTP(gomock.Any(), snow.ID(42)).Return(&domain.UserTOTP{UserID: 42, ConfirmedAt: &confirmed}, nil)
}

func TestLoginWithUsernamePassword_MFAOldClientRefused(t *testing.T) {
	srv, repo := newMFALoginServer(t, enrolledUser())
	expectEnrolled(repo)
	repo.EXPECT().DeleteExpiredChallenges(gomock.Any(), gomock.Any()).Return(nil)
	repo.EXPECT().CreateChallenge(gomock.Any(), gomock.Any()).Return(nil)

	_, err := srv.LoginWithUsernamePassword(context.Background(), &pb.LoginUsernamePasswordRequest{Username: "alice@example.com", Password: "pw"})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	require.Contains(t, status.Convert(err).Message(), "upgrade nipa or log in with an SSH key")
}

func TestLoginWithUsernamePassword_MFAChallenge(t *testing.T) {
	srv, repo := newMFALoginServer(t, enrolledUser())
	expectEnrolled(repo)
	repo.EXPECT().DeleteExpiredChallenges(gomock.Any(), gomock.Any()).Return(nil)
	repo.EXPECT().CreateChallenge(gomock.Any(), gomock.Any()).Return(nil)

	resp, err := srv.LoginWithUsernamePassword(context.Background(), &pb.LoginUsernamePasswordRequest{Username: "alice@example.com", Password: "pw", SupportsMfa: true})
	require.NoError(t, err)
	require.NotEmpty(t, resp.GetMfaChallenge())
	require.Empty(t, resp.GetAccessToken())
	require.Empty(t, resp.GetRefreshToken())
	require.Equal(t, int32(300), resp.GetExpiresIn())
}

func TestLoginWithUsernamePassword_MFAUnavailable(t *testing.T) {
	users := &stubUserRepo{user: enrolledUser()}
	repo := NewMockmfaRepository(gomock.NewController(t))
	mfa := usecase.NewMFA(repo, users, stubPasswordHasher{}, nil, nil)
	srv := New(&loginMockContainer{auth: usecase.NewAuth("test-secret", stubPasswordHasher{}, users, &stubAuthRepo{}).WithMFA(mfa)})
	expectEnrolled(repo)

	_, err := srv.LoginWithUsernamePassword(context.Background(), &pb.LoginUsernamePasswordRequest{Username: "alice@example.com", Password: "pw", SupportsMfa: true})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	require.Contains(t, status.Convert(err).Message(), "not configured")
}

func TestCompleteLoginMFA(t *testing.T) {
	srv, repo := newMFALoginServer(t, enrolledUser())
	challenge := &domain.MFAChallenge{UserID: 42, Method: usecase.LoginMethodPassword, ExpiresAt: time.Now().Add(time.Minute)}
	repo.EXPECT().GetChallenge(gomock.Any(), gomock.Any()).Return(challenge, nil)
	repo.EXPECT().AddChallengeAttempt(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	repo.EXPECT().UseRecoveryCode(gomock.Any(), snow.ID(42), gomock.Any(), gomock.Any()).Return(true, nil)
	repo.EXPECT().DeleteChallenge(gomock.Any(), gomock.Any()).Return(true, nil)

	resp, err := srv.CompleteLoginMFA(context.Background(), &pb.CompleteLoginMFARequest{Challenge: "tok", Code: "ABCDE-FGHJK"})
	require.NoError(t, err)
	require.NotEmpty(t, resp.GetAccessToken())
	require.NotEmpty(t, resp.GetRefreshToken())
	require.Empty(t, resp.GetMfaChallenge())

	repo.EXPECT().GetChallenge(gomock.Any(), gomock.Any()).Return(nil, domain.NewErrorRecordNotFound())
	_, err = srv.CompleteLoginMFA(context.Background(), &pb.CompleteLoginMFARequest{Challenge: "gone", Code: "123456"})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestCompleteLoginMFA_IsPublic(t *testing.T) {
	require.True(t, isPublicMethod("/greet.NipaService/CompleteLoginMFA"))
}

func TestHandleError_ServiceUnavailable(t *testing.T) {
	err := handleError(domain.NewErrorServiceUnavailable("not configured"))
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
}
