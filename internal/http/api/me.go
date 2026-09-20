package api

import (
	"net/http"

	"github.com/nipalab/nipa/internal/http/handler"
	"github.com/nipalab/nipa/internal/http/model"

	restfulspec "github.com/emicklei/go-restful-openapi/v2"
	"github.com/emicklei/go-restful/v3"
)

func setupMeRouter(ws *restful.WebService, h *handler.Handler) {
	tags := []string{"Me"}

	ws.Route(
		ws.GET("/me").
			To(wrap(h.Me)).
			Doc("Get the authenticated user profile").
			Returns(http.StatusOK, "current user", model.MeResponse{}).
			Operation("getMe").
			Metadata(restfulspec.KeyOpenAPITags, tags))
}
