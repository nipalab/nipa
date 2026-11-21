package handler

import (
	nethttp "net/http"
	"strconv"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/http"
	"github.com/nipalab/nipa/internal/http/model"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

func (h *Handler) ListIssueTrackerJobs(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	opts := usecase.IssueJobListOptions{State: appCtx.QueryParameter("state")}
	if raw := appCtx.QueryParameter("after"); raw != "" {
		after, err := snow.ParseBase36(raw)
		if err != nil {
			appCtx.HandleError(domain.NewErrorUser("invalid after"))
			return
		}
		opts.After = &after
	}
	if raw := appCtx.QueryParameter("limit"); raw != "" {
		limit, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || limit < 0 {
			appCtx.HandleError(domain.NewErrorUser("invalid limit"))
			return
		}
		opts.Limit = limit
	}
	jobs, next, err := h.useCase.IssueTracker().ListJobs(appCtx.Context(), project.ID, opts)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	resp := model.IssueTrackerJobListResponse{Jobs: make([]model.IssueTrackerJobResponse, 0, len(jobs)), NextCursor: next}
	for _, job := range jobs {
		resp.Jobs = append(resp.Jobs, toIssueTrackerJobResponse(job))
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, resp)
}

func (h *Handler) RetryIssueTrackerJob(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	id, err := parseID(appCtx.PathParameter("id"), "job")
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	job, err := h.useCase.IssueTracker().RetryJob(appCtx.Context(), project.ID, id)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, toIssueTrackerJobResponse(job))
}

func toIssueTrackerJobResponse(job *domain.IssueTrackerJob) model.IssueTrackerJobResponse {
	detail := usecase.DescribeIssueJob(job)
	resp := model.IssueTrackerJobResponse{
		ID:          job.ID.Base36(),
		IssueKey:    job.IssueKey,
		Action:      job.Action,
		State:       job.State,
		Attempts:    job.Attempt,
		LastError:   job.LastError,
		Title:       detail.Title,
		URL:         detail.URL,
		Status:      detail.Status,
		DeliveredAt: job.DeliveredAt,
		CreatedAt:   job.CreatedAt,
	}
	if job.State == domain.IssueJobPending {
		resp.NextRetryAt = job.NextRetryAt
	}
	return resp
}
