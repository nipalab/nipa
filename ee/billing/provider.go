package billing

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

const (
	IntervalMonth = "month"
	IntervalYear  = "year"

	PlanTeam = "team"
	PlanFree = "free"
)

// ErrInvalidSignature is returned by Provider.ParseWebhook for a request that
// does not carry a valid, fresh signature.
var ErrInvalidSignature = errors.New("invalid webhook signature")

// Items is what a customer buys. Price ids are chosen by the provider from
// server configuration, never from the client.
type Items struct {
	Seats        int64
	StoragePacks int64
	Interval     string
}

// Validate normalizes the interval and checks the quantities.
func (i Items) Validate() (Items, error) {
	switch strings.ToLower(strings.TrimSpace(i.Interval)) {
	case IntervalMonth, "monthly":
		i.Interval = IntervalMonth
	case IntervalYear, "yearly":
		i.Interval = IntervalYear
	default:
		return i, domain.NewErrorUser("interval must be month or year")
	}
	if i.Seats < 0 {
		return i, domain.NewErrorUser("seats must not be negative")
	}
	if i.StoragePacks < 0 {
		return i, domain.NewErrorUser("storage_packs must not be negative")
	}
	if i.Seats+i.StoragePacks < 1 {
		return i, domain.NewErrorUser("buy at least one seat or storage pack; cancel the subscription to return to Free")
	}
	return i, nil
}

// Event is one verified provider webhook event.
type Event struct {
	ID         string
	Type       string
	OccurredAt time.Time
	Payload    []byte
}

// EventSubscription is the subscription an event carries. UserID is zero when
// the provider data names no user; CustomerID is the fallback.
type EventSubscription struct {
	Subscription Subscription
	CustomerID   string
}

// Provider is the billing provider seam; ee/billing/paddle implements it.
type Provider interface {
	Name() string
	EnsureCustomer(ctx context.Context, userID snow.ID, email, name string) (string, error)
	ResolveDiscount(ctx context.Context, code string) (string, error)
	CreateCheckout(ctx context.Context, customerID string, userID snow.ID, items Items, discountID string) (string, error)
	UpdateSubscription(ctx context.Context, subscriptionID string, items Items) (Subscription, error)
	GetSubscription(ctx context.Context, subscriptionID string) (Subscription, error)
	ListSubscriptions(ctx context.Context, customerID string) ([]Subscription, error)
	PortalURL(ctx context.Context, customerID, subscriptionID string) (string, error)
	ParseWebhook(header http.Header, body []byte) (Event, error)
	DecodeEvent(payload []byte) (Event, error)
	EventSubscription(event Event) (EventSubscription, error)
}
