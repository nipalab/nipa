package daemon

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/client/localrepo"
)

func hitPaths(hits []*daemonpb.SearchFileHit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.GetPath())
	}
	return out
}

func TestServer_SearchFilesRanksBasenameFirst(t *testing.T) {
	root := newTreeClone(t, map[string]string{
		"README.md":                  "hello\n",
		"Assets/hero.png":            "\x89PNG\x00pixels",
		"Assets/Hero/armor.png":      "\x89PNG\x00armor",
		"Assets/superhero_icon.png":  "\x89PNG\x00icon",
		"Assets/h_e_r_o.txt":         "spread\n",
		"Source/Player/Movement.cs":  "class Movement {}\n",
		"Source/Player/PlayerHUD.cs": "class PlayerHUD {}\n",
	})
	srv := newOpServer(t, RepoOps{})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)
	ctx := context.Background()

	res, err := srv.SearchFiles(ctx, &daemonpb.SearchFilesRequest{Root: root, Query: "hero"})
	require.NoError(t, err)
	require.False(t, res.GetTruncated())
	require.Equal(t, []string{"Assets/hero.png", "Assets/superhero_icon.png", "Assets/h_e_r_o.txt", "Assets/Hero/armor.png"}, hitPaths(res.GetHits()))
	first := res.GetHits()[0]
	require.Equal(t, "hero.png", first.GetName())
	require.Equal(t, "image", first.GetKind())
	require.True(t, first.GetIsBinary())
	require.Equal(t, int64(len("\x89PNG\x00pixels")), first.GetSizeBytes())

	res, err = srv.SearchFiles(ctx, &daemonpb.SearchFilesRequest{Root: root, Query: "pl mov"})
	require.NoError(t, err)
	require.Equal(t, []string{"Source/Player/Movement.cs"}, hitPaths(res.GetHits()))

	res, err = srv.SearchFiles(ctx, &daemonpb.SearchFilesRequest{Root: root, Query: "cs", Limit: 1})
	require.NoError(t, err)
	require.Len(t, res.GetHits(), 1)

	res, err = srv.SearchFiles(ctx, &daemonpb.SearchFilesRequest{Root: root, Query: " "})
	require.NoError(t, err)
	require.Empty(t, res.GetHits())

	_, err = srv.SearchFiles(ctx, &daemonpb.SearchFilesRequest{Root: t.TempDir(), Query: "x"})
	require.Error(t, err)
}

func TestRankPaths(t *testing.T) {
	hits := []localrepo.PathHit{
		{Path: "a/b/long_theme_music.wav"},
		{Path: "Music/theme.wav"},
		{Path: "x/THEME.WAV"},
		{Path: "t/h/e/m/e.txt"},
		{Path: "docs/the_mega_entry.md"},
		{Path: "Café/menu.png"},
		{Path: "CAFÉ/x.png"},
	}
	ranked := rankPaths("theme", hits, 50)
	paths := make([]string, 0, len(ranked))
	for _, h := range ranked {
		paths = append(paths, h.Path)
	}
	require.Equal(t, []string{"x/THEME.WAV", "Music/theme.wav", "a/b/long_theme_music.wav", "docs/the_mega_entry.md", "t/h/e/m/e.txt"}, paths)

	ranked = rankPaths("café", hits, 50)
	require.Len(t, ranked, 2)

	require.Len(t, rankPaths("e", hits, 2), 2)
	require.Nil(t, rankPaths("  ", hits, 10))
}

func TestSubsequenceSpanPicksTightestWindow(t *testing.T) {
	span, ok := subsequenceSpan([]rune("ab"), []rune("a__ab"))
	require.True(t, ok)
	require.Equal(t, 2, span)
	_, ok = subsequenceSpan([]rune("ba"), []rune("ab"))
	require.False(t, ok)
}

func BenchmarkRankPaths(b *testing.B) {
	hits := make([]localrepo.PathHit, 0, searchPrefilter)
	for i := range searchPrefilter {
		hits = append(hits, localrepo.PathHit{Path: fmt.Sprintf("Assets/Textures/Environment/Set%03d/rock_albedo_%05d.png", i%300, i)})
	}
	b.ResetTimer()
	for range b.N {
		rankPaths("rkalb", hits, defaultSearchLimit)
	}
}
