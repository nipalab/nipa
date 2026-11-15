package domain

import (
	"time"

	"github.com/nipalab/nipa/internal/snow"
)

// SignupInvite allows one email address to create an account while open
// registration is closed.
type SignupInvite struct {
	ID        snow.ID
	Email     string
	InvitedBy snow.ID
	CreatedAt time.Time
	ExpiresAt *time.Time
}
