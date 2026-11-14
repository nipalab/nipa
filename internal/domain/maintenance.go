package domain

import (
	"time"

	"github.com/nipalab/nipa/internal/snow"
)

// MaintenanceWindow is one stored maintenance window: the server window has
// neither OrgID nor ProjectID, an org window only OrgID, a project window only
// ProjectID. OrgSlug (org and project windows) and ProjectSlug are filled on
// reads.
type MaintenanceWindow struct {
	ID          snow.ID
	OrgID       *snow.ID
	ProjectID   *snow.ID
	OrgSlug     string
	ProjectSlug string
	Mode        string
	Message     string
	StartsAt    time.Time
	EndsAt      *time.Time
	Notify      bool
	CreatedBy   snow.ID
	UpdatedAt   time.Time
}

type MaintenanceRecipient struct {
	UserID snow.ID
	Name   string
	Email  string
}
