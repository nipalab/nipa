package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/emicklei/go-restful/v3"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/stretchr/testify/require"
)

type stubTokenValidator struct {
	claims *domain.Claims
	err    error
}

func (s *stubTokenValidator) ValidateToken(_ context.Context, _ string) (*domain.Claims, error) {
	return s.claims, s.err
}

func runFilter(t *testing.T, filter restful.FilterFunction, req *restful.Request) *restful.Response {
	t.Helper()

	resp := restful.NewResponse(httptest.NewRecorder())
	chain := &restful.FilterChain{
		Filters: []restful.FilterFunction{
			func(_ *restful.Request, _ *restful.Response, _ *restful.FilterChain) {},
		},
	}
	filter(req, resp, chain)
	return resp
}

func TestAuthFilter_MissingAuthorizationHeader(t *testing.T) {
	filter := NewAuthFilter(&stubTokenValidator{}).Auth()

	req := restful.NewRequest(httptest.NewRequest(http.MethodGet, "/protected", nil))
	resp := runFilter(t, filter, req)

	require.Equal(t, http.StatusUnauthorized, resp.StatusCode())
}

func TestAuthFilter_WrongAuthorizationFormat(t *testing.T) {
	filter := NewAuthFilter(&stubTokenValidator{}).Auth()

	for _, header := range []string{"Bearer", "Bearer a b", "token123"} {
		req := httptest.NewRequest(http.MethodGet, "/protected", nil)
		req.Header.Set("Authorization", header)
		resp := runFilter(t, filter, restful.NewRequest(req))

		require.Equal(t, http.StatusUnauthorized, resp.StatusCode(), "header: %q", header)
	}
}

func TestAuthFilter_InvalidToken(t *testing.T) {
	filter := NewAuthFilter(&stubTokenValidator{err: domain.NewErrorUser("invalid token")}).Auth()

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer bad-token")
	resp := runFilter(t, filter, restful.NewRequest(req))

	require.Equal(t, http.StatusUnauthorized, resp.StatusCode())
}

func TestAuthFilter_ValidTokenSetsAttributesAndContinuesChain(t *testing.T) {
	wantClaims := &domain.Claims{
		RegisteredClaims: registeredClaimsForSubject("42"),
		UserID:           42,
	}
	filter := NewAuthFilter(&stubTokenValidator{claims: wantClaims}).Auth()

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer valid-token")

	var gotClaims *domain.Claims
	var gotToken string
	var gotContextClaims *domain.Claims
	chainProcessed := false

	resp := restful.NewResponse(httptest.NewRecorder())
	chain := &restful.FilterChain{
		Filters: []restful.FilterFunction{
			func(r *restful.Request, _ *restful.Response, _ *restful.FilterChain) {
				chainProcessed = true
				gotClaims, _ = r.Attribute(AttributeClaims).(*domain.Claims)
				gotToken, _ = r.Attribute(AttributeToken).(string)
				if claims, ok := domain.ClaimFromContext(r.Request.Context()); ok {
					gotContextClaims = &claims
				}
			},
		},
	}
	filter(restful.NewRequest(req), resp, chain)

	require.True(t, chainProcessed)
	require.Same(t, wantClaims, gotClaims)
	require.Equal(t, "valid-token", gotToken)
	require.NotNil(t, gotContextClaims, "usecases read claims from the request context")
	require.Equal(t, snow.ID(42), gotContextClaims.UserID)
	require.Equal(t, http.StatusOK, resp.StatusCode())
}
