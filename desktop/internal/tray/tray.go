// Package tray turns clone activity into the tray tooltip and menu.
package tray

import (
	"fmt"
	"strings"

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
func Tooltip(list []service.RepoActivity) string {
	incoming, reviews, offline := 0, 0, 0
	for _, a := range list {
		incoming += a.Incoming
		reviews += a.ReviewRequests
		if a.Error != "" {
			offline++
		}
	}
	var parts []string
	if incoming > 0 {
		parts = append(parts, plural(incoming, "new commit", "new commits"))
	}
	if reviews > 0 {
		parts = append(parts, plural(reviews, "review waiting", "reviews waiting"))
	}
	if offline > 0 {
		parts = append(parts, plural(offline, "clone unreachable", "clones unreachable"))
	}
	if len(parts) == 0 {
		if len(list) == 0 {
			return "Nipa"
		}
		return "Nipa: up to date"
	}
	return "Nipa: " + strings.Join(parts, ", ")
}

// Menu lists the clones with what waits on each, plus the app actions.
func Menu(list []service.RepoActivity) []Entry {
	entries := []Entry{{Label: "Open Nipa", Action: ActionShow}}
	if len(list) > 0 {
		entries = append(entries, Entry{Separator: true})
	}
	for _, a := range list {
		entries = append(entries, Entry{Label: label(a), Action: ActionNavigate, Target: target(a)})
	}
	return append(entries,
		Entry{Separator: true},
		Entry{Label: "Settings…", Action: ActionNavigate, Target: service.Navigation{Tab: "settings"}},
		Entry{Label: "Quit Nipa", Action: ActionQuit},
	)
}

func label(a service.RepoActivity) string {
	parts := []string{a.Name}
	if a.Incoming > 0 {
		parts = append(parts, plural(a.Incoming, "new commit", "new commits"))
	}
	if a.ReviewRequests > 0 {
		parts = append(parts, plural(a.ReviewRequests, "review", "reviews"))
	}
	if a.Error != "" {
		parts = append(parts, "unreachable")
	}
	return strings.Join(parts, " · ")
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

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	if n >= 100 {
		return fmt.Sprintf("%d+ %s", n, many)
	}
	return fmt.Sprintf("%d %s", n, many)
}
