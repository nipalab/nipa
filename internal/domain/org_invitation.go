package domain

import (
	"time"

	"github.com/nipalab/nipa/internal/snow"
)

const (
	OrgInvitationPending  = "pending"
	OrgInvitationAccepted = "accepted"
	OrgInvitationDeclined = "declined"
	OrgInvitationRevoked  = "revoked"
	OrgInvitationExpired  = "expired"
)

// IsValidOrgInvitationStatus reports whether status names an invitation state.
func IsValidOrgInvitationStatus(status string) bool {
	switch status {
	case OrgInvitationPending, OrgInvitationAccepted, OrgInvitationDeclined, OrgInvitationRevoked, OrgInvitationExpired:
		return true
	default:
		return false
	}
}

// OrgInvitation is a pending offer to join an organization. Only the hash of
// the acceptance token is ever stored.
type OrgInvitation struct {
	ID          snow.ID    `json:"id"`
	OrgID       snow.ID    `json:"org_id"`
	OrgName     string     `json:"org_name"`
	OrgSlug     string     `json:"org_slug"`
	InvitedBy   snow.ID    `json:"invited_by"`
	InviterName string     `json:"inviter_name"`
	Email       string     `json:"email"`
	Role        string     `json:"role"`
	TokenHash   []byte     `json:"-"`
	Status      string     `json:"status"`
	ExpiresAt   time.Time  `json:"expires_at"`
	AcceptedBy  *snow.ID   `json:"accepted_by,omitempty"`
	AcceptedAt  *time.Time `json:"accepted_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

// EffectiveStatus reports pending invitations past their expiry as expired.
func (i *OrgInvitation) EffectiveStatus(now time.Time) string {
	if i.Status == OrgInvitationPending && !i.ExpiresAt.After(now) {
		return OrgInvitationExpired
	}
	return i.Status
}
