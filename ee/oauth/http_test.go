package oauth

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
	"github.com/nipalab/nipa/internal/http/api"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

type stubCredentialHasher struct{}

func (stubCredentialHasher) Hash(password string) (string, error) { return "hash:" + password, nil }
func (stubCredentialHasher) Compare(hash, password string) bool   { return hash == "hash:"+password }

type stubAuthSessions struct{}

func (stubAuthSessions) SaveRefreshToken(context.Context, snow.ID, string, time.Time) error {
	return nil
}

func (stubAuthSessions) GetAndDeleteRefreshToken(context.Context, string) (*domain.RefreshToken, error) {
	return nil, domain.NewErrorRecordNotFound()
}

func (stubAuthSessions) CreateSSHLoginChallenge(context.Context, string, string, time.Time) error {
	return nil
}

func (stubAuthSessions) DeleteExpiredSSHLoginChallenges(context.Context, time.Time) error {
	return nil
}

func (stubAuthSessions) ConsumeSSHLoginChallenge(context.Context, string) (*domain.SSHLoginChallenge, error) {
	return nil, domain.NewErrorRecordNotFound()
}

type oauthServer struct {
	env    *oauthEnv
	server *httptest.Server
	client *http.Client
}

// newOAuthHandlerServer wires the public and authenticated routes the way
// production does: public routes on the unfiltered /api/v1/auth WebService,
// identity management behind a filter that injects the caller's claims.
func newOAuthHandlerServer(t *testing.T, userID snow.ID) *oauthServer {
	t.Helper()

	env := newOAuthEnv(t)
	handler := NewHandler(env.oauth, usecase.NewAuth("test-secret", stubCredentialHasher{}, env.users, stubAuthSessions{}))

	publicWs := new(restful.WebService)
	publicWs.Path("/api/v1/auth").Consumes(restful.MIME_JSON).Produces(restful.MIME_JSON)
	RegisterPublicRoutes(publicWs, handler)

	authedWs := new(restful.WebService)
	authedWs.Path("/api/v1").
		Filter(func(req *restful.Request, resp *restful.Response, chain *restful.FilterChain) {
			req.SetAttribute(api.AttributeClaims, &domain.Claims{UserID: userID})
			chain.ProcessFilter(req, resp)
		}).
		Consumes(restful.MIME_JSON).
		Produces(restful.MIME_JSON)
	RegisterRoutes(authedWs, handler)

	container := restful.NewContainer()
	container.Add(publicWs)
	container.Add(authedWs)
	server := httptest.NewServer(container)
	t.Cleanup(server.Close)
	return &oauthServer{
		env:    env,
		server: server,
		client: &http.Client{
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

func (s *oauthServer) do(t *testing.T, method, path, body string) (*http.Response, string) {
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
	resp, err := s.client.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp, string(data)
}

func (s *oauthServer) signedState(t *testing.T, redirect string, linkUser int64) string {
	t.Helper()

	state, err := s.env.oauth.states.Sign(redirect, linkUser)
	require.NoError(t, err)
	return state
}

func TestStartRedirectsToTheProvider(t *testing.T) {
	server := newOAuthHandlerServer(t, 42)

	resp, _ := server.do(t, http.MethodGet, "/api/v1/auth/oauth/github/start?redirect=/onboarding", "")
	require.Equal(t, http.StatusFound, resp.StatusCode)
	location := resp.Header.Get("Location")
	require.True(t, strings.HasPrefix(location, "https://github.example/authorize?state="), location)
	state := strings.TrimPrefix(location, "https://github.example/authorize?state=")
	verified, err := server.env.oauth.states.Verify(state)
	require.NoError(t, err)
	require.Equal(t, "/onboarding", verified.Redirect)

	resp, _ = server.do(t, http.MethodGet, "/api/v1/auth/oauth/google/start", "")
	require.Equal(t, http.StatusNotFound, resp.StatusCode, "providers without credentials are disabled")
}

func TestCallbackRedirectsWithTheLoginCode(t *testing.T) {
	server := newOAuthHandlerServer(t, 42)
	server.env.provider.profile = &Profile{AccountID: "1001", Email: "alice@example.com", EmailVerified: true}
	server.env.users.users = []*domain.User{{ID: 42, Email: "alice@example.com", EmailVerified: true}}

	state := server.signedState(t, "/oauth/callback", 0)
	resp, _ := server.do(t, http.MethodGet, "/api/v1/auth/oauth/github/callback?code=abc&state="+state, "")
	require.Equal(t, http.StatusFound, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Location"), "/oauth/callback?code=")

	resp, _ = server.do(t, http.MethodGet, "/api/v1/auth/oauth/github/callback?state="+state, "")
	require.Equal(t, http.StatusBadRequest, resp.StatusCode, "a missing code is rejected")

	resp, _ = server.do(t, http.MethodGet, "/api/v1/auth/oauth/github/callback?code=abc&state=tampered", "")
	require.Equal(t, http.StatusBadRequest, resp.StatusCode, "a tampered state is rejected")

	server.env.provider.err = domain.NewErrorUser("github sign-in failed")
	resp, _ = server.do(t, http.MethodGet, "/api/v1/auth/oauth/github/callback?code=abc&state="+state, "")
	require.Equal(t, http.StatusBadRequest, resp.StatusCode, "provider errors surface as client errors")

	resp, _ = server.do(t, http.MethodGet, "/api/v1/auth/oauth/google/callback", "")
	require.Equal(t, http.StatusNotFound, resp.StatusCode, "disabled providers reject callbacks")
}

func TestCompleteExchangesTheCodeForASession(t *testing.T) {
	server := newOAuthHandlerServer(t, 42)
	server.env.users.users = []*domain.User{{ID: 42, Email: "alice@example.com", EmailVerified: true}}
	server.env.repo.codes["code-1"] = codeRow{userID: 42, expiresAt: server.env.now.Add(time.Minute)}

	resp, body := server.do(t, http.MethodPost, "/api/v1/auth/oauth/complete", `{"code":"code-1"}`)
	require.Equal(t, http.StatusOK, resp.StatusCode, body)
	var login struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &login))
	require.NotEmpty(t, login.AccessToken)
	require.Equal(t, "Bearer", login.TokenType)
	cookies := resp.Cookies()
	require.NotEmpty(t, cookies)
	require.Equal(t, "nipa_refresh", cookies[0].Name)

	resp, body = server.do(t, http.MethodPost, "/api/v1/auth/oauth/complete", `{"code":"missing"}`)
	require.Equal(t, http.StatusNotFound, resp.StatusCode, body)

	resp, body = server.do(t, http.MethodPost, "/api/v1/auth/oauth/complete", `{"code":""}`)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode, body)

	resp, _ = server.do(t, http.MethodPost, "/api/v1/auth/oauth/complete", `{"code":`)
	require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

func TestIdentityEndpoints(t *testing.T) {
	server := newOAuthHandlerServer(t, 42)

	resp, body := server.do(t, http.MethodGet, "/api/v1/me/identities", "")
	require.Equal(t, http.StatusOK, resp.StatusCode, body)
	require.Equal(t, "[]", body)

	identity := &domain.UserIdentity{
		ID: 9, UserID: 42, Provider: ProviderGitHub, ProviderAccountID: "1001",
		Email: "alice@example.com", CreatedAt: server.env.now,
	}
	server.env.repo.identities[identityKey(ProviderGitHub, "1001")] = identity
	server.env.repo.byID[identity.ID] = identity

	resp, body = server.do(t, http.MethodGet, "/api/v1/me/identities", "")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var list []struct {
		ID        string `json:"id"`
		Provider  string `json:"provider"`
		Email     string `json:"email"`
		CreatedAt string `json:"created_at"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &list))
	require.Len(t, list, 1)
	require.Equal(t, snow.ID(9).Base36(), list[0].ID)
	require.Equal(t, ProviderGitHub, list[0].Provider)
	require.Equal(t, "alice@example.com", list[0].Email)
	require.NotEmpty(t, list[0].CreatedAt)

	resp, body = server.do(t, http.MethodGet, "/api/v1/me/identities/github/start?redirect=/settings/profile", "")
	require.Equal(t, http.StatusOK, resp.StatusCode, body)
	var urlResponse struct {
		URL string `json:"url"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &urlResponse))
	require.True(t, strings.HasPrefix(urlResponse.URL, "https://github.example/authorize?state="), urlResponse.URL)

	resp, _ = server.do(t, http.MethodGet, "/api/v1/me/identities/google/start", "")
	require.Equal(t, http.StatusNotFound, resp.StatusCode)

	resp, _ = server.do(t, http.MethodDelete, "/api/v1/me/identities/!!", "")
	require.Equal(t, http.StatusBadRequest, resp.StatusCode, "invalid identity ids are rejected")

	resp, _ = server.do(t, http.MethodDelete, "/api/v1/me/identities/1", "")
	require.Equal(t, http.StatusNotFound, resp.StatusCode)

	owner := &domain.User{ID: 42, Email: "alice@example.com", EmailVerified: true, Password: "hash"}
	server.env.users.users = []*domain.User{owner}
	resp, body = server.do(t, http.MethodDelete, "/api/v1/me/identities/"+snow.ID(9).Base36(), "")
	require.Equal(t, http.StatusOK, resp.StatusCode, body)
	require.Equal(t, []snow.ID{9}, server.env.repo.deleted)
}

func TestIdentityEndpointsRequireAuthentication(t *testing.T) {
	env := newOAuthEnv(t)
	handler := NewHandler(env.oauth, usecase.NewAuth("test-secret", stubCredentialHasher{}, env.users, stubAuthSessions{}))

	ws := new(restful.WebService)
	ws.Path("/api/v1").Consumes(restful.MIME_JSON).Produces(restful.MIME_JSON)
	RegisterRoutes(ws, handler)
	container := restful.NewContainer()
	container.Add(ws)
	server := httptest.NewServer(container)
	t.Cleanup(server.Close)

	for _, request := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/me/identities"},
		{http.MethodGet, "/api/v1/me/identities/github/start"},
		{http.MethodDelete, "/api/v1/me/identities/1"},
	} {
		req, err := http.NewRequest(request.method, server.URL+request.path, nil)
		require.NoError(t, err)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		require.Equal(t, http.StatusUnauthorized, resp.StatusCode, "%s %s without claims", request.method, request.path)
	}
}
