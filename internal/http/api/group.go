package api

import (
	"net/http"

	"github.com/nipalab/nipa/internal/http/handler"
	"github.com/nipalab/nipa/internal/http/model"

	restfulspec "github.com/emicklei/go-restful-openapi/v2"
	"github.com/emicklei/go-restful/v3"
)

func setupGroupRouter(ws *restful.WebService, h *handler.Handler) {
	tags := []string{"Groups"}

	ws.Route(
		ws.GET("/orgs/{org}/groups").
			To(wrap(h.ListGroups)).
			Param(ws.PathParameter("org", "organization slug")).
			Doc("List organization groups (org owner or global admin)").
			Returns(http.StatusOK, "groups", []model.GroupResponse{}).
			Operation("listGroups").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.POST("/orgs/{org}/groups").
			To(wrap(h.CreateGroup)).
			Param(ws.PathParameter("org", "organization slug")).
			Reads(model.CreateGroupRequest{}).
			Doc("Create an organization group (org owner or global admin)").
			Returns(http.StatusOK, "created group", model.GroupResponse{}).
			Operation("createGroup").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.GET("/orgs/{org}/groups/{group}").
			To(wrap(h.GetGroup)).
			Param(ws.PathParameter("org", "organization slug")).
			Param(ws.PathParameter("group", "group id (base36)")).
			Doc("Get a group with its members (org owner or global admin)").
			Returns(http.StatusOK, "group", model.GroupResponse{}).
			Operation("getGroup").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.POST("/orgs/{org}/groups/{group}/members").
			To(wrap(h.AddGroupMember)).
			Param(ws.PathParameter("org", "organization slug")).
			Param(ws.PathParameter("group", "group id (base36)")).
			Reads(model.GroupMemberRequest{}).
			Doc("Add a user to a group (org owner or global admin)").
			Returns(http.StatusOK, "added", model.MessageResponse{}).
			Operation("addGroupMember").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.DELETE("/orgs/{org}/groups/{group}/members/{user}").
			To(wrap(h.RemoveGroupMember)).
			Param(ws.PathParameter("org", "organization slug")).
			Param(ws.PathParameter("group", "group id (base36)")).
			Param(ws.PathParameter("user", "user id (base36)")).
			Doc("Remove a user from a group (org owner or global admin)").
			Returns(http.StatusOK, "removed", model.MessageResponse{}).
			Operation("removeGroupMember").
			Metadata(restfulspec.KeyOpenAPITags, tags))
}
