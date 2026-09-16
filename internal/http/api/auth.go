package api

import (
	"net/http"

	"github.com/nipalab/nipa/internal/http/handler"
	"github.com/nipalab/nipa/internal/http/model"

	restfulspec "github.com/emicklei/go-restful-openapi/v2"
	"github.com/emicklei/go-restful/v3"
)

func setupAuthRouter(ws *restful.WebService, h *handler.Handler) {
	tags := []string{"Auth"}

	ws.Route(
		ws.POST("/").
			To(wrap(h.AuthLogin)).
			Reads(model.LoginRequest{}).
			Doc("Login with email and password").
			Notes("Login with email and password").
			Returns(http.StatusOK, "login status", model.LoginResponse{}).
			Operation("loginUsernamePassword").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.POST("/refresh").
			To(wrap(h.AuthRefreshToken)).
			Reads(model.RefreshTokenRequest{}).
			Doc("Exchange a refresh token for a new access token pair").
			Notes("Exchange a refresh token for a new access token pair").
			Returns(http.StatusOK, "new tokens", model.LoginResponse{}).
			Operation("loginRefreshToken").
			Metadata(restfulspec.KeyOpenAPITags, tags))
}
