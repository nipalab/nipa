package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	database "github.com/nipalab/nipa/db"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/http/model"
	sqlcSqlite "github.com/nipalab/nipa/internal/repository/sqlc/sqlite"
	"github.com/nipalab/nipa/internal/repository/sqlite"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
	"github.com/stretchr/testify/require"
)

type testRegistry struct {
	auth *usecase.Auth
	user *usecase.User
}

func (r *testRegistry) Auth() *usecase.Auth { return r.auth }
func (r *testRegistry) User() *usecase.User { return r.user }

type stubPasswordHasher struct{}

func (stubPasswordHasher) Hash(_ string) (string, error) { return "hash", nil }
func (stubPasswordHasher) Compare(_, _ string) bool      { return true }

// TestAPIRoutes exercises the fully wired HTTP API. It must call SetupRoute
// exactly once per process: SetupSwagger registers on the default mux, which
// panics on duplicate registration.
func TestAPIRoutes(t *testing.T) {
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
		PhotoUrl: sql.NullString{},
		IsAdmin:  true,
	})
	require.NoError(t, err)

	node, _ := snow.NewNode(1)
	reg := &testRegistry{
		auth: usecase.NewAuth("test-secret", stubPasswordHasher{}, sqlite.NewUserRepository(dbConn), sqlite.NewAuthRepository(dbConn)),
		user: usecase.NewUser(node),
	}

	container := NewAPI(reg).SetupRoute()
	server := httptest.NewServer(container)
	t.Cleanup(server.Close)

	login := func(t *testing.T) (model.LoginResponse, *http.Cookie) {
		t.Helper()

		resp := doJSON(t, server.URL+"/api/v1/auth/login", `{"email":"alice@example.com","password":"whatever"}`, nil, "")
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)

		var loginResponse model.LoginResponse
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&loginResponse))

		var refresh *http.Cookie
		for _, cookie := range resp.Cookies() {
			if cookie.Name == "nipa_refresh" {
				refresh = cookie
			}
		}
		require.NotNil(t, refresh)
		return loginResponse, refresh
	}

	t.Run("login sets an httpOnly refresh cookie", func(t *testing.T) {
		loginResponse, refresh := login(t)

		require.Equal(t, "Bearer", loginResponse.TokenType)
		require.NotEmpty(t, loginResponse.AccessToken)
		require.Equal(t, 30*60, loginResponse.ExpiresIn)

		require.NotEmpty(t, refresh.Value)
		require.True(t, refresh.HttpOnly)
		require.True(t, refresh.Secure)
		require.Equal(t, http.SameSiteStrictMode, refresh.SameSite)
		require.Equal(t, "/api/v1/auth", refresh.Path)
		require.Greater(t, refresh.MaxAge, 0)

		parsed := parseLoginAccessToken(t, loginResponse.AccessToken, "test-secret")
		require.Equal(t, snow.ID(userID), parsed.UserID)
	})

	t.Run("refresh rotates the cookie", func(t *testing.T) {
		_, refresh := login(t)

		resp := doJSON(t, server.URL+"/api/v1/auth/refresh", `{}`, []*http.Cookie{refresh}, "")
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)

		var refreshed model.LoginResponse
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&refreshed))
		require.Equal(t, "Bearer", refreshed.TokenType)
		require.NotEmpty(t, refreshed.AccessToken)

		var rotated *http.Cookie
		for _, cookie := range resp.Cookies() {
			if cookie.Name == "nipa_refresh" {
				rotated = cookie
			}
		}
		require.NotNil(t, rotated)
		require.NotEmpty(t, rotated.Value)
		require.NotEqual(t, refresh.Value, rotated.Value)

		parsed := parseLoginAccessToken(t, refreshed.AccessToken, "test-secret")
		require.Equal(t, snow.ID(userID), parsed.UserID)
	})

	t.Run("refresh without a cookie is unauthorized", func(t *testing.T) {
		resp := doJSON(t, server.URL+"/api/v1/auth/refresh", `{}`, nil, "")
		defer resp.Body.Close()
		require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

		var apiErr model.APIError
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&apiErr))
		require.Equal(t, "refresh token required", apiErr.Error)
	})

	t.Run("logout revokes the refresh token and clears the cookie", func(t *testing.T) {
		_, refresh := login(t)

		resp := doJSON(t, server.URL+"/api/v1/auth/logout", `{}`, []*http.Cookie{refresh}, "")
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)

		var cleared *http.Cookie
		for _, cookie := range resp.Cookies() {
			if cookie.Name == "nipa_refresh" {
				cleared = cookie
			}
		}
		require.NotNil(t, cleared)
		require.Equal(t, -1, cleared.MaxAge)

		replay := doJSON(t, server.URL+"/api/v1/auth/refresh", `{}`, []*http.Cookie{refresh}, "")
		defer replay.Body.Close()
		require.Equal(t, http.StatusNotFound, replay.StatusCode)
	})

	t.Run("stale refresh does not clobber a rotated cookie", func(t *testing.T) {
		_, first := login(t)

		rotatedResp := doJSON(t, server.URL+"/api/v1/auth/refresh", `{}`, []*http.Cookie{first}, "")
		defer rotatedResp.Body.Close()
		require.Equal(t, http.StatusOK, rotatedResp.StatusCode)

		var second *http.Cookie
		for _, cookie := range rotatedResp.Cookies() {
			if cookie.Name == "nipa_refresh" {
				second = cookie
			}
		}
		require.NotNil(t, second)

		staleResp := doJSON(t, server.URL+"/api/v1/auth/refresh", `{}`, []*http.Cookie{first}, "")
		defer staleResp.Body.Close()
		require.Equal(t, http.StatusNotFound, staleResp.StatusCode)
		for _, cookie := range staleResp.Cookies() {
			if cookie.Name == "nipa_refresh" {
				require.NotEqual(t, -1, cookie.MaxAge, "the losing tab must not clear the rotated cookie")
			}
		}

		okResp := doJSON(t, server.URL+"/api/v1/auth/refresh", `{}`, []*http.Cookie{second}, "")
		defer okResp.Body.Close()
		require.Equal(t, http.StatusOK, okResp.StatusCode)
	})

	t.Run("login unknown user", func(t *testing.T) {
		resp := doJSON(t, server.URL+"/api/v1/auth/login", `{"email":"ghost@example.com","password":"whatever"}`, nil, "")
		defer resp.Body.Close()

		require.Equal(t, http.StatusBadRequest, resp.StatusCode)
		var apiErr model.APIError
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&apiErr))
		require.Equal(t, "invalid email or password", apiErr.Error)
	})

	t.Run("login invalid json", func(t *testing.T) {
		resp := doJSON(t, server.URL+"/api/v1/auth/login", `{"email":`, nil, "")
		defer resp.Body.Close()

		require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
		var apiErr model.APIError
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&apiErr))
		require.Equal(t, "internal unknown error", apiErr.Error)
	})

	t.Run("cross-origin writes are rejected", func(t *testing.T) {
		resp := doJSON(t, server.URL+"/api/v1/auth/login", `{"email":"alice@example.com","password":"whatever"}`, nil, "http://evil.example")
		defer resp.Body.Close()

		require.Equal(t, http.StatusForbidden, resp.StatusCode)
		var apiErr model.APIError
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&apiErr))
		require.Equal(t, "cross-origin request rejected", apiErr.Error)
	})

	t.Run("same-origin writes are allowed", func(t *testing.T) {
		resp := doJSON(t, server.URL+"/api/v1/auth/login", `{"email":"alice@example.com","password":"whatever"}`, nil, server.URL)
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("openapi doc is served", func(t *testing.T) {
		resp, err := http.Get(server.URL + "/docs/api.json")
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)
	})
}

func doJSON(t *testing.T, url, body string, cookies []*http.Cookie, origin string) *http.Response {
	t.Helper()

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBufferString(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

func parseLoginAccessToken(t *testing.T, token string, secret string) *domain.Claims {
	t.Helper()

	claims := domain.Claims{}
	parsed, err := jwt.ParseWithClaims(token, &claims, func(_ *jwt.Token) (interface{}, error) {
		return []byte(secret), nil
	})
	require.NoError(t, err)
	require.True(t, parsed.Valid)
	return &claims
}
