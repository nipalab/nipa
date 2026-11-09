package billing

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/emicklei/go-restful/v3"
	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/http/api"
	"github.com/nipalab/nipa/internal/snow"
)

type tokenValidator map[string]domain.Claims

func (v tokenValidator) ValidateToken(_ context.Context, token string) (*domain.Claims, error) {
	claims, ok := v[token]
	if !ok {
		return nil, errors.New("invalid token")
	}
	return &claims, nil
}

type httpHarness struct {
	t        *testing.T
	server   *httptest.Server
	repo     *stubRepo
	ledger   *stubLedger
	provider *stubProvider
}

func newHTTPHarness(t *testing.T) *httpHarness {
	t.Helper()
	svc, repo, ledger, provider := newProviderService()
	ws := new(restful.WebService).Path("/api/v1").
		Filter(api.NewAuthFilter(tokenValidator{
			"super": {UserID: 1, IsSuperAdmin: true},
			"payer": {UserID: payer},
		}).Auth()).
		Consumes(restful.MIME_JSON).
		Produces(restful.MIME_JSON)
	RegisterRoutes(ws, svc)
	container := restful.NewContainer()
	container.Add(ws)
	container.Add(WebhookService(svc))
	server := httptest.NewServer(container)
	t.Cleanup(server.Close)
	return &httpHarness{t: t, server: server, repo: repo, ledger: ledger, provider: provider}
}

func (h *httpHarness) do(method, path, token, body string, header http.Header) (int, map[string]any) {
	h.t.Helper()
	req, err := http.NewRequest(method, h.server.URL+path, strings.NewReader(body))
	require.NoError(h.t, err)
	req.Header.Set("Content-Type", "application/json")
	for key, values := range header {
		req.Header[key] = values
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(h.t, err)
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(h.t, err)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func TestHTTP_Checkout(t *testing.T) {
	h := newHTTPHarness(t)
	h.repo.users = map[snow.ID]*domain.BillingUser{payer: {ID: payer, Email: "pat@example.com"}}
	h.provider.customerID = "ctm_1"
	h.provider.checkoutID = "txn_1"

	status, body := h.do(http.MethodPost, "/api/v1/billing/checkout", "payer", `{"seats":2,"storage_packs":1,"interval":"year"}`, nil)
	require.Equal(t, http.StatusOK, status, body)
	require.Equal(t, "txn_1", body["transaction_id"])
	require.Equal(t, []Items{{Seats: 2, StoragePacks: 1, Interval: IntervalYear}}, h.provider.checkouts)
	require.Empty(t, h.provider.resolved)

	h.provider.discountID = "dsc_edu"
	status, body = h.do(http.MethodPost, "/api/v1/billing/checkout", "payer", `{"seats":1,"interval":"month","discount_code":"EDU"}`, nil)
	require.Equal(t, http.StatusOK, status, body)
	require.Equal(t, []string{"EDU"}, h.provider.resolved)
	require.Equal(t, []string{"", "dsc_edu"}, h.provider.discountIDs)

	h.provider.discountErr = domain.NewErrorUser("discount code is not valid")
	status, body = h.do(http.MethodPost, "/api/v1/billing/checkout", "payer", `{"seats":1,"interval":"month","discount_code":"NOPE"}`, nil)
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, "discount code is not valid", body["error"])
	h.provider.discountErr = nil

	status, _ = h.do(http.MethodPost, "/api/v1/billing/checkout", "payer", `{"seats":0,"interval":"year"}`, nil)
	require.Equal(t, http.StatusBadRequest, status)
	status, _ = h.do(http.MethodPost, "/api/v1/billing/checkout", "payer", `{"seats":`, nil)
	require.Equal(t, http.StatusBadRequest, status)
	status, _ = h.do(http.MethodPost, "/api/v1/billing/checkout", "", `{}`, nil)
	require.Equal(t, http.StatusUnauthorized, status)

	h.repo.live = liveSub()
	status, _ = h.do(http.MethodPost, "/api/v1/billing/checkout", "payer", `{"seats":2,"interval":"year"}`, nil)
	require.Equal(t, http.StatusConflict, status)
}

func TestHTTP_ChangeSubscription(t *testing.T) {
	h := newHTTPHarness(t)
	h.repo.live = liveSub()
	h.provider.updated = Subscription{ProviderSubscriptionID: "sub_live", Status: StatusActive, Plan: PlanTeam, Seats: 6, ProviderUpdatedAt: at(5)}

	status, body := h.do(http.MethodPatch, "/api/v1/billing/subscription", "payer", `{"seats":6,"storage_packs":0}`, nil)
	require.Equal(t, http.StatusOK, status, body)
	require.Equal(t, "team", body["plan"])
	require.Equal(t, []Items{{Seats: 6, Interval: IntervalYear}}, h.provider.updates)

	status, _ = h.do(http.MethodPatch, "/api/v1/billing/subscription", "payer", `nope`, nil)
	require.Equal(t, http.StatusBadRequest, status)

	h.provider.updateErr = domain.NewErrorConflict("pending scheduled change")
	status, _ = h.do(http.MethodPatch, "/api/v1/billing/subscription", "payer", `{"seats":6}`, nil)
	require.Equal(t, http.StatusConflict, status)

	h.provider.updateErr = nil
	h.repo.countErr = errors.New("boom")
	status, body = h.do(http.MethodPatch, "/api/v1/billing/subscription", "payer", `{"seats":6}`, nil)
	require.Equal(t, http.StatusInternalServerError, status)
	require.Equal(t, "internal unknown error", body["error"])
}

func TestHTTP_Portal(t *testing.T) {
	h := newHTTPHarness(t)
	status, _ := h.do(http.MethodPost, "/api/v1/billing/portal", "payer", `{}`, nil)
	require.Equal(t, http.StatusNotFound, status)

	h.repo.accounts = map[snow.ID]*domain.BillingAccount{payer: {UserID: payer, ProviderCustomerID: "ctm_1"}}
	h.provider.portal = "https://portal"
	status, body := h.do(http.MethodPost, "/api/v1/billing/portal", "payer", `{}`, nil)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "https://portal", body["url"])
}

func TestHTTP_AdminSync(t *testing.T) {
	h := newHTTPHarness(t)
	path := "/api/v1/admin/billing/owners/" + payer.Base36() + "/sync"

	status, _ := h.do(http.MethodPost, path, "payer", `{}`, nil)
	require.Equal(t, http.StatusForbidden, status)
	status, _ = h.do(http.MethodPost, "/api/v1/admin/billing/owners/bad!/sync", "super", `{}`, nil)
	require.Equal(t, http.StatusBadRequest, status)
	status, _ = h.do(http.MethodPost, path, "super", `{}`, nil)
	require.Equal(t, http.StatusNotFound, status)

	h.repo.accounts = map[snow.ID]*domain.BillingAccount{payer: {UserID: payer, ProviderCustomerID: "ctm_1"}}
	h.provider.listed = []Subscription{{ProviderSubscriptionID: "sub_1", Status: StatusActive, Seats: 4, ProviderUpdatedAt: at(1)}}
	status, body := h.do(http.MethodPost, path, "super", `{}`, nil)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, float64(1), body["synced"])
	require.Equal(t, payer, h.repo.upserted[0].UserID)
}

func TestHTTP_AdminReplay(t *testing.T) {
	h := newHTTPHarness(t)
	status, _ := h.do(http.MethodPost, "/api/v1/admin/billing/events/evt_1/replay", "payer", `{}`, nil)
	require.Equal(t, http.StatusForbidden, status)
	status, _ = h.do(http.MethodPost, "/api/v1/admin/billing/events/evt_1/replay", "super", `{}`, nil)
	require.Equal(t, http.StatusNotFound, status)

	h.repo.events = map[string]*domain.BillingEvent{"evt_1": {EventID: "evt_1", Type: "transaction.completed",
		Payload: []byte(`{"id":"evt_1","type":"transaction.completed"}`)}}
	status, body := h.do(http.MethodPost, "/api/v1/admin/billing/events/evt_1/replay", "super", `{}`, nil)
	require.Equal(t, http.StatusOK, status, body)
	require.Equal(t, "evt_1", body["event_id"])
	require.NotNil(t, body["processed_at"])
}

func signed() http.Header {
	header := http.Header{}
	header.Set("X-Stub-Signature", "ok")
	return header
}

func TestHTTP_Webhook(t *testing.T) {
	h := newHTTPHarness(t)
	h.repo.users = map[snow.ID]*domain.BillingUser{payer: {ID: payer}}
	h.repo.accounts = map[snow.ID]*domain.BillingAccount{payer: {UserID: payer, Provider: "stub", ProviderCustomerID: "ctm_1"}}
	h.provider.carried = &EventSubscription{Subscription: Subscription{ProviderSubscriptionID: "sub_1", UserID: payer, Status: StatusActive, Seats: 4, ProviderUpdatedAt: at(1)}, CustomerID: "ctm_1"}
	event := `{"id":"evt_1","type":"subscription.activated"}`
	path := WebhookPath(h.provider)
	require.Equal(t, "/api/webhooks/stub", path)

	status, _ := h.do(http.MethodPost, path, "", event, nil)
	require.Equal(t, http.StatusUnauthorized, status)
	require.Empty(t, h.repo.events, "nothing is stored before the signature is verified")

	status, body := h.do(http.MethodPost, path, "", event, signed())
	require.Equal(t, http.StatusOK, status, body)
	require.Len(t, h.repo.upserted, 1)

	status, _ = h.do(http.MethodPost, path, "", event, signed())
	require.Equal(t, http.StatusOK, status)
	require.Len(t, h.repo.upserted, 1, "a redelivery is a no-op")

	h.provider.carried.Subscription.UserID = 99
	status, _ = h.do(http.MethodPost, path, "", `{"id":"evt_2","type":"subscription.updated"}`, signed())
	require.Equal(t, http.StatusOK, status, "a permanently failed event is acknowledged")
	require.NotEmpty(t, h.repo.events["evt_2"].Error)

	h.repo.eventErr = errors.New("db down")
	status, _ = h.do(http.MethodPost, path, "", `{"id":"evt_3","type":"subscription.updated"}`, signed())
	require.Equal(t, http.StatusInternalServerError, status, "a transient failure asks for a retry")

	h.repo.eventErr = nil
	status, _ = h.do(http.MethodPost, path, "", `{"type":"x"}`, signed())
	require.Equal(t, http.StatusBadRequest, status)

	status, _ = h.do(http.MethodPost, path, "", strings.Repeat("x", maxWebhookBytes+1), signed())
	require.Equal(t, http.StatusRequestEntityTooLarge, status)
}
