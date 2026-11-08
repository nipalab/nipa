package api

import (
	"net/http"
	"net/netip"

	"github.com/emicklei/go-restful/v3"
	httpApp "github.com/nipalab/nipa/internal/http"
	"github.com/nipalab/nipa/internal/http/handler"
	"github.com/nipalab/nipa/internal/http/model"
	"github.com/nipalab/nipa/internal/http/swagger"
	"github.com/nipalab/nipa/internal/obs"
	"github.com/nipalab/nipa/internal/usecase"
)

type usecaseContainer interface {
	Auth() *usecase.Auth
	User() *usecase.User
	SSHKey() *usecase.SSHKey
	Common() *usecase.Common
	Permission() *usecase.Permission
	Org() *usecase.Org
	Group() *usecase.Group
	Project() *usecase.Project
	Branch() *usecase.Branch
	Tag() *usecase.Tag
	Chunk() *usecase.Chunk
	MergeRequest() *usecase.MergeRequest
	MergeRequestReview() *usecase.MergeRequestReview
	MergeRequestCheck() *usecase.MergeRequestCheck
	FileLock() *usecase.FileLock
	Webhook() *usecase.Webhook
	ServiceToken() *usecase.ServiceToken
	EmailDelivery() *usecase.EmailDelivery
	Audit() *usecase.Audit
}

type API struct {
	useCase      usecaseContainer
	metrics      *obs.Metrics
	proxies      []netip.Prefix
	rateLimit    *RateLimit
	routes       []func(*restful.WebService)
	publicRoutes []func(*restful.WebService)
	webServices  []*restful.WebService
}

// Option tunes optional API behavior.
type Option func(*apiOptions)

type apiOptions struct {
	metrics      *obs.Metrics
	proxies      []netip.Prefix
	rateLimit    *RateLimit
	routes       []func(*restful.WebService)
	publicRoutes []func(*restful.WebService)
	webServices  []*restful.WebService
}

// WithMetrics enables per-route HTTP metrics on the REST container.
func WithMetrics(metrics *obs.Metrics) Option {
	return func(o *apiOptions) {
		o.metrics = metrics
	}
}

// WithTrustedProxies sets the proxies whose X-Forwarded-For is honoured for
// the client IP recorded on audited requests.
func WithTrustedProxies(proxies []netip.Prefix) Option {
	return func(o *apiOptions) {
		o.proxies = proxies
	}
}

// WithRateLimit throttles the public auth routes per IP and the authenticated
// routes per user. A nil value disables it.
func WithRateLimit(rl *RateLimit) Option {
	return func(o *apiOptions) {
		o.rateLimit = rl
	}
}

// WithRoutes registers extra routes on the authenticated /api/v1 WebService
// before it is added to the container. Extensions use it to expose additional
// API surfaces; a second WebService with the same root path is rejected by
// go-restful, so new routes must share this one.
func WithRoutes(register func(*restful.WebService)) Option {
	return func(o *apiOptions) {
		o.routes = append(o.routes, register)
	}
}

// WithPublicRoutes registers extra routes on the unauthenticated
// /api/v1/auth WebService, e.g. the SaaS registration and OAuth flows. The
// routes are public: whoever registers them owns their authentication.
func WithPublicRoutes(register func(*restful.WebService)) Option {
	return func(o *apiOptions) {
		o.publicRoutes = append(o.publicRoutes, register)
	}
}

// WithWebService adds a WebService with its own root path, e.g. a provider
// webhook that authenticates by signature. No bearer or origin filter is
// attached: the WebService owns its authentication.
func WithWebService(ws *restful.WebService) Option {
	return func(o *apiOptions) {
		o.webServices = append(o.webServices, ws)
	}
}

func NewAPI(useCase usecaseContainer, opts ...Option) *API {
	var options apiOptions
	for _, opt := range opts {
		opt(&options)
	}
	return &API{
		useCase:      useCase,
		metrics:      options.metrics,
		proxies:      options.proxies,
		rateLimit:    options.rateLimit,
		routes:       options.routes,
		publicRoutes: options.publicRoutes,
		webServices:  options.webServices,
	}
}

func (a *API) SetupRoute() http.Handler {
	cors := restful.CrossOriginResourceSharing{
		AllowedMethods: []string{"POST", "GET", "PUT", "PATCH", "DELETE"},
		AllowedHeaders: []string{"Content-Type", "Accept", "Authorization"},
	}

	h := handler.NewHandler(a.useCase)

	authWs := new(restful.WebService).ApiVersion("1.0.0")
	authWs.Path("/api/v1/auth").
		Filter(cors.Filter).
		Filter(sameOriginFilter).
		Consumes(restful.MIME_JSON).
		Produces(restful.MIME_JSON)
	if a.rateLimit.active() {
		authWs.Filter(a.rateLimit.AuthFilter())
	}
	setupAuthRouter(authWs, h)
	for _, register := range a.publicRoutes {
		register(authWs)
	}
	restful.Add(authWs)

	apiWs := new(restful.WebService).ApiVersion("1.0.0")
	apiWs.Path("/api/v1").
		Filter(cors.Filter).
		Filter(sameOriginFilter).
		Filter(NewAuthFilter(a.useCase.Auth()).WithServiceTokens(a.useCase.ServiceToken()).WithTrustedProxies(a.proxies).Auth()).
		Consumes(restful.MIME_JSON).
		Produces(restful.MIME_JSON)
	if a.rateLimit.active() {
		apiWs.Filter(a.rateLimit.UserFilter())
	}
	setupUserRouter(apiWs, h)
	setupSSHKeyRouter(apiWs, h)
	setupProjectRouter(apiWs, h)
	setupBrowserRouter(apiWs, h)
	setupTagRouter(apiWs, h)
	setupMergeRequestRouter(apiWs, h)
	setupMergeRequestReviewRouter(apiWs, h)
	setupFileLockRouter(apiWs, h)
	setupOrgRouter(apiWs, h)
	setupGroupRouter(apiWs, h)
	setupPermissionRouter(apiWs, h)
	setupWebhookRouter(apiWs, h)
	setupServiceTokenRouter(apiWs, h)
	setupEmailDeliveryRouter(apiWs, h)
	setupAuditRouter(apiWs, h)
	for _, register := range a.routes {
		register(apiWs)
	}
	restful.Add(apiWs)

	chunkWs := new(restful.WebService).ApiVersion("1.0.0")
	chunkWs.Path("/api/chunks").
		Filter(cors.Filter).
		Filter(sameOriginFilter)
	setupChunkRouter(chunkWs, handler.NewChunkHandler(a.useCase.Chunk()))
	restful.Add(chunkWs)

	for _, ws := range a.webServices {
		restful.Add(ws)
	}

	swagger.SetupSwagger()

	if a.metrics != nil {
		restful.DefaultContainer.Filter(metricsFilter(a.metrics))
	}

	return restful.DefaultContainer
}

// Wrap adapts an AppContext handler to a go-restful route function. Extension
// packages (e.g. the enterprise onboarding routes) reuse it to write handlers
// with the same request/response helpers as the core API.
func Wrap(fn func(httpApp.AppContext)) restful.RouteFunction {
	return func(req *restful.Request, resp *restful.Response) {
		ctx, err := newAppContext(req, resp)
		if err != nil {
			_ = resp.WriteHeaderAndJson(http.StatusInternalServerError, model.NewAPIError(err.Error()), restful.MIME_JSON)
			return
		}
		fn(ctx)
	}
}
