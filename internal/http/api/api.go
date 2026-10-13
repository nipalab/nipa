package api

import (
	"net/http"

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
	EmailDelivery() *usecase.EmailDelivery
}

type API struct {
	useCase usecaseContainer
	metrics *obs.Metrics
	routes  []func(*restful.WebService)
}

// Option tunes optional API behavior.
type Option func(*apiOptions)

type apiOptions struct {
	metrics *obs.Metrics
	routes  []func(*restful.WebService)
}

// WithMetrics enables per-route HTTP metrics on the REST container.
func WithMetrics(metrics *obs.Metrics) Option {
	return func(o *apiOptions) {
		o.metrics = metrics
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

func NewAPI(useCase usecaseContainer, opts ...Option) *API {
	var options apiOptions
	for _, opt := range opts {
		opt(&options)
	}
	return &API{
		useCase: useCase,
		metrics: options.metrics,
		routes:  options.routes,
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
	setupAuthRouter(authWs, h)
	restful.Add(authWs)

	apiWs := new(restful.WebService).ApiVersion("1.0.0")
	apiWs.Path("/api/v1").
		Filter(cors.Filter).
		Filter(sameOriginFilter).
		Filter(NewAuthFilter(a.useCase.Auth()).Auth()).
		Consumes(restful.MIME_JSON).
		Produces(restful.MIME_JSON)
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
	setupEmailDeliveryRouter(apiWs, h)
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

	swagger.SetupSwagger()

	if a.metrics != nil {
		restful.DefaultContainer.Filter(metricsFilter(a.metrics))
	}

	return restful.DefaultContainer
}

func wrap(fn func(httpApp.AppContext)) restful.RouteFunction {
	return func(req *restful.Request, resp *restful.Response) {
		ctx, err := newAppContext(req, resp)
		if err != nil {
			resp.WriteHeaderAndJson(http.StatusInternalServerError, model.NewAPIError(err.Error()), restful.MIME_JSON)
			return
		}
		fn(ctx)
	}
}
