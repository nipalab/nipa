package paddle

import (
	"fmt"
	"strings"
	"time"

	"github.com/nipalab/nipa/ee/billing"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type subscription struct {
	ID         string     `json:"id"`
	Status     string     `json:"status"`
	CustomerID string     `json:"customer_id"`
	UpdatedAt  time.Time  `json:"updated_at"`
	CustomData customData `json:"custom_data"`
	Items      []struct {
		Quantity int64 `json:"quantity"`
		Price    struct {
			ID string `json:"id"`
		} `json:"price"`
	} `json:"items"`
	CurrentBillingPeriod *struct {
		EndsAt time.Time `json:"ends_at"`
	} `json:"current_billing_period"`
	ScheduledChange *struct {
		Action string `json:"action"`
	} `json:"scheduled_change"`
}

type priceKind struct {
	seat     bool
	interval string
}

func (c *Client) priceKinds() map[string]priceKind {
	return map[string]priceKind{
		c.prices.SeatMonthly:    {seat: true, interval: billing.IntervalMonth},
		c.prices.SeatYearly:     {seat: true, interval: billing.IntervalYear},
		c.prices.StorageMonthly: {interval: billing.IntervalMonth},
		c.prices.StorageYearly:  {interval: billing.IntervalYear},
	}
}

func invalidSubscription(id, format string, args ...any) error {
	return domain.NewErrorUser(fmt.Sprintf("paddle subscription %s: %s", id, fmt.Sprintf(format, args...)))
}

// mapSubscription turns a Paddle subscription into the provider-neutral one.
// An item with a price the server does not know is an error.
func (c *Client) mapSubscription(raw subscription) (billing.Subscription, error) {
	if raw.ID == "" {
		return billing.Subscription{}, invalidSubscription("", "missing id")
	}
	sub := billing.Subscription{
		ProviderSubscriptionID: raw.ID,
		Plan:                   billing.PlanTeam,
		Status:                 raw.Status,
		ProviderUpdatedAt:      raw.UpdatedAt,
		CancelAtPeriodEnd:      raw.ScheduledChange != nil && raw.ScheduledChange.Action == "cancel",
	}
	if raw.CustomData.UserID != "" {
		userID, err := snow.ParseBase36(strings.TrimSpace(raw.CustomData.UserID))
		if err != nil {
			return billing.Subscription{}, invalidSubscription(raw.ID, "invalid custom_data.user_id")
		}
		sub.UserID = userID
	}
	if raw.CurrentBillingPeriod != nil && !raw.CurrentBillingPeriod.EndsAt.IsZero() {
		end := raw.CurrentBillingPeriod.EndsAt
		sub.CurrentPeriodEnd = &end
	}
	kinds := c.priceKinds()
	seatItem := false
	for _, item := range raw.Items {
		kind, ok := kinds[item.Price.ID]
		if !ok || item.Price.ID == "" {
			return billing.Subscription{}, invalidSubscription(raw.ID, "unknown price %q", item.Price.ID)
		}
		if sub.Interval != "" && sub.Interval != kind.interval {
			return billing.Subscription{}, invalidSubscription(raw.ID, "items mix billing intervals")
		}
		sub.Interval = kind.interval
		if kind.seat {
			sub.Seats += item.Quantity
			seatItem = true
		} else {
			sub.StoragePacks += item.Quantity
		}
	}
	if !seatItem {
		return billing.Subscription{}, invalidSubscription(raw.ID, "no seat item")
	}
	return sub, nil
}
