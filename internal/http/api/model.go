package api

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/emicklei/go-restful/v3"
	"github.com/nipalab/nipa/internal/domain"
	httpApp "github.com/nipalab/nipa/internal/http"
	"github.com/nipalab/nipa/internal/http/model"
)

type appContext struct {
	req    *restful.Request
	resp   *restful.Response
	claims *domain.Claims
}

func newAppContext(req *restful.Request, resp *restful.Response) (httpApp.AppContext, error) {
	var claims *domain.Claims
	claimInterface, ok := req.Attribute(AttributeClaims).(*domain.Claims)
	if ok {
		claims = claimInterface
	}
	return &appContext{
		req:    req,
		resp:   resp,
		claims: claims,
	}, nil
}

func (a *appContext) Context() context.Context {
	return a.req.Request.Context()
}

func (a *appContext) Claims() *domain.Claims {
	return a.claims
}

func (a *appContext) ReadJson(v any) error {
	return a.req.ReadEntity(v)
}

func (a *appContext) WriteJson(statusCode int, v any) error {
	err := a.resp.WriteHeaderAndJson(statusCode, v, restful.MIME_JSON)
	if err != nil {
		return domain.NewErrorUser(err.Error())
	}
	return nil
}

func (a *appContext) SetCookie(cookie *http.Cookie) {
	http.SetCookie(a.resp, cookie)
}

func (a *appContext) Cookie(name string) (*http.Cookie, error) {
	return a.req.Request.Cookie(name)
}

func (a *appContext) HandleError(err error) {
	apiErr, ok := err.(*domain.Error)
	if !ok {
		slog.Error("error unknown", "error", err)
		a.resp.WriteHeaderAndJson(http.StatusInternalServerError, model.NewAPIError("internal unknown error"), restful.MIME_JSON)
	} else {
		a.resp.WriteHeaderAndJson(apiErr.Code, model.NewAPIError(apiErr.Message), restful.MIME_JSON)
	}
}
