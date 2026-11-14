package maintenance

import (
	"time"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

// Window is a maintenance window in memory. Org is the current org slug of an
// org or project window, Project the project slug of a project window.
type Window struct {
	ID        snow.ID
	OrgID     *snow.ID
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
	Org      string     `json:"org,omitempty"`
	Project  string     `json:"project,omitempty"`
	Notified *bool      `json:"notified,omitempty"`
}

func FromDomain(w domain.MaintenanceWindow) Window {
	return Window{
		ID:        w.ID,
		OrgID:     w.OrgID,
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

func (w *Window) Scope() Scope {
	switch {
	case w.ProjectID != nil || w.Project != "":
		return ScopeProject
	case w.OrgID != nil || w.Org != "":
		return ScopeOrg
	default:
		return ScopeServer
	}
}

// Key is the snapshot key: "org/project" for a project window, the org slug
// for an org window and empty for the server window.
func (w *Window) Key() string {
	switch w.Scope() {
	case ScopeProject:
		return ProjectKey(w.Org, w.Project)
	case ScopeOrg:
		return w.Org
	default:
		return ""
	}
}

func ProjectKey(org, project string) string {
	return org + "/" + project
}

func (w *Window) Status(now time.Time) Status {
	st := Status{
		Mode:     w.Mode,
		Active:   w.Active(now),
		Message:  w.Message,
		StartsAt: w.StartsAt,
		EndsAt:   w.EndsAt,
	}
	switch w.Scope() {
	case ScopeProject:
		st.Org, st.Project = w.Org, w.Key()
	case ScopeOrg:
		st.Org = w.Org
	}
	return st
}
