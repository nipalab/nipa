package handler

import (
	nethttp "net/http"
	"strconv"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/http"
	"github.com/nipalab/nipa/internal/http/model"
)

func (h *Handler) ListProjectRules(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	rules, err := h.useCase.Permission().ListRules(appCtx.Context(), project.ID)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	resp := make([]model.PBACRuleResponse, 0, len(rules))
	for _, rule := range rules {
		resp = append(resp, toPBACRuleResponse(rule))
	}
	appCtx.WriteJson(nethttp.StatusOK, resp)
}

func (h *Handler) CreateProjectRule(appCtx http.AppContext) {
	org, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	body := &model.CreatePBACRuleRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(err)
		return
	}
	rule := domain.PBACRule{
		OrgID:      org.ID,
		ProjectID:  &project.ID,
		PathPrefix: body.PathPrefix,
		Permission: domain.Permission(body.Permission),
	}
	if body.UserID != "" {
		id, err := parseID(body.UserID, "user")
		if err != nil {
			appCtx.HandleError(err)
			return
		}
		rule.UserID = &id
	}
	if body.GroupID != "" {
		id, err := parseID(body.GroupID, "group")
		if err != nil {
			appCtx.HandleError(err)
			return
		}
		rule.GroupID = &id
	}
	created, err := h.useCase.Permission().CreateRule(appCtx.Context(), rule)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, toPBACRuleResponse(created))
}

func (h *Handler) DeleteProjectRule(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	ruleID, err := strconv.ParseInt(appCtx.PathParameter("id"), 10, 64)
	if err != nil {
		appCtx.HandleError(domain.NewErrorUser("invalid rule id"))
		return
	}
	if err := h.useCase.Permission().DeleteRule(appCtx.Context(), project.ID, ruleID); err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, model.MessageResponse{Message: "rule deleted"})
}

func (h *Handler) ListProjectDefaults(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	defaults, err := h.useCase.Permission().ListPathPermissions(appCtx.Context(), project.ID)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	resp := make([]model.PermissionEntry, 0, len(defaults))
	for _, entry := range defaults {
		resp = append(resp, model.PermissionEntry{
			PathPrefix: entry.PathPrefix,
			Permission: uint64(entry.Permission),
		})
	}
	appCtx.WriteJson(nethttp.StatusOK, resp)
}

func (h *Handler) SetProjectDefault(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	body := &model.SetPathPermissionRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(err)
		return
	}
	entry, err := h.useCase.Permission().SetPathPermission(
		appCtx.Context(), project.ID, body.PathPrefix, domain.Permission(body.Permission),
	)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, model.PermissionEntry{
		PathPrefix: entry.PathPrefix,
		Permission: uint64(entry.Permission),
	})
}

func (h *Handler) DeleteProjectDefault(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	path := appCtx.QueryParameter("path")
	if err := h.useCase.Permission().DeletePathPermission(appCtx.Context(), project.ID, path); err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, model.MessageResponse{Message: "default removed"})
}

func toPBACRuleResponse(rule *domain.PBACRule) model.PBACRuleResponse {
	resp := model.PBACRuleResponse{
		ID:         rule.ID,
		PathPrefix: rule.PathPrefix,
		Permission: uint64(rule.Permission),
	}
	if rule.UserID != nil {
		resp.UserID = rule.UserID.Base36()
	}
	if rule.GroupID != nil {
		resp.GroupID = rule.GroupID.Base36()
	}
	return resp
}
