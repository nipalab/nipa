package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/ratelimit"
)

func identityLimiter(burst int) *ratelimit.Limiter {
	return ratelimit.NewLimiter(ratelimit.NewMemory(), map[string]ratelimit.Rule{
		ratelimit.ScopeAuthID: {Rate: 0.0001, Burst: burst},
	})
}

func TestAuth_LoginThrottledPerIdentity(t *testing.T) {
	ctrl := gomock.NewController(t)
	userRepo := NewMockuserRepository(ctrl)
	authRepo := NewMockauthRepository(ctrl)
	userRepo.EXPECT().GetByEmail(gomock.Any(), gomock.Any()).Return(nil, errors.New("boom")).Times(2)

	auth := NewAuth("secret", nil, userRepo, authRepo).WithLimiter(identityLimiter(2))
	ctx := context.Background()

	_, err := auth.LoginWithEmailPassword(ctx, "user@example.com", "pw")
	require.EqualError(t, err, "boom")
	_, err = auth.LoginWithEmailPassword(ctx, " USER@example.com ", "pw")
	require.EqualError(t, err, "boom")

	_, err = auth.LoginWithEmailPassword(ctx, "user@example.com", "pw")
	var derr *domain.Error
	require.ErrorAs(t, err, &derr)
	require.Equal(t, 429, derr.Code)

	userRepo.EXPECT().GetByEmail(gomock.Any(), "other@example.com").Return(nil, errors.New("boom"))
	_, err = auth.LoginWithEmailPassword(ctx, "other@example.com", "pw")
	require.EqualError(t, err, "boom")
}

func TestAuth_SSHLoginThrottledOnlyWithEmail(t *testing.T) {
	ctrl := gomock.NewController(t)
	auth := NewAuth("secret", nil, NewMockuserRepository(ctrl), NewMockauthRepository(ctrl)).WithLimiter(identityLimiter(1))
	ctx := context.Background()

	_, err := auth.CompleteSSHKeyLogin(ctx, "a@example.com", "", nil, nil)
	require.Error(t, err)
	_, err = auth.CompleteSSHKeyLogin(ctx, "a@example.com", "", nil, nil)
	var derr *domain.Error
	require.ErrorAs(t, err, &derr)
	require.Equal(t, 429, derr.Code)

	_, err = auth.BeginSSHKeyLogin(ctx, "a@example.com")
	require.ErrorAs(t, err, &derr)
	require.Equal(t, 429, derr.Code)

	for i := 0; i < 3; i++ {
		_, err = auth.CompleteSSHKeyLogin(ctx, "", "", nil, nil)
		require.Error(t, err)
		if errors.As(err, &derr) {
			require.NotEqual(t, 429, derr.Code)
		}
	}
}
