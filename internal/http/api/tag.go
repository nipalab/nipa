package api

import (
	"net/http"

	"github.com/nipalab/nipa/internal/http/handler"
	"github.com/nipalab/nipa/internal/http/model"

	restfulspec "github.com/emicklei/go-restful-openapi/v2"
	"github.com/emicklei/go-restful/v3"
)

func setupTagRouter(ws *restful.WebService, h *handler.Handler) {
	tags := []string{"Tags"}
	project := func(b *restful.RouteBuilder) *restful.RouteBuilder {
		return b.
			Param(ws.PathParameter("org", "organization slug")).
			Param(ws.PathParameter("project", "project slug"))
	}

	ws.Route(project(
		ws.GET("/orgs/{org}/projects/{project}/tags").
			To(wrap(h.ListProjectTags)).
			Param(ws.QueryParameter("limit", "maximum number of tags")).
			Param(ws.QueryParameter("last_id", "pagination cursor (base36 tag id)")).
			Param(ws.QueryParameter("last_created_at", "pagination cursor (RFC3339)")).
			Doc("List tags (project read)").
			Returns(http.StatusOK, "tags", []model.TagResponse{}).
			Operation("listProjectTags").
			Metadata(restfulspec.KeyOpenAPITags, tags)))

	ws.Route(project(
		ws.POST("/orgs/{org}/projects/{project}/tags").
			To(wrap(h.CreateProjectTag)).
			Reads(model.CreateTagRequest{}).
			Doc("Create a tag pointing at a branch head or commit (project write)").
			Returns(http.StatusOK, "created tag", model.TagResponse{}).
			Operation("createProjectTag").
			Metadata(restfulspec.KeyOpenAPITags, tags)))

	ws.Route(project(
		ws.GET("/orgs/{org}/projects/{project}/tags/{name}").
			To(wrap(h.GetProjectTag)).
			Param(ws.PathParameter("name", "tag name")).
			Doc("Get a tag by name (project read)").
			Returns(http.StatusOK, "tag", model.TagResponse{}).
			Operation("getProjectTag").
			Metadata(restfulspec.KeyOpenAPITags, tags)))

	ws.Route(project(
		ws.DELETE("/orgs/{org}/projects/{project}/tags/{name}").
			To(wrap(h.DeleteProjectTag)).
			Param(ws.PathParameter("name", "tag name")).
			Doc("Delete a tag (project write)").
			Returns(http.StatusOK, "deleted", model.MessageResponse{}).
			Operation("deleteProjectTag").
			Metadata(restfulspec.KeyOpenAPITags, tags)))
}
