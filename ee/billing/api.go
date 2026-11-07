package billing

import (
	"errors"
	"net/http"
	"time"

	"github.com/emicklei/go-restful/v3"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/http/model"
	"github.com/nipalab/nipa/internal/snow"
)

type itemsRequest struct {
	Seats        int64  `json:"seats"`
	StoragePacks int64  `json:"storage_packs"`
	Interval     string `json:"interval"`
}

type checkoutResponse struct {
	TransactionID string `json:"transaction_id"`
}

type portalResponse struct {
	URL string `json:"url"`
}

type syncResponse struct {
	Synced int `json:"synced"`
}

type eventResponse struct {
	EventID     string     `json:"event_id"`
	Type        string     `json:"type"`
	OccurredAt  time.Time  `json:"occurred_at"`
	ProcessedAt *time.Time `json:"processed_at"`
	Error       string     `json:"error,omitempty"`
}

// RegisterRoutes adds the billing routes to the authenticated /api/v1
// WebService. GET /billing/me stays in ee/storageusage.
func RegisterRoutes(ws *restful.WebService, svc *Service) {
	ws.Route(ws.POST("/billing/checkout").
		To(svc.checkout).
		Reads(itemsRequest{}).
		Doc("Start a subscription; returns the provider transaction to open in the checkout").
		Returns(http.StatusOK, "checkout transaction", checkoutResponse{}).
		Operation("billingCheckout"))

	ws.Route(ws.PATCH("/billing/subscription").
		To(svc.changeSubscription).
		Reads(itemsRequest{}).
		Doc("Change seats, storage packs or the billing interval of the live subscription").
		Operation("changeBillingSubscription"))

	ws.Route(ws.POST("/billing/portal").
		To(svc.portal).
		Doc("Create a provider customer-portal link").
		Returns(http.StatusOK, "portal link", portalResponse{}).
		Operation("billingPortal"))

	ws.Route(ws.POST("/admin/billing/owners/{user}/sync").
		To(svc.adminSync).
		Param(ws.PathParameter("user", "user id (base36)")).
		Doc("Re-apply an owner's subscriptions from the provider (super admin)").
		Returns(http.StatusOK, "synced", syncResponse{}).
		Operation("syncBillingOwner"))

	ws.Route(ws.POST("/admin/billing/events/{id}/replay").
		To(svc.adminReplay).
		Param(ws.PathParameter("id", "provider event id")).
		Doc("Re-run a stored provider event (super admin)").
		Returns(http.StatusOK, "event state", eventResponse{}).
		Operation("replayBillingEvent"))
}

func readItems(req *restful.Request) (Items, error) {
	var body itemsRequest
	if err := req.ReadEntity(&body); err != nil {
		return Items{}, domain.NewErrorUser("invalid request body")
	}
	return Items(body), nil
}

func caller(req *restful.Request, resp *restful.Response) (snow.ID, bool) {
	claim, ok := domain.ClaimFromContext(req.Request.Context())
	if !ok {
		writeError(resp, domain.NewErrorUnauthorized("authentication required"))
		return 0, false
	}
	return claim.UserID, true
}

func superAdmin(req *restful.Request, resp *restful.Response) bool {
	claim, ok := domain.ClaimFromContext(req.Request.Context())
	if !ok || !claim.IsSuperAdmin {
		writeError(resp, domain.NewErrorNoPermission())
		return false
	}
	return true
}

func (s *Service) checkout(req *restful.Request, resp *restful.Response) {
	userID, ok := caller(req, resp)
	if !ok {
		return
	}
	items, err := readItems(req)
	if err != nil {
		writeError(resp, err)
		return
	}
	txn, err := s.Checkout(req.Request.Context(), userID, items)
	if err != nil {
		writeError(resp, err)
		return
	}
	writeJSON(resp, http.StatusOK, checkoutResponse{TransactionID: txn})
}

func (s *Service) changeSubscription(req *restful.Request, resp *restful.Response) {
	userID, ok := caller(req, resp)
	if !ok {
		return
	}
	items, err := readItems(req)
	if err != nil {
		writeError(resp, err)
		return
	}
	if _, err := s.ChangeSubscription(req.Request.Context(), userID, items); err != nil {
		writeError(resp, err)
		return
	}
	summary, err := s.Summary(req.Request.Context(), userID)
	if err != nil {
		writeError(resp, err)
		return
	}
	writeJSON(resp, http.StatusOK, summary)
}

func (s *Service) portal(req *restful.Request, resp *restful.Response) {
	userID, ok := caller(req, resp)
	if !ok {
		return
	}
	link, err := s.Portal(req.Request.Context(), userID)
	if err != nil {
		writeError(resp, err)
		return
	}
	writeJSON(resp, http.StatusOK, portalResponse{URL: link})
}

func (s *Service) adminSync(req *restful.Request, resp *restful.Response) {
	if !superAdmin(req, resp) {
		return
	}
	userID, err := snow.ParseBase36(req.PathParameter("user"))
	if err != nil {
		writeError(resp, domain.NewErrorUser("invalid user id"))
		return
	}
	synced, err := s.Sync(req.Request.Context(), userID)
	if err != nil {
		writeError(resp, err)
		return
	}
	writeJSON(resp, http.StatusOK, syncResponse{Synced: synced})
}

func (s *Service) adminReplay(req *restful.Request, resp *restful.Response) {
	if !superAdmin(req, resp) {
		return
	}
	event, err := s.Replay(req.Request.Context(), req.PathParameter("id"))
	if err != nil {
		writeError(resp, err)
		return
	}
	writeJSON(resp, http.StatusOK, eventResponse{
		EventID:     event.EventID,
		Type:        event.Type,
		OccurredAt:  event.OccurredAt,
		ProcessedAt: event.ProcessedAt,
		Error:       event.Error,
	})
}

func writeJSON(resp *restful.Response, status int, body any) {
	_ = resp.WriteHeaderAndJson(status, body, restful.MIME_JSON)
}

func writeError(resp *restful.Response, err error) {
	var domErr *domain.Error
	if errors.As(err, &domErr) {
		_ = resp.WriteHeaderAndJson(domErr.Code, model.NewAPIError(domErr.Message), restful.MIME_JSON)
		return
	}
	_ = resp.WriteHeaderAndJson(http.StatusInternalServerError, model.NewAPIError("internal unknown error"), restful.MIME_JSON)
}
