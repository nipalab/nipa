package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"

	"github.com/emicklei/go-restful/v3"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/http/model"
	"github.com/nipalab/nipa/internal/ratelimit"
)

const (
	AttributeClaims = "claims"
	AttributeToken  = "token"
)

type tokenValidator interface {
	ValidateToken(ctx context.Context, tokenString string) (*domain.Claims, error)
}

type serviceTokenAuthenticator interface {
	Authenticate(ctx context.Context, plaintext, orgSlug, projectSlug, scope string) (*domain.Claims, error)
}

type AuthFilter struct {
	tokenValidator tokenValidator
	serviceTokens  serviceTokenAuthenticator
	proxies        []netip.Prefix
}

func NewAuthFilter(tv tokenValidator) *AuthFilter {
	return &AuthFilter{
		tokenValidator: tv,
	}
}

// WithTrustedProxies sets the proxies whose X-Forwarded-For is honoured for
// the client IP attached to the request.
func (f *AuthFilter) WithTrustedProxies(proxies []netip.Prefix) *AuthFilter {
	f.proxies = proxies
	return f
}

// WithServiceTokens accepts project service tokens on the routes listed in
// serviceTokenRoutes.
func (f *AuthFilter) WithServiceTokens(st serviceTokenAuthenticator) *AuthFilter {
	f.serviceTokens = st
	return f
}

func (f *AuthFilter) Auth() restful.FilterFunction {
	return func(req *restful.Request, resp *restful.Response, chain *restful.FilterChain) {
		tokenString, err := f.getToken(req)
		if err != nil || tokenString == "" {
			_ = resp.WriteHeaderAndJson(http.StatusUnauthorized, model.NewAPIError("unauthorized access"), restful.MIME_JSON)
			return
		}
		var claim *domain.Claims
		if strings.HasPrefix(tokenString, domain.ServiceTokenPrefix) {
			claim, err = f.serviceTokenClaims(req, tokenString)
			if err != nil {
				status, message := http.StatusUnauthorized, "unauthorized access, token not valid"
				var domErr *domain.Error
				if errors.As(err, &domErr) && domErr.Code == http.StatusForbidden {
					status, message = http.StatusForbidden, domErr.Message
				} else if !errors.As(err, &domErr) || domErr.Code >= http.StatusInternalServerError {
					status, message = http.StatusInternalServerError, "internal server error"
				}
				_ = resp.WriteHeaderAndJson(status, model.NewAPIError(message), restful.MIME_JSON)
				return
			}
		} else {
			var er error
			claim, er = f.tokenValidator.ValidateToken(req.Request.Context(), tokenString)
			if er != nil {
				if domain.IsServerError(er) {
					slog.Error("token validation failed", "error", er)
					_ = resp.WriteHeaderAndJson(http.StatusInternalServerError, model.NewAPIError("internal server error"), restful.MIME_JSON)
					return
				}
				_ = resp.WriteHeaderAndJson(http.StatusUnauthorized, model.NewAPIError("unauthorized access, token not valid"), restful.MIME_JSON)
				return
			}
		}
		ctx := domain.ContextWithClaim(req.Request.Context(), *claim)
		ctx = domain.ContextWithRequestMeta(ctx, RequestMeta(req.Request, f.proxies))
		req.Request = req.Request.WithContext(ctx)
		req.SetAttribute(AttributeClaims, claim)
		req.SetAttribute(AttributeToken, tokenString)
		chain.ProcessFilter(req, resp)
	}
}

// serviceTokenRoutes maps the routes a project service token may call to the
// scope each one needs. Every other route rejects service tokens.
var serviceTokenRoutes = map[string]string{
	"GET /api/v1/orgs/{org}/projects/{project}":                                                  domain.ServiceTokenScopeRead,
	"GET /api/v1/orgs/{org}/projects/{project}/branches":                                         domain.ServiceTokenScopeRead,
	"GET /api/v1/orgs/{org}/projects/{project}/commits":                                          domain.ServiceTokenScopeRead,
	"GET /api/v1/orgs/{org}/projects/{project}/commits/{commit}/diff":                            domain.ServiceTokenScopeRead,
	"GET /api/v1/orgs/{org}/projects/{project}/commits/{commit}/contains":                        domain.ServiceTokenScopeRead,
	"GET /api/v1/orgs/{org}/projects/{project}/tags":                                             domain.ServiceTokenScopeRead,
	"GET /api/v1/orgs/{org}/projects/{project}/tags/{name}":                                      domain.ServiceTokenScopeRead,
	"GET /api/v1/orgs/{org}/projects/{project}/merge-requests":                                   domain.ServiceTokenScopeRead,
	"GET /api/v1/orgs/{org}/projects/{project}/merge-requests/{id}":                              domain.ServiceTokenScopeRead,
	"GET /api/v1/orgs/{org}/projects/{project}/merge-requests/{id}/commits":                      domain.ServiceTokenScopeRead,
	"GET /api/v1/orgs/{org}/projects/{project}/merge-requests/{id}/checks":                       domain.ServiceTokenScopeRead,
	"POST /api/v1/orgs/{org}/projects/{project}/merge-requests/{id}/checks":                      domain.ServiceTokenScopeChecksWrite,
	"GET /api/v1/orgs/{org}/projects/{project}/webhooks":                                         domain.ServiceTokenScopeWebhooksWrite,
	"POST /api/v1/orgs/{org}/projects/{project}/webhooks":                                        domain.ServiceTokenScopeWebhooksWrite,
	"GET /api/v1/orgs/{org}/projects/{project}/webhooks/{id}":                                    domain.ServiceTokenScopeWebhooksWrite,
	"PATCH /api/v1/orgs/{org}/projects/{project}/webhooks/{id}":                                  domain.ServiceTokenScopeWebhooksWrite,
	"DELETE /api/v1/orgs/{org}/projects/{project}/webhooks/{id}":                                 domain.ServiceTokenScopeWebhooksWrite,
	"POST /api/v1/orgs/{org}/projects/{project}/webhooks/{id}/test":                              domain.ServiceTokenScopeWebhooksWrite,
	"GET /api/v1/orgs/{org}/projects/{project}/webhooks/{id}/deliveries":                         domain.ServiceTokenScopeWebhooksWrite,
	"POST /api/v1/orgs/{org}/projects/{project}/webhooks/{id}/deliveries/{deliveryId}/redeliver": domain.ServiceTokenScopeWebhooksWrite,
}

func (f *AuthFilter) serviceTokenClaims(req *restful.Request, token string) (*domain.Claims, error) {
	if f.serviceTokens == nil {
		return nil, domain.NewErrorUnauthorized("service tokens are not accepted")
	}
	scope, ok := serviceTokenRoutes[req.Request.Method+" "+req.SelectedRoutePath()]
	if !ok {
		return nil, domain.NewErrorForbidden("service tokens cannot call this endpoint")
	}
	return f.serviceTokens.Authenticate(req.Request.Context(), token, req.PathParameter("org"), req.PathParameter("project"), scope)
}

func (f *AuthFilter) getToken(req *restful.Request) (string, error) {
	bearerToken := req.HeaderParameter("Authorization")
	if bearerToken == "" {
		return "", errors.New("auth token not found")
	}
	scheme, token, ok := strings.Cut(bearerToken, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" || strings.Contains(token, " ") {
		return "", errors.New("auth token wrong format")
	}
	return token, nil
}

// RequestMeta resolves the caller's client IP and user agent.
func RequestMeta(r *http.Request, proxies []netip.Prefix) domain.RequestMeta {
	return domain.RequestMeta{
		ClientIP:  ratelimit.ClientIP(r.RemoteAddr, r.Header.Get("X-Forwarded-For"), proxies),
		UserAgent: r.UserAgent(),
	}
}
