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

	t.Run("login success", func(t *testing.T) {
		body := bytes.NewBufferString(`{"email":"alice@example.com","password":"whatever"}`)
		resp, err := http.Post(server.URL+"/auth/", "application/json", body)
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)

		var login model.LoginResponse
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&login))

		require.Equal(t, "Bearer", login.TokenType)
		require.NotEmpty(t, login.AccessToken)
		require.NotEmpty(t, login.RefreshToken)

		parsed := parseLoginAccessToken(t, login.AccessToken, "test-secret")
		require.Equal(t, snow.ID(userID), parsed.UserID)
	})

	t.Run("refresh token", func(t *testing.T) {
		body := bytes.NewBufferString(`{"email":"alice@example.com","password":"whatever"}`)
		resp, err := http.Post(server.URL+"/auth/", "application/json", body)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		defer resp.Body.Close()

		var loginResponse model.LoginResponse
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&loginResponse))
		require.NotEmpty(t, loginResponse.RefreshToken)

		refreshBody := bytes.NewBufferString(`{"refresh_token": "` + loginResponse.RefreshToken + `"}`)
		refreshResp, err := http.Post(server.URL+"/auth/refresh", "application/json", refreshBody)
		require.NoError(t, err)
		defer refreshResp.Body.Close()

		require.Equal(t, http.StatusOK, refreshResp.StatusCode)

		var refreshed model.LoginResponse
		require.NoError(t, json.NewDecoder(refreshResp.Body).Decode(&refreshed))
		require.Equal(t, "Bearer", refreshed.TokenType)
		require.NotEmpty(t, refreshed.AccessToken)
		require.NotEmpty(t, refreshed.RefreshToken)
	})

	t.Run("login unknown user", func(t *testing.T) {
		body := bytes.NewBufferString(`{"email":"ghost@example.com","password":"whatever"}`)
		resp, err := http.Post(server.URL+"/auth/", "application/json", body)
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusBadRequest, resp.StatusCode)
		var apiErr model.APIError
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&apiErr))
		require.Equal(t, "invalid email or password", apiErr.Error)
	})

	t.Run("login invalid json", func(t *testing.T) {
		body := bytes.NewBufferString(`{"email":`)
		resp, err := http.Post(server.URL+"/auth/", "application/json", body)
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
		var apiErr model.APIError
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&apiErr))
		require.Equal(t, "internal unknown error", apiErr.Error)
	})

	t.Run("openapi doc is served", func(t *testing.T) {
		resp, err := http.Get(server.URL + "/docs/api.json")
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)
	})
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
