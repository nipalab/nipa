package model

import "time"

type AuditEntryResponse struct {
	ID          string         `json:"id"`
	OccurredAt  time.Time      `json:"occurred_at"`
	ActorType   string         `json:"actor_type"`
	ActorUserID string         `json:"actor_user_id,omitempty"`
	ActorLabel  string         `json:"actor_label,omitempty"`
	Action      string         `json:"action"`
	OrgID       string         `json:"org_id,omitempty"`
	ProjectID   string         `json:"project_id,omitempty"`
	TargetType  string         `json:"target_type,omitempty"`
	TargetID    string         `json:"target_id,omitempty"`
	Metadata    map[string]any `json:"metadata"`
	RequestID   string         `json:"request_id,omitempty"`
	ClientIP    string         `json:"client_ip,omitempty"`
	UserAgent   string         `json:"user_agent,omitempty"`
}

// AuditListResponse is one page of audit entries. NextCursor is set when
// another page exists; pass it as the after query parameter to continue.
type AuditListResponse struct {
	Entries    []AuditEntryResponse `json:"entries"`
	NextCursor string               `json:"next_cursor,omitempty"`
}
