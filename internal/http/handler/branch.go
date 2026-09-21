package handler

import (
	nethttp "net/http"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/http"
	"github.com/nipalab/nipa/internal/http/model"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

func (h *Handler) CreateProjectBranch(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	body := &model.CreateBranchRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(err)
		return
	}
	fork := usecase.BranchForkPoint{BranchName: body.From}
	if body.From != "" {
		// Branch names such as "main" are valid base36; prefer the branch.
		if _, err := h.useCase.Branch().GetBranchByName(appCtx.Context(), project.ID, body.From); domain.IsErrorNotFound(err) {
			if id, parseErr := snow.ParseBase36(body.From); parseErr == nil {
				fork = usecase.BranchForkPoint{CommitID: &id}
			}
		} else if err != nil {
			appCtx.HandleError(err)
			return
		}
	}
	branch, err := h.useCase.Branch().CreateBranch(appCtx.Context(), project.ID, body.Name, fork)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, toBranchResponse(branch))
}

func (h *Handler) RenameProjectBranch(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	body := &model.RenameBranchRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(err)
		return
	}
	branch, err := h.useCase.Branch().Rename(appCtx.Context(), project.ID, appCtx.PathParameter("name"), body.Name)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, toBranchResponse(branch))
}

func (h *Handler) DeleteProjectBranch(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	if err := h.useCase.Branch().Delete(appCtx.Context(), project.ID, appCtx.PathParameter("name")); err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, model.MessageResponse{Message: "branch deleted"})
}

func (h *Handler) SetProjectBranchDefault(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	branch, err := h.useCase.Branch().SetDefault(appCtx.Context(), project.ID, appCtx.PathParameter("name"))
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, toBranchResponse(branch))
}

func (h *Handler) SetProjectBranchProtection(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	body := &model.SetBranchProtectionRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(err)
		return
	}
	branch, err := h.useCase.Branch().SetProtection(
		appCtx.Context(), project.ID, appCtx.PathParameter("name"), body.Protected,
	)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, toBranchResponse(branch))
}
