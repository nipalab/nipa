package oauth

import (
	nethttp "net/http"

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
	oauth *OAuth
	auth  *usecase.Auth
}

func NewHandler(o *OAuth, auth *usecase.Auth) *Handler {
	return &Handler{oauth: o, auth: auth}
}

type startResponse struct {
	URL string `json:"url"`
}

type completeRequest struct {
	Code string `json:"code"`
}

type identityResponse struct {
	ID        string `json:"id"`
	Provider  string `json:"provider"`
	Email     string `json:"email"`
	CreatedAt string `json:"created_at"`
}

// RegisterPublicRoutes attaches the unauthenticated OAuth endpoints to the
// /api/v1/auth WebService.
func RegisterPublicRoutes(ws *restful.WebService, h *Handler) {
	tags := []string{"Auth"}

	ws.Route(
		ws.GET("/oauth/{provider}/start").
			To(api.Wrap(h.Start)).
			Doc("Redirect to a social login provider").
			Notes("The optional redirect query parameter names the SPA path to return to").
			Operation("startOAuth").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.GET("/oauth/{provider}/callback").
			To(api.Wrap(h.Callback)).
			Doc("OAuth provider callback").
			Notes("Redirects back to the SPA with a one-time login code, a linked marker or an error parameter").
			Operation("oauthCallback").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.POST("/oauth/complete").
			To(api.Wrap(h.Complete)).
			Reads(completeRequest{}).
			Doc("Exchange an OAuth login code for a session").
			Returns(nethttp.StatusOK, "access token", model.LoginResponse{}).
			Operation("completeOAuth").
			Metadata(restfulspec.KeyOpenAPITags, tags))
}

// RegisterRoutes attaches the authenticated identity management endpoints to
// the /api/v1 WebService.
func RegisterRoutes(ws *restful.WebService, h *Handler) {
	tags := []string{"Auth"}

	ws.Route(
		ws.GET("/me/identities").
			To(api.Wrap(h.ListIdentities)).
			Doc("List the social identities linked to the account").
			Returns(nethttp.StatusOK, "identities", []identityResponse{}).
			Operation("listIdentities").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.GET("/me/identities/{provider}/start").
			To(api.Wrap(h.StartLink)).
			Doc("Build the URL that links a social provider to the account").
			Returns(nethttp.StatusOK, "provider authorization URL", startResponse{}).
			Operation("startIdentityLink").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.DELETE("/me/identities/{id}").
			To(api.Wrap(h.DisconnectIdentity)).
			Doc("Unlink a social identity from the account").
			Returns(nethttp.StatusOK, "unlinked", model.MessageResponse{}).
			Operation("disconnectIdentity").
			Metadata(restfulspec.KeyOpenAPITags, tags))
}

func (h *Handler) Start(appCtx httpApp.AppContext) {
	target, err := h.oauth.Start(appCtx.PathParameter("provider"), appCtx.QueryParameter("redirect"), 0)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	nethttp.Redirect(appCtx.ResponseWriter(), appCtx.Request(), target, nethttp.StatusFound)
}

func (h *Handler) Callback(appCtx httpApp.AppContext) {
	target, err := h.oauth.Callback(
		appCtx.Context(),
		appCtx.PathParameter("provider"),
		appCtx.QueryParameter("code"),
		appCtx.QueryParameter("state"),
	)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	nethttp.Redirect(appCtx.ResponseWriter(), appCtx.Request(), target, nethttp.StatusFound)
}

func (h *Handler) Complete(appCtx httpApp.AppContext) {
	body := &completeRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(err)
		return
	}
	user, err := h.oauth.Complete(appCtx.Context(), body.Code)
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

func (h *Handler) ListIdentities(appCtx httpApp.AppContext) {
	claim := appCtx.Claims()
	if claim == nil {
		appCtx.HandleError(domain.NewErrorUnauthorized("authentication required"))
		return
	}
	identities, err := h.oauth.ListIdentities(appCtx.Context(), claim.UserID)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	response := make([]identityResponse, 0, len(identities))
	for _, identity := range identities {
		response = append(response, identityResponse{
			ID:        identity.ID.Base36(),
			Provider:  identity.Provider,
			Email:     identity.Email,
			CreatedAt: identity.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		})
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, response)
}

func (h *Handler) StartLink(appCtx httpApp.AppContext) {
	claim := appCtx.Claims()
	if claim == nil {
		appCtx.HandleError(domain.NewErrorUnauthorized("authentication required"))
		return
	}
	target, err := h.oauth.Start(appCtx.PathParameter("provider"), appCtx.QueryParameter("redirect"), claim.UserID)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, startResponse{URL: target})
}

func (h *Handler) DisconnectIdentity(appCtx httpApp.AppContext) {
	claim := appCtx.Claims()
	if claim == nil {
		appCtx.HandleError(domain.NewErrorUnauthorized("authentication required"))
		return
	}
	identityID, err := snow.ParseBase36(appCtx.PathParameter("id"))
	if err != nil {
		appCtx.HandleError(domain.NewErrorUser("invalid identity id"))
		return
	}
	if err := h.oauth.DisconnectIdentity(appCtx.Context(), claim.UserID, identityID); err != nil {
		appCtx.HandleError(err)
		return
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, model.MessageResponse{Message: "identity unlinked"})
}
