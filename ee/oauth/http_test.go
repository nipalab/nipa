package oauth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/emicklei/go-restful/v3"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/http/api"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/totp"
	"github.com/nipalab/nipa/internal/usecase"
)

type stubCredentialHasher struct{}

func (stubCredentialHasher) Hash(password string) (string, error) { return "hash:" + password, nil }
func (stubCredentialHasher) Compare(hash, password string) bool   { return hash == "hash:"+password }

type stubAuthSessions struct{}

func (stubAuthSessions) CreateRefreshSession(context.Context, domain.NewRefreshSession) (int64, error) {
	return 1, nil
}

func (stubAuthSessions) GetRefreshSession(context.Context, int64) (*domain.RefreshToken, error) {
	return nil, domain.NewErrorRecordNotFound()
}

func (stubAuthSessions) RotateRefreshSession(context.Context, domain.RefreshRotation) (bool, error) {
	return false, nil
}

func (stubAuthSessions) DeleteRefreshSession(context.Context, int64) error {
	return nil
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
	auth   *usecase.Auth
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
	auth := usecase.NewAuth("test-secret", stubCredentialHasher{}, env.users, stubAuthSessions{})
	handler := NewHandler(env.oauth, auth)

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
		auth:   auth,
		env:    env,
		server: server,
		client: &http.Client{
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

func (s *oauthServer) do(t *testing.T, method, path, body string) (*http.Response, string) {
	t.Helper()
	return s.send(t, method, path, body, "")
}

func (s *oauthServer) doWithBinding(t *testing.T, method, path, binding string) (*http.Response, string) {
	t.Helper()
	return s.send(t, method, path, "", binding)
}

func (s *oauthServer) send(t *testing.T, method, path, body, binding string) (*http.Response, string) {
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
	if binding != "" {
		req.AddCookie(&http.Cookie{Name: bindingCookieName, Value: binding})
	}
	resp, err := s.client.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp, string(data)
}

func bindingCleared(resp *http.Response) bool {
	for _, cookie := range resp.Cookies() {
		if cookie.Name == bindingCookieName && cookie.MaxAge < 0 && cookie.Path == bindingCookiePath {
			return true
		}
	}
	return false
}

func (s *oauthServer) signedState(t *testing.T, redirect string, linkUser int64) string {
	t.Helper()

	state, err := s.env.oauth.states.Sign(redirect, linkUser, testBinding, "")
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
	resp, _ := server.doWithBinding(t, http.MethodGet, "/api/v1/auth/oauth/github/callback?code=abc&state="+state, testBinding)
	require.Equal(t, http.StatusFound, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Location"), "/oauth/callback?code=")
	require.False(t, bindingCleared(resp), "the binding cookie is kept until complete")
	for _, cookie := range resp.Cookies() {
		require.NotEqual(t, bindingCookieName, cookie.Name)
	}

	resp, _ = server.doWithBinding(t, http.MethodGet, "/api/v1/auth/oauth/github/callback?state="+state, testBinding)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode, "a missing code is rejected")
	require.True(t, bindingCleared(resp))

	resp, _ = server.doWithBinding(t, http.MethodGet, "/api/v1/auth/oauth/github/callback?code=abc&state=tampered", testBinding)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode, "a tampered state is rejected")
	require.True(t, bindingCleared(resp))

	server.env.provider.err = domain.NewErrorUser("github sign-in failed")
	resp, _ = server.doWithBinding(t, http.MethodGet, "/api/v1/auth/oauth/github/callback?code=abc&state="+state, testBinding)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode, "provider errors surface as client errors")
	require.True(t, bindingCleared(resp))

	resp, _ = server.do(t, http.MethodGet, "/api/v1/auth/oauth/google/callback", "")
	require.Equal(t, http.StatusNotFound, resp.StatusCode, "disabled providers reject callbacks")
	require.True(t, bindingCleared(resp))
}

func TestCallbackClearsTheBindingWithoutALoginCode(t *testing.T) {
	server := newOAuthHandlerServer(t, 42)
	server.env.users.users = []*domain.User{{ID: 42, Email: "alice@example.com", EmailVerified: true}}
	server.env.provider.profile = &Profile{AccountID: "1001", Email: "alice@example.com", EmailVerified: true}

	state := server.signedState(t, "/settings/profile", 42)
	resp, _ := server.doWithBinding(t, http.MethodGet, "/api/v1/auth/oauth/github/callback?code=abc&state="+state, testBinding)
	require.Equal(t, http.StatusFound, resp.StatusCode)
	require.Equal(t, "/settings/profile?oauth=linked", resp.Header.Get("Location"))
	require.True(t, bindingCleared(resp), "the link flow has no complete step")

	server.env.provider.profile = &Profile{AccountID: "2002"}
	state = server.signedState(t, "/login", 0)
	resp, _ = server.doWithBinding(t, http.MethodGet, "/api/v1/auth/oauth/github/callback?code=abc&state="+state, testBinding)
	require.Equal(t, http.StatusFound, resp.StatusCode)
	require.Equal(t, "/login?oauth_error=email_required", resp.Header.Get("Location"))
	require.True(t, bindingCleared(resp))
}

func TestOAuthLoginRoundTripNeedsTheStartingBrowser(t *testing.T) {
	server := newOAuthHandlerServer(t, 42)
	server.env.users.users = []*domain.User{{ID: 42, Email: "alice@example.com", EmailVerified: true}}
	server.env.provider.profile = &Profile{AccountID: "1001", Email: "alice@example.com", EmailVerified: true}

	login := func() string {
		resp, _ := server.do(t, http.MethodGet, "/api/v1/auth/oauth/github/start", "")
		require.Equal(t, http.StatusFound, resp.StatusCode)
		var binding string
		for _, cookie := range resp.Cookies() {
			if cookie.Name == bindingCookieName {
				binding = cookie.Value
			}
		}
		require.NotEmpty(t, binding)
		state := strings.TrimPrefix(resp.Header.Get("Location"), "https://github.example/authorize?state=")
		resp, _ = server.doWithBinding(t, http.MethodGet, "/api/v1/auth/oauth/github/callback?code=abc&state="+state, binding)
		require.Equal(t, http.StatusFound, resp.StatusCode)
		location, err := url.Parse(resp.Header.Get("Location"))
		require.NoError(t, err)
		code := location.Query().Get("code")
		require.NotEmpty(t, code)
		return code + "|" + binding
	}

	attacker := strings.Split(login(), "|")
	victim := strings.Split(login(), "|")

	resp, body := server.send(t, http.MethodPost, "/api/v1/auth/oauth/complete", `{"code":"`+attacker[0]+`"}`, victim[1])
	require.Equal(t, http.StatusNotFound, resp.StatusCode, "a code handed to another browser is refused: %s", body)
	require.True(t, bindingCleared(resp))

	resp, body = server.send(t, http.MethodPost, "/api/v1/auth/oauth/complete", `{"code":"`+attacker[0]+`"}`, attacker[1])
	require.Equal(t, http.StatusNotFound, resp.StatusCode, "the failed attempt consumed the code: %s", body)

	resp, body = server.send(t, http.MethodPost, "/api/v1/auth/oauth/complete", `{"code":"`+victim[0]+`"}`, victim[1])
	require.Equal(t, http.StatusOK, resp.StatusCode, body)
	require.True(t, bindingCleared(resp))
}

func TestCallbackRejectsAnotherBrowser(t *testing.T) {
	server := newOAuthHandlerServer(t, 42)
	server.env.provider.profile = &Profile{AccountID: "1001", Email: "alice@example.com", EmailVerified: true}
	server.env.users.users = []*domain.User{{ID: 42, Email: "alice@example.com", EmailVerified: true}}
	state := server.signedState(t, "/", 0)
	path := "/api/v1/auth/oauth/github/callback?code=abc&state=" + state

	resp, _ := server.do(t, http.MethodGet, path, "")
	require.Equal(t, http.StatusBadRequest, resp.StatusCode, "a browser without the binding cookie is refused")

	resp, _ = server.doWithBinding(t, http.MethodGet, path, "someone-elses-binding")
	require.Equal(t, http.StatusBadRequest, resp.StatusCode, "a different binding is refused")
	require.Empty(t, server.env.repo.identities)
}

func TestStartSetsTheBindingCookie(t *testing.T) {
	server := newOAuthHandlerServer(t, 42)

	resp, _ := server.do(t, http.MethodGet, "/api/v1/auth/oauth/github/start", "")
	require.Equal(t, http.StatusFound, resp.StatusCode)
	var binding *http.Cookie
	for _, cookie := range resp.Cookies() {
		if cookie.Name == bindingCookieName {
			binding = cookie
		}
	}
	require.NotNil(t, binding)
	require.True(t, binding.HttpOnly)
	require.True(t, binding.Secure)
	require.Equal(t, http.SameSiteLaxMode, binding.SameSite)

	state, err := server.env.oauth.states.Verify(strings.TrimPrefix(resp.Header.Get("Location"), "https://github.example/authorize?state="))
	require.NoError(t, err)
	require.True(t, state.bound(binding.Value))
	require.False(t, state.bound("other"))
}

func TestCompleteExchangesTheCodeForASession(t *testing.T) {
	server := newOAuthHandlerServer(t, 42)
	server.env.users.users = []*domain.User{{ID: 42, Email: "alice@example.com", EmailVerified: true}}
	server.env.repo.codes["code-1"] = codeRow{userID: 42, expiresAt: server.env.now.Add(time.Minute), binding: bindingHash(testBinding)}

	resp, body := server.send(t, http.MethodPost, "/api/v1/auth/oauth/complete", `{"code":"code-1"}`, testBinding)
	require.Equal(t, http.StatusOK, resp.StatusCode, body)
	require.True(t, bindingCleared(resp))
	var login struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &login))
	require.NotEmpty(t, login.AccessToken)
	require.Equal(t, "Bearer", login.TokenType)
	refresh := false
	for _, cookie := range resp.Cookies() {
		refresh = refresh || cookie.Name == "nipa_refresh"
	}
	require.True(t, refresh)

	resp, body = server.send(t, http.MethodPost, "/api/v1/auth/oauth/complete", `{"code":"code-1"}`, testBinding)
	require.Equal(t, http.StatusNotFound, resp.StatusCode, "the code is single use: %s", body)

	resp, body = server.do(t, http.MethodPost, "/api/v1/auth/oauth/complete", `{"code":"missing"}`)
	require.Equal(t, http.StatusNotFound, resp.StatusCode, body)
	require.True(t, bindingCleared(resp))

	server.env.repo.codes["code-2"] = codeRow{userID: 42, expiresAt: server.env.now.Add(time.Minute), binding: bindingHash(testBinding)}
	resp, body = server.do(t, http.MethodPost, "/api/v1/auth/oauth/complete", `{"code":"code-2"}`)
	require.Equal(t, http.StatusNotFound, resp.StatusCode, "a browser without the binding cookie is refused: %s", body)
	require.True(t, bindingCleared(resp))

	server.env.repo.codes["code-3"] = codeRow{userID: 42, expiresAt: server.env.now.Add(time.Minute), binding: bindingHash(testBinding)}
	resp, body = server.send(t, http.MethodPost, "/api/v1/auth/oauth/complete", `{"code":"code-3"}`, "someone-elses-binding")
	require.Equal(t, http.StatusNotFound, resp.StatusCode, "another browser's binding is refused: %s", body)

	server.env.repo.codes["legacy"] = codeRow{userID: 42, expiresAt: server.env.now.Add(time.Minute)}
	resp, body = server.send(t, http.MethodPost, "/api/v1/auth/oauth/complete", `{"code":"legacy"}`, testBinding)
	require.Equal(t, http.StatusNotFound, resp.StatusCode, "a code issued without a binding is refused: %s", body)

	resp, body = server.do(t, http.MethodPost, "/api/v1/auth/oauth/complete", `{"code":""}`)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode, body)

	resp, _ = server.do(t, http.MethodPost, "/api/v1/auth/oauth/complete", `{"code":`)
	require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

func TestCompleteRecordsOneOAuthLogin(t *testing.T) {
	server := newOAuthHandlerServer(t, 42)
	rec := &recordedAudit{}
	server.auth.WithAudit(rec)
	server.env.users.users = []*domain.User{{ID: 42, Email: "alice@example.com", EmailVerified: true}}
	server.env.repo.codes["code-1"] = codeRow{userID: 42, expiresAt: server.env.now.Add(time.Minute), binding: bindingHash(testBinding)}

	resp, body := server.send(t, http.MethodPost, "/api/v1/auth/oauth/complete", `{"code":"code-1"}`, testBinding)
	require.Equal(t, http.StatusOK, resp.StatusCode, body)
	require.Len(t, rec.entries, 1)
	require.Equal(t, domain.AuditAuthLoginSucceeded, rec.entries[0].Action)
	require.Equal(t, usecase.LoginMethodOAuth, rec.entries[0].Metadata["method"])
	require.Equal(t, snow.ID(42).Base36(), rec.entries[0].TargetID)
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

func TestCompleteAsksEnrolledAccountsForASecondFactor(t *testing.T) {
	server := newOAuthHandlerServer(t, 42)
	cipher, err := totp.NewCipher([]byte("0123456789abcdef0123456789abcdef"))
	require.NoError(t, err)
	repo := NewMockmfaRepository(gomock.NewController(t))
	server.auth.WithMFA(usecase.NewMFA(repo, server.env.users, stubCredentialHasher{}, cipher, nil))
	server.env.users.users = []*domain.User{{ID: 42, Email: "alice@example.com", EmailVerified: true}}
	server.env.repo.codes["code-1"] = codeRow{userID: 42, expiresAt: server.env.now.Add(time.Minute), binding: bindingHash(testBinding)}

	confirmed := time.Now()
	repo.EXPECT().GetTOTP(gomock.Any(), snow.ID(42)).Return(&domain.UserTOTP{UserID: 42, ConfirmedAt: &confirmed}, nil)
	repo.EXPECT().DeleteExpiredChallenges(gomock.Any(), gomock.Any()).Return(nil)
	repo.EXPECT().CreateChallenge(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, c domain.MFAChallenge) error {
		require.Equal(t, usecase.LoginMethodOAuth, c.Method)
		return nil
	})

	resp, body := server.send(t, http.MethodPost, "/api/v1/auth/oauth/complete", `{"code":"code-1"}`, testBinding)
	require.Equal(t, http.StatusOK, resp.StatusCode, body)
	var challenge struct {
		MFARequired  bool   `json:"mfa_required"`
		MFAChallenge string `json:"mfa_challenge"`
		AccessToken  string `json:"access_token"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &challenge))
	require.True(t, challenge.MFARequired)
	require.NotEmpty(t, challenge.MFAChallenge)
	require.Empty(t, challenge.AccessToken)
	for _, cookie := range resp.Cookies() {
		require.NotEqual(t, "nipa_refresh", cookie.Name)
	}
}
