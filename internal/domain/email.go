package domain

import (
	"time"

	"github.com/nipalab/nipa/internal/snow"
)

// Email delivery states. A delivery starts pending, is claimed into sending,
// then ends delivered or failed; stale sending rows are reclaimed to pending.
const (
	EmailDeliveryPending   = "pending"
	EmailDeliverySending   = "sending"
	EmailDeliveryDelivered = "delivered"
	EmailDeliveryFailed    = "failed"
)

// EmailDelivery is one rendered notification queued in the outbox for a single
// recipient. Body holds the encoded mail message.
type EmailDelivery struct {
	ID            snow.ID    `json:"id"`
	Event         string     `json:"event"`
	ProjectID     snow.ID    `json:"project_id"`
	UserID        snow.ID    `json:"user_id"`
	Email         string     `json:"email"`
	Subject       string     `json:"subject"`
	Body          []byte     `json:"-"`
	State         string     `json:"state"`
	Attempts      int64      `json:"attempts"`
	NextAttemptAt *time.Time `json:"next_attempt_at,omitempty"`
	LastError     string     `json:"last_error,omitempty"`
	ClaimedAt     *time.Time `json:"claimed_at,omitempty"`
	DeliveredAt   *time.Time `json:"delivered_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}
