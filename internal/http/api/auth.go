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
		ws.POST("/login").
			To(wrap(h.AuthLogin)).
			Reads(model.LoginRequest{}).
			Doc("Login with email and password").
			Notes("Returns a short-lived access token and sets an HttpOnly refresh cookie").
			Returns(http.StatusOK, "access token", model.LoginResponse{}).
			Operation("loginUsernamePassword").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.POST("/refresh").
			To(wrap(h.AuthRefreshToken)).
			Doc("Exchange the refresh cookie for a new access token").
			Notes("Rotates the refresh cookie; the previous refresh token is invalidated").
			Returns(http.StatusOK, "access token", model.LoginResponse{}).
			Operation("loginRefreshToken").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.POST("/logout").
			To(wrap(h.AuthLogout)).
			Doc("Revoke the refresh token and clear the cookie").
			Returns(http.StatusOK, "logged out", model.MessageResponse{}).
			Operation("logout").
			Metadata(restfulspec.KeyOpenAPITags, tags))
}
