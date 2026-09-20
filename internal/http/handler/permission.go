package handler

import (
	nethttp "net/http"

	"github.com/nipalab/nipa/internal/http"
	"github.com/nipalab/nipa/internal/http/model"
)

func (h *Handler) MyProjectPermissions(appCtx http.AppContext) {
	_, project, err := h.useCase.Common().ResolveBySlug(
		appCtx.Context(),
		appCtx.PathParameter("org"),
		appCtx.PathParameter("project"),
	)
	if err != nil {
		appCtx.HandleError(err)
		return
	}

	mask, rules, defaults, err := h.useCase.Permission().MyPermissions(appCtx.Context(), project.ID)
	if err != nil {
		appCtx.HandleError(err)
		return
	}

	resp := model.ProjectPermissionResponse{
		ProjectPermission: uint64(mask),
		Rules:             make([]model.PermissionEntry, 0, len(rules)),
		Defaults:          make([]model.PermissionEntry, 0, len(defaults)),
	}
	for _, rule := range rules {
		resp.Rules = append(resp.Rules, model.PermissionEntry{
			PathPrefix: rule.PathPrefix,
			Permission: uint64(rule.Permission),
		})
	}
	for _, def := range defaults {
		resp.Defaults = append(resp.Defaults, model.PermissionEntry{
			PathPrefix: def.PathPrefix,
			Permission: uint64(def.Permission),
		})
	}
	appCtx.WriteJson(nethttp.StatusOK, resp)
}
