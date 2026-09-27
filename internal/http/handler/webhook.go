package handler

import (
	nethttp "net/http"

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
