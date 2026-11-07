package billing

import (
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/emicklei/go-restful/v3"

	"github.com/nipalab/nipa/internal/domain"
)

const maxWebhookBytes = 1 << 20

// WebhookPath is where the provider posts its events.
func WebhookPath(provider Provider) string {
	return "/api/webhooks/" + provider.Name()
}

// WebhookService is the provider webhook root. It has no bearer filter: the
// signature over the raw body is the authentication.
func WebhookService(svc *Service) *restful.WebService {
	ws := new(restful.WebService).ApiVersion("1.0.0")
	ws.Path(WebhookPath(svc.provider)).Produces(restful.MIME_JSON)
	ws.Route(ws.POST("").
		To(svc.webhook).
		Consumes(restful.MIME_JSON, "*/*").
		Doc("Billing provider webhook (signature verified)").
		Returns(http.StatusOK, "event accepted", nil).
		Returns(http.StatusUnauthorized, "invalid signature", nil).
		Operation("billingWebhook"))
	return ws
}

func (s *Service) webhook(req *restful.Request, resp *restful.Response) {
	body, err := io.ReadAll(http.MaxBytesReader(resp.ResponseWriter, req.Request.Body, maxWebhookBytes))
	if err != nil {
		writeError(resp, &domain.Error{Code: http.StatusRequestEntityTooLarge, Message: "webhook body too large"})
		return
	}
	event, err := s.provider.ParseWebhook(req.Request.Header, body)
	if errors.Is(err, ErrInvalidSignature) {
		writeError(resp, domain.NewErrorUnauthorized("invalid signature"))
		return
	}
	if err != nil {
		writeError(resp, err)
		return
	}
	if err := s.HandleEvent(req.Request.Context(), event); err != nil {
		slog.Error("billing webhook failed", "event_id", event.ID, "type", event.Type, "error", err)
		writeError(resp, domain.NewErrorInternalServer("billing event not applied"))
		return
	}
	writeJSON(resp, http.StatusOK, map[string]string{"status": "ok"})
}
