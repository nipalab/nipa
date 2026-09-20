package handler

import (
	nethttp "net/http"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/http"
	"github.com/nipalab/nipa/internal/http/model"
)

func (h *Handler) Me(appCtx http.AppContext) {
	claims := appCtx.Claims()
	if claims == nil {
		appCtx.HandleError(domain.NewErrorUnauthorized("authentication required"))
		return
	}
	user, err := h.useCase.User().Get(appCtx.Context(), claims.UserID)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, toUserResponse(user))
}

func (h *Handler) UpdateMyProfile(appCtx http.AppContext) {
	claims := appCtx.Claims()
	if claims == nil {
		appCtx.HandleError(domain.NewErrorUnauthorized("authentication required"))
		return
	}
	body := &model.UpdateProfileRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(err)
		return
	}
	user, err := h.useCase.User().UpdateProfile(appCtx.Context(), claims.UserID, body.Name, body.PhotoUrl)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, toUserResponse(user))
}

func (h *Handler) ChangeMyPassword(appCtx http.AppContext) {
	claims := appCtx.Claims()
	if claims == nil {
		appCtx.HandleError(domain.NewErrorUnauthorized("authentication required"))
		return
	}
	body := &model.ChangePasswordRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(err)
		return
	}
	if err := h.useCase.User().ChangePassword(appCtx.Context(), claims.UserID, body.OldPassword, body.NewPassword); err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, model.MessageResponse{Message: "password updated"})
}

func (h *Handler) ListUsers(appCtx http.AppContext) {
	users, err := h.useCase.User().List(appCtx.Context())
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	resp := make([]model.UserResponse, 0, len(users))
	for _, user := range users {
		resp = append(resp, toUserResponse(user))
	}
	appCtx.WriteJson(nethttp.StatusOK, resp)
}

func (h *Handler) CreateUser(appCtx http.AppContext) {
	body := &model.CreateUserRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(err)
		return
	}
	user, err := h.useCase.User().Create(appCtx.Context(), body.Name, body.Email, body.Password)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, toUserResponse(user))
}

func (h *Handler) UpdateUser(appCtx http.AppContext) {
	userID, err := parseID(appCtx.PathParameter("user"), "user")
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	body := &model.UpdateUserRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(err)
		return
	}
	user, err := h.useCase.User().UpdateEmail(appCtx.Context(), userID, body.Email)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, toUserResponse(user))
}

func (h *Handler) DeleteUser(appCtx http.AppContext) {
	userID, err := parseID(appCtx.PathParameter("user"), "user")
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	if err := h.useCase.User().Deactivate(appCtx.Context(), userID); err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, model.MessageResponse{Message: "user deactivated"})
}

func (h *Handler) ResetUserPassword(appCtx http.AppContext) {
	userID, err := parseID(appCtx.PathParameter("user"), "user")
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	body := &model.ResetPasswordRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(err)
		return
	}
	if err := h.useCase.User().ResetPassword(appCtx.Context(), userID, body.Password); err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, model.MessageResponse{Message: "password reset"})
}

func (h *Handler) UpdateUserFlags(appCtx http.AppContext) {
	userID, err := parseID(appCtx.PathParameter("user"), "user")
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	body := &model.UpdateUserFlagsRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(err)
		return
	}
	user, err := h.useCase.User().SetAdminFlags(appCtx.Context(), userID, body.IsAdmin, body.IsSuperAdmin)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, toUserResponse(user))
}
