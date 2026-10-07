package domain

import (
	"time"

	"github.com/nipalab/nipa/internal/snow"
)

// Webhook event types emitted by the server. Ping is only sent through the
// manual test endpoint.
const (
	WebhookEventPush           = "push"
	WebhookEventBranchCreated  = "branch.created"
	WebhookEventBranchDeleted  = "branch.deleted"
	WebhookEventTagCreated     = "tag.created"
	WebhookEventTagDeleted     = "tag.deleted"
	WebhookEventMRCreated      = "mr.created"
	WebhookEventMRUpdated      = "mr.updated"
	WebhookEventMRSynchronized = "mr.synchronized"
	WebhookEventMRMerged       = "mr.merged"
	WebhookEventMRClosed       = "mr.closed"
	WebhookEventMRReopened     = "mr.reopened"
	WebhookEventMRReady        = "mr.ready_for_review"

	WebhookEventMRReviewSubmitted   = "mr.review_submitted"
	WebhookEventMRReviewDismissed   = "mr.review_dismissed"
	WebhookEventMRReviewRequested   = "mr.review_requested"
	WebhookEventMRReviewUnrequested = "mr.review_request_removed"
	WebhookEventMRCommentCreated    = "mr.comment_created"

	WebhookEventPing = "ping"
)

// Webhook delivery states.
const (
	WebhookDeliveryPending   = "pending"
	WebhookDeliveryDelivered = "delivered"
	WebhookDeliveryFailed    = "failed"
)

// Webhook is a project-scoped endpoint notified about events after they
// happened. Events holds the subscribed event types; PathPrefix narrows pushes
// to paths under the prefix, empty means every path.
type Webhook struct {
	ID          snow.ID   `json:"id"`
	ProjectID   snow.ID   `json:"project_id"`
	Name        string    `json:"name"`
	URL         string    `json:"url"`
	Secret      string    `json:"-"`
	Events      []string  `json:"events"`
	PathPrefix  string    `json:"path_prefix"`
	IsActive    bool      `json:"is_active"`
	InsecureTLS bool      `json:"insecure_tls"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// WebhookDelivery is one attempt-tracked delivery of a rendered JSON payload.
// Payload is kept verbatim so a failed delivery can be replayed.
type WebhookDelivery struct {
	ID             snow.ID    `json:"id"`
	WebhookID      snow.ID    `json:"webhook_id"`
	EventType      string     `json:"event_type"`
	Payload        []byte     `json:"payload"`
	State          string     `json:"state"`
	Attempt        int64      `json:"attempt"`
	ResponseStatus *int64     `json:"response_status,omitempty"`
	LastError      string     `json:"last_error,omitempty"`
	NextRetryAt    *time.Time `json:"next_retry_at,omitempty"`
	DeliveredAt    *time.Time `json:"delivered_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}
