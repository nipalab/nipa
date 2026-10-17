package registration

import (
	nethttp "net/http"
	"strings"

	restfulspec "github.com/emicklei/go-restful-openapi/v2"
	"github.com/emicklei/go-restful/v3"

	httpApp "github.com/nipalab/nipa/internal/http"
	"github.com/nipalab/nipa/internal/http/api"
	"github.com/nipalab/nipa/internal/http/handler"
	"github.com/nipalab/nipa/internal/http/model"
	"github.com/nipalab/nipa/internal/usecase"
)

type Handler struct {
	reg       *Registration
	auth      *usecase.Auth
	providers func() []string
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

type capabilitiesResponse struct {
	Registration bool     `json:"registration"`
	Providers    []string `json:"providers"`
}

type registerRequest struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

type registerResponse struct {
	Status string `json:"status"`
	Email  string `json:"email"`
}

type resendRequest struct {
	Email string `json:"email"`
}

type verifyRequest struct {
	Token string `json:"token"`
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

func (h *Handler) Capabilities(appCtx httpApp.AppContext) {
	providers := []string{}
	if h.providers != nil {
		providers = h.providers()
	}
	if providers == nil {
		providers = []string{}
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, capabilitiesResponse{Registration: true, Providers: providers})
}

func (h *Handler) Register(appCtx httpApp.AppContext) {
	body := &registerRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(err)
		return
	}
	if err := h.reg.Register(appCtx.Context(), body.Name, body.Email, body.Password); err != nil {
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
	user, err := h.reg.Verify(appCtx.Context(), body.Token)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	result, err := h.auth.LoginResult(appCtx.Context(), user)
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
