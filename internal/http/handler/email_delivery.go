package handler

import (
	nethttp "net/http"
	"strconv"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/http"
	"github.com/nipalab/nipa/internal/http/model"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

func (h *Handler) ListEmailDeliveries(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	opts := usecase.EmailDeliveryListOptions{State: appCtx.QueryParameter("state")}
	if raw := appCtx.QueryParameter("after"); raw != "" {
		after, err := snow.ParseBase36(raw)
		if err != nil {
			appCtx.HandleError(domain.NewErrorUser("invalid after"))
			return
		}
		opts.After = &after
	}
	if raw := appCtx.QueryParameter("limit"); raw != "" {
		limit, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || limit < 0 {
			appCtx.HandleError(domain.NewErrorUser("invalid limit"))
			return
		}
		opts.Limit = limit
	}
	deliveries, next, err := h.useCase.EmailDelivery().List(appCtx.Context(), project.ID, opts)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	resp := model.EmailDeliveryListResponse{
		Deliveries: make([]model.EmailDeliveryResponse, 0, len(deliveries)),
		NextCursor: next,
	}
	for _, delivery := range deliveries {
		resp.Deliveries = append(resp.Deliveries, toEmailDeliveryResponse(delivery))
	}
	appCtx.WriteJson(nethttp.StatusOK, resp)
}

func (h *Handler) RedeliverEmailDelivery(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	id, err := parseID(appCtx.PathParameter("id"), "delivery")
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	delivery, err := h.useCase.EmailDelivery().Redeliver(appCtx.Context(), project.ID, id)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, toEmailDeliveryResponse(delivery))
}

func toEmailDeliveryResponse(delivery *domain.EmailDelivery) model.EmailDeliveryResponse {
	return model.EmailDeliveryResponse{
		ID:            delivery.ID.Base36(),
		Event:         delivery.Event,
		UserID:        delivery.UserID.Base36(),
		Email:         delivery.Email,
		Subject:       delivery.Subject,
		State:         delivery.State,
		Attempts:      delivery.Attempts,
		LastError:     delivery.LastError,
		NextAttemptAt: delivery.NextAttemptAt,
		DeliveredAt:   delivery.DeliveredAt,
		CreatedAt:     delivery.CreatedAt,
		UpdatedAt:     delivery.UpdatedAt,
	}
}
