package domain

import (
	"fmt"
	"strings"
	"time"

	"github.com/nipalab/nipa/internal/maintenance"
)

// MaintenanceWindow is one window of a maintenance status answer.
type MaintenanceWindow struct {
	Mode     string     `json:"mode"`
	Active   bool       `json:"active"`
	Message  string     `json:"message,omitempty"`
	StartsAt time.Time  `json:"starts_at,omitzero"`
	EndsAt   *time.Time `json:"ends_at,omitempty"`
	Org      string     `json:"org,omitempty"`
	Project  string     `json:"project,omitempty"`
}

// MaintenanceStatus is the server's answer for a clone: the server window and,
// for a signed-in caller with access, the org and project windows.
type MaintenanceStatus struct {
	Server  *MaintenanceWindow `json:"server,omitempty"`
	Org     *MaintenanceWindow `json:"org,omitempty"`
	Project *MaintenanceWindow `json:"project,omitempty"`
	Source  string             `json:"source,omitempty"`
}

// Windows returns the set windows, broadest scope first.
func (s *MaintenanceStatus) Windows() []*MaintenanceWindow {
	if s == nil {
		return nil
	}
	var out []*MaintenanceWindow
	for _, w := range []*MaintenanceWindow{s.Server, s.Org, s.Project} {
		if w != nil {
			out = append(out, w)
		}
	}
	return out
}

// Refusal returns the error a write would get from the strictest active
// window, nil when no window is active.
func (s *MaintenanceStatus) Refusal() *Error {
	var strictest *MaintenanceWindow
	mode := maintenance.Off
	for _, w := range s.Windows() {
		if !w.Active {
			continue
		}
		if next := maintenance.Stricter(mode, maintenance.Mode(w.Mode)); next != mode {
			strictest, mode = w, next
		}
	}
	if strictest == nil {
		return nil
	}
	return NewMaintenanceError(strictest.Info(), nil)
}

// NextStart returns the earliest scheduled window, nil when none is.
func (s *MaintenanceStatus) NextStart() *MaintenanceWindow {
	var next *MaintenanceWindow
	for _, w := range s.Windows() {
		if w.Active || w.StartsAt.IsZero() {
			continue
		}
		if next == nil || w.StartsAt.Before(next.StartsAt) {
			next = w
		}
	}
	return next
}

func (w *MaintenanceWindow) Scope() maintenance.Scope {
	switch {
	case w.Project != "":
		return maintenance.ScopeProject
	case w.Org != "":
		return maintenance.ScopeOrg
	default:
		return maintenance.ScopeServer
	}
}

func (w *MaintenanceWindow) Info() maintenance.Info {
	return maintenance.Info{
		Scope:    w.Scope(),
		Mode:     maintenance.Mode(w.Mode),
		Message:  w.Message,
		StartsAt: w.StartsAt,
		EndsAt:   w.EndsAt,
		Org:      w.Org,
		Project:  w.Project,
	}
}

// Describe renders the window on one line, e.g. "server read-only until
// ~14:00 UTC: moving storage" or "project acme/game closed from 2026-11-15
// 14:00 UTC (scheduled)".
func (w *MaintenanceWindow) Describe() string {
	var b strings.Builder
	b.WriteString(subject(w.Info()))
	b.WriteString(" " + maintenance.Mode(w.Mode).Label())
	if !w.Active && !w.StartsAt.IsZero() {
		fmt.Fprintf(&b, " from %s (scheduled)", formatUTC(w.StartsAt))
	}
	if w.EndsAt != nil {
		fmt.Fprintf(&b, " until ~%s", formatUTC(*w.EndsAt))
	}
	if w.Message != "" {
		b.WriteString(": " + w.Message)
	}
	return b.String()
}

func subject(info maintenance.Info) string {
	switch info.Scope {
	case maintenance.ScopeProject:
		return "project " + info.Project
	case maintenance.ScopeOrg:
		return "organization " + info.Org
	default:
		return "server"
	}
}

func formatUTC(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04") + " UTC"
}

// NewMaintenanceError builds the 503 a maintenance refusal maps to. A nil
// cause is replaced by the gRPC form of the refusal, so every maintenance
// error carries the same status details.
func NewMaintenanceError(info maintenance.Info, cause error) *Error {
	refused := &maintenance.Error{Info: info}
	if cause == nil {
		cause = refused.GRPCError(time.Now())
	}
	return &Error{
		Code:    503,
		Message: refused.Error(),
		Hint:    MaintenanceHint(info),
		Cause:   cause,
		Maintenance: &MaintenanceInfo{
			Scope:    string(info.Scope),
			Mode:     string(info.Mode),
			Message:  info.Message,
			StartsAt: info.StartsAt,
			EndsAt:   info.EndsAt,
			Org:      info.Org,
			Project:  info.Project,
		},
	}
}

func MaintenanceHint(info maintenance.Info) string {
	switch info.Mode {
	case maintenance.ReadOnly:
		return "read-only commands still work"
	case maintenance.Frozen:
		return "sign-in is paused; commands that read work while your session lasts"
	default:
		if info.EndsAt != nil {
			return "try again after ~" + info.EndsAt.Local().Format(time.DateTime)
		}
		return "try again later"
	}
}

// Describe renders the refusal for terminals: the message, the estimated end
// and the hint.
func (m *MaintenanceInfo) Describe(message, hint string) string {
	lines := []string{message}
	if m.EndsAt != nil {
		lines = append(lines, "estimated end: "+formatUTC(*m.EndsAt)+" (maintenance ends manually)")
	}
	if hint != "" {
		lines = append(lines, "hint: "+hint)
	}
	return strings.Join(lines, "\n")
}
