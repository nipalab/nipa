package api

import (
	"net/http"

	"github.com/nipalab/nipa/internal/http/handler"
	"github.com/nipalab/nipa/internal/http/model"

	restfulspec "github.com/emicklei/go-restful-openapi/v2"
	"github.com/emicklei/go-restful/v3"
)

func setupBrowserRouter(ws *restful.WebService, h *handler.Handler) {
	tags := []string{"Browser"}

	ws.Route(
		ws.GET("/orgs/{org}/projects/{project}/branches").
			To(wrap(h.ListProjectBranches)).
			Param(ws.PathParameter("org", "organization slug")).
			Param(ws.PathParameter("project", "project slug")).
			Param(ws.QueryParameter("limit", "maximum number of branches")).
			Param(ws.QueryParameter("last_id", "pagination cursor (base36 branch id)")).
			Param(ws.QueryParameter("last_updated_at", "pagination cursor (RFC3339)")).
			Doc("List branches").
			Returns(http.StatusOK, "branches", []model.BranchResponse{}).
			Operation("listProjectBranches").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.POST("/orgs/{org}/projects/{project}/branches").
			To(wrap(h.CreateProjectBranch)).
			Param(ws.PathParameter("org", "organization slug")).
			Param(ws.PathParameter("project", "project slug")).
			Reads(model.CreateBranchRequest{}).
			Doc("Create a branch (project write)").
			Returns(http.StatusOK, "created branch", model.BranchResponse{}).
			Operation("createProjectBranch").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.PATCH("/orgs/{org}/projects/{project}/branches/{name}").
			To(wrap(h.RenameProjectBranch)).
			Param(ws.PathParameter("org", "organization slug")).
			Param(ws.PathParameter("project", "project slug")).
			Param(ws.PathParameter("name", "current branch name")).
			Reads(model.RenameBranchRequest{}).
			Doc("Rename a branch (project write; project admin when protected)").
			Returns(http.StatusOK, "renamed branch", model.BranchResponse{}).
			Operation("renameProjectBranch").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.DELETE("/orgs/{org}/projects/{project}/branches/{name}").
			To(wrap(h.DeleteProjectBranch)).
			Param(ws.PathParameter("org", "organization slug")).
			Param(ws.PathParameter("project", "project slug")).
			Param(ws.PathParameter("name", "branch name")).
			Doc("Delete a branch (project write; project admin when protected)").
			Returns(http.StatusOK, "deleted", model.MessageResponse{}).
			Operation("deleteProjectBranch").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.POST("/orgs/{org}/projects/{project}/branches/{name}/default").
			To(wrap(h.SetProjectBranchDefault)).
			AllowedMethodsWithoutContentType([]string{"POST"}).
			Param(ws.PathParameter("org", "organization slug")).
			Param(ws.PathParameter("project", "project slug")).
			Param(ws.PathParameter("name", "branch name")).
			Doc("Make a branch the default (project admin)").
			Returns(http.StatusOK, "default branch", model.BranchResponse{}).
			Operation("setProjectBranchDefault").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.PUT("/orgs/{org}/projects/{project}/branches/{name}/protection").
			To(wrap(h.SetProjectBranchProtection)).
			Param(ws.PathParameter("org", "organization slug")).
			Param(ws.PathParameter("project", "project slug")).
			Param(ws.PathParameter("name", "branch name")).
			Reads(model.SetBranchProtectionRequest{}).
			Doc("Enable or disable branch protection (project admin)").
			Returns(http.StatusOK, "protected branch", model.BranchResponse{}).
			Operation("setProjectBranchProtection").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.GET("/orgs/{org}/projects/{project}/commits").
			To(wrap(h.ListCommits)).
			Param(ws.PathParameter("org", "organization slug")).
			Param(ws.PathParameter("project", "project slug")).
			Param(ws.QueryParameter("branch", "branch name (defaults to the default branch)")).
			Param(ws.QueryParameter("start", "start commit id (base36, inclusive)")).
			Param(ws.QueryParameter("limit", "maximum number of commits")).
			Param(ws.QueryParameter("path", "only commits that touched this file or directory")).
			Doc("List commits on a branch, optionally filtered by path").
			Returns(http.StatusOK, "commits", []model.CommitResponse{}).
			Operation("listCommits").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.GET("/orgs/{org}/projects/{project}/commits/{commit}/diff").
			To(wrap(h.GetCommitDiff)).
			Param(ws.PathParameter("org", "organization slug")).
			Param(ws.PathParameter("project", "project slug")).
			Param(ws.PathParameter("commit", "commit id (base36)")).
			Param(ws.QueryParameter("base", "base commit id (defaults to the first parent)")).
			Doc("Diff a commit against its first parent or an explicit base").
			Returns(http.StatusOK, "commit diff", model.CommitDiffResponse{}).
			Operation("getCommitDiff").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.GET("/orgs/{org}/projects/{project}/tree").
			To(wrap(h.GetTree)).
			Param(ws.PathParameter("org", "organization slug")).
			Param(ws.PathParameter("project", "project slug")).
			Param(ws.QueryParameter("rev", "branch name or commit id (defaults to the default branch)")).
			Param(ws.QueryParameter("path", "directory path inside the repository")).
			Param(ws.QueryParameter("history", "include the latest commit per entry (1)")).
			Param(ws.QueryParameter("recursive", "flatten every file under path (1)")).
			Doc("List one directory of the readable tree").
			Returns(http.StatusOK, "tree", model.TreeResponse{}).
			Operation("getTree").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.GET("/orgs/{org}/projects/{project}/blob").
			To(wrap(h.GetBlob)).
			Param(ws.PathParameter("org", "organization slug")).
			Param(ws.PathParameter("project", "project slug")).
			Param(ws.QueryParameter("rev", "branch name or commit id (defaults to the default branch)")).
			Param(ws.QueryParameter("path", "file path inside the repository")).
			Doc("Download one readable file").
			Produces("text/plain", "application/octet-stream").
			Returns(http.StatusOK, "file content", nil).
			Operation("getBlob").
			Metadata(restfulspec.KeyOpenAPITags, tags))
}
