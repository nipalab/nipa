package pathmatch

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCompile_Matches(t *testing.T) {
	tests := []struct {
		name     string
		patterns []string
		path     string
		isDir    bool
		want     bool
	}{
		{"basename at depth", []string{"*.png"}, "Assets/Textures/a.png", false, true},
		{"anchored double star", []string{"Assets/Textures/**"}, "Assets/Textures/ui/a.png", false, true},
		{"anchored double star other dir", []string{"Assets/Textures/**"}, "Assets/Audio/a.wav", false, false},
		{"middle double star", []string{"Assets/Characters/**/*.fbx"}, "Assets/Characters/hero/hero.fbx", false, true},
		{"middle double star direct", []string{"Assets/Characters/**/*.fbx"}, "Assets/Characters/hero.fbx", false, true},
		{"leading double star", []string{"**/*.glb"}, "a/b/c.glb", false, true},
		{"only double star", []string{"**"}, "a/b", false, true},
		{"directory covers children", []string{"Assets/Audio"}, "Assets/Audio/x/y.wav", false, true},
		{"dir only skips files", []string{"build/"}, "build", false, false},
		{"negation", []string{"*.png", "!ui/*.png"}, "ui/a.png", false, false},
		{"question mark", []string{"a?.png"}, "ab.png", false, true},
		{"comment and blanks", []string{"# x", "", "!", "/", "*.wav"}, "a.wav", false, true},
		{"no match", []string{"*.wav"}, "a.png", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, Compile(tt.patterns).Matches(tt.path, tt.isDir))
		})
	}
}

func TestPatterns_EmptyAndJoin(t *testing.T) {
	var nilPatterns *Patterns
	require.True(t, nilPatterns.Empty())
	require.False(t, nilPatterns.Matches("a", false))
	require.True(t, Parse("# only\n").Empty())
	p := Compile([]string{"*.png"})
	require.False(t, p.Matches("", false))
	require.False(t, p.Matches(".", true))
	joined := Join(p, nil, Parse("!keep.png"))
	require.True(t, joined.Matches("a.png", false))
	require.False(t, joined.Matches("keep.png", false))
}
