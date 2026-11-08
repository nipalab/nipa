package model

import "time"

type CreateOrgInvitationRequest struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

type OrgInvitationRequestedResponse struct {
	Status string `json:"status"`
}

type OrgInvitationResponse struct {
	ID          string     `json:"id"`
	OrgID       string     `json:"org_id"`
	OrgSlug     string     `json:"org_slug"`
	OrgName     string     `json:"org_name"`
	Email       string     `json:"email"`
	Role        string     `json:"role"`
	Status      string     `json:"status"`
	InvitedBy   string     `json:"invited_by"`
	InviterName string     `json:"inviter_name"`
	ExpiresAt   time.Time  `json:"expires_at"`
	CreatedAt   time.Time  `json:"created_at"`
	AcceptedAt  *time.Time `json:"accepted_at,omitempty"`
}
