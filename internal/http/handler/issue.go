package handler

import (
	"context"
	nethttp "net/http"
	"strings"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/http"
	"github.com/nipalab/nipa/internal/http/model"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

func (h *Handler) ResolveIssues(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	var keys []string
	for _, raw := range strings.Split(appCtx.QueryParameter("keys"), ",") {
		if raw = strings.TrimSpace(raw); raw != "" {
			keys = append(keys, raw)
		}
	}
	issues, err := h.useCase.IssueTracker().ResolveIssues(appCtx.Context(), project.ID, keys)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	resp := model.IssueListResponse{Issues: make([]model.IssueResponse, 0, len(issues))}
	for _, issue := range issues {
		resp.Issues = append(resp.Issues, toIssueResponse(issue))
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, resp)
}

func (h *Handler) SearchIssues(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	res, err := h.useCase.IssueTracker().SearchIssues(appCtx.Context(), project.ID, appCtx.QueryParameter("q"), queryInt(appCtx.QueryParameter("limit"), 0))
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	resp := model.IssueSearchResponse{Issues: make([]model.IssueResponse, 0, len(res.Issues)), Supported: res.Supported, Unavailable: res.Unavailable}
	for _, issue := range res.Issues {
		resp.Issues = append(resp.Issues, toIssueResponse(issue))
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, resp)
}

func (h *Handler) ListIssueLinks(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	var before snow.ID
	if raw := appCtx.QueryParameter("after"); raw != "" {
		if before, err = snow.ParseBase36(raw); err != nil {
			appCtx.HandleError(domain.NewErrorUser("invalid after cursor"))
			return
		}
	}
	page, err := h.useCase.IssueTracker().IssueLinks(appCtx.Context(), project.ID, appCtx.PathParameter("key"), before, queryInt(appCtx.QueryParameter("limit"), 0))
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	resp := model.IssueLinksResponse{Key: page.Key, URL: page.URL, Links: make([]model.IssueLinkResponse, 0, len(page.Links))}
	for _, link := range page.Links {
		resp.Links = append(resp.Links, toIssueLinkResponse(link))
	}
	if page.Next != 0 {
		resp.NextCursor = page.Next.Base36()
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, resp)
}

// issueKeys looks up the issue keys of subjects the caller has already read;
// a project without an issue tracker answers nil.
func (h *Handler) issueKeys(ctx context.Context, projectID snow.ID, kind string, ids []snow.ID) map[snow.ID][]model.IssueKeyResponse {
	refs := h.useCase.IssueTracker().SubjectIssueKeys(ctx, projectID, kind, ids)
	if len(refs) == 0 {
		return nil
	}
	out := make(map[snow.ID][]model.IssueKeyResponse, len(refs))
	for id, keys := range refs {
		for _, key := range keys {
			out[id] = append(out[id], model.IssueKeyResponse{Key: key.Key, URL: key.URL})
		}
	}
	return out
}

func (h *Handler) commitResponses(ctx context.Context, projectID snow.ID, entries []*domain.CommitLogEntry) []model.CommitResponse {
	ids := make([]snow.ID, len(entries))
	for i, entry := range entries {
		ids[i] = entry.ID
	}
	keys := h.issueKeys(ctx, projectID, domain.IssueSubjectCommit, ids)
	resp := make([]model.CommitResponse, 0, len(entries))
	for _, entry := range entries {
		commit := toCommitResponse(entry)
		commit.IssueKeys = keys[entry.ID]
		resp = append(resp, commit)
	}
	return resp
}

func (h *Handler) mergeRequestIssueKeys(ctx context.Context, projectID snow.ID, requests []*domain.MergeRequest) map[snow.ID][]model.IssueKeyResponse {
	ids := make([]snow.ID, len(requests))
	for i, request := range requests {
		ids[i] = snow.ID(request.ID)
	}
	return h.issueKeys(ctx, projectID, domain.IssueSubjectMergeRequest, ids)
}

func toIssueResponse(issue usecase.ResolvedIssue) model.IssueResponse {
	resp := model.IssueResponse{Key: issue.Key, URL: issue.URL, State: issue.State}
	if issue.Issue != nil {
		resp.Title = issue.Issue.Title
		resp.Type = issue.Issue.Type
		resp.Status = issue.Issue.Status
		resp.StatusCategory = issue.Issue.StatusCategory
		resp.Assignee = issue.Issue.Assignee
	}
	return resp
}

func toIssueLinkResponse(link domain.IssueLinkSubject) model.IssueLinkResponse {
	resp := model.IssueLinkResponse{
		ID:        link.ID.Base36(),
		Kind:      link.SubjectKind,
		Relation:  link.Relation,
		CreatedAt: link.CreatedAt,
	}
	switch link.SubjectKind {
	case domain.IssueSubjectCommit:
		title, _, _ := strings.Cut(link.CommitMessage, "\n")
		resp.Commit = &model.IssueLinkCommitResponse{ID: link.SubjectID.Base36(), Title: strings.TrimSpace(title), CreatedAt: link.CommitCreatedAt}
	case domain.IssueSubjectMergeRequest:
		resp.MergeRequest = &model.IssueLinkMergeRequestResponse{Number: link.MRNumber, Title: link.MRTitle, State: link.MRStatus}
	case domain.IssueSubjectBranch:
		resp.Branch = &model.IssueLinkBranchResponse{Name: link.BranchName}
	}
	return resp
}
