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

	ws.Route(
		ws.GET("/orgs/{org}/projects/{project}/permissions/rules").
			To(wrap(h.ListProjectRules)).
			Param(ws.PathParameter("org", "organization slug")).
			Param(ws.PathParameter("project", "project slug")).
			Doc("List project access rules (project admin)").
			Returns(http.StatusOK, "rules", []model.PBACRuleResponse{}).
			Operation("listProjectRules").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.POST("/orgs/{org}/projects/{project}/permissions/rules").
			To(wrap(h.CreateProjectRule)).
			Param(ws.PathParameter("org", "organization slug")).
			Param(ws.PathParameter("project", "project slug")).
			Reads(model.CreatePBACRuleRequest{}).
			Doc("Grant a user or group access to a path prefix (project admin)").
			Returns(http.StatusOK, "created rule", model.PBACRuleResponse{}).
			Operation("createProjectRule").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.DELETE("/orgs/{org}/projects/{project}/permissions/rules/{id}").
			To(wrap(h.DeleteProjectRule)).
			Param(ws.PathParameter("org", "organization slug")).
			Param(ws.PathParameter("project", "project slug")).
			Param(ws.PathParameter("id", "rule id")).
			Doc("Revoke a rule (project admin)").
			Returns(http.StatusOK, "deleted", model.MessageResponse{}).
			Operation("deleteProjectRule").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.GET("/orgs/{org}/projects/{project}/permissions/defaults").
			To(wrap(h.ListProjectDefaults)).
			Param(ws.PathParameter("org", "organization slug")).
			Param(ws.PathParameter("project", "project slug")).
			Doc("List project path defaults (project admin)").
			Returns(http.StatusOK, "defaults", []model.PermissionEntry{}).
			Operation("listProjectDefaults").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.PUT("/orgs/{org}/projects/{project}/permissions/defaults").
			To(wrap(h.SetProjectDefault)).
			Param(ws.PathParameter("org", "organization slug")).
			Param(ws.PathParameter("project", "project slug")).
			Reads(model.SetPathPermissionRequest{}).
			Doc("Set the default permission for a path prefix (project admin)").
			Returns(http.StatusOK, "default", model.PermissionEntry{}).
			Operation("setProjectDefault").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.DELETE("/orgs/{org}/projects/{project}/permissions/defaults").
			To(wrap(h.DeleteProjectDefault)).
			Param(ws.PathParameter("org", "organization slug")).
			Param(ws.PathParameter("project", "project slug")).
			Param(ws.QueryParameter("path", "path prefix to remove")).
			Doc("Remove a project path default (project admin)").
			Returns(http.StatusOK, "removed", model.MessageResponse{}).
			Operation("deleteProjectDefault").
			Metadata(restfulspec.KeyOpenAPITags, tags))
}
