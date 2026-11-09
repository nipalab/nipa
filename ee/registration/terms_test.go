package registration

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/emicklei/go-restful/v3"
	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/http/api"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

const testTermsVersion = "2026-11-10"

func newTermsEnv(t *testing.T) *registrationEnv {
	t.Helper()
	env := newRegistrationEnv(t)
	env.registration.cfg.TermsVersion = testTermsVersion
	return env
}

func requireDomainCode(t *testing.T, err error, code int) {
	t.Helper()
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, code, domErr.Code)
}

func TestRegisterRequiresTermsAcceptance(t *testing.T) {
	env := newTermsEnv(t)

	err := env.registration.Register(context.Background(), "Alice", "alice@example.com", false)
	requireDomainCode(t, err, http.StatusBadRequest)
	require.Empty(t, env.repo.created)
	require.Empty(t, env.outbox.deliveries)
}

func TestRegisterRecordsTermsAcceptance(t *testing.T) {
	env := newTermsEnv(t)
	rec := &recordedAudit{}
	env.registration.WithAudit(rec)

	require.NoError(t, env.registration.Register(context.Background(), "Alice", "alice@example.com", true))
	require.Len(t, env.repo.created, 1)
	require.Equal(t, []domain.TermsAcceptance{{
		UserID: env.repo.created[0].ID, Version: testTermsVersion, Source: TermsSourceRegistration, AcceptedAt: env.now,
	}}, env.repo.terms)
	require.Equal(t, testTermsVersion, rec.entries[0].Metadata["terms_version"])
}

func TestRegisterTermsWriteFailureAbortsTheSignup(t *testing.T) {
	env := newTermsEnv(t)
	env.repo.termsErr = errors.New("terms down")

	require.EqualError(t, env.registration.Register(context.Background(), "Alice", "alice@example.com", true), "terms down")
	require.Empty(t, env.outbox.deliveries)
}

func TestRegisterWithoutTermsVersionSkipsAcceptance(t *testing.T) {
	env := newRegistrationEnv(t)
	rec := &recordedAudit{}
	env.registration.WithAudit(rec)

	require.NoError(t, env.registration.Register(context.Background(), "Alice", "alice@example.com", false))
	require.Empty(t, env.repo.terms)
	require.NotContains(t, rec.entries[0].Metadata, "terms_version")
}

func TestTermsStatus(t *testing.T) {
	ctx := context.Background()

	disabled := newRegistrationEnv(t)
	status, err := disabled.registration.TermsStatus(ctx, 7)
	require.NoError(t, err)
	require.Equal(t, &TermsStatus{}, status)

	env := newTermsEnv(t)
	status, err = env.registration.TermsStatus(ctx, 7)
	require.NoError(t, err)
	require.Equal(t, &TermsStatus{Version: testTermsVersion, Required: true}, status)

	require.NoError(t, env.registration.AcceptTerms(ctx, 7, testTermsVersion))
	status, err = env.registration.TermsStatus(ctx, 7)
	require.NoError(t, err)
	require.False(t, status.Required)
	require.Equal(t, testTermsVersion, status.Accepted.Version)
	require.Equal(t, TermsSourceApp, status.Accepted.Source)

	env.repo.latestTermsErr = errors.New("lookup failed")
	_, err = env.registration.TermsStatus(ctx, 7)
	require.EqualError(t, err, "lookup failed")
}

func TestAcceptTerms(t *testing.T) {
	ctx := context.Background()

	disabled := newRegistrationEnv(t)
	requireDomainCode(t, disabled.registration.AcceptTerms(ctx, 7, testTermsVersion), http.StatusNotFound)

	env := newTermsEnv(t)
	requireDomainCode(t, env.registration.AcceptTerms(ctx, 7, "2020-01-01"), http.StatusConflict)
	require.Empty(t, env.repo.terms)

	rec := &recordedAudit{}
	env.registration.WithAudit(rec)
	require.NoError(t, env.registration.AcceptTerms(ctx, 7, testTermsVersion))
	require.Len(t, env.repo.terms, 1)
	require.Len(t, rec.entries, 1)
	entry := rec.entries[0]
	require.Equal(t, domain.AuditUserTermsAccepted, entry.Action)
	require.Equal(t, snow.ID(7).Base36(), entry.TargetID)
	require.Equal(t, testTermsVersion, entry.Metadata["version"])

	env.registration.WithAudit(&recordedAudit{err: errors.New("audit down")})
	require.EqualError(t, env.registration.AcceptTerms(ctx, 7, testTermsVersion), "audit down")

	env.repo.termsErr = errors.New("terms down")
	require.EqualError(t, env.registration.AcceptTerms(ctx, 7, testTermsVersion), "terms down")
}

func newTermsServer(t *testing.T, env *registrationEnv, userID snow.ID) *registrationServer {
	t.Helper()

	handler := NewHandler(env.registration, usecase.NewAuth("test-secret", stubHasher{}, env.users, stubSessionStore{}))
	publicWs := new(restful.WebService)
	publicWs.Path("/api/v1/auth").Consumes(restful.MIME_JSON).Produces(restful.MIME_JSON)
	RegisterPublicRoutes(publicWs, handler)

	authedWs := new(restful.WebService)
	authedWs.Path("/api/v1").
		Filter(func(req *restful.Request, resp *restful.Response, chain *restful.FilterChain) {
			if userID != 0 {
				req.SetAttribute(api.AttributeClaims, &domain.Claims{UserID: userID})
			}
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
	return &registrationServer{env: env, server: server}
}

func TestCapabilitiesReportsTerms(t *testing.T) {
	server := newTermsServer(t, newTermsEnv(t), 7)

	resp, body := server.do(t, http.MethodGet, "/api/v1/auth/capabilities", "")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.JSONEq(t, `{"registration": true, "providers": [], "terms": {"version": "2026-11-10"}}`, body)
}

func TestRegisterEndpointRequiresTerms(t *testing.T) {
	server := newTermsServer(t, newTermsEnv(t), 7)

	resp, body := server.do(t, http.MethodPost, "/api/v1/auth/register", `{"name":"Alice","email":"alice@example.com"}`)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode, body)
	require.Contains(t, body, "terms of service")

	resp, body = server.do(t, http.MethodPost, "/api/v1/auth/register",
		`{"name":"Alice","email":"alice@example.com","accept_terms":true}`)
	require.Equal(t, http.StatusAccepted, resp.StatusCode, body)
	require.Len(t, server.env.repo.terms, 1)
}

func TestTermsEndpoints(t *testing.T) {
	env := newTermsEnv(t)
	server := newTermsServer(t, env, 7)

	resp, body := server.do(t, http.MethodGet, "/api/v1/me/terms", "")
	require.Equal(t, http.StatusOK, resp.StatusCode, body)
	require.JSONEq(t, `{"version": "2026-11-10", "required": true}`, body)

	resp, body = server.do(t, http.MethodPost, "/api/v1/me/terms", `{"version":"2020-01-01"}`)
	require.Equal(t, http.StatusConflict, resp.StatusCode, body)

	resp, body = server.do(t, http.MethodPost, "/api/v1/me/terms", `{"version":" 2026-11-10 "}`)
	require.Equal(t, http.StatusOK, resp.StatusCode, body)
	require.JSONEq(t, `{"version": "2026-11-10", "accepted_version": "2026-11-10",
		"accepted_at": "2026-07-01T12:00:00Z", "required": false}`, body)

	resp, _ = server.do(t, http.MethodPost, "/api/v1/me/terms", `{"version":`)
	require.Equal(t, http.StatusInternalServerError, resp.StatusCode)

	env.repo.latestTermsErr = errors.New("lookup failed")
	resp, _ = server.do(t, http.MethodGet, "/api/v1/me/terms", "")
	require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

func TestTermsEndpointsRequireAuthentication(t *testing.T) {
	server := newTermsServer(t, newTermsEnv(t), 0)

	resp, _ := server.do(t, http.MethodGet, "/api/v1/me/terms", "")
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	resp, _ = server.do(t, http.MethodPost, "/api/v1/me/terms", `{"version":"2026-11-10"}`)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}
