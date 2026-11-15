package service

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/desktop/internal/lang"
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/maintenance"
)

const (
	ScopeServer  = "server"
	ScopeOrg     = "org"
	ScopeProject = "project"
)

// MaintenanceWindow is one server, org or project maintenance window; times
// are RFC 3339 and EndsAt is an estimate, empty when unknown.
type MaintenanceWindow struct {
	Scope    string `json:"scope"`
	Mode     string `json:"mode"`
	Active   bool   `json:"active"`
	Message  string `json:"message"`
	StartsAt string `json:"startsAt"`
	EndsAt   string `json:"endsAt"`
	Org      string `json:"org"`
	Project  string `json:"project"`
}

// MaintenanceError is an operation the server refused for maintenance; the
// frontend reads Window from the error's cause.
type MaintenanceError struct {
	Text   string            `json:"message"`
	Window MaintenanceWindow `json:"maintenance"`
}

func (e *MaintenanceError) Error() string { return e.Text }

func toWindow(scope string, w *pb.MaintenanceWindow) *MaintenanceWindow {
	if w == nil {
		return nil
	}
	if scope == "" {
		scope = scopeFor(w.GetOrg(), w.GetProject())
	}
	return &MaintenanceWindow{
		Scope:    scope,
		Mode:     w.GetMode(),
		Active:   w.GetActive(),
		Message:  w.GetMessage(),
		StartsAt: rfc3339(w.GetStartsAt()),
		EndsAt:   rfc3339(w.GetEndsAt()),
		Org:      w.GetOrg(),
		Project:  w.GetProject(),
	}
}

func scopeFor(org, project string) string {
	switch {
	case project != "":
		return ScopeProject
	case org != "":
		return ScopeOrg
	}
	return ScopeServer
}

func toMaintenance(state *daemonpb.MaintenanceState) []MaintenanceWindow {
	var out []MaintenanceWindow
	for _, w := range []*MaintenanceWindow{
		toWindow(ScopeServer, state.GetServer()),
		toWindow(ScopeOrg, state.GetOrg()),
		toWindow(ScopeProject, state.GetProject()),
	} {
		if w != nil {
			out = append(out, *w)
		}
	}
	return out
}

func failureError(f *daemonpb.OpFailure) error {
	if w := toWindow("", f.GetMaintenance()); w != nil {
		return &MaintenanceError{Text: f.GetMessage(), Window: *w}
	}
	return errors.New(f.GetMessage())
}

func maintenanceError(err error) (*MaintenanceError, bool) {
	refused, ok := maintenance.FromGRPC(err)
	if !ok {
		return nil, false
	}
	info := refused.Info
	w := MaintenanceWindow{Scope: string(info.Scope), Mode: string(info.Mode), Active: true, Message: info.Message, Org: info.Org, Project: info.Project}
	if !info.StartsAt.IsZero() {
		w.StartsAt = info.StartsAt.UTC().Format(time.RFC3339)
	}
	if info.EndsAt != nil {
		w.EndsAt = info.EndsAt.UTC().Format(time.RFC3339)
	}
	if w.Scope == "" {
		w.Scope = scopeFor(w.Org, w.Project)
	}
	return &MaintenanceError{Text: status.Convert(err).Message(), Window: w}, true
}

func maintenanceScope(l lang.Language, w MaintenanceWindow) string {
	switch w.Scope {
	case ScopeProject:
		return l.T("maintenance.project", w.Project)
	case ScopeOrg:
		return l.T("maintenance.org", w.Org)
	}
	return l.T("maintenance.server")
}

func localTime(stamp string) string {
	t, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return stamp
	}
	return t.Local().Format("Jan 2 15:04")
}

func maintenanceNotification(l lang.Language, name, root string, e TeamEvent) Notification {
	w := e.Maintenance
	scope := maintenanceScope(l, *w)
	mode := l.T("maintenance.mode." + w.Mode)
	lines := []string{}
	switch e.Message {
	case "ended":
		lines = append(lines, l.T("notify.maintenanceOver", scope))
	case "scheduled":
		lines = append(lines, l.T("notify.maintenanceSoon", scope, mode, localTime(w.StartsAt)))
	default:
		lines = append(lines, l.T("notify.maintenanceNow", scope, mode))
	}
	if e.Message != "ended" {
		if w.Message != "" {
			lines = append(lines, w.Message)
		}
		if w.EndsAt != "" {
			lines = append(lines, l.T("notify.maintenanceUntil", localTime(w.EndsAt)))
		}
	}
	phase := e.Message
	if phase == "" {
		phase = "updated"
	}
	return Notification{
		ID:     fmt.Sprintf("maintenance:%s:%s:%s:%s", root, w.Scope, phase, w.StartsAt),
		Title:  l.T("notify.maintenance."+phase, name),
		Body:   strings.Join(lines, "\n"),
		Target: Navigation{Root: root, Tab: "changes"},
	}
}
