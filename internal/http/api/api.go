package api

import (
	"net/http"

	"github.com/emicklei/go-restful/v3"
	httpApp "github.com/nipalab/nipa/internal/http"
	"github.com/nipalab/nipa/internal/http/handler"
	"github.com/nipalab/nipa/internal/http/model"
	"github.com/nipalab/nipa/internal/http/swagger"
	"github.com/nipalab/nipa/internal/usecase"
)

type usecaseContainer interface {
	Auth() *usecase.Auth
	User() *usecase.User
	Common() *usecase.Common
	Permission() *usecase.Permission
	Org() *usecase.Org
	Group() *usecase.Group
	Project() *usecase.Project
}

type API struct {
	useCase usecaseContainer
}

func NewAPI(useCase usecaseContainer) *API {
	return &API{
		useCase: useCase,
	}
}

func (a *API) SetupRoute() http.Handler {
	cors := restful.CrossOriginResourceSharing{
		AllowedMethods: []string{"POST", "GET", "PUT", "PATCH", "DELETE"},
		AllowedHeaders: []string{"Content-Type", "Accept", "Authorization"},
	}

	handler := handler.NewHandler(a.useCase)

	authWs := new(restful.WebService).ApiVersion("1.0.0")
	authWs.Path("/api/v1/auth").
		Filter(cors.Filter).
		Filter(sameOriginFilter).
		Consumes(restful.MIME_JSON).
		Produces(restful.MIME_JSON)
	setupAuthRouter(authWs, handler)
	restful.Add(authWs)

	apiWs := new(restful.WebService).ApiVersion("1.0.0")
	apiWs.Path("/api/v1").
		Filter(cors.Filter).
		Filter(sameOriginFilter).
		Filter(NewAuthFilter(a.useCase.Auth()).Auth()).
		Consumes(restful.MIME_JSON).
		Produces(restful.MIME_JSON)
	setupUserRouter(apiWs, handler)
	setupProjectRouter(apiWs, handler)
	setupOrgRouter(apiWs, handler)
	setupGroupRouter(apiWs, handler)
	setupPermissionRouter(apiWs, handler)
	restful.Add(apiWs)

	swagger.SetupSwagger()

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
