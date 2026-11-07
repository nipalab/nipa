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
