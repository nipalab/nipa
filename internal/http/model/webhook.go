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
