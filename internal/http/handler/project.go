package handler

import (
	nethttp "net/http"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/http"
	"github.com/nipalab/nipa/internal/http/model"
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
	appCtx.WriteJson(nethttp.StatusOK, resp)
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
	project, err := h.useCase.Project().Create(
		appCtx.Context(), org.ID, body.Name, body.Description, body.Slug,
	)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, toProjectResponse(project))
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
	appCtx.WriteJson(nethttp.StatusOK, toProjectResponse(got))
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
	appCtx.WriteJson(nethttp.StatusOK, toProjectResponse(updated))
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
	appCtx.WriteJson(nethttp.StatusOK, model.MessageResponse{Message: "project deleted"})
}

func (h *Handler) resolveProject(appCtx http.AppContext) (*domain.Organization, *domain.Project, error) {
	return h.useCase.Common().ResolveBySlug(
		appCtx.Context(),
		appCtx.PathParameter("org"),
		appCtx.PathParameter("project"),
	)
}
