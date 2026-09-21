package api

import (
	"net/http"

	"github.com/nipalab/nipa/internal/http/handler"
	"github.com/nipalab/nipa/internal/http/model"

	restfulspec "github.com/emicklei/go-restful-openapi/v2"
	"github.com/emicklei/go-restful/v3"
)

func setupUserRouter(ws *restful.WebService, h *handler.Handler) {
	tags := []string{"Users"}

	ws.Route(
		ws.GET("/me").
			To(wrap(h.Me)).
			Doc("Get the authenticated user profile").
			Returns(http.StatusOK, "current user", model.UserResponse{}).
			Operation("getMe").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.PATCH("/me").
			To(wrap(h.UpdateMyProfile)).
			Reads(model.UpdateProfileRequest{}).
			Doc("Update the authenticated user profile").
			Returns(http.StatusOK, "updated user", model.UserResponse{}).
			Operation("updateMyProfile").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.POST("/me/password").
			To(wrap(h.ChangeMyPassword)).
			Reads(model.ChangePasswordRequest{}).
			Doc("Change the authenticated user password").
			Returns(http.StatusOK, "updated", model.MessageResponse{}).
			Operation("changeMyPassword").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.GET("/users").
			To(wrap(h.ListUsers)).
			Doc("List all users (global admin)").
			Returns(http.StatusOK, "users", []model.UserResponse{}).
			Operation("listUsers").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.POST("/users").
			To(wrap(h.CreateUser)).
			Reads(model.CreateUserRequest{}).
			Doc("Create a user (global admin)").
			Returns(http.StatusOK, "created user", model.UserResponse{}).
			Operation("createUser").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.PATCH("/users/{user}").
			To(wrap(h.UpdateUser)).
			Param(ws.PathParameter("user", "user id (base36)")).
			Reads(model.UpdateUserRequest{}).
			Doc("Update a user email (global admin)").
			Returns(http.StatusOK, "updated user", model.UserResponse{}).
			Operation("updateUser").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.DELETE("/users/{user}").
			To(wrap(h.DeleteUser)).
			Param(ws.PathParameter("user", "user id (base36)")).
			Doc("Deactivate a user (global admin)").
			Returns(http.StatusOK, "deactivated", model.MessageResponse{}).
			Operation("deleteUser").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.POST("/users/{user}/password").
			To(wrap(h.ResetUserPassword)).
			Param(ws.PathParameter("user", "user id (base36)")).
			Reads(model.ResetPasswordRequest{}).
			Doc("Reset a user password (global admin)").
			Returns(http.StatusOK, "reset", model.MessageResponse{}).
			Operation("resetUserPassword").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.PATCH("/users/{user}/admin").
			To(wrap(h.UpdateUserFlags)).
			Param(ws.PathParameter("user", "user id (base36)")).
			Reads(model.UpdateUserFlagsRequest{}).
			Doc("Set user admin flags (super admin)").
			Returns(http.StatusOK, "updated user", model.UserResponse{}).
			Operation("updateUserFlags").
			Metadata(restfulspec.KeyOpenAPITags, tags))
}
