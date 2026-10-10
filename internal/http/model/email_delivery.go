package model

import "time"

type EmailDeliveryResponse struct {
	ID            string     `json:"id"`
	Event         string     `json:"event"`
	UserID        string     `json:"user_id"`
	Email         string     `json:"email"`
	Subject       string     `json:"subject"`
	State         string     `json:"state"`
	Attempts      int64      `json:"attempts"`
	LastError     string     `json:"last_error,omitempty"`
	NextAttemptAt *time.Time `json:"next_attempt_at,omitempty"`
	DeliveredAt   *time.Time `json:"delivered_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// EmailDeliveryListResponse is one page of deliveries. NextCursor is set when
// the page was full; pass it as the after query parameter to continue.
type EmailDeliveryListResponse struct {
	Deliveries []EmailDeliveryResponse `json:"deliveries"`
	NextCursor string                  `json:"next_cursor,omitempty"`
}
