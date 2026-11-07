package billing

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type stubProvider struct {
	calls []string

	customerID  string
	customerErr error
	checkoutID  string
	checkoutErr error
	checkouts   []Items
	updated     Subscription
	updateErr   error
	updates     []Items
	listed      []Subscription
	listErr     error
	portal      string
	portalErr   error
	portalSubs  []string
	webhookErr  error
	carried     *EventSubscription
	carriedErr  error
}

func (p *stubProvider) Name() string { return "stub" }

func (p *stubProvider) EnsureCustomer(context.Context, snow.ID, string, string) (string, error) {
	p.calls = append(p.calls, "ensure_customer")
	return p.customerID, p.customerErr
}

func (p *stubProvider) CreateCheckout(_ context.Context, _ string, _ snow.ID, items Items) (string, error) {
	p.calls = append(p.calls, "checkout")
	p.checkouts = append(p.checkouts, items)
	return p.checkoutID, p.checkoutErr
}

func (p *stubProvider) UpdateSubscription(_ context.Context, _ string, items Items) (Subscription, error) {
	p.calls = append(p.calls, "update")
	p.updates = append(p.updates, items)
	return p.updated, p.updateErr
}

func (p *stubProvider) GetSubscription(context.Context, string) (Subscription, error) {
	return p.updated, p.updateErr
}

func (p *stubProvider) ListSubscriptions(context.Context, string) ([]Subscription, error) {
	p.calls = append(p.calls, "list")
	return p.listed, p.listErr
}

func (p *stubProvider) PortalURL(_ context.Context, _ string, subID string) (string, error) {
	p.calls = append(p.calls, "portal")
	p.portalSubs = append(p.portalSubs, subID)
	return p.portal, p.portalErr
}

type stubEnvelope struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

func (p *stubProvider) ParseWebhook(header http.Header, body []byte) (Event, error) {
	if header.Get("X-Stub-Signature") != "ok" {
		return Event{}, ErrInvalidSignature
	}
	if p.webhookErr != nil {
		return Event{}, p.webhookErr
	}
	return p.DecodeEvent(body)
}

func (p *stubProvider) DecodeEvent(payload []byte) (Event, error) {
	var envelope stubEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil || envelope.ID == "" {
		return Event{}, domain.NewErrorUser("invalid event")
	}
	return Event{ID: envelope.ID, Type: envelope.Type, OccurredAt: at(0), Payload: payload}, nil
}

func (p *stubProvider) EventSubscription(Event) (EventSubscription, error) {
	if p.carriedErr != nil {
		return EventSubscription{}, p.carriedErr
	}
	return *p.carried, nil
}

func newProviderService() (*Service, *stubRepo, *stubLedger, *stubProvider) {
	svc, repo, ledger, _ := newStubService()
	provider := &stubProvider{}
	return svc.WithProvider(provider), repo, ledger, provider
}
