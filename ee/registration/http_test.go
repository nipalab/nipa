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
	"github.com/nipalab/nipa/internal/ratelimit"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

// stubSessionStore satisfies the auth repository surface usecase.Auth needs;
// only CreateRefreshSession is exercised by the login-result path.
type stubSessionStore struct{}

func (stubSessionStore) CreateRefreshSession(context.Context, domain.NewRefreshSession) (int64, error) {
	return 1, nil
}

func (stubSessionStore) GetRefreshSession(context.Context, int64) (*domain.RefreshToken, error) {
	return nil, domain.NewErrorRecordNotFound()
}

func (stubSessionStore) RotateRefreshSession(context.Context, domain.RefreshRotation) (bool, error) {
	return false, nil
}

func (stubSessionStore) DeleteRefreshSession(context.Context, int64) error {
	return nil
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
	auth   *usecase.Auth
	env    *registrationEnv
	server *httptest.Server
}

func newRegistrationServer(t *testing.T, providers func() []string) *registrationServer {
	t.Helper()
	return newRegistrationServerWith(t, providers, nil)
}

func newRegistrationServerWith(t *testing.T, providers func() []string, billing *BillingCapabilities) *registrationServer {
	t.Helper()

	env := newRegistrationEnv(t)
	auth := usecase.NewAuth("test-secret", stubHasher{}, env.users, stubSessionStore{})
	handler := NewHandler(env.registration, auth)
	if providers != nil {
		handler = handler.WithProviders(providers)
	}
	if billing != nil {
		handler = handler.WithBilling(billing)
	}

	ws := new(restful.WebService)
	ws.Path("/api/v1/auth").Consumes(restful.MIME_JSON).Produces(restful.MIME_JSON)
	RegisterPublicRoutes(ws, handler)

	container := restful.NewContainer()
	container.Add(ws)
	server := httptest.NewServer(container)
	t.Cleanup(server.Close)
	return &registrationServer{auth: auth, env: env, server: server}
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

func TestCapabilitiesReportsBilling(t *testing.T) {
	server := newRegistrationServerWith(t, nil, &BillingCapabilities{
		Provider: "paddle", Environment: "sandbox", ClientToken: "test_token",
		Prices: BillingPrices{
			Seat:    IntervalPrices{Monthly: "pri_sm", Yearly: "pri_sy"},
			Storage: IntervalPrices{Monthly: "pri_stm", Yearly: "pri_sty"},
		},
		FreeSeats: 3, FreeQuotaBytes: 10, SeatIncludedBytes: 50, StoragePackBytes: 100,
	})
	resp, body := server.do(t, http.MethodGet, "/api/v1/auth/capabilities", "")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.JSONEq(t, `{"registration": true, "providers": [], "billing": {
		"provider": "paddle", "environment": "sandbox", "client_token": "test_token",
		"prices": {"seat": {"monthly": "pri_sm", "yearly": "pri_sy"}, "storage": {"monthly": "pri_stm", "yearly": "pri_sty"}},
		"free_seats": 3, "free_quota_bytes": 10, "seat_included_bytes": 50, "storage_pack_bytes": 100}}`, body)
}

func TestRegisterEndpoint(t *testing.T) {
	server := newRegistrationServer(t, nil)

	resp, body := server.do(t, http.MethodPost, "/api/v1/auth/register",
		`{"name":"Alice","email":" alice@example.com "}`)
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	require.JSONEq(t, `{"status":"pending_verification","email":"alice@example.com"}`, body)

	require.Len(t, server.env.repo.created, 1)
	require.Equal(t, "alice@example.com", server.env.repo.created[0].Email)
	require.Len(t, server.env.outbox.deliveries, 1)
	created := server.env.repo.created[0]
	server.env.users.users = append(server.env.users.users, &created)

	resp, body = server.do(t, http.MethodPost, "/api/v1/auth/register",
		`{"name":"Alice","email":"alice@example.com"}`)
	require.Equal(t, http.StatusAccepted, resp.StatusCode, body)

	resp, body = server.do(t, http.MethodPost, "/api/v1/auth/register",
		`{"name":"","email":"bob@example.com"}`)
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

func TestRegisterAndResendEndpointsAnswer429WhenThrottled(t *testing.T) {
	server := newRegistrationServer(t, nil)
	server.env.registration.WithLimiter(ratelimit.NewLimiter(ratelimit.NewMemory(), map[string]ratelimit.Rule{
		ratelimit.ScopeRegisterEmail: {Rate: 0.0001, Burst: 1},
	}))

	resp, body := server.do(t, http.MethodPost, "/api/v1/auth/register/resend", `{"email":"nobody@example.com"}`)
	require.Equal(t, http.StatusOK, resp.StatusCode, body)

	resp, body = server.do(t, http.MethodPost, "/api/v1/auth/register/resend", `{"email":"nobody@example.com"}`)
	require.Equal(t, http.StatusTooManyRequests, resp.StatusCode, body)
	require.Contains(t, body, "rate limit exceeded")

	resp, body = server.do(t, http.MethodPost, "/api/v1/auth/register", `{"name":"Nobody","email":"nobody@example.com"}`)
	require.Equal(t, http.StatusTooManyRequests, resp.StatusCode, body)
	require.Empty(t, server.env.outbox.deliveries)
}

func TestVerifyEndpoint(t *testing.T) {
	server := newRegistrationServer(t, nil)

	resp, body := server.do(t, http.MethodPost, "/api/v1/auth/verify", `{"token":"unknown","password":"password123"}`)
	require.Equal(t, http.StatusNotFound, resp.StatusCode, body)

	resp, body = server.do(t, http.MethodPost, "/api/v1/auth/verify", `{"token":"","password":"password123"}`)
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

	resp, body = server.do(t, http.MethodPost, "/api/v1/auth/verify", `{"token":"`+rawToken+`","password":"short"}`)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode, "a weak password is refused and the link stays usable: %s", body)

	resp, body = server.do(t, http.MethodPost, "/api/v1/auth/verify", `{"token":"`+rawToken+`","password":"password123"}`)
	require.Equal(t, http.StatusOK, resp.StatusCode, body)
	require.Equal(t, "hash:password123", server.env.repo.passwords[42])

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

func TestVerifyEndpointRecordsOneLogin(t *testing.T) {
	server := newRegistrationServer(t, nil)
	rec := &recordedAudit{}
	server.auth.WithAudit(rec)
	server.env.users.users = []*domain.User{{ID: 42, Name: "Alice", Email: "alice@example.com"}}
	server.env.repo.verifications[42] = &verificationRow{userID: 42, tokenHash: hashToken("token-42"), expiresAt: server.env.now.Add(time.Hour)}

	resp, body := server.do(t, http.MethodPost, "/api/v1/auth/verify", `{"token":"token-42","password":"password123"}`)
	require.Equal(t, http.StatusOK, resp.StatusCode, body)
	require.Len(t, rec.entries, 1)
	require.Equal(t, domain.AuditAuthLoginSucceeded, rec.entries[0].Action)
	require.Equal(t, usecase.LoginMethodEmailVerification, rec.entries[0].Metadata["method"])
	require.Equal(t, domain.AuditActorUser, rec.entries[0].ActorType)
}

func TestVerifyEndpointRejectsOverlongPassword(t *testing.T) {
	server := newRegistrationServer(t, nil)
	server.env.repo.verifications[42] = &verificationRow{userID: 42, tokenHash: hashToken("token-42"), expiresAt: server.env.now.Add(time.Hour)}

	resp, body := server.do(t, http.MethodPost, "/api/v1/auth/verify", `{"token":"token-42","password":"`+strings.Repeat("é", 37)+`"}`)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode, body)
	require.Contains(t, body, "at most 72 bytes")
	require.Contains(t, server.env.repo.verifications, snow.ID(42), "the link stays usable")
}
