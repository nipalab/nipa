package handler

import (
	nethttp "net/http"
	"strconv"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/http"
	"github.com/nipalab/nipa/internal/http/model"
	"github.com/nipalab/nipa/internal/usecase"
)

func (h *Handler) ListWebhooks(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	webhooks, err := h.useCase.Webhook().List(appCtx.Context(), project.ID)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	resp := make([]model.WebhookResponse, 0, len(webhooks))
	for _, webhook := range webhooks {
		resp = append(resp, toWebhookResponse(webhook, false))
	}
	appCtx.WriteJson(nethttp.StatusOK, resp)
}

func (h *Handler) CreateWebhook(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	body := &model.CreateWebhookRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(err)
		return
	}
	isActive := true
	if body.IsActive != nil {
		isActive = *body.IsActive
	}
	created, err := h.useCase.Webhook().Create(appCtx.Context(), project.ID, usecase.WebhookInput{
		Name:        body.Name,
		URL:         body.URL,
		Events:      body.Events,
		PathPrefix:  body.PathPrefix,
		IsActive:    isActive,
		InsecureTLS: body.InsecureTLS,
	})
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, toWebhookResponse(created, true))
}

func (h *Handler) GetWebhook(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	id, err := parseID(appCtx.PathParameter("id"), "webhook")
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	webhook, err := h.useCase.Webhook().Get(appCtx.Context(), project.ID, id)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, toWebhookResponse(webhook, false))
}

func (h *Handler) UpdateWebhook(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	id, err := parseID(appCtx.PathParameter("id"), "webhook")
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	body := &model.UpdateWebhookRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(err)
		return
	}
	updated, err := h.useCase.Webhook().Update(appCtx.Context(), project.ID, id, usecase.WebhookUpdate{
		Name:        body.Name,
		URL:         body.URL,
		Events:      body.Events,
		PathPrefix:  body.PathPrefix,
		IsActive:    body.IsActive,
		InsecureTLS: body.InsecureTLS,
	})
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, toWebhookResponse(updated, false))
}

func (h *Handler) DeleteWebhook(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	id, err := parseID(appCtx.PathParameter("id"), "webhook")
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	if err := h.useCase.Webhook().Delete(appCtx.Context(), project.ID, id); err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, model.MessageResponse{Message: "webhook deleted"})
}

func (h *Handler) RotateWebhookSecret(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	id, err := parseID(appCtx.PathParameter("id"), "webhook")
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	rotated, err := h.useCase.Webhook().RotateSecret(appCtx.Context(), project.ID, id)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, toWebhookResponse(rotated, true))
}

func (h *Handler) TestWebhook(appCtx http.AppContext) {
	org, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	id, err := parseID(appCtx.PathParameter("id"), "webhook")
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	delivery, err := h.useCase.Webhook().Test(appCtx.Context(), org, project, id)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, toWebhookDeliveryResponse(delivery))
}

func (h *Handler) ListWebhookDeliveries(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	id, err := parseID(appCtx.PathParameter("id"), "webhook")
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	limit, err := webhookPageParam(appCtx, "limit", 0)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	offset, err := webhookPageParam(appCtx, "offset", 0)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	deliveries, err := h.useCase.Webhook().Deliveries(appCtx.Context(), project.ID, id, limit, offset)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	resp := make([]model.WebhookDeliveryResponse, 0, len(deliveries))
	for _, delivery := range deliveries {
		resp = append(resp, toWebhookDeliveryResponse(delivery))
	}
	appCtx.WriteJson(nethttp.StatusOK, resp)
}

func (h *Handler) RedeliverWebhookDelivery(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	id, err := parseID(appCtx.PathParameter("id"), "webhook")
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	deliveryID, err := parseID(appCtx.PathParameter("deliveryId"), "delivery")
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	delivery, err := h.useCase.Webhook().Redeliver(appCtx.Context(), project.ID, id, deliveryID)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, toWebhookDeliveryResponse(delivery))
}

func webhookPageParam(appCtx http.AppContext, name string, fallback int64) (int64, error) {
	raw := appCtx.QueryParameter(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return 0, domain.NewErrorUser("invalid " + name)
	}
	return value, nil
}

func toWebhookResponse(webhook *domain.Webhook, includeSecret bool) model.WebhookResponse {
	resp := model.WebhookResponse{
		ID:          webhook.ID.Base36(),
		ProjectID:   webhook.ProjectID.Base36(),
		Name:        webhook.Name,
		URL:         webhook.URL,
		Events:      webhook.Events,
		PathPrefix:  webhook.PathPrefix,
		IsActive:    webhook.IsActive,
		InsecureTLS: webhook.InsecureTLS,
		CreatedAt:   webhook.CreatedAt,
		UpdatedAt:   webhook.UpdatedAt,
	}
	if resp.Events == nil {
		resp.Events = []string{}
	}
	if includeSecret {
		resp.Secret = webhook.Secret
	}
	return resp
}

func toWebhookDeliveryResponse(delivery *domain.WebhookDelivery) model.WebhookDeliveryResponse {
	return model.WebhookDeliveryResponse{
		ID:             delivery.ID.Base36(),
		WebhookID:      delivery.WebhookID.Base36(),
		EventType:      delivery.EventType,
		State:          delivery.State,
		Attempt:        delivery.Attempt,
		ResponseStatus: delivery.ResponseStatus,
		LastError:      delivery.LastError,
		NextRetryAt:    delivery.NextRetryAt,
		DeliveredAt:    delivery.DeliveredAt,
		CreatedAt:      delivery.CreatedAt,
	}
}
