package usecase

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type stubPasswordHasher struct {
	match bool
}

func (s stubPasswordHasher) Hash(_ string) (string, error) { return "hash", nil }
func (s stubPasswordHasher) Compare(_, _ string) bool      { return s.match }

func newTestAuth(secret string, match bool, userRepo userRepository, authRepo authRepository) *Auth {
	return NewAuth(secret, stubPasswordHasher{match: match}, userRepo, authRepo)
}

func TestAuth_LoginWithEmailPassword(t *testing.T) {
	ctx := context.Background()
	user := &domain.User{ID: snow.ID(42), Email: "user@example.com", IsAdmin: true}

	ctrl := gomock.NewController(t)
	userRepo := NewMockuserRepository(ctrl)
	authRepo := NewMockauthRepository(ctrl)

	userRepo.EXPECT().
		GetByEmail(gomock.Any(), "user@example.com").
		Return(user, nil)

	authRepo.EXPECT().
		SaveRefreshToken(gomock.Any(), snow.ID(42), gomock.Not(""), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ snow.ID, _ string, expiresAt time.Time) error {
			require.WithinDuration(t, time.Now().Add(60*24*time.Hour), expiresAt, 5*time.Second)
			return nil
		})

	got, err := newTestAuth("secret", true, userRepo, authRepo).LoginWithEmailPassword(ctx, "user@example.com", "password")
	require.NoError(t, err)

	require.Equal(t, "Bearer", got.TokenType)
	require.NotEmpty(t, got.RefreshToken)
	require.NotEmpty(t, got.AccessToken)
	require.Equal(t, int(refreshTokenExpiration.Seconds()), got.RefreshExpiresIn)

	claims := parseAccessToken(t, got.AccessToken, "secret")
	require.Equal(t, snow.ID(42).Base36(), claims.Subject)
	require.InDelta(t, 30*time.Minute.Seconds(), claims.ExpiresAt.Sub(claims.IssuedAt.Time).Seconds(), 2)

	require.Equal(t, snow.ID(42), claims.UserID)
	require.False(t, claims.IsSuperAdmin)
	require.True(t, claims.IsAdmin)
	require.Empty(t, claims.OrgID)
}

func TestAuth_LoginWithEmailPassword_UserNotFound(t *testing.T) {
	ctx := context.Background()
	wantErr := errors.New("not found")

	ctrl := gomock.NewController(t)
	userRepo := NewMockuserRepository(ctrl)
	authRepo := NewMockauthRepository(ctrl)

	userRepo.EXPECT().
		GetByEmail(gomock.Any(), "user@example.com").
		Return(nil, wantErr)

	_, err := NewAuth("secret", nil, userRepo, authRepo).LoginWithEmailPassword(ctx, "user@example.com", "password")
	require.ErrorIs(t, err, wantErr)
}

func TestAuth_LoginWithEmailPassword_SaveRefreshTokenError(t *testing.T) {
	ctx := context.Background()
	wantErr := errors.New("save failed")

	ctrl := gomock.NewController(t)
	userRepo := NewMockuserRepository(ctrl)
	authRepo := NewMockauthRepository(ctrl)

	userRepo.EXPECT().
		GetByEmail(gomock.Any(), "user@example.com").
		Return(&domain.User{ID: 1}, nil)
	authRepo.EXPECT().
		SaveRefreshToken(gomock.Any(), snow.ID(1), gomock.Any(), gomock.Any()).
		Return(wantErr)

	_, err := newTestAuth("secret", true, userRepo, authRepo).LoginWithEmailPassword(ctx, "user@example.com", "password")
	require.ErrorIs(t, err, wantErr)
}

func TestAuth_LoginWithRefreshToken(t *testing.T) {
	ctx := context.Background()
	stored := &domain.RefreshToken{UserID: 7, Token: "old-refresh-token", ExpiresAt: time.Now().Add(time.Hour)}

	ctrl := gomock.NewController(t)
	userRepo := NewMockuserRepository(ctrl)
	authRepo := NewMockauthRepository(ctrl)

	authRepo.EXPECT().
		GetAndDeleteRefreshToken(gomock.Any(), "old-refresh-token").
		Return(stored, nil)

	userRepo.EXPECT().
		GetByID(gomock.Any(), snow.ID(7)).
		Return(&domain.User{ID: 7, IsSuperAdmin: true}, nil)

	authRepo.EXPECT().
		SaveRefreshToken(gomock.Any(), snow.ID(7), gomock.Not("old-refresh-token"), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ snow.ID, _ string, expiresAt time.Time) error {
			require.WithinDuration(t, time.Now().Add(60*24*time.Hour), expiresAt, 5*time.Second)
			return nil
		})

	got, err := NewAuth("secret", nil, userRepo, authRepo).LoginWithRefreshToken(ctx, "old-refresh-token")
	require.NoError(t, err)

	require.Equal(t, "Bearer", got.TokenType)
	require.NotEmpty(t, got.RefreshToken)
	require.NotEmpty(t, got.AccessToken)
	require.Equal(t, int(refreshTokenExpiration.Seconds()), got.RefreshExpiresIn)

	claims := parseAccessToken(t, got.AccessToken, "secret")
	require.Equal(t, strconv.FormatInt(7, 10), claims.Subject)

	require.Equal(t, snow.ID(7), claims.UserID)
	require.True(t, claims.IsSuperAdmin)
	require.False(t, claims.IsAdmin)
	require.Empty(t, claims.OrgID)
}

func TestAuth_LoginWithRefreshToken_Expired(t *testing.T) {
	ctx := context.Background()

	ctrl := gomock.NewController(t)
	userRepo := NewMockuserRepository(ctrl)
	authRepo := NewMockauthRepository(ctrl)

	authRepo.EXPECT().
		GetAndDeleteRefreshToken(gomock.Any(), "expired-token").
		Return(&domain.RefreshToken{UserID: 7, ExpiresAt: time.Now().Add(-time.Hour)}, nil)

	_, err := NewAuth("secret", nil, userRepo, authRepo).LoginWithRefreshToken(ctx, "expired-token")
	require.Error(t, err)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 400, domErr.Code)
	require.Equal(t, "refresh token expired", domErr.Message)
}

func TestAuth_LoginWithRefreshToken_GetError(t *testing.T) {
	ctx := context.Background()
	wantErr := errors.New("token not found")

	ctrl := gomock.NewController(t)
	userRepo := NewMockuserRepository(ctrl)
	authRepo := NewMockauthRepository(ctrl)

	authRepo.EXPECT().
		GetAndDeleteRefreshToken(gomock.Any(), "unknown-token").
		Return(nil, wantErr)

	_, err := NewAuth("secret", nil, userRepo, authRepo).LoginWithRefreshToken(ctx, "unknown-token")
	require.ErrorIs(t, err, wantErr)
}

func TestAuth_LoginWithRefreshToken_UserNotFound(t *testing.T) {
	ctx := context.Background()
	wantErr := errors.New("not found")

	ctrl := gomock.NewController(t)
	userRepo := NewMockuserRepository(ctrl)
	authRepo := NewMockauthRepository(ctrl)

	authRepo.EXPECT().
		GetAndDeleteRefreshToken(gomock.Any(), "valid-token").
		Return(&domain.RefreshToken{UserID: 99, ExpiresAt: time.Now().Add(time.Hour)}, nil)

	userRepo.EXPECT().
		GetByID(gomock.Any(), snow.ID(99)).
		Return(nil, wantErr)

	_, err := NewAuth("secret", nil, userRepo, authRepo).LoginWithRefreshToken(ctx, "valid-token")
	require.ErrorIs(t, err, wantErr)
}

func TestAuth_Logout(t *testing.T) {
	ctx := context.Background()

	t.Run("revokes the token", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		authRepo := NewMockauthRepository(ctrl)
		authRepo.EXPECT().
			GetAndDeleteRefreshToken(gomock.Any(), "tok").
			Return(&domain.RefreshToken{Token: "tok"}, nil)

		auth := NewAuth("secret", nil, NewMockuserRepository(ctrl), authRepo)
		require.NoError(t, auth.Logout(ctx, "tok"))
	})

	t.Run("idempotent for unknown token", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		authRepo := NewMockauthRepository(ctrl)
		authRepo.EXPECT().
			GetAndDeleteRefreshToken(gomock.Any(), "gone").
			Return(nil, domain.NewErrorRecordNotFound())

		auth := NewAuth("secret", nil, NewMockuserRepository(ctrl), authRepo)
		require.NoError(t, auth.Logout(ctx, "gone"))
	})

	t.Run("empty token is a no-op", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		auth := NewAuth("secret", nil, NewMockuserRepository(ctrl), NewMockauthRepository(ctrl))
		require.NoError(t, auth.Logout(ctx, ""))
	})

	t.Run("repository error is returned", func(t *testing.T) {
		wantErr := errors.New("db down")
		ctrl := gomock.NewController(t)
		authRepo := NewMockauthRepository(ctrl)
		authRepo.EXPECT().
			GetAndDeleteRefreshToken(gomock.Any(), "boom").
			Return(nil, wantErr)

		auth := NewAuth("secret", nil, NewMockuserRepository(ctrl), authRepo)
		require.ErrorIs(t, auth.Logout(ctx, "boom"), wantErr)
	})
}

func TestAuth_ValidateToken_Valid(t *testing.T) {
	ctx := context.Background()
	want := domain.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "42",
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
		UserID:       42,
		IsSuperAdmin: true,
		IsAdmin:      true,
		OrgID:        []snow.ID{snow.ID(1), snow.ID(2), snow.ID(3)},
	}

	token := signToken(t, jwt.SigningMethodHS256, []byte("secret"), want)

	got, err := NewAuth("secret", nil, nil, nil).ValidateToken(ctx, token)
	require.NoError(t, err)

	require.Equal(t, "42", got.Subject)
	require.Equal(t, snow.ID(42), got.UserID)
	require.True(t, got.IsSuperAdmin)
	require.True(t, got.IsAdmin)
	require.Equal(t, []snow.ID{snow.ID(1), snow.ID(2), snow.ID(3)}, got.OrgID)
	require.WithinDuration(t, want.ExpiresAt.Time, got.ExpiresAt.Time, time.Second)
}

func TestAuth_ValidateToken_Expired(t *testing.T) {
	ctx := context.Background()
	claims := domain.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "42",
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Hour)),
		},
		UserID: 42,
	}

	token := signToken(t, jwt.SigningMethodHS256, []byte("secret"), claims)

	got, err := NewAuth("secret", nil, nil, nil).ValidateToken(ctx, token)
	assertInvalidTokenError(t, err)
	require.Nil(t, got)
}

func TestAuth_ValidateToken_WrongSecret(t *testing.T) {
	ctx := context.Background()
	claims := domain.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "42",
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
		UserID: 42,
	}

	token := signToken(t, jwt.SigningMethodHS256, []byte("other-secret"), claims)

	got, err := NewAuth("secret", nil, nil, nil).ValidateToken(ctx, token)
	assertInvalidTokenError(t, err)
	require.Nil(t, got)
}

func TestAuth_ValidateToken_Malformed(t *testing.T) {
	ctx := context.Background()

	got, err := NewAuth("secret", nil, nil, nil).ValidateToken(ctx, "not-a-jwt")
	assertInvalidTokenError(t, err)
	require.Nil(t, got)
}

func TestAuth_ValidateToken_UnexpectedSigningMethod(t *testing.T) {
	ctx := context.Background()
	claims := domain.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "42",
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
		UserID: 42,
	}

	token, err := jwt.NewWithClaims(jwt.SigningMethodNone, claims).
		SignedString(jwt.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)

	got, err := NewAuth("secret", nil, nil, nil).ValidateToken(ctx, token)
	assertInvalidTokenError(t, err)
	require.Nil(t, got)
}

func parseAccessToken(t *testing.T, token string, secret string) *domain.Claims {
	t.Helper()

	claims := domain.Claims{}
	parsed, err := jwt.ParseWithClaims(token, &claims, func(_ *jwt.Token) (interface{}, error) {
		return []byte(secret), nil
	})
	require.NoError(t, err)
	require.True(t, parsed.Valid)
	return &claims
}

func signToken(t *testing.T, method jwt.SigningMethod, key interface{}, claims domain.Claims) string {
	t.Helper()

	signed, err := jwt.NewWithClaims(method, claims).SignedString(key)
	require.NoError(t, err)
	return signed
}

func assertInvalidTokenError(t *testing.T, err error) {
	t.Helper()

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 400, domErr.Code)
	require.Equal(t, "invalid token", domErr.Message)
}
