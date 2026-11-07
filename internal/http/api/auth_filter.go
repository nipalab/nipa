package api

import (
	"context"
	"errors"
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

type AuthFilter struct {
	tokenValidator tokenValidator
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

func (f *AuthFilter) Auth() restful.FilterFunction {
	return func(req *restful.Request, resp *restful.Response, chain *restful.FilterChain) {
		tokenString, err := f.getToken(req)
		if err != nil || tokenString == "" {
			_ = resp.WriteHeaderAndJson(http.StatusUnauthorized, model.NewAPIError("unauthorized access"), restful.MIME_JSON)
			return
		}
		claim, er := f.tokenValidator.ValidateToken(req.Request.Context(), tokenString)
		if er != nil {
			_ = resp.WriteHeaderAndJson(http.StatusUnauthorized, model.NewAPIError("unauthorized access, token not valid"), restful.MIME_JSON)
			return
		}
		ctx := domain.ContextWithClaim(req.Request.Context(), *claim)
		ctx = domain.ContextWithRequestMeta(ctx, RequestMeta(req.Request, f.proxies))
		req.Request = req.Request.WithContext(ctx)
		req.SetAttribute(AttributeClaims, claim)
		req.SetAttribute(AttributeToken, tokenString)
		chain.ProcessFilter(req, resp)
	}
}

func (f *AuthFilter) getToken(req *restful.Request) (string, error) {
	bearerToken := req.HeaderParameter("Authorization")
	if bearerToken == "" {
		return "", errors.New("auth token not found")
	}
	tokens := strings.Split(bearerToken, " ")
	if len(tokens) != 2 {
		return "", errors.New("auth token wrong format")
	}
	return tokens[1], nil
}

// RequestMeta resolves the caller's client IP and user agent.
func RequestMeta(r *http.Request, proxies []netip.Prefix) domain.RequestMeta {
	return domain.RequestMeta{
		ClientIP:  ratelimit.ClientIP(r.RemoteAddr, r.Header.Get("X-Forwarded-For"), proxies),
		UserAgent: r.UserAgent(),
	}
}
