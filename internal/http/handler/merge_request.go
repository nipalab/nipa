package handler

import (
	nethttp "net/http"
	"strconv"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/http"
	"github.com/nipalab/nipa/internal/http/model"
	"github.com/nipalab/nipa/internal/snow"
)

const defaultMergeRequestLimit = 50

func (h *Handler) ListMergeRequests(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	limit := queryInt(appCtx.QueryParameter("limit"), defaultMergeRequestLimit)
	if limit <= 0 {
		limit = defaultMergeRequestLimit
	}
	opts := domain.MergeRequestListOptions{
		Status:       appCtx.QueryParameter("status"),
		SourceBranch: appCtx.QueryParameter("source"),
		TargetBranch: appCtx.QueryParameter("target"),
		Search:       appCtx.QueryParameter("search"),
		Limit:        limit + 1,
	}
	if raw := appCtx.QueryParameter("author"); raw != "" {
		author, err := snow.ParseBase36(raw)
		if err != nil {
			appCtx.HandleError(domain.NewErrorUser("invalid author id"))
			return
		}
		opts.Author = &author
	}
	if raw := appCtx.QueryParameter("assignee"); raw != "" {
		assignee, err := snow.ParseBase36(raw)
		if err != nil {
			appCtx.HandleError(domain.NewErrorUser("invalid assignee id"))
			return
		}
		opts.Assignee = &assignee
	}
	if raw := appCtx.QueryParameter("after"); raw != "" {
		after, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || after <= 0 {
			appCtx.HandleError(domain.NewErrorUser("invalid after cursor"))
			return
		}
		opts.After = after
	}
	if raw := appCtx.QueryParameter("draft"); raw != "" {
		draft, err := strconv.ParseBool(raw)
		if err != nil {
			appCtx.HandleError(domain.NewErrorUser("invalid draft filter"))
			return
		}
		opts.Draft = &draft
	}
	requests, err := h.useCase.MergeRequest().List(appCtx.Context(), project.ID, opts)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	hasMore := len(requests) > limit
	if hasMore {
		requests = requests[:limit]
	}
	if err := h.useCase.MergeRequestReview().AttachSummaries(appCtx.Context(), project.ID, requests); err != nil {
		appCtx.HandleError(err)
		return
	}
	resp := model.MergeRequestListResponse{MergeRequests: make([]model.MergeRequestResponse, 0, len(requests))}
	for _, request := range requests {
		resp.MergeRequests = append(resp.MergeRequests, toMergeRequestResponse(request, nil))
	}
	if hasMore {
		resp.NextCursor = strconv.FormatInt(requests[len(requests)-1].Number, 10)
	}
	appCtx.WriteJson(nethttp.StatusOK, resp)
}

func (h *Handler) CreateMergeRequest(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	body := &model.CreateMergeRequestRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(err)
		return
	}
	request, err := h.useCase.MergeRequest().Create(
		appCtx.Context(), project.ID, body.Title, body.Description, body.SourceBranch, body.TargetBranch, body.Draft,
	)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, toMergeRequestResponse(request, nil))
}

func (h *Handler) UpdateMergeRequest(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	number, err := parseMergeRequestNumber(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	body := &model.UpdateMergeRequestRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(err)
		return
	}
	var toggled *domain.MergeRequest
	if body.Draft != nil {
		updated, err := h.useCase.MergeRequest().SetDraft(appCtx.Context(), project.ID, number, *body.Draft)
		if err != nil {
			appCtx.HandleError(err)
			return
		}
		toggled = updated
	}
	if body.Title != "" || body.Description != "" {
		request, err := h.useCase.MergeRequest().Update(
			appCtx.Context(), project.ID, number, body.Title, body.Description,
		)
		if err != nil {
			appCtx.HandleError(err)
			return
		}
		appCtx.WriteJson(nethttp.StatusOK, toMergeRequestResponse(request, nil))
		return
	}
	if toggled == nil {
		appCtx.HandleError(domain.NewErrorUser("title or description is required"))
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, toMergeRequestResponse(toggled, nil))
}

func (h *Handler) ReportMergeRequestCheck(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	number, err := parseMergeRequestNumber(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	body := &model.ReportMergeRequestCheckRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(err)
		return
	}
	checks := h.useCase.MergeRequestCheck()
	if checks == nil {
		appCtx.HandleError(domain.NewErrorInternalServer("status checks are not configured"))
		return
	}
	check, err := checks.Report(appCtx.Context(), project.ID, number, body.Name, body.State, body.DetailsURL)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, toMergeRequestCheckResponse(check))
}

func (h *Handler) ListMergeRequestChecks(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	number, err := parseMergeRequestNumber(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	checks := h.useCase.MergeRequestCheck()
	if checks == nil {
		appCtx.HandleError(domain.NewErrorInternalServer("status checks are not configured"))
		return
	}
	list, err := checks.List(appCtx.Context(), project.ID, number)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	resp := make([]model.MergeRequestCheckResponse, 0, len(list))
	for _, check := range list {
		resp = append(resp, toMergeRequestCheckResponse(check))
	}
	appCtx.WriteJson(nethttp.StatusOK, resp)
}

func toMergeRequestCheckResponse(check *domain.MergeRequestCheck) model.MergeRequestCheckResponse {
	return model.MergeRequestCheckResponse{
		ID:         check.ID.Base36(),
		Name:       check.Name,
		State:      check.State,
		DetailsURL: check.DetailsURL,
		Reporter:   toReviewActorResponse(check.Reporter),
		CreatedAt:  check.CreatedAt,
		UpdatedAt:  check.UpdatedAt,
	}
}

func (h *Handler) SetMergeRequestAssignees(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	number, err := parseMergeRequestNumber(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	body := &model.SetAssigneesRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(err)
		return
	}
	userIDs := make([]snow.ID, 0, len(body.UserIDs))
	for _, raw := range body.UserIDs {
		id, err := snow.ParseBase36(raw)
		if err != nil {
			appCtx.HandleError(domain.NewErrorUser("invalid assignee id"))
			return
		}
		userIDs = append(userIDs, id)
	}
	request, err := h.useCase.MergeRequest().SetAssignees(appCtx.Context(), project.ID, number, userIDs)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, toMergeRequestResponse(request, nil))
}

func (h *Handler) GetMergeRequest(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	number, err := parseMergeRequestNumber(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	request, err := h.useCase.MergeRequest().Get(appCtx.Context(), project.ID, number)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	info, err := h.useCase.MergeRequest().Check(appCtx.Context(), project.ID, number)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	if err := h.useCase.MergeRequestReview().AttachSummaries(appCtx.Context(), project.ID,
		[]*domain.MergeRequest{request}); err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, toMergeRequestResponse(request, info))
}

func (h *Handler) CheckMergeRequest(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	number, err := parseMergeRequestNumber(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	info, err := h.useCase.MergeRequest().Check(appCtx.Context(), project.ID, number)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, toMergeabilityResponse(info))
}

func (h *Handler) MergeMergeRequest(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	number, err := parseMergeRequestNumber(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	body := &model.MergeMergeRequestRequest{}
	if appCtx.Request().ContentLength > 0 {
		if err := appCtx.ReadJson(body); err != nil {
			appCtx.HandleError(err)
			return
		}
	}
	request, info, err := h.useCase.MergeRequest().Merge(appCtx.Context(), project.ID, number, body.Strategy, body.DeleteSource)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, toMergeRequestResponse(request, info))
}

func (h *Handler) MarkMergeRequestMerged(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	number, err := parseMergeRequestNumber(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	request, err := h.useCase.MergeRequest().MarkMerged(appCtx.Context(), project.ID, number)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, toMergeRequestResponse(request, nil))
}

func (h *Handler) CloseMergeRequest(appCtx http.AppContext) {
	h.setMergeRequestStatus(appCtx, func(number int64) (*domain.MergeRequest, error) {
		_, project, err := h.resolveProject(appCtx)
		if err != nil {
			return nil, err
		}
		return h.useCase.MergeRequest().Close(appCtx.Context(), project.ID, number)
	})
}

func (h *Handler) ReopenMergeRequest(appCtx http.AppContext) {
	h.setMergeRequestStatus(appCtx, func(number int64) (*domain.MergeRequest, error) {
		_, project, err := h.resolveProject(appCtx)
		if err != nil {
			return nil, err
		}
		return h.useCase.MergeRequest().Reopen(appCtx.Context(), project.ID, number)
	})
}

func (h *Handler) MergeRequestDiff(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	number, err := parseMergeRequestNumber(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	files, err := h.useCase.MergeRequest().Diff(appCtx.Context(), project.ID, number)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	resp := model.MergeRequestDiffResponse{Files: make([]model.DiffFileResponse, 0, len(files))}
	for _, file := range files {
		resp.Files = append(resp.Files, toDiffFileResponse(file))
	}
	appCtx.WriteJson(nethttp.StatusOK, resp)
}

func (h *Handler) ListMergeRequestCommits(appCtx http.AppContext) {
	project, number, ok := h.resolveMergeRequest(appCtx)
	if !ok {
		return
	}
	commits, err := h.useCase.MergeRequest().Commits(appCtx.Context(), project.ID, number)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	resp := make([]model.CommitResponse, 0, len(commits))
	for _, commit := range commits {
		resp = append(resp, toCommitResponse(commit))
	}
	appCtx.WriteJson(nethttp.StatusOK, resp)
}

func (h *Handler) setMergeRequestStatus(appCtx http.AppContext, set func(number int64) (*domain.MergeRequest, error)) {
	if _, _, err := h.resolveProject(appCtx); err != nil {
		appCtx.HandleError(err)
		return
	}
	number, err := parseMergeRequestNumber(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	request, err := set(number)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, toMergeRequestResponse(request, nil))
}

func parseMergeRequestNumber(appCtx http.AppContext) (int64, error) {
	number, err := strconv.ParseInt(appCtx.PathParameter("id"), 10, 64)
	if err != nil || number <= 0 {
		return 0, domain.NewErrorUser("invalid merge request number")
	}
	return number, nil
}

func toMergeRequestResponse(request *domain.MergeRequest, info *domain.Mergeability) model.MergeRequestResponse {
	resp := model.MergeRequestResponse{
		ID:           snow.ID(request.ID).Base36(),
		Number:       request.Number,
		ProjectID:    request.ProjectID.Base36(),
		SourceBranch: request.SourceBranch,
		TargetBranch: request.TargetBranch,
		Title:        request.Title,
		Description:  request.Description,
		Status:       request.Status,
		Draft:        request.Draft,
		CreatedBy:    request.CreatedBy.Base36(),
		CreatedAt:    request.CreatedAt,
		UpdatedAt:    request.UpdatedAt,
	}
	if request.MergeCommitID != nil {
		resp.MergeCommitID = request.MergeCommitID.Base36()
	}
	if request.MergeBaseCommitID != nil {
		resp.MergeBaseCommitID = request.MergeBaseCommitID.Base36()
	}
	if info != nil {
		resp.Mergeability = toMergeabilityResponse(info)
	}
	if request.Review != nil {
		review := toReviewStateResponse(request.Review)
		resp.Review = &review
	}
	for _, assignee := range request.Assignees {
		resp.Assignees = append(resp.Assignees, toReviewActorResponse(assignee))
	}
	return resp
}

func toMergeabilityResponse(info *domain.Mergeability) *model.MergeabilityResponse {
	resp := &model.MergeabilityResponse{Status: info.Status, BlockedBy: info.BlockedBy}
	if info.SourceCommitID != nil {
		resp.SourceCommitID = info.SourceCommitID.Base36()
	}
	if info.TargetCommitID != nil {
		resp.TargetCommitID = info.TargetCommitID.Base36()
	}
	if info.MergeBaseCommitID != nil {
		resp.MergeBaseCommitID = info.MergeBaseCommitID.Base36()
	}
	return resp
}
