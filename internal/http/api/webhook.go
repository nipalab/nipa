package api

import (
	"net/http"

	"github.com/nipalab/nipa/internal/http/handler"
	"github.com/nipalab/nipa/internal/http/model"

	restfulspec "github.com/emicklei/go-restful-openapi/v2"
	"github.com/emicklei/go-restful/v3"
)

func setupWebhookRouter(ws *restful.WebService, h *handler.Handler) {
	tags := []string{"Webhooks"}

	ws.Route(
		ws.GET("/orgs/{org}/projects/{project}/webhooks").
			To(wrap(h.ListWebhooks)).
			Param(ws.PathParameter("org", "organization slug")).
			Param(ws.PathParameter("project", "project slug")).
			Doc("List project webhooks (project admin)").
			Returns(http.StatusOK, "webhooks", []model.WebhookResponse{}).
			Operation("listWebhooks").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.POST("/orgs/{org}/projects/{project}/webhooks").
			To(wrap(h.CreateWebhook)).
			Param(ws.PathParameter("org", "organization slug")).
			Param(ws.PathParameter("project", "project slug")).
			Reads(model.CreateWebhookRequest{}).
			Doc("Create a webhook; the signing secret is returned once (project admin)").
			Returns(http.StatusOK, "created webhook", model.WebhookResponse{}).
			Operation("createWebhook").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.GET("/orgs/{org}/projects/{project}/webhooks/{id}").
			To(wrap(h.GetWebhook)).
			Param(ws.PathParameter("org", "organization slug")).
			Param(ws.PathParameter("project", "project slug")).
			Param(ws.PathParameter("id", "webhook id")).
			Doc("Get a webhook (project admin)").
			Returns(http.StatusOK, "webhook", model.WebhookResponse{}).
			Operation("getWebhook").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.PATCH("/orgs/{org}/projects/{project}/webhooks/{id}").
			To(wrap(h.UpdateWebhook)).
			Param(ws.PathParameter("org", "organization slug")).
			Param(ws.PathParameter("project", "project slug")).
			Param(ws.PathParameter("id", "webhook id")).
			Reads(model.UpdateWebhookRequest{}).
			Doc("Update a webhook (project admin)").
			Returns(http.StatusOK, "updated webhook", model.WebhookResponse{}).
			Operation("updateWebhook").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.DELETE("/orgs/{org}/projects/{project}/webhooks/{id}").
			To(wrap(h.DeleteWebhook)).
			Param(ws.PathParameter("org", "organization slug")).
			Param(ws.PathParameter("project", "project slug")).
			Param(ws.PathParameter("id", "webhook id")).
			Doc("Delete a webhook (project admin)").
			Returns(http.StatusOK, "deleted", model.MessageResponse{}).
			Operation("deleteWebhook").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.POST("/orgs/{org}/projects/{project}/webhooks/{id}/rotate-secret").
			To(wrap(h.RotateWebhookSecret)).
			Param(ws.PathParameter("org", "organization slug")).
			Param(ws.PathParameter("project", "project slug")).
			Param(ws.PathParameter("id", "webhook id")).
			Doc("Rotate the signing secret; the new secret is returned once (project admin)").
			Returns(http.StatusOK, "webhook with new secret", model.WebhookResponse{}).
			Operation("rotateWebhookSecret").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.POST("/orgs/{org}/projects/{project}/webhooks/{id}/test").
			To(wrap(h.TestWebhook)).
			Param(ws.PathParameter("org", "organization slug")).
			Param(ws.PathParameter("project", "project slug")).
			Param(ws.PathParameter("id", "webhook id")).
			Doc("Send a ping delivery to verify the endpoint (project admin)").
			Returns(http.StatusOK, "queued delivery", model.WebhookDeliveryResponse{}).
			Operation("testWebhook").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.GET("/orgs/{org}/projects/{project}/webhooks/{id}/deliveries").
			To(wrap(h.ListWebhookDeliveries)).
			Param(ws.PathParameter("org", "organization slug")).
			Param(ws.PathParameter("project", "project slug")).
			Param(ws.PathParameter("id", "webhook id")).
			Param(ws.QueryParameter("limit", "page size (default 50, max 200)")).
			Param(ws.QueryParameter("offset", "page offset")).
			Doc("List recent deliveries, newest first (project admin)").
			Returns(http.StatusOK, "deliveries", []model.WebhookDeliveryResponse{}).
			Operation("listWebhookDeliveries").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.POST("/orgs/{org}/projects/{project}/webhooks/{id}/deliveries/{deliveryId}/redeliver").
			To(wrap(h.RedeliverWebhookDelivery)).
			Param(ws.PathParameter("org", "organization slug")).
			Param(ws.PathParameter("project", "project slug")).
			Param(ws.PathParameter("id", "webhook id")).
			Param(ws.PathParameter("deliveryId", "delivery id")).
			Doc("Re-queue a stored delivery (project admin)").
			Returns(http.StatusOK, "requeued delivery", model.WebhookDeliveryResponse{}).
			Operation("redeliverWebhookDelivery").
			Metadata(restfulspec.KeyOpenAPITags, tags))
}
