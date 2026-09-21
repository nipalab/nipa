package handler

import (
	nethttp "net/http"
	"strings"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/http"
	"github.com/nipalab/nipa/internal/http/model"
)

func (h *Handler) ListMyOrgs(appCtx http.AppContext) {
	claims := appCtx.Claims()
	if claims == nil {
		appCtx.HandleError(domain.NewErrorUnauthorized("authentication required"))
		return
	}
	memberships, err := h.useCase.Org().ListForUser(appCtx.Context(), claims.UserID)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	resp := make([]model.OrgResponse, 0, len(memberships))
	for _, membership := range memberships {
		resp = append(resp, model.OrgResponse{
			ID:   membership.Org.ID.Base36(),
			Slug: membership.Org.Slug,
			Name: membership.Org.Name,
			Role: membership.Role,
		})
	}
	appCtx.WriteJson(nethttp.StatusOK, resp)
}

func (h *Handler) ListOrgMembers(appCtx http.AppContext) {
	org, err := h.resolveOrg(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	members, err := h.useCase.Org().ListMembers(appCtx.Context(), org.ID)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	resp := make([]model.OrgMemberResponse, 0, len(members))
	for _, member := range members {
		resp = append(resp, toOrgMemberResponse(member))
	}
	appCtx.WriteJson(nethttp.StatusOK, resp)
}

func (h *Handler) AddOrgMember(appCtx http.AppContext) {
	org, err := h.resolveOrg(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	body := &model.AddOrgMemberRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(err)
		return
	}
	role := strings.TrimSpace(body.Role)
	if role == "" {
		role = domain.OrgRoleMember
	}

	var user *domain.User
	switch {
	case body.UserID != "" && body.Email != "":
		appCtx.HandleError(domain.NewErrorUser("provide either user_id or email, not both"))
		return
	case body.UserID != "":
		userID, err := parseID(body.UserID, "user")
		if err != nil {
			appCtx.HandleError(err)
			return
		}
		user, err = h.useCase.User().Get(appCtx.Context(), userID)
		if err != nil {
			appCtx.HandleError(err)
			return
		}
	case body.Email != "":
		user, err = h.useCase.User().GetByEmail(appCtx.Context(), body.Email)
		if err != nil {
			appCtx.HandleError(err)
			return
		}
	default:
		appCtx.HandleError(domain.NewErrorUser("user_id or email is required"))
		return
	}

	if err := h.useCase.Org().AddMember(appCtx.Context(), org.ID, user.ID, role); err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, toOrgMemberResponse(&domain.OrgMember{User: *user, Role: role}))
}

func (h *Handler) UpdateOrgMember(appCtx http.AppContext) {
	org, err := h.resolveOrg(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	userID, err := parseID(appCtx.PathParameter("user"), "user")
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	body := &model.UpdateOrgMemberRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(err)
		return
	}
	if err := h.useCase.Org().UpdateMemberRole(appCtx.Context(), org.ID, userID, strings.TrimSpace(body.Role)); err != nil {
		appCtx.HandleError(err)
		return
	}
	user, err := h.useCase.User().Get(appCtx.Context(), userID)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, toOrgMemberResponse(&domain.OrgMember{User: *user, Role: strings.TrimSpace(body.Role)}))
}

func (h *Handler) RemoveOrgMember(appCtx http.AppContext) {
	org, err := h.resolveOrg(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	userID, err := parseID(appCtx.PathParameter("user"), "user")
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	if err := h.useCase.Org().RemoveMember(appCtx.Context(), org.ID, userID); err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, model.MessageResponse{Message: "member removed"})
}
