package maintenance

import (
	"time"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

// Window is a maintenance window in memory. Org and Project are the current
// slugs of a project window and empty for the server window.
type Window struct {
	ID        snow.ID
	ProjectID *snow.ID
	Org       string
	Project   string
	Mode      Mode
	Message   string
	StartsAt  time.Time
	EndsAt    *time.Time
	Notify    bool
	CreatedBy snow.ID
	UpdatedAt time.Time
}

// Status is the wire shape of a window for status answers.
type Status struct {
	Mode     Mode       `json:"mode"`
	Active   bool       `json:"active"`
	Message  string     `json:"message"`
	StartsAt time.Time  `json:"starts_at"`
	EndsAt   *time.Time `json:"ends_at,omitempty"`
	Project  string     `json:"project,omitempty"`
	Notified *bool      `json:"notified,omitempty"`
}

func FromDomain(w domain.MaintenanceWindow) Window {
	return Window{
		ID:        w.ID,
		ProjectID: w.ProjectID,
		Org:       w.OrgSlug,
		Project:   w.ProjectSlug,
		Mode:      Mode(w.Mode),
		Message:   w.Message,
		StartsAt:  w.StartsAt,
		EndsAt:    w.EndsAt,
		Notify:    w.Notify,
		CreatedBy: w.CreatedBy,
		UpdatedAt: w.UpdatedAt,
	}
}

// Active reports whether the window applies at now. Before StartsAt the window
// is only an announcement; EndsAt never ends it.
func (w *Window) Active(now time.Time) bool {
	return w != nil && w.Mode.Valid() && !now.Before(w.StartsAt)
}

// Key is the "org/project" key of a project window, empty for the server.
func (w *Window) Key() string {
	if w.ProjectID == nil && w.Project == "" {
		return ""
	}
	return ProjectKey(w.Org, w.Project)
}

func ProjectKey(org, project string) string {
	return org + "/" + project
}

func (w *Window) Status(now time.Time) Status {
	return Status{
		Mode:     w.Mode,
		Active:   w.Active(now),
		Message:  w.Message,
		StartsAt: w.StartsAt,
		EndsAt:   w.EndsAt,
		Project:  w.Key(),
	}
}
