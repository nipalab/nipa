package api

import (
	"net/http"

	"github.com/nipalab/nipa/internal/http/handler"
	"github.com/nipalab/nipa/internal/http/model"

	restfulspec "github.com/emicklei/go-restful-openapi/v2"
	"github.com/emicklei/go-restful/v3"
)

func setupPermissionRouter(ws *restful.WebService, h *handler.Handler) {
	tags := []string{"Permissions"}

	ws.Route(
		ws.GET("/orgs/{org}/projects/{project}/permissions/me").
			To(wrap(h.MyProjectPermissions)).
			Param(ws.PathParameter("org", "organization slug")).
			Param(ws.PathParameter("project", "project slug")).
			Doc("Get the caller's effective permissions on a project").
			Returns(http.StatusOK, "effective permissions", model.ProjectPermissionResponse{}).
			Operation("getMyProjectPermissions").
			Metadata(restfulspec.KeyOpenAPITags, tags))
}
