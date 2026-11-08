package handler

import (
	nethttp "net/http"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/http"
	"github.com/nipalab/nipa/internal/http/model"
	"github.com/nipalab/nipa/internal/snow"
)

const invitationRequestedStatus = "invited"

func (h *Handler) CreateOrgInvitation(appCtx http.AppContext) {
	org, err := h.resolveOrg(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	body := &model.CreateOrgInvitationRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(err)
		return
	}
	h.writeInvitationRequested(appCtx, org, body.Email, body.Role)
}

func (h *Handler) writeInvitationRequested(appCtx http.AppContext, org *domain.Organization, email, role string) {
	if err := h.useCase.OrgInvitation().Create(appCtx.Context(), org, email, role); err != nil {
		appCtx.HandleError(err)
		return
	}
	_ = appCtx.WriteJson(nethttp.StatusAccepted, model.OrgInvitationRequestedResponse{Status: invitationRequestedStatus})
}

func (h *Handler) ListOrgInvitations(appCtx http.AppContext) {
	org, err := h.resolveOrg(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	invs, err := h.useCase.OrgInvitation().List(appCtx.Context(), org.ID, appCtx.QueryParameter("status"))
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	writeInvitations(appCtx, invs)
}

func (h *Handler) RevokeOrgInvitation(appCtx http.AppContext) {
	org, id, ok := h.orgAndInvitationID(appCtx)
	if !ok {
		return
	}
	if err := h.useCase.OrgInvitation().Revoke(appCtx.Context(), org.ID, id); err != nil {
		appCtx.HandleError(err)
		return
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, model.MessageResponse{Message: "invitation revoked"})
}

func (h *Handler) ResendOrgInvitation(appCtx http.AppContext) {
	org, id, ok := h.orgAndInvitationID(appCtx)
	if !ok {
		return
	}
	if err := h.useCase.OrgInvitation().Resend(appCtx.Context(), org.ID, id); err != nil {
		appCtx.HandleError(err)
		return
	}
	_ = appCtx.WriteJson(nethttp.StatusAccepted, model.OrgInvitationRequestedResponse{Status: invitationRequestedStatus})
}

func (h *Handler) orgAndInvitationID(appCtx http.AppContext) (*domain.Organization, snow.ID, bool) {
	org, err := h.resolveOrg(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return nil, 0, false
	}
	id, err := parseID(appCtx.PathParameter("id"), "invitation")
	if err != nil {
		appCtx.HandleError(err)
		return nil, 0, false
	}
	return org, id, true
}

func (h *Handler) ListMyInvitations(appCtx http.AppContext) {
	invs, err := h.useCase.OrgInvitation().ListMine(appCtx.Context())
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	writeInvitations(appCtx, invs)
}

func (h *Handler) GetInvitation(appCtx http.AppContext) {
	inv, err := h.useCase.OrgInvitation().Preview(appCtx.Context(), appCtx.PathParameter("token"))
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, toInvitationResponse(inv))
}

func (h *Handler) AcceptInvitation(appCtx http.AppContext) {
	inv, err := h.useCase.OrgInvitation().Accept(appCtx.Context(), appCtx.PathParameter("token"))
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, toInvitationResponse(inv))
}

func (h *Handler) DeclineInvitation(appCtx http.AppContext) {
	if err := h.useCase.OrgInvitation().Decline(appCtx.Context(), appCtx.PathParameter("token")); err != nil {
		appCtx.HandleError(err)
		return
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, model.MessageResponse{Message: "invitation declined"})
}

func (h *Handler) AcceptMyInvitation(appCtx http.AppContext) {
	id, err := parseID(appCtx.PathParameter("id"), "invitation")
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	inv, err := h.useCase.OrgInvitation().AcceptByID(appCtx.Context(), id)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, toInvitationResponse(inv))
}

func (h *Handler) DeclineMyInvitation(appCtx http.AppContext) {
	id, err := parseID(appCtx.PathParameter("id"), "invitation")
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	if err := h.useCase.OrgInvitation().DeclineByID(appCtx.Context(), id); err != nil {
		appCtx.HandleError(err)
		return
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, model.MessageResponse{Message: "invitation declined"})
}

func writeInvitations(appCtx http.AppContext, invs []*domain.OrgInvitation) {
	out := make([]model.OrgInvitationResponse, 0, len(invs))
	for _, inv := range invs {
		out = append(out, toInvitationResponse(inv))
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, out)
}

func toInvitationResponse(inv *domain.OrgInvitation) model.OrgInvitationResponse {
	return model.OrgInvitationResponse{
		ID:          inv.ID.Base36(),
		OrgID:       inv.OrgID.Base36(),
		OrgSlug:     inv.OrgSlug,
		OrgName:     inv.OrgName,
		Email:       inv.Email,
		Role:        inv.Role,
		Status:      inv.Status,
		InvitedBy:   inv.InvitedBy.Base36(),
		InviterName: inv.InviterName,
		ExpiresAt:   inv.ExpiresAt,
		CreatedAt:   inv.CreatedAt,
		AcceptedAt:  inv.AcceptedAt,
	}
}
