package api

import (
	"net/http"

	"github.com/nipalab/nipa/internal/http/handler"
	"github.com/nipalab/nipa/internal/http/model"

	restfulspec "github.com/emicklei/go-restful-openapi/v2"
	"github.com/emicklei/go-restful/v3"
)

func setupEmailDeliveryRouter(ws *restful.WebService, h *handler.Handler) {
	tags := []string{"Email"}
	project := func(b *restful.RouteBuilder) *restful.RouteBuilder {
		return b.
			Param(ws.PathParameter("org", "organization slug")).
			Param(ws.PathParameter("project", "project slug"))
	}

	ws.Route(project(
		ws.GET("/orgs/{org}/projects/{project}/emails/deliveries").
			To(wrap(h.ListEmailDeliveries)).
			Param(ws.QueryParameter("state", "filter by delivery state")).
			Param(ws.QueryParameter("after", "keyset cursor: the last id of the previous page")).
			Param(ws.QueryParameter("limit", "page size (default 50, max 200)")).
			Doc("List email outbox deliveries, newest first (project admin)").
			Returns(http.StatusOK, "deliveries", model.EmailDeliveryListResponse{}).
			Operation("listEmailDeliveries").
			Metadata(restfulspec.KeyOpenAPITags, tags)))

	ws.Route(project(
		ws.POST("/orgs/{org}/projects/{project}/emails/deliveries/{id}/redeliver").
			To(wrap(h.RedeliverEmailDelivery)).
			Param(ws.PathParameter("id", "delivery id (base36)")).
			Doc("Re-queue a delivered or failed notification (project admin)").
			Returns(http.StatusOK, "requeued delivery", model.EmailDeliveryResponse{}).
			Operation("redeliverEmailDelivery").
			Metadata(restfulspec.KeyOpenAPITags, tags)))
}
