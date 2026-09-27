package webhook

import "time"

// Envelope carries the fields every payload starts with. Event payloads embed
// it and add their subject (changes, merge request, ...).
type Envelope struct {
	Event        string       `json:"event"`
	Timestamp    time.Time    `json:"timestamp"`
	Actor        Actor        `json:"actor"`
	Organization Organization `json:"organization"`
	Project      Project      `json:"project"`
	WebhookID    string       `json:"webhook_id"`
}

type Actor struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Email    string `json:"email,omitempty"`
}

type Organization struct {
	Slug string `json:"slug"`
}

type Project struct {
	Slug          string `json:"slug"`
	DefaultBranch string `json:"default_branch,omitempty"`
}
