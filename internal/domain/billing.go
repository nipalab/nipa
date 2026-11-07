package domain

import (
	"time"

	"github.com/nipalab/nipa/internal/snow"
)

// BillingSubscription is a provider-neutral snapshot of one billing
// subscription, stored by the enterprise billing module.
type BillingSubscription struct {
	ProviderSubscriptionID string
	UserID                 snow.ID
	Plan                   string
	Status                 string
	Interval               string
	Seats                  int64
	StoragePacks           int64
	CurrentPeriodEnd       *time.Time
	CancelAtPeriodEnd      bool
	ProviderUpdatedAt      time.Time
}

// BillingAccount links a user to their customer record at the billing
// provider.
type BillingAccount struct {
	UserID             snow.ID
	Provider           string
	ProviderCustomerID string
}

// BillingEvent is one provider webhook event as stored for dedup and replay.
type BillingEvent struct {
	EventID     string
	Type        string
	OccurredAt  time.Time
	Payload     []byte
	ProcessedAt *time.Time
	Error       string
}

// BillingUser is the account data billing sends to the provider.
type BillingUser struct {
	ID      snow.ID
	Name    string
	Email   string
	Deleted bool
}
