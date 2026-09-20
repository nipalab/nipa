package handler

import (
	nethttp "net/http"
	"time"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/http"
	"github.com/nipalab/nipa/internal/http/model"
)

const (
	refreshCookieName = "nipa_refresh"
	refreshCookiePath = "/api/v1/auth"
)

func (h *Handler) AuthLogin(appCtx http.AppContext) {
	body := &model.LoginRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(err)
		return
	}
	result, err := h.useCase.Auth().LoginWithEmailPassword(appCtx.Context(), body.Email, body.Password)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.SetCookie(refreshCookie(result.RefreshToken, result.RefreshExpiresIn))
	appCtx.WriteJson(nethttp.StatusOK, model.LoginResponse{
		AccessToken: result.AccessToken,
		TokenType:   result.TokenType,
		ExpiresIn:   result.ExpiresIn,
	})
}

func (h *Handler) AuthRefreshToken(appCtx http.AppContext) {
	cookie, err := appCtx.Cookie(refreshCookieName)
	if err != nil || cookie.Value == "" {
		appCtx.HandleError(domain.NewErrorUnauthorized("refresh token required"))
		return
	}
	result, err := h.useCase.Auth().LoginWithRefreshToken(appCtx.Context(), cookie.Value)
	if err != nil {
		clearRefreshCookie(appCtx)
		appCtx.HandleError(err)
		return
	}
	appCtx.SetCookie(refreshCookie(result.RefreshToken, result.RefreshExpiresIn))
	appCtx.WriteJson(nethttp.StatusOK, model.LoginResponse{
		AccessToken: result.AccessToken,
		TokenType:   result.TokenType,
		ExpiresIn:   result.ExpiresIn,
	})
}

func (h *Handler) AuthLogout(appCtx http.AppContext) {
	if cookie, err := appCtx.Cookie(refreshCookieName); err == nil && cookie.Value != "" {
		_ = h.useCase.Auth().Logout(appCtx.Context(), cookie.Value)
	}
	clearRefreshCookie(appCtx)
	appCtx.WriteJson(nethttp.StatusOK, model.MessageResponse{Message: "logged out"})
}

func refreshCookie(token string, maxAge int) *nethttp.Cookie {
	return &nethttp.Cookie{
		Name:     refreshCookieName,
		Value:    token,
		Path:     refreshCookiePath,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   true,
		SameSite: nethttp.SameSiteStrictMode,
	}
}

func clearRefreshCookie(appCtx http.AppContext) {
	appCtx.SetCookie(&nethttp.Cookie{
		Name:     refreshCookieName,
		Value:    "",
		Path:     refreshCookiePath,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
		HttpOnly: true,
		Secure:   true,
		SameSite: nethttp.SameSiteStrictMode,
	})
}
