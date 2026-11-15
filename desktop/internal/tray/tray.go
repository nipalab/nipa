// Package tray turns clone activity into the tray tooltip and menu.
package tray

import (
	"strings"

	"github.com/nipalab/nipa/desktop/internal/lang"
	"github.com/nipalab/nipa/desktop/internal/service"
)

type ActionKind int

const (
	ActionNone ActionKind = iota
	ActionShow
	ActionNavigate
	ActionQuit
)

// Entry is one tray menu row; a separator has no label.
type Entry struct {
	Label     string
	Separator bool
	Action    ActionKind
	Target    service.Navigation
}

// Tooltip summarizes every clone in one line.
func Tooltip(l lang.Language, list []service.RepoActivity) string {
	incoming, reviews, offline, closed, soon := 0, 0, 0, 0, 0
	for _, a := range list {
		incoming += a.Incoming
		reviews += a.ReviewRequests
		if a.Error != "" {
			offline++
		}
		switch {
		case inMaintenance(a):
			closed++
		case len(a.Maintenance) > 0:
			soon++
		}
	}
	var parts []string
	if incoming > 0 {
		parts = append(parts, l.N("tray.commits", incoming))
	}
	if reviews > 0 {
		parts = append(parts, l.N("tray.reviewsWaiting", reviews))
	}
	if offline > 0 {
		parts = append(parts, l.N("tray.unreachable", offline))
	}
	if closed > 0 {
		parts = append(parts, l.N("tray.maintenance", closed))
	}
	if soon > 0 {
		parts = append(parts, l.N("tray.maintenanceSoon", soon))
	}
	if len(parts) == 0 {
		if len(list) == 0 {
			return "Nipa"
		}
		return l.T("tray.upToDate")
	}
	return "Nipa: " + strings.Join(parts, ", ")
}

// Menu lists the clones with what waits on each, plus the app actions.
func Menu(l lang.Language, list []service.RepoActivity) []Entry {
	entries := []Entry{{Label: l.T("tray.open"), Action: ActionShow}}
	if len(list) > 0 {
		entries = append(entries, Entry{Separator: true})
	}
	for _, a := range list {
		entries = append(entries, Entry{Label: label(l, a), Action: ActionNavigate, Target: target(a)})
	}
	return append(entries,
		Entry{Separator: true},
		Entry{Label: l.T("tray.settings"), Action: ActionNavigate, Target: service.Navigation{Tab: "settings"}},
		Entry{Label: l.T("tray.quit"), Action: ActionQuit},
	)
}

func label(l lang.Language, a service.RepoActivity) string {
	parts := []string{a.Name}
	if a.Incoming > 0 {
		parts = append(parts, l.N("tray.commits", a.Incoming))
	}
	if a.ReviewRequests > 0 {
		parts = append(parts, l.N("tray.reviews", a.ReviewRequests))
	}
	if a.Error != "" {
		parts = append(parts, l.T("tray.cloneUnreachable"))
	}
	if inMaintenance(a) {
		parts = append(parts, l.T("tray.cloneMaintenance"))
	}
	return strings.Join(parts, " · ")
}

func inMaintenance(a service.RepoActivity) bool {
	for _, w := range a.Maintenance {
		if w.Active {
			return true
		}
	}
	return false
}

func target(a service.RepoActivity) service.Navigation {
	switch {
	case a.ReviewRequests > 0 && a.Incoming == 0:
		return service.Navigation{Root: a.Root, Tab: "mrs"}
	case a.Incoming > 0:
		return service.Navigation{Root: a.Root, Tab: "history"}
	}
	return service.Navigation{Root: a.Root, Tab: "changes"}
}
