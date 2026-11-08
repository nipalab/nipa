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
	_ = appCtx.WriteJson(nethttp.StatusOK, resp)
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
	_ = appCtx.WriteJson(nethttp.StatusOK, resp)
}

func (h *Handler) CreateOrg(appCtx http.AppContext) {
	body := &model.CreateOrgRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(err)
		return
	}
	org, err := h.useCase.Org().Create(appCtx.Context(), body.Name, body.Slug)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, model.OrgResponse{
		ID:   org.ID.Base36(),
		Slug: org.Slug,
		Name: org.Name,
		Role: domain.OrgRoleOwner,
	})
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
	if err := h.useCase.Org().EnsureOwner(appCtx.Context(), org.ID); err != nil {
		appCtx.HandleError(err)
		return
	}
	if body.UserID != "" && body.Email != "" {
		appCtx.HandleError(domain.NewErrorUser("provide either user_id or email, not both"))
		return
	}
	if !isGlobalAdmin(appCtx.Claims()) {
		if body.UserID != "" {
			appCtx.HandleError(domain.NewErrorUser("members join by invitation, invite them by email"))
			return
		}
		h.writeInvitationRequested(appCtx, org, body.Email, body.Role)
		return
	}
	role := strings.TrimSpace(body.Role)
	if role == "" {
		role = domain.OrgRoleMember
	}
	user, err := h.adminLookupUser(appCtx, body)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	if err := h.useCase.Org().AddMember(appCtx.Context(), org.ID, user.ID, role); err != nil {
		appCtx.HandleError(err)
		return
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, toOrgMemberResponse(&domain.OrgMember{User: *user, Role: role}))
}

func isGlobalAdmin(claims *domain.Claims) bool {
	return claims != nil && (claims.IsAdmin || claims.IsSuperAdmin)
}

// adminLookupUser resolves the target of a direct add. Unknown, deleted and
// unverified accounts answer the same not-found error.
func (h *Handler) adminLookupUser(appCtx http.AppContext, body *model.AddOrgMemberRequest) (*domain.User, error) {
	var (
		user *domain.User
		err  error
	)
	switch {
	case body.UserID != "":
		userID, perr := parseID(body.UserID, "user")
		if perr != nil {
			return nil, perr
		}
		user, err = h.useCase.User().Get(appCtx.Context(), userID)
	case body.Email != "":
		user, err = h.useCase.User().GetByEmail(appCtx.Context(), body.Email)
	default:
		return nil, domain.NewErrorUser("user_id or email is required")
	}
	if err != nil && !domain.IsErrorNotFound(err) {
		return nil, err
	}
	if err != nil || user.Deleted || !user.EmailVerified {
		return nil, domain.NewErrorNotFound("user not found")
	}
	return user, nil
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
	_ = appCtx.WriteJson(nethttp.StatusOK, toOrgMemberResponse(&domain.OrgMember{User: *user, Role: strings.TrimSpace(body.Role)}))
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
	_ = appCtx.WriteJson(nethttp.StatusOK, model.MessageResponse{Message: "member removed"})
}
