package api

import (
	"net/http"

	"github.com/nipalab/nipa/internal/http/handler"
	"github.com/nipalab/nipa/internal/http/model"

	restfulspec "github.com/emicklei/go-restful-openapi/v2"
	"github.com/emicklei/go-restful/v3"
)

func setupFileLockRouter(ws *restful.WebService, h *handler.Handler) {
	tags := []string{"FileLocks"}
	project := func(b *restful.RouteBuilder) *restful.RouteBuilder {
		return b.
			Param(ws.PathParameter("org", "organization slug")).
			Param(ws.PathParameter("project", "project slug"))
	}

	ws.Route(project(
		ws.GET("/orgs/{org}/projects/{project}/locks").
			To(wrap(h.ListFileLocks)).
			Doc("List binary file locks (project read)").
			Returns(http.StatusOK, "file locks", []model.FileLockResponse{}).
			Operation("listFileLocks").
			Metadata(restfulspec.KeyOpenAPITags, tags)))

	ws.Route(project(
		ws.POST("/orgs/{org}/projects/{project}/locks").
			To(wrap(h.LockFile)).
			Reads(model.LockFileRequest{}).
			Doc("Lock a binary file or directory prefix (project write)").
			Returns(http.StatusOK, "acquired lock", model.FileLockResponse{}).
			Operation("lockFile").
			Metadata(restfulspec.KeyOpenAPITags, tags)))

	ws.Route(project(
		ws.POST("/orgs/{org}/projects/{project}/locks/release").
			To(wrap(h.UnlockFile)).
			Reads(model.LockFileRequest{}).
			Doc("Release a lock (holder or project admin)").
			Returns(http.StatusOK, "released", nil).
			Operation("unlockFile").
			Metadata(restfulspec.KeyOpenAPITags, tags)))
}
