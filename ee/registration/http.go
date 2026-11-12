package registration

import (
	nethttp "net/http"
	"strings"
	"time"

	restfulspec "github.com/emicklei/go-restful-openapi/v2"
	"github.com/emicklei/go-restful/v3"

	"github.com/nipalab/nipa/internal/domain"
	httpApp "github.com/nipalab/nipa/internal/http"
	"github.com/nipalab/nipa/internal/http/api"
	"github.com/nipalab/nipa/internal/http/handler"
	"github.com/nipalab/nipa/internal/http/model"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

type Handler struct {
	reg       *Registration
	auth      *usecase.Auth
	providers func() []string
	billing   *BillingCapabilities
}

// BillingCapabilities tells the SPA which billing provider runs and which
// prices it may preview, plus the entitlement sizes the plan editor shows.
// Price ids and the client token are public by design.
type BillingCapabilities struct {
	Provider    string        `json:"provider"`
	Environment string        `json:"environment"`
	ClientToken string        `json:"client_token"`
	Prices      BillingPrices `json:"prices"`

	FreeSeats         int64 `json:"free_seats"`
	FreeQuotaBytes    int64 `json:"free_quota_bytes"`
	SeatIncludedBytes int64 `json:"seat_included_bytes"`
	StoragePackBytes  int64 `json:"storage_pack_bytes"`
}

type BillingPrices struct {
	Seat    IntervalPrices `json:"seat"`
	Storage IntervalPrices `json:"storage"`
}

type IntervalPrices struct {
	Monthly string `json:"monthly"`
	Yearly  string `json:"yearly"`
}

func NewHandler(reg *Registration, auth *usecase.Auth) *Handler {
	return &Handler{reg: reg, auth: auth}
}

// WithProviders exposes the enabled social providers through the capabilities
// endpoint; without it the provider list stays empty.
func (h *Handler) WithProviders(providers func() []string) *Handler {
	h.providers = providers
	return h
}

// WithBilling adds the billing block to the capabilities endpoint; without it
// the block is omitted and the SPA hides billing.
func (h *Handler) WithBilling(billing *BillingCapabilities) *Handler {
	h.billing = billing
	return h
}

type capabilitiesResponse struct {
	Registration bool                 `json:"registration"`
	Providers    []string             `json:"providers"`
	Billing      *BillingCapabilities `json:"billing,omitempty"`
	Terms        *termsCapabilities   `json:"terms,omitempty"`
}

type termsCapabilities struct {
	Version string `json:"version"`
}

type registerRequest struct {
	Name        string `json:"name"`
	Email       string `json:"email"`
	AcceptTerms bool   `json:"accept_terms"`
}

type termsStatusResponse struct {
	Version         string `json:"version"`
	AcceptedVersion string `json:"accepted_version,omitempty"`
	AcceptedAt      string `json:"accepted_at,omitempty"`
	Required        bool   `json:"required"`
}

type acceptTermsRequest struct {
	Version string `json:"version"`
}

type registerResponse struct {
	Status string `json:"status"`
	Email  string `json:"email"`
}

type resendRequest struct {
	Email string `json:"email"`
}

type verifyRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

// RegisterPublicRoutes attaches the unauthenticated registration endpoints to
// the /api/v1/auth WebService.
func RegisterPublicRoutes(ws *restful.WebService, h *Handler) {
	tags := []string{"Auth"}

	ws.Route(
		ws.GET("/capabilities").
			To(api.Wrap(h.Capabilities)).
			Doc("Report the enabled onboarding features").
			Notes("Lets the SPA show the registration and social login entry points; 404 on servers without them").
			Returns(nethttp.StatusOK, "onboarding capabilities", capabilitiesResponse{}).
			Operation("onboardingCapabilities").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.POST("/register").
			To(api.Wrap(h.Register)).
			Reads(registerRequest{}).
			Doc("Create an account").
			Notes("Creates an inactive account and sends a confirmation email; the account can log in after confirming").
			Returns(nethttp.StatusAccepted, "verification pending", registerResponse{}).
			Operation("register").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.POST("/register/resend").
			To(api.Wrap(h.ResendVerification)).
			Reads(resendRequest{}).
			Doc("Resend the confirmation email").
			Notes("Always succeeds so the endpoint cannot be used to probe for accounts").
			Returns(nethttp.StatusOK, "accepted", model.MessageResponse{}).
			Operation("resendVerification").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.POST("/verify").
			To(api.Wrap(h.Verify)).
			Reads(verifyRequest{}).
			Doc("Confirm an email address").
			Notes("Consumes the one-time confirmation link and returns a session like a login").
			Returns(nethttp.StatusOK, "access token", model.LoginResponse{}).
			Operation("verifyEmail").
			Metadata(restfulspec.KeyOpenAPITags, tags))
}

// RegisterRoutes attaches the authenticated terms endpoints to the /api/v1
// WebService.
func RegisterRoutes(ws *restful.WebService, h *Handler) {
	tags := []string{"Auth"}

	ws.Route(
		ws.GET("/me/terms").
			To(api.Wrap(h.TermsStatus)).
			Doc("Report the terms version the account accepted").
			Returns(nethttp.StatusOK, "terms status", termsStatusResponse{}).
			Operation("termsStatus").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.POST("/me/terms").
			To(api.Wrap(h.AcceptTerms)).
			Reads(acceptTermsRequest{}).
			Doc("Accept the current terms of service and privacy policy").
			Notes("The version must be the current one; an outdated version is a 409").
			Returns(nethttp.StatusOK, "terms status", termsStatusResponse{}).
			Operation("acceptTerms").
			Metadata(restfulspec.KeyOpenAPITags, tags))
}

func (h *Handler) Capabilities(appCtx httpApp.AppContext) {
	providers := []string{}
	if h.providers != nil {
		providers = h.providers()
	}
	if providers == nil {
		providers = []string{}
	}
	response := capabilitiesResponse{Registration: true, Providers: providers, Billing: h.billing}
	if version := h.reg.TermsVersion(); version != "" {
		response.Terms = &termsCapabilities{Version: version}
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, response)
}

func (h *Handler) Register(appCtx httpApp.AppContext) {
	body := &registerRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(err)
		return
	}
	if err := h.reg.Register(appCtx.Context(), body.Name, body.Email, body.AcceptTerms); err != nil {
		appCtx.HandleError(err)
		return
	}
	_ = appCtx.WriteJson(nethttp.StatusAccepted, registerResponse{Status: "pending_verification", Email: strings.TrimSpace(body.Email)})
}

func (h *Handler) ResendVerification(appCtx httpApp.AppContext) {
	body := &resendRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(err)
		return
	}
	if err := h.reg.ResendVerification(appCtx.Context(), body.Email); err != nil {
		appCtx.HandleError(err)
		return
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, model.MessageResponse{Message: "if the account exists and is unconfirmed, a new confirmation email was sent"})
}

func (h *Handler) Verify(appCtx httpApp.AppContext) {
	body := &verifyRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(err)
		return
	}
	user, err := h.reg.Verify(appCtx.Context(), body.Token, body.Password)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	result, err := h.auth.LoginResultVia(appCtx.Context(), user, usecase.LoginMethodEmailVerification)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.SetCookie(handler.RefreshCookie(result.RefreshToken, result.RefreshExpiresIn))
	_ = appCtx.WriteJson(nethttp.StatusOK, model.LoginResponse{
		AccessToken: result.AccessToken,
		TokenType:   result.TokenType,
		ExpiresIn:   result.ExpiresIn,
	})
}

func (h *Handler) TermsStatus(appCtx httpApp.AppContext) {
	claim := appCtx.Claims()
	if claim == nil {
		appCtx.HandleError(domain.NewErrorUnauthorized("authentication required"))
		return
	}
	h.writeTermsStatus(appCtx, claim.UserID)
}

func (h *Handler) AcceptTerms(appCtx httpApp.AppContext) {
	claim := appCtx.Claims()
	if claim == nil {
		appCtx.HandleError(domain.NewErrorUnauthorized("authentication required"))
		return
	}
	body := &acceptTermsRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(err)
		return
	}
	if err := h.reg.AcceptTerms(appCtx.Context(), claim.UserID, strings.TrimSpace(body.Version)); err != nil {
		appCtx.HandleError(err)
		return
	}
	h.writeTermsStatus(appCtx, claim.UserID)
}

func (h *Handler) writeTermsStatus(appCtx httpApp.AppContext, userID snow.ID) {
	status, err := h.reg.TermsStatus(appCtx.Context(), userID)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	response := termsStatusResponse{Version: status.Version, Required: status.Required}
	if status.Accepted != nil {
		response.AcceptedVersion = status.Accepted.Version
		response.AcceptedAt = status.Accepted.AcceptedAt.UTC().Format(time.RFC3339)
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, response)
}
