package api

import (
	"net/http"

	"github.com/nipalab/nipa/internal/http/handler"
	"github.com/nipalab/nipa/internal/http/model"

	restfulspec "github.com/emicklei/go-restful-openapi/v2"
	"github.com/emicklei/go-restful/v3"
)

func setupOrgInvitationRouter(ws *restful.WebService, h *handler.Handler) {
	tags := []string{"Invitations"}
	org := ws.PathParameter("org", "organization slug")
	id := ws.PathParameter("id", "invitation id (base36)")
	token := ws.PathParameter("token", "invitation token from the email link")

	ws.Route(ws.POST("/orgs/{org}/invitations").
		To(Wrap(h.CreateOrgInvitation)).
		Param(org).
		Reads(model.CreateOrgInvitationRequest{}).
		Doc("Invite an email address to the organization; the answer never reveals whether an account exists (org owner or global admin)").
		Returns(http.StatusAccepted, "invitation requested", model.OrgInvitationRequestedResponse{}).
		Operation("createOrgInvitation").
		Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(ws.GET("/orgs/{org}/invitations").
		To(Wrap(h.ListOrgInvitations)).
		Param(org).
		Param(ws.QueryParameter("status", "pending, accepted, declined, revoked or expired")).
		Doc("List organization invitations (org owner or global admin)").
		Returns(http.StatusOK, "invitations", []model.OrgInvitationResponse{}).
		Operation("listOrgInvitations").
		Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(ws.DELETE("/orgs/{org}/invitations/{id}").
		To(Wrap(h.RevokeOrgInvitation)).
		Param(org).
		Param(id).
		Doc("Revoke a pending invitation (org owner or global admin)").
		Returns(http.StatusOK, "revoked", model.MessageResponse{}).
		Operation("revokeOrgInvitation").
		Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(ws.POST("/orgs/{org}/invitations/{id}/resend").
		To(Wrap(h.ResendOrgInvitation)).
		Param(org).
		Param(id).
		Doc("Issue a fresh token and expiry for a pending invitation and mail it again (org owner or global admin)").
		Returns(http.StatusAccepted, "invitation requested", model.OrgInvitationRequestedResponse{}).
		Operation("resendOrgInvitation").
		Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(ws.GET("/me/invitations").
		To(Wrap(h.ListMyInvitations)).
		Doc("List live invitations addressed to the caller's verified email").
		Returns(http.StatusOK, "invitations", []model.OrgInvitationResponse{}).
		Operation("listMyInvitations").
		Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(ws.POST("/me/invitations/{id}/accept").
		To(Wrap(h.AcceptMyInvitation)).
		Param(id).
		Doc("Accept an invitation addressed to the caller's verified email").
		Returns(http.StatusOK, "accepted", model.OrgInvitationResponse{}).
		Operation("acceptMyInvitation").
		Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(ws.POST("/me/invitations/{id}/decline").
		To(Wrap(h.DeclineMyInvitation)).
		Param(id).
		Doc("Decline an invitation addressed to the caller's verified email").
		Returns(http.StatusOK, "declined", model.MessageResponse{}).
		Operation("declineMyInvitation").
		Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(ws.GET("/invitations/{token}").
		To(Wrap(h.GetInvitation)).
		Param(token).
		Doc("Preview an invitation by its token (the caller's verified email must match)").
		Returns(http.StatusOK, "invitation", model.OrgInvitationResponse{}).
		Operation("getInvitation").
		Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(ws.POST("/invitations/{token}/accept").
		To(Wrap(h.AcceptInvitation)).
		Param(token).
		Doc("Accept an invitation by its token").
		Returns(http.StatusOK, "accepted", model.OrgInvitationResponse{}).
		Operation("acceptInvitation").
		Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(ws.POST("/invitations/{token}/decline").
		To(Wrap(h.DeclineInvitation)).
		Param(token).
		Doc("Decline an invitation by its token").
		Returns(http.StatusOK, "declined", model.MessageResponse{}).
		Operation("declineInvitation").
		Metadata(restfulspec.KeyOpenAPITags, tags))
}
