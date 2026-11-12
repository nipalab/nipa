package service

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseLink(t *testing.T) {
	for raw, want := range map[string]Navigation{
		"nipa://nipa.example.com/studio/game":                       {Tab: "changes", Server: "https://nipa.example.com", Org: "studio", Project: "game"},
		"NIPA://nipa.example.com/studio/game/":                      {Tab: "changes", Server: "https://nipa.example.com", Org: "studio", Project: "game"},
		"nipa://nipa.example.com/studio/game/merge-requests/12":     {Tab: "mrs", Number: 12, Server: "https://nipa.example.com", Org: "studio", Project: "game"},
		"nipa://nipa.example.com/studio/game/merge-requests":        {Tab: "mrs", Server: "https://nipa.example.com", Org: "studio", Project: "game"},
		"nipa://nipa.example.com/studio/game/commits/abc12":         {Tab: "history", Commit: "abc12", Server: "https://nipa.example.com", Org: "studio", Project: "game"},
		"nipa://localhost:6811/studio/game/locks":                   {Tab: "locks", Server: "http://localhost:6811", Org: "studio", Project: "game"},
		"nipa://127.0.0.1:6811/studio/game/branches":                {Tab: "branches", Server: "http://127.0.0.1:6811", Org: "studio", Project: "game"},
		"nipa://vcs.lan/studio/game/history?server=http://vcs.lan":  {Tab: "history", Server: "http://vcs.lan", Org: "studio", Project: "game"},
		"nipa://vcs.lan/studio/game/settings?server=ftp://x":        {Tab: "details", Server: "https://vcs.lan", Org: "studio", Project: "game"},
		"nipa://vcs.lan/studio/game/files":                          {Tab: "files", Server: "https://vcs.lan", Org: "studio", Project: "game"},
		"nipa://vcs.lan/studio/game/file/Assets/Hero/hero%20v2.png": {Tab: "files", Server: "https://vcs.lan", Org: "studio", Project: "game", Path: "Assets/Hero/hero v2.png"},
		"nipa://vcs.lan/studio/game/file/readme.md":                 {Tab: "files", Server: "https://vcs.lan", Org: "studio", Project: "game", Path: "readme.md"},
	} {
		got, err := ParseLink(raw)
		require.NoError(t, err, raw)
		require.Equal(t, want, got, raw)
	}
	for _, raw := range []string{
		"https://nipa.example.com/studio/game",
		"nipa:///studio/game",
		"nipa://host/studio",
		"nipa://host/studio/game/unknown",
		"nipa://host/studio/game/merge-requests/x",
		"nipa://host/studio/game/merge-requests/0",
		"nipa://host/studio/game/locks/extra",
		"nipa://host/a/b/c/d/e",
		"nipa://host/studio/game/file",
		"nipa://host//game",
		"::bad",
	} {
		_, err := ParseLink(raw)
		require.ErrorIs(t, err, errBadLink, raw)
	}
}

func TestLinkArg(t *testing.T) {
	require.Equal(t, "nipa://h/o/p", LinkArg([]string{"--background", "nipa://h/o/p"}))
	require.Empty(t, LinkArg([]string{"--background"}))
}

func TestService_OpenLinkMatchesOpenedClones(t *testing.T) {
	h := newHarness()
	h.store.repos = []string{"/work/game", "/work/other"}
	var events []any
	h.svc.Events = func(name string, data any) {
		if name == EventNavigate {
			events = append(events, data)
		}
	}

	nav, err := h.svc.OpenLink(ctx, "nipa://nipa.test/org/game/merge-requests/3")
	require.NoError(t, err)
	require.Equal(t, "/work/game", nav.Root)
	require.Equal(t, int64(3), nav.Number)
	require.Equal(t, []any{nav}, events)
	require.Equal(t, &nav, h.svc.TakeNavigation())
	require.Nil(t, h.svc.TakeNavigation())

	nav, err = h.svc.OpenLink(ctx, "nipa://nipa.test/org/missing")
	require.NoError(t, err)
	require.Empty(t, nav.Root)
	require.Equal(t, "https://nipa.test", nav.Server)

	_, err = h.svc.OpenLink(ctx, "bad")
	require.ErrorIs(t, err, errBadLink)

	h.store.err = errors.New("disk")
	_, err = h.svc.OpenLink(ctx, "nipa://nipa.test/org/game")
	require.Error(t, err)
}

func TestService_OpenLinkSkipsUnparsableURLs(t *testing.T) {
	h := newHarness()
	h.store.repos = []string{"/work/game"}
	h.daemon.badURL = true
	nav, err := h.svc.OpenLink(ctx, "nipa://nipa.test/org/game")
	require.NoError(t, err)
	require.Empty(t, nav.Root)
}
