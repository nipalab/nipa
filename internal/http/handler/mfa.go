package handler

import (
	nethttp "net/http"

	"github.com/nipalab/nipa/internal/http"
	"github.com/nipalab/nipa/internal/http/model"
	"github.com/nipalab/nipa/internal/usecase/dto"
)

// WriteLoginResult answers a completed first factor: the session with its
// refresh cookie, or the MFA challenge without any cookie.
func WriteLoginResult(appCtx http.AppContext, result *dto.LoginResult) {
	if result.MFAChallenge != nil {
		_ = appCtx.WriteJson(nethttp.StatusOK, model.MFAChallengeResponse{
			MFARequired:  true,
			MFAChallenge: result.MFAChallenge.Token,
			ExpiresIn:    result.MFAChallenge.ExpiresIn,
		})
		return
	}
	appCtx.SetCookie(RefreshCookie(result.RefreshToken, result.RefreshExpiresIn))
	_ = appCtx.WriteJson(nethttp.StatusOK, model.LoginResponse{
		AccessToken: result.AccessToken,
		TokenType:   result.TokenType,
		ExpiresIn:   result.ExpiresIn,
	})
}

func (h *Handler) AuthVerifyMFA(appCtx http.AppContext) {
	body := &model.MFAVerifyRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(err)
		return
	}
	result, err := h.useCase.Auth().CompleteLoginMFA(appCtx.Context(), body.Challenge, body.Code)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	WriteLoginResult(appCtx, result)
}

func (h *Handler) GetMyMFA(appCtx http.AppContext) {
	status, err := h.useCase.MFA().Status(appCtx.Context())
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, model.MFAStatusResponse{
		Available:         status.Available,
		TOTP:              model.TOTPStatusResponse{Enabled: status.TOTPEnabled, ConfirmedAt: status.ConfirmedAt},
		RecoveryCodesLeft: status.RecoveryCodesLeft,
	})
}

func (h *Handler) StartMyTOTP(appCtx http.AppContext) {
	body := &model.MFAStartRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(err)
		return
	}
	enrollment, err := h.useCase.MFA().StartTOTP(appCtx.Context(), body.Password)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, model.TOTPEnrollmentResponse{Secret: enrollment.Secret, URI: enrollment.URI})
}

func (h *Handler) ConfirmMyTOTP(appCtx http.AppContext) {
	body := &model.MFACodeRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(err)
		return
	}
	codes, err := h.useCase.MFA().ConfirmTOTP(appCtx.Context(), body.Code)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, model.RecoveryCodesResponse{RecoveryCodes: codes.Codes})
}

func (h *Handler) DisableMyTOTP(appCtx http.AppContext) {
	body := &model.MFACodeRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(err)
		return
	}
	if err := h.useCase.MFA().DisableTOTP(appCtx.Context(), body.Code); err != nil {
		appCtx.HandleError(err)
		return
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, model.MessageResponse{Message: "two-factor authentication disabled"})
}

func (h *Handler) RegenerateMyRecoveryCodes(appCtx http.AppContext) {
	body := &model.MFACodeRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(err)
		return
	}
	codes, err := h.useCase.MFA().RegenerateRecoveryCodes(appCtx.Context(), body.Code)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, model.RecoveryCodesResponse{RecoveryCodes: codes.Codes})
}

func (h *Handler) ResetUserMFA(appCtx http.AppContext) {
	userID, err := parseID(appCtx.PathParameter("user"), "user")
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	if err := h.useCase.MFA().AdminReset(appCtx.Context(), userID); err != nil {
		appCtx.HandleError(err)
		return
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, model.MessageResponse{Message: "two-factor authentication reset"})
}
