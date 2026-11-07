// Package billing turns provider subscriptions into seat and storage
// entitlements and enforces the seat limit.
//
// Enterprise Edition: see ee/LICENSE.
package billing

import "math"

const (
	StatusActive   = "active"
	StatusTrialing = "trialing"
	StatusPastDue  = "past_due"
	StatusPaused   = "paused"
	StatusCanceled = "canceled"
)

// Config holds the pricing numbers. They come from the deployment
// environment only; the code ships no commercial defaults.
type Config struct {
	FreeSeats         int64
	FreeQuotaBytes    int64
	SeatIncludedBytes int64
	StoragePackBytes  int64
}

// Entitlement is what an owner may use. A QuotaBytes of zero is unlimited,
// like everywhere in the storage ledger.
type Entitlement struct {
	Paid       bool
	SeatLimit  int64
	QuotaBytes int64
}

// IsLive reports whether a subscription status grants paid entitlements.
// past_due keeps them while the provider's dunning runs.
func IsLive(status string) bool {
	switch status {
	case StatusActive, StatusTrialing, StatusPastDue:
		return true
	default:
		return false
	}
}

// Free is the entitlement without a live subscription.
func (c Config) Free() Entitlement {
	return Entitlement{SeatLimit: c.FreeSeats, QuotaBytes: c.FreeQuotaBytes}
}

// Entitlement derives the entitlement of a subscription; nil or a status that
// is not live yields the free tier.
func (c Config) Entitlement(sub *Subscription) Entitlement {
	if sub == nil || !IsLive(sub.Status) {
		return c.Free()
	}
	seats := max(sub.Seats, 0)
	packs := max(sub.StoragePacks, 0)
	quota := int64(0)
	if c.FreeQuotaBytes > 0 {
		computed := addSaturating(mulSaturating(seats, c.SeatIncludedBytes), mulSaturating(packs, c.StoragePackBytes))
		quota = max(c.FreeQuotaBytes, computed)
	}
	return Entitlement{
		Paid:       true,
		SeatLimit:  max(seats, c.FreeSeats),
		QuotaBytes: quota,
	}
}

func mulSaturating(a, b int64) int64 {
	if a <= 0 || b <= 0 {
		return 0
	}
	if a > math.MaxInt64/b {
		return math.MaxInt64
	}
	return a * b
}

func addSaturating(a, b int64) int64 {
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}
