package api

import (
	"net/http"

	"github.com/nipalab/nipa/internal/http/handler"
	"github.com/nipalab/nipa/internal/http/model"

	restfulspec "github.com/emicklei/go-restful-openapi/v2"
	"github.com/emicklei/go-restful/v3"
)

func setupOrgRouter(ws *restful.WebService, h *handler.Handler) {
	tags := []string{"Organizations"}

	ws.Route(
		ws.GET("/orgs").
			To(wrap(h.ListMyOrgs)).
			Doc("List the organizations the caller belongs to").
			Returns(http.StatusOK, "organizations", []model.OrgResponse{}).
			Operation("listMyOrgs").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.GET("/orgs/{org}/members").
			To(wrap(h.ListOrgMembers)).
			Param(ws.PathParameter("org", "organization slug")).
			Doc("List organization members (org owner or global admin)").
			Returns(http.StatusOK, "members", []model.OrgMemberResponse{}).
			Operation("listOrgMembers").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.POST("/orgs/{org}/members").
			To(wrap(h.AddOrgMember)).
			Param(ws.PathParameter("org", "organization slug")).
			Reads(model.AddOrgMemberRequest{}).
			Doc("Add an existing user to the organization (org owner or global admin)").
			Returns(http.StatusOK, "member", model.OrgMemberResponse{}).
			Operation("addOrgMember").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.PATCH("/orgs/{org}/members/{user}").
			To(wrap(h.UpdateOrgMember)).
			Param(ws.PathParameter("org", "organization slug")).
			Param(ws.PathParameter("user", "user id (base36)")).
			Reads(model.UpdateOrgMemberRequest{}).
			Doc("Change an organization member role (org owner or global admin)").
			Returns(http.StatusOK, "member", model.OrgMemberResponse{}).
			Operation("updateOrgMember").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.DELETE("/orgs/{org}/members/{user}").
			To(wrap(h.RemoveOrgMember)).
			Param(ws.PathParameter("org", "organization slug")).
			Param(ws.PathParameter("user", "user id (base36)")).
			Doc("Remove an organization member (org owner or global admin)").
			Returns(http.StatusOK, "removed", model.MessageResponse{}).
			Operation("removeOrgMember").
			Metadata(restfulspec.KeyOpenAPITags, tags))
}
