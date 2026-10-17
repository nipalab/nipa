package registration

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/emicklei/go-restful/v3"
	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

// stubSessionStore satisfies the auth repository surface usecase.Auth needs;
// only SaveRefreshToken is exercised by the login-result path.
type stubSessionStore struct{}

func (stubSessionStore) SaveRefreshToken(context.Context, snow.ID, string, time.Time) error {
	return nil
}

func (stubSessionStore) GetAndDeleteRefreshToken(context.Context, string) (*domain.RefreshToken, error) {
	return nil, domain.NewErrorRecordNotFound()
}

func (stubSessionStore) CreateSSHLoginChallenge(context.Context, string, string, time.Time) error {
	return nil
}

func (stubSessionStore) DeleteExpiredSSHLoginChallenges(context.Context, time.Time) error {
	return nil
}

func (stubSessionStore) ConsumeSSHLoginChallenge(context.Context, string) (*domain.SSHLoginChallenge, error) {
	return nil, domain.NewErrorRecordNotFound()
}

type registrationServer struct {
	env    *registrationEnv
	server *httptest.Server
}

func newRegistrationServer(t *testing.T, providers func() []string) *registrationServer {
	t.Helper()

	env := newRegistrationEnv(t)
	var handler *Handler
	if providers != nil {
		handler = NewHandler(env.registration, usecase.NewAuth("test-secret", stubHasher{}, env.users, stubSessionStore{})).
			WithProviders(providers)
	} else {
		handler = NewHandler(env.registration, usecase.NewAuth("test-secret", stubHasher{}, env.users, stubSessionStore{}))
	}

	ws := new(restful.WebService)
	ws.Path("/api/v1/auth").Consumes(restful.MIME_JSON).Produces(restful.MIME_JSON)
	RegisterPublicRoutes(ws, handler)

	container := restful.NewContainer()
	container.Add(ws)
	server := httptest.NewServer(container)
	t.Cleanup(server.Close)
	return &registrationServer{env: env, server: server}
}

func (s *registrationServer) do(t *testing.T, method, path, body string) (*http.Response, string) {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, s.server.URL+path, reader)
	require.NoError(t, err)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp, string(data)
}

func TestCapabilitiesReportsProviders(t *testing.T) {
	server := newRegistrationServer(t, nil)

	resp, body := server.do(t, http.MethodGet, "/api/v1/auth/capabilities", "")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.JSONEq(t, `{"registration": true, "providers": []}`, body)

	withProviders := newRegistrationServer(t, func() []string { return []string{"github", "google"} })
	resp, body = withProviders.do(t, http.MethodGet, "/api/v1/auth/capabilities", "")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.JSONEq(t, `{"registration": true, "providers": ["github", "google"]}`, body)
}

func TestRegisterEndpoint(t *testing.T) {
	server := newRegistrationServer(t, nil)

	resp, body := server.do(t, http.MethodPost, "/api/v1/auth/register",
		`{"name":"Alice","email":" alice@example.com ","password":"password123"}`)
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	require.JSONEq(t, `{"status":"pending_verification","email":"alice@example.com"}`, body)

	require.Len(t, server.env.repo.created, 1)
	require.Equal(t, "alice@example.com", server.env.repo.created[0].Email)
	require.Len(t, server.env.outbox.deliveries, 1)
	created := server.env.repo.created[0]
	server.env.users.users = append(server.env.users.users, &created)

	resp, body = server.do(t, http.MethodPost, "/api/v1/auth/register",
		`{"name":"Alice","email":"alice@example.com","password":"password123"}`)
	require.Equal(t, http.StatusConflict, resp.StatusCode, body)

	resp, body = server.do(t, http.MethodPost, "/api/v1/auth/register",
		`{"name":"Bob","email":"bob@example.com","password":"short"}`)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode, body)

	resp, _ = server.do(t, http.MethodPost, "/api/v1/auth/register", `{"name":`)
	require.Equal(t, http.StatusInternalServerError, resp.StatusCode, "malformed bodies surface as unhandled errors")
}

func TestResendVerificationEndpoint(t *testing.T) {
	server := newRegistrationServer(t, nil)

	resp, body := server.do(t, http.MethodPost, "/api/v1/auth/register/resend", `{"email":"nobody@example.com"}`)
	require.Equal(t, http.StatusOK, resp.StatusCode, body)

	server.env.users.users = []*domain.User{{ID: 42, Name: "Alice", Email: "alice@example.com", EmailVerified: false}}
	resp, body = server.do(t, http.MethodPost, "/api/v1/auth/register/resend", `{"email":"alice@example.com"}`)
	require.Equal(t, http.StatusOK, resp.StatusCode, body)
	require.Len(t, server.env.outbox.deliveries, 1)

	resp, _ = server.do(t, http.MethodPost, "/api/v1/auth/register/resend", `{"email":`)
	require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

func TestVerifyEndpoint(t *testing.T) {
	server := newRegistrationServer(t, nil)

	resp, body := server.do(t, http.MethodPost, "/api/v1/auth/verify", `{"token":"unknown"}`)
	require.Equal(t, http.StatusNotFound, resp.StatusCode, body)

	resp, body = server.do(t, http.MethodPost, "/api/v1/auth/verify", `{"token":""}`)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode, body)

	resp, _ = server.do(t, http.MethodPost, "/api/v1/auth/verify", `{"token":`)
	require.Equal(t, http.StatusInternalServerError, resp.StatusCode)

	user := &domain.User{ID: 42, Name: "Alice", Email: "alice@example.com", EmailVerified: false}
	server.env.users.users = []*domain.User{user}
	rawToken := "token-42"
	server.env.repo.verifications[42] = &verificationRow{
		userID:    42,
		tokenHash: hashToken(rawToken),
		expiresAt: server.env.now.Add(time.Hour),
	}

	resp, body = server.do(t, http.MethodPost, "/api/v1/auth/verify", `{"token":"`+rawToken+`"}`)
	require.Equal(t, http.StatusOK, resp.StatusCode, body)

	var login struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &login))
	require.NotEmpty(t, login.AccessToken)
	require.Equal(t, "Bearer", login.TokenType)
	require.Equal(t, 1800, login.ExpiresIn)

	cookies := resp.Cookies()
	require.NotEmpty(t, cookies)
	require.Equal(t, "nipa_refresh", cookies[0].Name)
	require.True(t, cookies[0].HttpOnly)
	require.NotEmpty(t, cookies[0].Value)
}
