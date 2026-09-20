package handler

import (
	nethttp "net/http"

	"github.com/nipalab/nipa/internal/http"
	"github.com/nipalab/nipa/internal/http/model"
)

func (h *Handler) ListGroups(appCtx http.AppContext) {
	org, err := h.resolveOrg(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	groups, err := h.useCase.Group().List(appCtx.Context(), org.ID)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	resp := make([]model.GroupResponse, 0, len(groups))
	for _, group := range groups {
		resp = append(resp, toGroupResponse(group, nil))
	}
	appCtx.WriteJson(nethttp.StatusOK, resp)
}

func (h *Handler) CreateGroup(appCtx http.AppContext) {
	org, err := h.resolveOrg(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	body := &model.CreateGroupRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(err)
		return
	}
	group, err := h.useCase.Group().Create(appCtx.Context(), org.ID, body.Name, body.Description)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, toGroupResponse(group, nil))
}

func (h *Handler) GetGroup(appCtx http.AppContext) {
	org, err := h.resolveOrg(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	groupID, err := parseID(appCtx.PathParameter("group"), "group")
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	group, err := h.useCase.Group().Get(appCtx.Context(), org.ID, groupID)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	members, err := h.useCase.Group().Members(appCtx.Context(), org.ID, groupID)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, toGroupResponse(group, members))
}

func (h *Handler) AddGroupMember(appCtx http.AppContext) {
	org, err := h.resolveOrg(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	groupID, err := parseID(appCtx.PathParameter("group"), "group")
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	body := &model.GroupMemberRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(err)
		return
	}
	userID, err := parseID(body.UserID, "user")
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	if err := h.useCase.Group().AddMember(appCtx.Context(), org.ID, groupID, userID); err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, model.MessageResponse{Message: "member added"})
}

func (h *Handler) RemoveGroupMember(appCtx http.AppContext) {
	org, err := h.resolveOrg(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	groupID, err := parseID(appCtx.PathParameter("group"), "group")
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	userID, err := parseID(appCtx.PathParameter("user"), "user")
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	if err := h.useCase.Group().RemoveMember(appCtx.Context(), org.ID, groupID, userID); err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, model.MessageResponse{Message: "member removed"})
}
