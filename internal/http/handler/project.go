package handler

import (
	nethttp "net/http"
	"strings"
	"time"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/http"
	"github.com/nipalab/nipa/internal/http/model"
	"github.com/nipalab/nipa/internal/snow"
)

func (h *Handler) ListProjects(appCtx http.AppContext) {
	org, err := h.resolveOrg(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	projects, err := h.useCase.Project().List(appCtx.Context(), org.ID)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	resp := make([]model.ProjectResponse, 0, len(projects))
	for _, project := range projects {
		resp = append(resp, toProjectResponse(project))
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, resp)
}

func (h *Handler) CreateProject(appCtx http.AppContext) {
	org, err := h.resolveOrg(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	body := &model.CreateProjectRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(err)
		return
	}
	var storageServerID *snow.ID
	if raw := strings.TrimSpace(body.StorageServerID); raw != "" {
		id, err := snow.ParseBase36(raw)
		if err != nil {
			appCtx.HandleError(domain.NewErrorUser("invalid storage_server_id"))
			return
		}
		storageServerID = &id
	}
	project, err := h.useCase.Project().Create(
		appCtx.Context(), org.ID, body.Name, body.Description, body.Slug, storageServerID,
	)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, toProjectResponse(project))
}

func (h *Handler) GetProject(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	got, err := h.useCase.Project().Get(appCtx.Context(), project.ID)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, toProjectResponse(got))
}

func (h *Handler) UpdateProject(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	body := &model.UpdateProjectRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(err)
		return
	}
	updated, err := h.useCase.Project().Update(appCtx.Context(), project.ID, body.Name, body.Description)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, toProjectResponse(updated))
}

func (h *Handler) DeleteProject(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	if err := h.useCase.Project().Delete(appCtx.Context(), project.ID); err != nil {
		appCtx.HandleError(err)
		return
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, model.MessageResponse{Message: "project deleted"})
}

// ListDeletedProjects lists the organization's restorable deleted projects.
func (h *Handler) ListDeletedProjects(appCtx http.AppContext) {
	org, err := h.resolveOrg(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	uc := h.useCase.Project()
	projects, err := uc.ListDeleted(appCtx.Context(), org.ID)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	resp := make([]model.DeletedProjectResponse, 0, len(projects))
	for _, project := range projects {
		resp = append(resp, toDeletedProjectResponse(project, uc.RestoreWindow()))
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, resp)
}

// RestoreProject undeletes a project by id; its slug stays reserved while it
// is deleted, so the restored project keeps its URL.
func (h *Handler) RestoreProject(appCtx http.AppContext) {
	org, err := h.resolveOrg(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	projectID, err := snow.ParseBase36(appCtx.PathParameter("id"))
	if err != nil {
		appCtx.HandleError(domain.NewErrorNotFound("project not found"))
		return
	}
	restored, err := h.useCase.Project().Restore(appCtx.Context(), org.ID, projectID)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, toProjectResponse(restored))
}

func toDeletedProjectResponse(project *domain.Project, window time.Duration) model.DeletedProjectResponse {
	resp := model.DeletedProjectResponse{
		ID:    project.ID.Base36(),
		OrgID: project.OrgID.Base36(),
		Slug:  project.Slug,
		Name:  project.Name,
	}
	if project.DeletedAt != nil {
		resp.DeletedAt = project.DeletedAt.UTC().Format(time.RFC3339)
		if window > 0 {
			resp.PurgeAfter = project.DeletedAt.Add(window).UTC().Format(time.RFC3339)
		}
	}
	return resp
}

func (h *Handler) resolveProject(appCtx http.AppContext) (*domain.Organization, *domain.Project, error) {
	return h.useCase.Common().ResolveBySlug(
		appCtx.Context(),
		appCtx.PathParameter("org"),
		appCtx.PathParameter("project"),
	)
}
