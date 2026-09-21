package api

import (
	"net/http"

	"github.com/nipalab/nipa/internal/http/handler"
	"github.com/nipalab/nipa/internal/http/model"

	restfulspec "github.com/emicklei/go-restful-openapi/v2"
	"github.com/emicklei/go-restful/v3"
)

func setupProjectRouter(ws *restful.WebService, h *handler.Handler) {
	tags := []string{"Projects"}

	ws.Route(
		ws.GET("/orgs/{org}/projects").
			To(wrap(h.ListProjects)).
			Param(ws.PathParameter("org", "organization slug")).
			Doc("List the projects the caller can read").
			Returns(http.StatusOK, "projects", []model.ProjectResponse{}).
			Operation("listProjects").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.POST("/orgs/{org}/projects").
			To(wrap(h.CreateProject)).
			Param(ws.PathParameter("org", "organization slug")).
			Reads(model.CreateProjectRequest{}).
			Doc("Create a project (org owner or global admin)").
			Returns(http.StatusOK, "created project", model.ProjectResponse{}).
			Operation("createProject").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.GET("/orgs/{org}/projects/{project}").
			To(wrap(h.GetProject)).
			Param(ws.PathParameter("org", "organization slug")).
			Param(ws.PathParameter("project", "project slug")).
			Doc("Get a project the caller can read").
			Returns(http.StatusOK, "project", model.ProjectResponse{}).
			Operation("getProject").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.PATCH("/orgs/{org}/projects/{project}").
			To(wrap(h.UpdateProject)).
			Param(ws.PathParameter("org", "organization slug")).
			Param(ws.PathParameter("project", "project slug")).
			Reads(model.UpdateProjectRequest{}).
			Doc("Update project settings (project admin or org owner)").
			Returns(http.StatusOK, "updated project", model.ProjectResponse{}).
			Operation("updateProject").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.DELETE("/orgs/{org}/projects/{project}").
			To(wrap(h.DeleteProject)).
			Param(ws.PathParameter("org", "organization slug")).
			Param(ws.PathParameter("project", "project slug")).
			Doc("Delete a project (org owner or global admin)").
			Returns(http.StatusOK, "deleted", model.MessageResponse{}).
			Operation("deleteProject").
			Metadata(restfulspec.KeyOpenAPITags, tags))
}
