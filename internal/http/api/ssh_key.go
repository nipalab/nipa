package api

import (
	"net/http"

	"github.com/nipalab/nipa/internal/http/handler"
	"github.com/nipalab/nipa/internal/http/model"

	restfulspec "github.com/emicklei/go-restful-openapi/v2"
	"github.com/emicklei/go-restful/v3"
)

func setupSSHKeyRouter(ws *restful.WebService, h *handler.Handler) {
	tags := []string{"SSH keys"}

	ws.Route(
		ws.GET("/keys").
			To(Wrap(h.ListMySSHKeys)).
			Doc("List the authenticated user's SSH public keys").
			Returns(http.StatusOK, "SSH keys", []model.SSHKeyResponse{}).
			Operation("listMySSHKeys").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.POST("/keys").
			To(Wrap(h.AddMySSHKey)).
			Reads(model.AddSSHKeyRequest{}).
			Doc("Register an SSH public key for the authenticated user (optional name, falls back to the key comment)").
			Returns(http.StatusOK, "created SSH key", model.SSHKeyResponse{}).
			Operation("addMySSHKey").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.DELETE("/keys/{key}").
			To(Wrap(h.RemoveMySSHKey)).
			Param(ws.PathParameter("key", "SSH key id (base36)")).
			Doc("Remove one of the authenticated user's SSH public keys").
			Returns(http.StatusOK, "removed", model.MessageResponse{}).
			Operation("removeMySSHKey").
			Metadata(restfulspec.KeyOpenAPITags, tags))
}
