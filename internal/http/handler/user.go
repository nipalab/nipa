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
	appCtx.WriteJson(nethttp.StatusOK, model.MeResponse{
		ID:           user.ID.Base36(),
		Name:         user.Name,
		Email:        user.Email,
		PhotoUrl:     user.PhotoUrl,
		IsAdmin:      user.IsAdmin,
		IsSuperAdmin: user.IsSuperAdmin,
	})
}
