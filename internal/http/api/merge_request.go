package api

import (
	"net/http"

	"github.com/nipalab/nipa/internal/http/handler"
	"github.com/nipalab/nipa/internal/http/model"

	restfulspec "github.com/emicklei/go-restful-openapi/v2"
	"github.com/emicklei/go-restful/v3"
)

func setupMergeRequestRouter(ws *restful.WebService, h *handler.Handler) {
	tags := []string{"MergeRequests"}
	project := func(b *restful.RouteBuilder) *restful.RouteBuilder {
		return b.
			Param(ws.PathParameter("org", "organization slug")).
			Param(ws.PathParameter("project", "project slug"))
	}
	requestID := func(b *restful.RouteBuilder) *restful.RouteBuilder {
		return project(b).Param(ws.PathParameter("id", "merge request id (base36)"))
	}

	ws.Route(project(
		ws.GET("/orgs/{org}/projects/{project}/merge-requests").
			To(wrap(h.ListMergeRequests)).
			Param(ws.QueryParameter("status", "filter by status")).
			Param(ws.QueryParameter("limit", "maximum number of results")).
			Doc("List merge requests").
			Returns(http.StatusOK, "merge requests", []model.MergeRequestResponse{}).
			Operation("listMergeRequests").
			Metadata(restfulspec.KeyOpenAPITags, tags)))

	ws.Route(project(
		ws.POST("/orgs/{org}/projects/{project}/merge-requests").
			To(wrap(h.CreateMergeRequest)).
			Reads(model.CreateMergeRequestRequest{}).
			Doc("Open a merge request (project read)").
			Returns(http.StatusOK, "created merge request", model.MergeRequestResponse{}).
			Operation("createMergeRequest").
			Metadata(restfulspec.KeyOpenAPITags, tags)))

	ws.Route(requestID(
		ws.GET("/orgs/{org}/projects/{project}/merge-requests/{id}").
			To(wrap(h.GetMergeRequest)).
			Doc("Get a merge request with its live mergeability").
			Returns(http.StatusOK, "merge request", model.MergeRequestResponse{}).
			Operation("getMergeRequest").
			Metadata(restfulspec.KeyOpenAPITags, tags)))

	ws.Route(requestID(
		ws.PATCH("/orgs/{org}/projects/{project}/merge-requests/{id}").
			To(wrap(h.UpdateMergeRequest)).
			Reads(model.UpdateMergeRequestRequest{}).
			Doc("Update a merge request's title or description (author or project admin)").
			Returns(http.StatusOK, "updated merge request", model.MergeRequestResponse{}).
			Operation("updateMergeRequest").
			Metadata(restfulspec.KeyOpenAPITags, tags)))

	ws.Route(requestID(
		ws.GET("/orgs/{org}/projects/{project}/merge-requests/{id}/check").
			To(wrap(h.CheckMergeRequest)).
			Doc("Recompute a merge request's mergeability").
			Returns(http.StatusOK, "mergeability", model.MergeabilityResponse{}).
			Operation("checkMergeRequest").
			Metadata(restfulspec.KeyOpenAPITags, tags)))

	ws.Route(requestID(
		ws.POST("/orgs/{org}/projects/{project}/merge-requests/{id}/merge").
			To(wrap(h.MergeMergeRequest)).
			AllowedMethodsWithoutContentType([]string{"POST"}).
			Doc("Merge the request into its target branch (project write; project admin for protected targets)").
			Returns(http.StatusOK, "merged merge request", model.MergeRequestResponse{}).
			Operation("mergeMergeRequest").
			Metadata(restfulspec.KeyOpenAPITags, tags)))

	ws.Route(requestID(
		ws.POST("/orgs/{org}/projects/{project}/merge-requests/{id}/close").
			To(wrap(h.CloseMergeRequest)).
			AllowedMethodsWithoutContentType([]string{"POST"}).
			Doc("Close a merge request (author or project admin)").
			Returns(http.StatusOK, "closed merge request", model.MergeRequestResponse{}).
			Operation("closeMergeRequest").
			Metadata(restfulspec.KeyOpenAPITags, tags)))

	ws.Route(requestID(
		ws.POST("/orgs/{org}/projects/{project}/merge-requests/{id}/reopen").
			To(wrap(h.ReopenMergeRequest)).
			AllowedMethodsWithoutContentType([]string{"POST"}).
			Doc("Reopen a closed merge request (author or project admin)").
			Returns(http.StatusOK, "reopened merge request", model.MergeRequestResponse{}).
			Operation("reopenMergeRequest").
			Metadata(restfulspec.KeyOpenAPITags, tags)))

	ws.Route(requestID(
		ws.GET("/orgs/{org}/projects/{project}/merge-requests/{id}/diff").
			To(wrap(h.MergeRequestDiff)).
			Doc("Three-dot diff of the source branch against the merge base").
			Returns(http.StatusOK, "merge request diff", model.MergeRequestDiffResponse{}).
			Operation("mergeRequestDiff").
			Metadata(restfulspec.KeyOpenAPITags, tags)))
}
