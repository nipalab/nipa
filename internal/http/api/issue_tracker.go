package api

import (
	"net/http"

	"github.com/nipalab/nipa/internal/http/handler"
	"github.com/nipalab/nipa/internal/http/model"

	restfulspec "github.com/emicklei/go-restful-openapi/v2"
	"github.com/emicklei/go-restful/v3"
)

func setupIssueTrackerRouter(ws *restful.WebService, h *handler.Handler) {
	tags := []string{"Issue tracker"}
	org := func(b *restful.RouteBuilder) *restful.RouteBuilder {
		return b.Param(ws.PathParameter("org", "organization slug")).Metadata(restfulspec.KeyOpenAPITags, tags)
	}
	project := func(b *restful.RouteBuilder) *restful.RouteBuilder {
		return org(b).Param(ws.PathParameter("project", "project slug"))
	}

	ws.Route(org(ws.GET("/orgs/{org}/issue-tracker")).
		To(Wrap(h.GetIssueTracker)).
		Doc("The org's issue tracker connection; owners see every field but the token, members kind, name and base URL").
		Returns(http.StatusOK, "issue tracker", model.IssueTrackerResponse{}).
		Operation("getIssueTracker"))
	ws.Route(org(ws.PUT("/orgs/{org}/issue-tracker")).
		To(Wrap(h.PutIssueTracker)).
		Reads(model.IssueTrackerRequest{}).
		Doc("Create or replace the org's issue tracker connection after a probe; an omitted token keeps the stored one (org owner)").
		Returns(http.StatusOK, "replaced connection", model.IssueTrackerResponse{}).
		Returns(http.StatusCreated, "created connection", model.IssueTrackerResponse{}).
		Returns(http.StatusBadRequest, "failed probe (check: auth, reach or tls) or invalid input", model.IssueTrackerProbeError{}).
		Operation("putIssueTracker"))
	ws.Route(org(ws.DELETE("/orgs/{org}/issue-tracker")).
		To(Wrap(h.DeleteIssueTracker)).
		Doc("Delete the org's issue tracker connection; 409 while a project uses it (org owner)").
		Operation("deleteIssueTracker"))
	ws.Route(org(ws.POST("/orgs/{org}/issue-tracker/test")).
		To(Wrap(h.TestIssueTracker)).
		Doc("Probe the stored connection again (org owner)").
		Returns(http.StatusOK, "probe result", model.IssueTrackerTestResponse{}).
		Operation("testIssueTracker"))

	ws.Route(project(ws.GET("/orgs/{org}/projects/{project}/issue-tracker")).
		To(Wrap(h.GetProjectIssueTracker)).
		Doc("The project's issue tracker settings (project admin)").
		Returns(http.StatusOK, "settings", model.ProjectIssueTrackerResponse{}).
		Operation("getProjectIssueTracker"))
	ws.Route(project(ws.PUT("/orgs/{org}/projects/{project}/issue-tracker")).
		To(Wrap(h.PutProjectIssueTracker)).
		Reads(model.ProjectIssueTrackerRequest{}).
		Doc("Use the org's connection in this project with its Jira project keys and rules (project admin)").
		Returns(http.StatusOK, "settings", model.ProjectIssueTrackerResponse{}).
		Operation("putProjectIssueTracker"))
	ws.Route(project(ws.DELETE("/orgs/{org}/projects/{project}/issue-tracker")).
		To(Wrap(h.DeleteProjectIssueTracker)).
		Doc("Stop using the issue tracker in this project (project admin)").
		Operation("deleteProjectIssueTracker"))
	ws.Route(project(ws.GET("/orgs/{org}/projects/{project}/issue-tracker/jobs")).
		To(Wrap(h.ListIssueTrackerJobs)).
		Param(ws.QueryParameter("state", "pending, delivered or failed")).
		Param(ws.QueryParameter("after", "keyset cursor: the last id of the previous page")).
		Param(ws.QueryParameter("limit", "page size (default 50, max 200)")).
		Doc("List the issue tracker outbox jobs (remote links, merge comments, transitions), newest first (project admin)").
		Returns(http.StatusOK, "jobs", model.IssueTrackerJobListResponse{}).
		Operation("listIssueTrackerJobs"))
	ws.Route(project(ws.POST("/orgs/{org}/projects/{project}/issue-tracker/jobs/{id}/retry")).
		To(Wrap(h.RetryIssueTrackerJob)).
		Param(ws.PathParameter("id", "job id (base36)")).
		Doc("Queue a delivered or failed job again with a fresh attempt count (project admin)").
		Returns(http.StatusOK, "queued job", model.IssueTrackerJobResponse{}).
		Operation("retryIssueTrackerJob"))

	ws.Route(project(ws.GET("/orgs/{org}/projects/{project}/issues")).
		To(Wrap(h.ResolveIssues)).
		Param(ws.QueryParameter("keys", "comma-separated issue keys, at most 50; keys outside the project's keys are left out")).
		Doc("Resolve title, status, type and assignee of issue keys through a 60 s cache; a tracker that is down answers key and url only (project read)").
		Returns(http.StatusOK, "issues", model.IssueListResponse{}).
		Operation("resolveIssues"))
	ws.Route(project(ws.GET("/orgs/{org}/projects/{project}/issues/search")).
		To(Wrap(h.SearchIssues)).
		Param(ws.QueryParameter("q", "search text")).
		Param(ws.QueryParameter("limit", "at most 50, default 20")).
		Doc("Search the project's issues for pickers; supported is false when the tracker cannot search (project read)").
		Returns(http.StatusOK, "issues", model.IssueSearchResponse{}).
		Operation("searchIssues"))
	ws.Route(project(ws.GET("/orgs/{org}/projects/{project}/issues/{key}/links")).
		To(Wrap(h.ListIssueLinks)).
		Param(ws.PathParameter("key", "issue key")).
		Param(ws.QueryParameter("after", "next_cursor of the previous page")).
		Param(ws.QueryParameter("limit", "at most 100, default 50")).
		Doc("Commits, branches and merge requests linked to an issue, newest first (project read)").
		Returns(http.StatusOK, "links", model.IssueLinksResponse{}).
		Operation("listIssueLinks"))
}
