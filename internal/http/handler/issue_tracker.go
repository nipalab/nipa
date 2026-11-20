package handler

import (
	"errors"
	nethttp "net/http"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/http"
	"github.com/nipalab/nipa/internal/http/model"
	"github.com/nipalab/nipa/internal/issuetracker"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

func (h *Handler) GetIssueTracker(appCtx http.AppContext) {
	org, err := h.resolveOrg(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	view, err := h.useCase.IssueTracker().Get(appCtx.Context(), org.ID)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	if !view.CanManage {
		_ = appCtx.WriteJson(nethttp.StatusOK, model.IssueTrackerResponse{
			Kind: view.Tracker.Kind, Name: view.Tracker.Name, BaseURL: view.Tracker.BaseURL,
		})
		return
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, toIssueTrackerResponse(view.Tracker, nil))
}

func (h *Handler) PutIssueTracker(appCtx http.AppContext) {
	org, err := h.resolveOrg(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	body := &model.IssueTrackerRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(err)
		return
	}
	res, err := h.useCase.IssueTracker().Put(appCtx.Context(), org.ID, usecase.IssueTrackerInput{
		Kind:     body.Kind,
		Name:     body.Name,
		BaseURL:  body.BaseURL,
		AuthKind: body.AuthKind,
		Account:  body.Account,
		Token:    body.Token,
	})
	var failure *usecase.IssueTrackerProbeFailure
	if errors.As(err, &failure) {
		_ = appCtx.WriteJson(nethttp.StatusBadRequest, model.IssueTrackerProbeError{Error: failure.Error(), Check: failure.Check})
		return
	}
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	status := nethttp.StatusOK
	if res.Created {
		status = nethttp.StatusCreated
	}
	_ = appCtx.WriteJson(status, toIssueTrackerResponse(res.Tracker, res.Site))
}

func (h *Handler) DeleteIssueTracker(appCtx http.AppContext) {
	org, err := h.resolveOrg(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	if err := h.useCase.IssueTracker().Delete(appCtx.Context(), org.ID); err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.ResponseWriter().WriteHeader(nethttp.StatusNoContent)
}

func (h *Handler) TestIssueTracker(appCtx http.AppContext) {
	org, err := h.resolveOrg(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	res, err := h.useCase.IssueTracker().Test(appCtx.Context(), org.ID)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, model.IssueTrackerTestResponse{
		OK: res.OK, Check: res.Check, Error: res.Error, Site: toIssueTrackerSite(res.Site),
	})
}

func (h *Handler) GetProjectIssueTracker(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	settings, err := h.useCase.IssueTracker().GetProject(appCtx.Context(), project.ID)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, toProjectIssueTrackerResponse(settings))
}

func (h *Handler) PutProjectIssueTracker(appCtx http.AppContext) {
	org, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	body := &model.ProjectIssueTrackerRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(err)
		return
	}
	in := usecase.ProjectIssueTrackerInput{
		Keys:              body.Keys,
		LinkMergeRequests: body.LinkMergeRequests,
		LinkCommits:       body.LinkCommits,
		CommentOnMerge:    body.CommentOnMerge,
		MergedStatus:      body.MergedStatus,
		OpenedStatus:      body.OpenedStatus,
		Active:            body.Active,
	}
	if body.TrackerID != "" {
		id, err := snow.ParseBase36(body.TrackerID)
		if err != nil {
			appCtx.HandleError(domain.NewErrorUser("invalid tracker_id"))
			return
		}
		in.TrackerID = &id
	}
	settings, err := h.useCase.IssueTracker().PutProject(appCtx.Context(), org.ID, project.ID, in)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, toProjectIssueTrackerResponse(settings))
}

func (h *Handler) DeleteProjectIssueTracker(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	if err := h.useCase.IssueTracker().DeleteProject(appCtx.Context(), project.ID); err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.ResponseWriter().WriteHeader(nethttp.StatusNoContent)
}

func toIssueTrackerResponse(t *domain.IssueTracker, site *issuetracker.Site) model.IssueTrackerResponse {
	return model.IssueTrackerResponse{
		ID:        t.ID.Base36(),
		Kind:      t.Kind,
		Name:      t.Name,
		BaseURL:   t.BaseURL,
		AuthKind:  t.AuthKind,
		Account:   t.Account,
		CreatedBy: t.CreatedBy.Base36(),
		CreatedAt: &t.CreatedAt,
		UpdatedAt: &t.UpdatedAt,
		CanManage: true,
		Site:      toIssueTrackerSite(site),
	}
}

func toIssueTrackerSite(site *issuetracker.Site) *model.IssueTrackerSite {
	if site == nil {
		return nil
	}
	return &model.IssueTrackerSite{
		Kind:        site.Kind,
		Title:       site.Title,
		Version:     site.Version,
		Deployment:  site.Deployment,
		AccountID:   site.AccountID,
		AccountName: site.AccountName,
	}
}

func toProjectIssueTrackerResponse(s *domain.ProjectIssueTracker) model.ProjectIssueTrackerResponse {
	keys := s.Keys
	if keys == nil {
		keys = []string{}
	}
	return model.ProjectIssueTrackerResponse{
		TrackerID:         s.TrackerID.Base36(),
		Keys:              keys,
		LinkMergeRequests: s.LinkMergeRequests,
		LinkCommits:       s.LinkCommits,
		CommentOnMerge:    s.CommentOnMerge,
		MergedStatus:      s.MergedStatus,
		OpenedStatus:      s.OpenedStatus,
		Active:            s.IsActive,
		SetBy:             s.SetBy.Base36(),
		UpdatedAt:         s.UpdatedAt,
	}
}
