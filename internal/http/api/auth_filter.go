package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/emicklei/go-restful/v3"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/http/model"
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
}

func NewAuthFilter(tv tokenValidator) *AuthFilter {
	return &AuthFilter{
		tokenValidator: tv,
	}
}

func (f *AuthFilter) Auth() restful.FilterFunction {
	return func(req *restful.Request, resp *restful.Response, chain *restful.FilterChain) {
		tokenString, err := f.getToken(req)
		if err != nil || tokenString == "" {
			resp.WriteHeaderAndJson(http.StatusUnauthorized, model.NewAPIError("unauthorized access"), restful.MIME_JSON)
			return
		}
		claim, er := f.tokenValidator.ValidateToken(req.Request.Context(), tokenString)
		if er != nil {
			resp.WriteHeaderAndJson(http.StatusUnauthorized, model.NewAPIError("unauthorized access, token not valid"), restful.MIME_JSON)
			return
		}
		ctx := domain.ContextWithClaim(req.Request.Context(), *claim)
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
