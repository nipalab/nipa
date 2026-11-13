package usecase

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/totp"
)

func newMockMFA(t *testing.T) (*MFA, *MockmfaRepository, *totp.Cipher) {
	t.Helper()
	c, err := totp.NewCipher(bytes.Repeat([]byte{9}, 32))
	require.NoError(t, err)
	repo := NewMockmfaRepository(gomock.NewController(t))
	users := fakeUsers{mfaAlice: {ID: mfaAlice, Email: "alice@example.com", Password: "hash", EmailVerified: true}}
	return NewMFA(repo, users, stubPasswordHasher{match: true}, c, nil), repo, c
}

func TestMFA_RepositoryErrors(t *testing.T) {
	boom := errors.New("db down")
	ctx := context.Background()
	now := time.Now()
	live := &domain.MFAChallenge{TokenHash: hashSubject("tok"), UserID: mfaAlice, Method: LoginMethodPassword, ExpiresAt: now.Add(time.Minute)}

	t.Run("status", func(t *testing.T) {
		m, repo, _ := newMockMFA(t)
		repo.EXPECT().GetTOTP(gomock.Any(), mfaAlice).Return(nil, boom)
		_, err := m.Status(userCtx(mfaAlice))
		require.ErrorIs(t, err, boom)
	})
	t.Run("required", func(t *testing.T) {
		m, repo, _ := newMockMFA(t)
		repo.EXPECT().GetTOTP(gomock.Any(), mfaAlice).Return(nil, boom)
		_, err := m.required(ctx, mfaAlice)
		require.ErrorIs(t, err, boom)
	})
	t.Run("begin challenge", func(t *testing.T) {
		m, repo, _ := newMockMFA(t)
		repo.EXPECT().DeleteExpiredChallenges(gomock.Any(), gomock.Any()).Return(boom)
		_, err := m.beginChallenge(ctx, mfaAlice, LoginMethodPassword)
		require.ErrorIs(t, err, boom)

		repo.EXPECT().DeleteExpiredChallenges(gomock.Any(), gomock.Any()).Return(nil)
		repo.EXPECT().CreateChallenge(gomock.Any(), gomock.Any()).Return(boom)
		_, err = m.beginChallenge(ctx, mfaAlice, LoginMethodPassword)
		require.ErrorIs(t, err, boom)
	})
	t.Run("get challenge", func(t *testing.T) {
		m, repo, _ := newMockMFA(t)
		repo.EXPECT().GetChallenge(gomock.Any(), live.TokenHash).Return(nil, boom)
		_, _, err := m.verifyLogin(ctx, "tok", "123456")
		require.ErrorIs(t, err, boom)
	})
	t.Run("add attempt", func(t *testing.T) {
		m, repo, _ := newMockMFA(t)
		repo.EXPECT().GetChallenge(gomock.Any(), live.TokenHash).Return(live, nil).Times(2)
		repo.EXPECT().AddChallengeAttempt(gomock.Any(), live.TokenHash).Return(int64(0), boom)
		_, _, err := m.verifyLogin(ctx, "tok", "123456")
		require.ErrorIs(t, err, boom)
		repo.EXPECT().AddChallengeAttempt(gomock.Any(), live.TokenHash).Return(int64(0), domain.NewErrorRecordNotFound())
		_, _, err = m.verifyLogin(ctx, "tok", "123456")
		requireCode(t, err, 400)
	})
	t.Run("consume", func(t *testing.T) {
		m, repo, _ := newMockMFA(t)
		repo.EXPECT().GetChallenge(gomock.Any(), live.TokenHash).Return(live, nil).AnyTimes()
		repo.EXPECT().AddChallengeAttempt(gomock.Any(), live.TokenHash).Return(int64(1), nil).AnyTimes()
		repo.EXPECT().GetTOTP(gomock.Any(), mfaAlice).Return(nil, boom)
		_, _, err := m.verifyLogin(ctx, "tok", "123456")
		require.ErrorIs(t, err, boom)

		repo.EXPECT().GetTOTP(gomock.Any(), mfaAlice).Return(nil, domain.NewErrorRecordNotFound())
		_, _, err = m.verifyLogin(ctx, "tok", "123456")
		requireCode(t, err, 400)

		repo.EXPECT().UseRecoveryCode(gomock.Any(), mfaAlice, gomock.Any(), gomock.Any()).Return(false, boom)
		_, _, err = m.verifyLogin(ctx, "tok", "ABCDE-FGHJK")
		require.ErrorIs(t, err, boom)

		repo.EXPECT().UseRecoveryCode(gomock.Any(), mfaAlice, gomock.Any(), gomock.Any()).Return(true, nil)
		repo.EXPECT().DeleteChallenge(gomock.Any(), live.TokenHash).Return(false, nil)
		_, _, err = m.verifyLogin(ctx, "tok", "ABCDE-FGHJK")
		requireCode(t, err, 400)

		repo.EXPECT().UseRecoveryCode(gomock.Any(), mfaAlice, gomock.Any(), gomock.Any()).Return(true, nil)
		repo.EXPECT().DeleteChallenge(gomock.Any(), live.TokenHash).Return(false, boom)
		_, _, err = m.verifyLogin(ctx, "tok", "ABCDE-FGHJK")
		require.ErrorIs(t, err, boom)
	})
	t.Run("advance step", func(t *testing.T) {
		m, repo, c := newMockMFA(t)
		secret := []byte("12345678901234567890")
		sealed, err := c.Seal(secret, secretAAD(mfaAlice))
		require.NoError(t, err)
		confirmed := now
		repo.EXPECT().GetTOTP(gomock.Any(), mfaAlice).Return(&domain.UserTOTP{UserID: mfaAlice, SecretEnc: sealed, ConfirmedAt: &confirmed}, nil).Times(2)
		repo.EXPECT().AdvanceTOTPStep(gomock.Any(), mfaAlice, gomock.Any()).Return(false, boom)
		_, err = m.consumeCode(ctx, mfaAlice, totp.Code(secret, totp.Step(now)))
		require.ErrorIs(t, err, boom)

		repo.EXPECT().AdvanceTOTPStep(gomock.Any(), mfaAlice, gomock.Any()).Return(false, nil)
		_, err = m.consumeCode(ctx, mfaAlice, totp.Code(secret, totp.Step(now)))
		requireCode(t, err, 400)
	})
	t.Run("remove all", func(t *testing.T) {
		m, repo, _ := newMockMFA(t)
		repo.EXPECT().DeleteTOTP(gomock.Any(), mfaAlice).Return(false, boom)
		require.ErrorIs(t, m.removeAll(ctx, mfaAlice), boom)
		repo.EXPECT().DeleteTOTP(gomock.Any(), mfaAlice).Return(true, nil)
		repo.EXPECT().DeleteRecoveryCodes(gomock.Any(), mfaAlice).Return(boom)
		require.ErrorIs(t, m.removeAll(ctx, mfaAlice), boom)
	})
}
