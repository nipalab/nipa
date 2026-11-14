package domain

import (
	"time"

	"github.com/nipalab/nipa/internal/snow"
)

// MaintenanceWindow is one stored maintenance window: the server window has no
// ProjectID. OrgSlug and ProjectSlug are filled for project windows on reads.
type MaintenanceWindow struct {
	ID          snow.ID
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
