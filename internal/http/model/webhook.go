package model

import "time"

type WebhookResponse struct {
	ID          string    `json:"id"`
	ProjectID   string    `json:"project_id"`
	Name        string    `json:"name"`
	URL         string    `json:"url"`
	Events      []string  `json:"events"`
	PathPrefix  string    `json:"path_prefix"`
	IsActive    bool      `json:"is_active"`
	InsecureTLS bool      `json:"insecure_tls"`
	Secret      string    `json:"secret,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type CreateWebhookRequest struct {
	Name        string   `json:"name"`
	URL         string   `json:"url"`
	Events      []string `json:"events"`
	PathPrefix  string   `json:"path_prefix"`
	IsActive    *bool    `json:"is_active"`
	InsecureTLS bool     `json:"insecure_tls"`
}

type UpdateWebhookRequest struct {
	Name        *string   `json:"name"`
	URL         *string   `json:"url"`
	Events      *[]string `json:"events"`
	PathPrefix  *string   `json:"path_prefix"`
	IsActive    *bool     `json:"is_active"`
	InsecureTLS *bool     `json:"insecure_tls"`
}

type WebhookDeliveryResponse struct {
	ID             string     `json:"id"`
	WebhookID      string     `json:"webhook_id"`
	EventType      string     `json:"event_type"`
	State          string     `json:"state"`
	Attempt        int64      `json:"attempt"`
	ResponseStatus *int64     `json:"response_status,omitempty"`
	LastError      string     `json:"last_error,omitempty"`
	NextRetryAt    *time.Time `json:"next_retry_at,omitempty"`
	DeliveredAt    *time.Time `json:"delivered_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}
