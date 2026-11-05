package tray

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/desktop/internal/service"
)

func TestTooltip(t *testing.T) {
	require.Equal(t, "Nipa", Tooltip(nil))
	require.Equal(t, "Nipa: up to date", Tooltip([]service.RepoActivity{{Name: "game"}}))
	require.Equal(t, "Nipa: 101+ new commits, 1 review waiting, 1 clone unreachable", Tooltip([]service.RepoActivity{
		{Name: "game", Incoming: 100, ReviewRequests: 1},
		{Name: "tools", Incoming: 1, Error: "offline"},
	}))
	require.Equal(t, "Nipa: 1 new commit, 2 reviews waiting, 2 clones unreachable", Tooltip([]service.RepoActivity{
		{Incoming: 1, ReviewRequests: 2, Error: "x"}, {Error: "y"},
	}))
}

func TestMenu(t *testing.T) {
	require.Equal(t, []Entry{
		{Label: "Open Nipa", Action: ActionShow},
		{Separator: true},
		{Label: "Settings…", Action: ActionNavigate, Target: service.Navigation{Tab: "settings"}},
		{Label: "Quit Nipa", Action: ActionQuit},
	}, Menu(nil))

	entries := Menu([]service.RepoActivity{
		{Root: "/a", Name: "art", Incoming: 2, ReviewRequests: 1},
		{Root: "/b", Name: "code", ReviewRequests: 3},
		{Root: "/c", Name: "docs", Error: "offline"},
	})
	require.Len(t, entries, 8)
	require.True(t, entries[1].Separator)
	require.Equal(t, Entry{Label: "art · 2 new commits · 1 review", Action: ActionNavigate, Target: service.Navigation{Root: "/a", Tab: "history"}}, entries[2])
	require.Equal(t, Entry{Label: "code · 3 reviews", Action: ActionNavigate, Target: service.Navigation{Root: "/b", Tab: "mrs"}}, entries[3])
	require.Equal(t, Entry{Label: "docs · unreachable", Action: ActionNavigate, Target: service.Navigation{Root: "/c", Tab: "changes"}}, entries[4])
}
