package usecase

import (
	"context"
	"errors"
	"strconv"
	"strings"
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
	return NewAuth(secret, stubPasswordHasher{match: match}, userRepo, authRepo).WithSSHAudience(testSSHAudience)
}

func TestAuth_LoginWithEmailPassword(t *testing.T) {
	ctx := context.Background()
	user := &domain.User{ID: snow.ID(42), Email: "user@example.com", IsAdmin: true, EmailVerified: true}

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
		Return(&domain.User{ID: 1, EmailVerified: true}, nil)
	authRepo.EXPECT().
		SaveRefreshToken(gomock.Any(), snow.ID(1), gomock.Any(), gomock.Any()).
		Return(wantErr)

	_, err := newTestAuth("secret", true, userRepo, authRepo).LoginWithEmailPassword(ctx, "user@example.com", "password")
	require.ErrorIs(t, err, wantErr)
}

func TestAuth_LoginWithEmailPassword_UnverifiedEmail(t *testing.T) {
	ctx := context.Background()

	ctrl := gomock.NewController(t)
	userRepo := NewMockuserRepository(ctrl)
	authRepo := NewMockauthRepository(ctrl)

	userRepo.EXPECT().
		GetByEmail(gomock.Any(), "user@example.com").
		Return(&domain.User{ID: 42, EmailVerified: false}, nil)

	_, err := newTestAuth("secret", true, userRepo, authRepo).LoginWithEmailPassword(ctx, "user@example.com", "password")
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 403, domErr.Code)
	require.Equal(t, "email not confirmed", domErr.Message)
}

func TestAuth_LoginWithRefreshToken_UnverifiedEmail(t *testing.T) {
	ctx := context.Background()

	ctrl := gomock.NewController(t)
	userRepo := NewMockuserRepository(ctrl)
	authRepo := NewMockauthRepository(ctrl)

	authRepo.EXPECT().
		GetAndDeleteRefreshToken(gomock.Any(), "some-token").
		Return(&domain.RefreshToken{UserID: 7, ExpiresAt: time.Now().Add(time.Hour)}, nil)
	userRepo.EXPECT().
		GetByID(gomock.Any(), snow.ID(7)).
		Return(&domain.User{ID: 7, EmailVerified: false}, nil)

	_, err := NewAuth("secret", nil, userRepo, authRepo).LoginWithRefreshToken(ctx, "some-token")
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 403, domErr.Code)
	require.Equal(t, "email not confirmed", domErr.Message)
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
		Return(&domain.User{ID: 7, IsSuperAdmin: true, EmailVerified: true}, nil)

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

func validTokenClaims(version int64) domain.Claims {
	return domain.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "42",
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
		UserID:       42,
		IsSuperAdmin: true,
		IsAdmin:      true,
		OrgID:        []snow.ID{snow.ID(1), snow.ID(2), snow.ID(3)},
		TokenVersion: version,
	}
}

func authWithUser(t *testing.T, user *domain.User, err error) *Auth {
	t.Helper()
	ctrl := gomock.NewController(t)
	userRepo := NewMockuserRepository(ctrl)
	userRepo.EXPECT().GetByID(gomock.Any(), snow.ID(42)).Return(user, err)
	return NewAuth("secret", nil, userRepo, nil)
}

func TestAuth_ValidateToken_Valid(t *testing.T) {
	want := validTokenClaims(3)
	token := signToken(t, jwt.SigningMethodHS256, []byte("secret"), want)

	auth := authWithUser(t, &domain.User{ID: 42, IsSuperAdmin: true, IsAdmin: true, TokenVersion: 3}, nil)
	got, err := auth.ValidateToken(context.Background(), token)
	require.NoError(t, err)

	require.Equal(t, "42", got.Subject)
	require.Equal(t, snow.ID(42), got.UserID)
	require.True(t, got.IsSuperAdmin)
	require.True(t, got.IsAdmin)
	require.Equal(t, int64(3), got.TokenVersion)
	require.Equal(t, []snow.ID{snow.ID(1), snow.ID(2), snow.ID(3)}, got.OrgID)
	require.WithinDuration(t, want.ExpiresAt.Time, got.ExpiresAt.Time, time.Second)
}

func TestAuth_ValidateToken_AdminFlagsComeFromRow(t *testing.T) {
	token := signToken(t, jwt.SigningMethodHS256, []byte("secret"), validTokenClaims(0))

	demoted := authWithUser(t, &domain.User{ID: 42}, nil)
	got, err := demoted.ValidateToken(context.Background(), token)
	require.NoError(t, err)
	require.False(t, got.IsAdmin)
	require.False(t, got.IsSuperAdmin)

	plain := validTokenClaims(0)
	plain.IsAdmin, plain.IsSuperAdmin = false, false
	token = signToken(t, jwt.SigningMethodHS256, []byte("secret"), plain)
	promoted := authWithUser(t, &domain.User{ID: 42, IsAdmin: true}, nil)
	got, err = promoted.ValidateToken(context.Background(), token)
	require.NoError(t, err)
	require.True(t, got.IsAdmin)
	require.False(t, got.IsSuperAdmin)
}

func TestAuth_ValidateToken_WithoutVersionClaimIsVersionZero(t *testing.T) {
	token := signToken(t, jwt.SigningMethodHS256, []byte("secret"), validTokenClaims(0))
	payload, err := jwt.NewParser().DecodeSegment(strings.Split(token, ".")[1])
	require.NoError(t, err)
	require.NotContains(t, string(payload), `"tv"`)

	got, err := authWithUser(t, &domain.User{ID: 42, IsAdmin: true}, nil).ValidateToken(context.Background(), token)
	require.NoError(t, err)
	require.Equal(t, int64(0), got.TokenVersion)
	require.True(t, got.IsAdmin)

	_, err = authWithUser(t, &domain.User{ID: 42, TokenVersion: 1}, nil).ValidateToken(context.Background(), token)
	assertUnauthorizedToken(t, err)
}

func TestAuth_ValidateToken_VersionMismatch(t *testing.T) {
	token := signToken(t, jwt.SigningMethodHS256, []byte("secret"), validTokenClaims(2))

	got, err := authWithUser(t, &domain.User{ID: 42, IsAdmin: true, TokenVersion: 3}, nil).ValidateToken(context.Background(), token)
	assertUnauthorizedToken(t, err)
	require.Nil(t, got)
}

func TestAuth_ValidateToken_DeletedUser(t *testing.T) {
	token := signToken(t, jwt.SigningMethodHS256, []byte("secret"), validTokenClaims(0))

	got, err := authWithUser(t, nil, domain.NewErrorRecordNotFound()).ValidateToken(context.Background(), token)
	assertUnauthorizedToken(t, err)
	require.False(t, domain.IsServerError(err))
	require.Nil(t, got)
}

func TestAuth_ValidateToken_DatabaseErrorIsNotAnAuthFailure(t *testing.T) {
	token := signToken(t, jwt.SigningMethodHS256, []byte("secret"), validTokenClaims(0))
	dbErr := domain.NewErrorDatabase("connection refused")

	got, err := authWithUser(t, nil, dbErr).ValidateToken(context.Background(), token)
	require.ErrorIs(t, err, dbErr)
	require.True(t, domain.IsServerError(err))
	require.Nil(t, got)
}

func TestAuth_LoginResult_CarriesTokenVersion(t *testing.T) {
	ctrl := gomock.NewController(t)
	authRepo := NewMockauthRepository(ctrl)
	authRepo.EXPECT().SaveRefreshToken(gomock.Any(), snow.ID(42), gomock.Any(), gomock.Any()).Return(nil)

	got, err := NewAuth("secret", nil, nil, authRepo).LoginResult(context.Background(), &domain.User{ID: 42, TokenVersion: 7})
	require.NoError(t, err)
	require.Equal(t, int64(7), parseAccessToken(t, got.AccessToken, "secret").TokenVersion)
}

func assertUnauthorizedToken(t *testing.T, err error) {
	t.Helper()
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 401, domErr.Code)
	require.Equal(t, "invalid token", domErr.Message)
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

func TestAuth_ValidateToken_RejectsNonHS256(t *testing.T) {
	claims := domain.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "42",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
		UserID: 42,
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS512, claims).SignedString([]byte("secret"))
	require.NoError(t, err)

	got, err := NewAuth("secret", nil, nil, nil).ValidateToken(context.Background(), token)
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
	assertUnauthorizedToken(t, err)
}

type countingHasher struct{ hashes, compares int }

func (c *countingHasher) Hash(_ string) (string, error) { c.hashes++; return "hash", nil }
func (c *countingHasher) Compare(_, _ string) bool      { c.compares++; return false }

func TestAuth_LoginWithEmailPassword_UnknownUserStillSpendsAHashComparison(t *testing.T) {
	ctrl := gomock.NewController(t)
	userRepo := NewMockuserRepository(ctrl)
	userRepo.EXPECT().GetByEmail(gomock.Any(), "ghost@example.com").Return(nil, domain.NewErrorRecordNotFound()).Times(2)
	hasher := &countingHasher{}
	auth := NewAuth("secret", hasher, userRepo, NewMockauthRepository(ctrl))

	for i := 0; i < 2; i++ {
		_, err := auth.LoginWithEmailPassword(context.Background(), "ghost@example.com", "password")
		requireUserError(t, err)
	}

	require.Equal(t, 1, hasher.hashes, "the equalizer hash is computed once")
	require.Equal(t, 2, hasher.compares)
}
