package localrepo

import (
	"fmt"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

func hitPaths(hits []PathHit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.Path)
	}
	sort.Strings(out)
	return out
}

func TestSearchPathsMatchesSubsequence(t *testing.T) {
	l := NewLocalRepo()
	require.NoError(t, l.Init(t.TempDir()))
	defer func() { _ = l.Close() }()
	bin := tf(4)
	bin.binary = true
	require.NoError(t, l.SaveTree(testTree(1, map[string]testFile{
		"README.md":                tf(1),
		"Assets/hero.png":          bin,
		"Assets/Audio/theme.wav":   tf(3),
		"Source/Hero/Movement.cs":  tf(6),
		"Docs/100%_done.md":        tf(7),
		"Docs/100 percent done.md": tf(8),
		"Art/Café/menu.png":        tf(9),
	})))

	hits, truncated, err := l.SearchPaths("HERO", 50)
	require.NoError(t, err)
	require.False(t, truncated)
	require.Equal(t, []string{"Assets/hero.png", "Source/Hero/Movement.cs"}, hitPaths(hits))
	for _, h := range hits {
		if h.Path == "Assets/hero.png" {
			require.True(t, h.IsBinary)
			require.Equal(t, int64(4), h.SizeBytes)
		}
	}

	hits, _, err = l.SearchPaths("aud thm", 50)
	require.NoError(t, err)
	require.Equal(t, []string{"Assets/Audio/theme.wav"}, hitPaths(hits))

	hits, _, err = l.SearchPaths("%_", 50)
	require.NoError(t, err)
	require.Equal(t, []string{"Docs/100%_done.md"}, hitPaths(hits))

	hits, _, err = l.SearchPaths("éme", 50)
	require.NoError(t, err)
	require.Contains(t, hitPaths(hits), "Art/Café/menu.png")

	hits, truncated, err = l.SearchPaths("   ", 50)
	require.NoError(t, err)
	require.False(t, truncated)
	require.Empty(t, hits)

	hits, _, err = l.SearchPaths("zzz", 50)
	require.NoError(t, err)
	require.Empty(t, hits)
}

func TestSearchPathsTruncates(t *testing.T) {
	l := NewLocalRepo()
	require.NoError(t, l.Init(t.TempDir()))
	defer func() { _ = l.Close() }()
	spec := map[string]testFile{}
	for i := range 30 {
		spec[fmt.Sprintf("Big/f%02d.png", i)] = tf(byte(i + 1))
	}
	require.NoError(t, l.SaveTree(testTree(1, spec)))

	hits, truncated, err := l.SearchPaths("png", 10)
	require.NoError(t, err)
	require.True(t, truncated)
	require.Len(t, hits, 10)

	hits, truncated, err = l.SearchPaths("png", 30)
	require.NoError(t, err)
	require.False(t, truncated)
	require.Len(t, hits, 30)

	hits, _, err = l.SearchPaths("png", 0)
	require.NoError(t, err)
	require.Empty(t, hits)
}

func TestSearchPathsUninitialized(t *testing.T) {
	_, _, err := NewLocalRepo().SearchPaths("x", 10)
	require.Error(t, err)
}

func TestSubsequencePattern(t *testing.T) {
	require.Equal(t, "%a%b%", SubsequencePattern("ab"))
	require.Equal(t, "%a%b%", SubsequencePattern(" a b "))
	require.Equal(t, `%\%%\_%\\%`, SubsequencePattern(`%_\`))
	require.Equal(t, "%c%_%", SubsequencePattern("cé"))
	require.Equal(t, "", SubsequencePattern(" \t"))
}
