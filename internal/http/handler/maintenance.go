package handler

import (
	nethttp "net/http"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/http"
	"github.com/nipalab/nipa/internal/http/model"
	"github.com/nipalab/nipa/internal/maintenance"
	"github.com/nipalab/nipa/internal/usecase"
)

func (h *Handler) GetMaintenance(appCtx http.AppContext) {
	state := h.useCase.Maintenance().Status(appCtx.Context())
	_ = appCtx.WriteJson(nethttp.StatusOK, model.MaintenanceStatusResponse{
		Server: toMaintenanceWindow(state.Server),
		Source: state.Source,
	})
}

func (h *Handler) GetAdminMaintenance(appCtx http.AppContext) {
	state, err := h.useCase.Maintenance().AdminStatus(appCtx.Context())
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, model.AdminMaintenanceResponse{
		Server:   toMaintenanceWindow(state.Server),
		Source:   state.Source,
		Orgs:     toMaintenanceWindows(state.Orgs),
		Projects: toMaintenanceWindows(state.Projects),
	})
}

func (h *Handler) SetAdminMaintenance(appCtx http.AppContext) {
	h.writeMaintenance(appCtx, func(in usecase.MaintenanceInput) (*maintenance.Status, error) {
		return h.useCase.Maintenance().SetServer(appCtx.Context(), in)
	})
}

func (h *Handler) EndAdminMaintenance(appCtx http.AppContext) {
	h.endMaintenance(appCtx, h.useCase.Maintenance().EndServer(appCtx.Context()))
}

func (h *Handler) GetOrgMaintenance(appCtx http.AppContext) {
	state, err := h.useCase.Maintenance().OrgStatus(appCtx.Context(), appCtx.PathParameter("org"))
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	resp := model.OrgMaintenanceResponse{
		Server: toMaintenanceWindow(state.Server),
		Org:    toMaintenanceWindow(state.Org),
	}
	if state.Projects != nil {
		resp.Projects = toMaintenanceWindows(state.Projects)
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, resp)
}

func (h *Handler) SetOrgMaintenance(appCtx http.AppContext) {
	h.writeMaintenance(appCtx, func(in usecase.MaintenanceInput) (*maintenance.Status, error) {
		return h.useCase.Maintenance().SetOrg(appCtx.Context(), appCtx.PathParameter("org"), in)
	})
}

func (h *Handler) EndOrgMaintenance(appCtx http.AppContext) {
	h.endMaintenance(appCtx, h.useCase.Maintenance().EndOrg(appCtx.Context(), appCtx.PathParameter("org")))
}

func (h *Handler) GetProjectMaintenance(appCtx http.AppContext) {
	state, err := h.useCase.Maintenance().ProjectStatus(appCtx.Context(), appCtx.PathParameter("org"), appCtx.PathParameter("project"))
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, model.ProjectMaintenanceResponse{
		Server:  toMaintenanceWindow(state.Server),
		Org:     toMaintenanceWindow(state.Org),
		Project: toMaintenanceWindow(state.Project),
	})
}

func (h *Handler) SetProjectMaintenance(appCtx http.AppContext) {
	h.writeMaintenance(appCtx, func(in usecase.MaintenanceInput) (*maintenance.Status, error) {
		return h.useCase.Maintenance().SetProject(appCtx.Context(), appCtx.PathParameter("org"), appCtx.PathParameter("project"), in)
	})
}

func (h *Handler) EndProjectMaintenance(appCtx http.AppContext) {
	h.endMaintenance(appCtx, h.useCase.Maintenance().EndProject(appCtx.Context(), appCtx.PathParameter("org"), appCtx.PathParameter("project")))
}

func (h *Handler) writeMaintenance(appCtx http.AppContext, set func(usecase.MaintenanceInput) (*maintenance.Status, error)) {
	body := &model.SetMaintenanceRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(domain.NewErrorUser("invalid request body"))
		return
	}
	st, err := set(usecase.MaintenanceInput{
		Mode:     body.Mode,
		Message:  body.Message,
		StartsAt: body.StartsAt,
		EndsAt:   body.EndsAt,
		Notify:   body.Notify,
	})
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, toMaintenanceWindow(st))
}

func (h *Handler) endMaintenance(appCtx http.AppContext, err error) {
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.ResponseWriter().WriteHeader(nethttp.StatusNoContent)
}

func toMaintenanceWindow(st *maintenance.Status) *model.MaintenanceWindowResponse {
	if st == nil {
		return nil
	}
	return &model.MaintenanceWindowResponse{
		Mode:     string(st.Mode),
		Active:   st.Active,
		Message:  st.Message,
		StartsAt: st.StartsAt,
		EndsAt:   st.EndsAt,
		Org:      st.Org,
		Project:  st.Project,
		Notified: st.Notified,
	}
}

func toMaintenanceWindows(list []maintenance.Status) []model.MaintenanceWindowResponse {
	out := make([]model.MaintenanceWindowResponse, 0, len(list))
	for i := range list {
		out = append(out, *toMaintenanceWindow(&list[i]))
	}
	return out
}
