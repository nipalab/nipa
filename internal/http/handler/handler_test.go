package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	database "github.com/nipalab/nipa/db"
	"github.com/nipalab/nipa/internal/domain"
	httpApp "github.com/nipalab/nipa/internal/http"
	"github.com/nipalab/nipa/internal/http/model"
	sqlcSqlite "github.com/nipalab/nipa/internal/repository/sqlc/sqlite"
	"github.com/nipalab/nipa/internal/repository/sqlite"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
	"github.com/stretchr/testify/require"
)

type fakeAppContext struct {
	body       []byte
	statusCode int
	response   any
	cookies    map[string]*http.Cookie
}

func (f *fakeAppContext) Context() context.Context { return context.Background() }
func (f *fakeAppContext) Claims() *domain.Claims   { return nil }

func (f *fakeAppContext) ReadJson(v any) error {
	return json.Unmarshal(f.body, v)
}

func (f *fakeAppContext) WriteJson(statusCode int, v any) error {
	f.statusCode = statusCode
	f.response = v
	return nil
}

func (f *fakeAppContext) SetCookie(cookie *http.Cookie) {
	if f.cookies == nil {
		f.cookies = map[string]*http.Cookie{}
	}
	f.cookies[cookie.Name] = cookie
}

func (f *fakeAppContext) Cookie(name string) (*http.Cookie, error) {
	if cookie, ok := f.cookies[name]; ok {
		return cookie, nil
	}
	return nil, http.ErrNoCookie
}

func (f *fakeAppContext) HandleError(err error) {
	apiErr, ok := err.(*domain.Error)
	if ok {
		f.statusCode = apiErr.Code
		f.response = model.NewAPIError(apiErr.Message)
		return
	}
	f.statusCode = http.StatusInternalServerError
	f.response = model.NewAPIError("internal unknown error")
}

type handlerRegistry struct {
	auth *usecase.Auth
	user *usecase.User
}

func (r *handlerRegistry) Auth() *usecase.Auth { return r.auth }
func (r *handlerRegistry) User() *usecase.User { return r.user }

type stubPasswordHasher struct{}

func (stubPasswordHasher) Hash(_ string) (string, error) { return "hash", nil }
func (stubPasswordHasher) Compare(_, _ string) bool      { return true }

func newHandlerTestSetup(t *testing.T) (*Handler, *sqlite.Auth, snow.ID) {
	t.Helper()

	dbConn, err := database.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, dbConn.Close()) })
	dbConn.SetMaxOpenConns(1)
	require.NoError(t, database.MigrateUp(dbConn, "sqlite3"))

	q := sqlcSqlite.New(dbConn)
	userID, err := q.UserCreate(context.Background(), sqlcSqlite.UserCreateParams{
		Name:     "alice",
		Email:    "alice@example.com",
		Password: "hashed-password",
		IsAdmin:  true,
	})
	require.NoError(t, err)

	authRepo := sqlite.NewAuthRepository(dbConn)
	node, _ := snow.NewNode(1)
	reg := &handlerRegistry{
		auth: usecase.NewAuth("test-secret", stubPasswordHasher{}, sqlite.NewUserRepository(dbConn), authRepo),
		user: usecase.NewUser(node),
	}
	return NewHandler(reg), authRepo, snow.ID(userID)
}

func TestNewHandler(t *testing.T) {
	h, _, _ := newHandlerTestSetup(t)
	require.NotNil(t, h)
}

func TestHandler_AuthLogin(t *testing.T) {
	h, _, userID := newHandlerTestSetup(t)

	appCtx := &fakeAppContext{body: []byte(`{"email":"alice@example.com","password":"whatever"}`)}
	h.AuthLogin(appCtx)

	require.Equal(t, http.StatusOK, appCtx.statusCode)

	loginResponse, ok := appCtx.response.(model.LoginResponse)
	require.True(t, ok)
	require.Equal(t, "Bearer", loginResponse.TokenType)
	require.NotEmpty(t, loginResponse.AccessToken)
	require.Equal(t, 30*60, loginResponse.ExpiresIn)

	cookie, ok := appCtx.cookies[refreshCookieName]
	require.True(t, ok)
	require.NotEmpty(t, cookie.Value)
	require.True(t, cookie.HttpOnly)
	require.True(t, cookie.Secure)
	require.Equal(t, http.SameSiteStrictMode, cookie.SameSite)
	require.Equal(t, refreshCookiePath, cookie.Path)
	require.Equal(t, int((60 * 24 * time.Hour).Seconds()), cookie.MaxAge)

	claims := parseHandlerAccessToken(t, loginResponse.AccessToken, "test-secret")
	require.Equal(t, userID, claims.UserID)
}

func TestHandler_AuthLogin_UnknownUser(t *testing.T) {
	h, _, _ := newHandlerTestSetup(t)

	appCtx := &fakeAppContext{body: []byte(`{"email":"ghost@example.com","password":"whatever"}`)}
	h.AuthLogin(appCtx)

	require.Equal(t, http.StatusBadRequest, appCtx.statusCode)

	apiErr, ok := appCtx.response.(*model.APIError)
	require.True(t, ok)
	require.Equal(t, "invalid email or password", apiErr.Error)
	require.Empty(t, appCtx.cookies)
}

func TestHandler_AuthLogin_InvalidJson(t *testing.T) {
	h, _, _ := newHandlerTestSetup(t)

	appCtx := &fakeAppContext{body: []byte(`{"email":`)}
	h.AuthLogin(appCtx)

	require.Equal(t, http.StatusInternalServerError, appCtx.statusCode)

	apiErr, ok := appCtx.response.(*model.APIError)
	require.True(t, ok)
	require.Equal(t, "internal unknown error", apiErr.Error)
}

func TestHandler_AuthRefreshToken(t *testing.T) {
	h, authRepo, userID := newHandlerTestSetup(t)

	require.NoError(t, authRepo.SaveRefreshToken(
		context.Background(), userID, "valid-refresh-token", time.Now().Add(time.Hour)))

	appCtx := &fakeAppContext{
		body:    []byte(`{}`),
		cookies: map[string]*http.Cookie{refreshCookieName: {Name: refreshCookieName, Value: "valid-refresh-token"}},
	}
	h.AuthRefreshToken(appCtx)

	require.Equal(t, http.StatusOK, appCtx.statusCode)

	refreshed, ok := appCtx.response.(model.LoginResponse)
	require.True(t, ok)
	require.Equal(t, "Bearer", refreshed.TokenType)
	require.NotEmpty(t, refreshed.AccessToken)

	cookie, ok := appCtx.cookies[refreshCookieName]
	require.True(t, ok)
	require.NotEmpty(t, cookie.Value)
	require.NotEqual(t, "valid-refresh-token", cookie.Value, "refresh tokens rotate")
	require.True(t, cookie.HttpOnly)
	require.True(t, cookie.Secure)
}

func TestHandler_AuthRefreshToken_MissingCookie(t *testing.T) {
	h, _, _ := newHandlerTestSetup(t)

	appCtx := &fakeAppContext{body: []byte(`{}`)}
	h.AuthRefreshToken(appCtx)

	require.Equal(t, http.StatusUnauthorized, appCtx.statusCode)

	apiErr, ok := appCtx.response.(*model.APIError)
	require.True(t, ok)
	require.Equal(t, "refresh token required", apiErr.Error)
}

func TestHandler_AuthRefreshToken_Expired(t *testing.T) {
	h, authRepo, userID := newHandlerTestSetup(t)

	require.NoError(t, authRepo.SaveRefreshToken(
		context.Background(), userID, "expired-refresh-token", time.Now().Add(-time.Hour)))

	appCtx := &fakeAppContext{
		body:    []byte(`{}`),
		cookies: map[string]*http.Cookie{refreshCookieName: {Name: refreshCookieName, Value: "expired-refresh-token"}},
	}
	h.AuthRefreshToken(appCtx)

	require.Equal(t, http.StatusBadRequest, appCtx.statusCode)

	apiErr, ok := appCtx.response.(*model.APIError)
	require.True(t, ok)
	require.Equal(t, "refresh token expired", apiErr.Error)

	cookie := appCtx.cookies[refreshCookieName]
	require.NotNil(t, cookie)
	require.Equal(t, -1, cookie.MaxAge, "invalid refresh cookies are cleared")
}

func TestHandler_AuthRefreshToken_UnknownToken(t *testing.T) {
	h, _, _ := newHandlerTestSetup(t)

	appCtx := &fakeAppContext{
		body:    []byte(`{}`),
		cookies: map[string]*http.Cookie{refreshCookieName: {Name: refreshCookieName, Value: "unknown"}},
	}
	h.AuthRefreshToken(appCtx)

	require.Equal(t, http.StatusNotFound, appCtx.statusCode)

	apiErr, ok := appCtx.response.(*model.APIError)
	require.True(t, ok)
	require.Equal(t, "record not found", apiErr.Error)

	cookie := appCtx.cookies[refreshCookieName]
	require.NotNil(t, cookie)
	require.Equal(t, "unknown", cookie.Value, "another tab may hold the rotated cookie")
	require.NotEqual(t, -1, cookie.MaxAge)
}

func TestHandler_AuthLogout(t *testing.T) {
	h, authRepo, userID := newHandlerTestSetup(t)
	ctx := context.Background()

	require.NoError(t, authRepo.SaveRefreshToken(ctx, userID, "logout-token", time.Now().Add(time.Hour)))

	appCtx := &fakeAppContext{
		body:    []byte(`{}`),
		cookies: map[string]*http.Cookie{refreshCookieName: {Name: refreshCookieName, Value: "logout-token"}},
	}
	h.AuthLogout(appCtx)

	require.Equal(t, http.StatusOK, appCtx.statusCode)
	message, ok := appCtx.response.(model.MessageResponse)
	require.True(t, ok)
	require.Equal(t, "logged out", message.Message)

	cookie := appCtx.cookies[refreshCookieName]
	require.NotNil(t, cookie)
	require.Equal(t, -1, cookie.MaxAge)

	_, err := authRepo.GetAndDeleteRefreshToken(ctx, "logout-token")
	require.True(t, domain.IsErrorNotFound(err), "logout revokes the refresh token")
}

func TestHandler_AuthLogout_NoCookie(t *testing.T) {
	h, _, _ := newHandlerTestSetup(t)

	appCtx := &fakeAppContext{body: []byte(`{}`)}
	h.AuthLogout(appCtx)

	require.Equal(t, http.StatusOK, appCtx.statusCode)

	cookie := appCtx.cookies[refreshCookieName]
	require.NotNil(t, cookie)
	require.Equal(t, -1, cookie.MaxAge)
}

var _ httpApp.AppContext = (*fakeAppContext)(nil)

func parseHandlerAccessToken(t *testing.T, token string, secret string) *domain.Claims {
	t.Helper()

	claims := domain.Claims{}
	parsed, err := jwt.ParseWithClaims(token, &claims, func(_ *jwt.Token) (interface{}, error) {
		return []byte(secret), nil
	})
	require.NoError(t, err)
	require.True(t, parsed.Valid)
	return &claims
}
