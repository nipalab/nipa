package tray

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/desktop/internal/lang"
	"github.com/nipalab/nipa/desktop/internal/service"
)

func TestTooltip(t *testing.T) {
	require.Equal(t, "Nipa", Tooltip(lang.English, nil))
	require.Equal(t, "Nipa: up to date", Tooltip(lang.English, []service.RepoActivity{{Name: "game"}}))
	require.Equal(t, "Nipa: 101+ new commits, 1 review waiting, 1 clone unreachable", Tooltip(lang.English, []service.RepoActivity{
		{Name: "game", Incoming: 100, ReviewRequests: 1},
		{Name: "tools", Incoming: 1, Error: "offline"},
	}))
	require.Equal(t, "Nipa: 1 new commit, 2 reviews waiting, 2 clones unreachable", Tooltip(lang.English, []service.RepoActivity{
		{Incoming: 1, ReviewRequests: 2, Error: "x"}, {Error: "y"},
	}))
}

func TestMenu(t *testing.T) {
	require.Equal(t, []Entry{
		{Label: "Open Nipa", Action: ActionShow},
		{Separator: true},
		{Label: "Settings…", Action: ActionNavigate, Target: service.Navigation{Tab: "settings"}},
		{Label: "Quit Nipa", Action: ActionQuit},
	}, Menu(lang.English, nil))

	entries := Menu(lang.English, []service.RepoActivity{
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

func TestLocalizedTray(t *testing.T) {
	list := []service.RepoActivity{{Name: "game", Root: "/g", Incoming: 2, Error: "x"}}
	require.Equal(t, "Nipa: 新しいコミット 2 件, 接続できないクローン 1 件", Tooltip(lang.Japanese, list))
	entries := Menu(lang.Korean, list)
	require.Equal(t, "Nipa 열기", entries[0].Label)
	require.Equal(t, "game · 새 커밋 2개 · 연결할 수 없음", entries[2].Label)
	require.Equal(t, "Nipa 종료", entries[len(entries)-1].Label)
}

func TestMaintenanceInTray(t *testing.T) {
	active := []service.MaintenanceWindow{{Scope: service.ScopeProject, Mode: "read_only", Active: true}}
	scheduled := []service.MaintenanceWindow{{Scope: service.ScopeServer, Mode: "full"}}
	list := []service.RepoActivity{
		{Root: "/a", Name: "art", Maintenance: active},
		{Root: "/b", Name: "code", Maintenance: scheduled},
		{Root: "/c", Name: "docs", Maintenance: append(append([]service.MaintenanceWindow{}, scheduled...), active...)},
	}
	require.Equal(t, "Nipa: 2 clones in maintenance, 1 clone with maintenance scheduled", Tooltip(lang.English, list))
	entries := Menu(lang.English, list)
	require.Equal(t, "art · maintenance", entries[2].Label)
	require.Equal(t, "code", entries[3].Label)
	require.Equal(t, "メンテナンス中のクローン 2 件, メンテナンス予定のクローン 1 件", Tooltip(lang.Japanese, list)[len("Nipa: "):])
}
