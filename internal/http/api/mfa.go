package api

import (
	"net/http"

	"github.com/nipalab/nipa/internal/http/handler"
	"github.com/nipalab/nipa/internal/http/model"

	restfulspec "github.com/emicklei/go-restful-openapi/v2"
	"github.com/emicklei/go-restful/v3"
)

func setupMFARouter(ws *restful.WebService, h *handler.Handler) {
	tags := []string{"Two-factor authentication"}

	ws.Route(
		ws.GET("/me/mfa").
			To(Wrap(h.GetMyMFA)).
			Doc("Two-factor authentication status of the authenticated user").
			Returns(http.StatusOK, "status", model.MFAStatusResponse{}).
			Operation("getMyMFA").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.POST("/me/mfa/totp").
			To(Wrap(h.StartMyTOTP)).
			Reads(model.MFAStartRequest{}).
			Doc("Start TOTP enrollment; returns the secret and its otpauth URI").
			Notes("Needs the current password; a passwordless account must have signed in within the last 10 minutes. Replaces an unconfirmed secret, 409 when already enabled, 503 when the server has no MFA_ENCRYPTION_KEY").
			Returns(http.StatusOK, "enrollment", model.TOTPEnrollmentResponse{}).
			Operation("startMyTOTP").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.POST("/me/mfa/totp/confirm").
			To(Wrap(h.ConfirmMyTOTP)).
			Reads(model.MFACodeRequest{}).
			Doc("Enable two-factor authentication with the first valid code").
			Notes("Returns the ten recovery codes; they are shown only once").
			Returns(http.StatusOK, "recovery codes", model.RecoveryCodesResponse{}).
			Operation("confirmMyTOTP").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.DELETE("/me/mfa/totp").
			To(Wrap(h.DisableMyTOTP)).
			Reads(model.MFACodeRequest{}).
			Doc("Disable two-factor authentication with a TOTP or recovery code").
			Returns(http.StatusOK, "disabled", model.MessageResponse{}).
			Operation("disableMyTOTP").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.POST("/me/mfa/recovery-codes").
			To(Wrap(h.RegenerateMyRecoveryCodes)).
			Reads(model.MFACodeRequest{}).
			Doc("Replace every recovery code; needs a TOTP or recovery code").
			Returns(http.StatusOK, "recovery codes", model.RecoveryCodesResponse{}).
			Operation("regenerateMyRecoveryCodes").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.DELETE("/users/{user}/mfa").
			To(Wrap(h.ResetUserMFA)).
			Param(ws.PathParameter("user", "user id (base36)")).
			Doc("Remove a user's two-factor authentication (global admin)").
			Returns(http.StatusOK, "reset", model.MessageResponse{}).
			Operation("resetUserMFA").
			Metadata(restfulspec.KeyOpenAPITags, tags))
}
