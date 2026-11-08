package api

import (
	"net/http"

	"github.com/nipalab/nipa/internal/http/handler"
	"github.com/nipalab/nipa/internal/http/model"

	restfulspec "github.com/emicklei/go-restful-openapi/v2"
	"github.com/emicklei/go-restful/v3"
)

func setupServiceTokenRouter(ws *restful.WebService, h *handler.Handler) {
	tags := []string{"Service tokens"}

	ws.Route(
		ws.GET("/orgs/{org}/projects/{project}/service-tokens").
			To(Wrap(h.ListServiceTokens)).
			Param(ws.PathParameter("org", "organization slug")).
			Param(ws.PathParameter("project", "project slug")).
			Doc("List project service tokens, including revoked ones (project admin)").
			Returns(http.StatusOK, "service tokens", []model.ServiceTokenResponse{}).
			Operation("listServiceTokens").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.POST("/orgs/{org}/projects/{project}/service-tokens").
			To(Wrap(h.CreateServiceToken)).
			Param(ws.PathParameter("org", "organization slug")).
			Param(ws.PathParameter("project", "project slug")).
			Reads(model.CreateServiceTokenRequest{}).
			Doc("Create a service token (scopes: read, checks:write, webhooks:write); the token is returned once (project admin)").
			Returns(http.StatusOK, "created service token", model.ServiceTokenResponse{}).
			Operation("createServiceToken").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.DELETE("/orgs/{org}/projects/{project}/service-tokens/{id}").
			To(Wrap(h.RevokeServiceToken)).
			Param(ws.PathParameter("org", "organization slug")).
			Param(ws.PathParameter("project", "project slug")).
			Param(ws.PathParameter("id", "service token id")).
			Doc("Revoke a service token (project admin)").
			Returns(http.StatusOK, "revoked service token", model.ServiceTokenResponse{}).
			Operation("revokeServiceToken").
			Metadata(restfulspec.KeyOpenAPITags, tags))
}
