package issuetracker

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeProjectKeys(t *testing.T) {
	keys, err := NormalizeProjectKeys([]string{" game", "ART_2", "GAME"})
	require.NoError(t, err)
	assert.Equal(t, []string{"ART_2", "GAME"}, keys)

	for _, bad := range []string{"", "G", "1GAME", "GA-ME", "GA ME", "_GAME", "GÄME", strings.Repeat("A", 33), "A.B"} {
		_, err := NormalizeProjectKeys([]string{bad})
		assert.Error(t, err, bad)
	}
}

func TestExtract(t *testing.T) {
	keys := []string{"GAME", "art"}
	tests := []struct {
		text string
		want []Ref
	}{
		{"", nil},
		{"nothing here", nil},
		{"GAME-12 tweak", []Ref{{"GAME-12", RelationMentions}}},
		{"Fixes GAME-1", []Ref{{"GAME-1", RelationCloses}}},
		{"fix: GAME-1", []Ref{{"GAME-1", RelationCloses}}},
		{"RESOLVED:GAME-1", []Ref{{"GAME-1", RelationCloses}}},
		{"closes\nGAME-1", []Ref{{"GAME-1", RelationCloses}}},
		{"prefix GAME-1", []Ref{{"GAME-1", RelationMentions}}},
		{"fixesGAME-1", nil},
		{"Fixes GAME-1 and ART-2", []Ref{{"GAME-1", RelationCloses}, {"ART-2", RelationMentions}}},
		{"see GAME-3, then fixed GAME-3", []Ref{{"GAME-3", RelationCloses}}},
		{"closed GAME-3 GAME-3", []Ref{{"GAME-3", RelationCloses}}},
		{"UTF-8 BC-7 XGAME-1 GAME-1x GAME-0 game-4 GAME_1 _GAME-1", nil},
		{"(GAME-5)/GAME-6.", []Ref{{"GAME-5", RelationMentions}, {"GAME-6", RelationMentions}}},
		{"branch GAME-7-new-rig", []Ref{{"GAME-7", RelationMentions}}},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, Extract(tt.text, keys), tt.text)
	}
}

func TestExtractWithoutKeys(t *testing.T) {
	assert.Nil(t, Extract("GAME-1", nil))
	assert.Nil(t, Extract("GAME-1", []string{"bad key"}))
}

func TestExtractQuotesKeys(t *testing.T) {
	assert.Equal(t, []Ref{{"A_B-1", RelationMentions}}, Extract("A_B-1 AXB-1", []string{"A_B"}))
}

func TestExtractCap(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= MaxRefs+5; i++ {
		fmt.Fprintf(&b, "GAME-%d ", i)
	}
	b.WriteString("fixes GAME-1 fixes GAME-25")
	refs := Extract(b.String(), []string{"GAME"})
	require.Len(t, refs, MaxRefs)
	assert.Equal(t, Ref{"GAME-1", RelationCloses}, refs[0])
	assert.Equal(t, "GAME-20", refs[MaxRefs-1].Key)
}

func TestBranchKey(t *testing.T) {
	keys := []string{"GAME", "ART"}
	tests := []struct {
		name string
		want string
	}{
		{"GAME-123-new-rig", "GAME-123"},
		{"feature/GAME-123-new-rig", "GAME-123"},
		{"user/feature/art-9_fix", "ART-9"},
		{"GAME-5", "GAME-5"},
		{"feature/GAME-5/sub", ""},
		{"main", ""},
		{"new-GAME-1", ""},
		{"GAME-12a", ""},
		{"GAME-0-x", ""},
		{"OTHER-1", ""},
	}
	for _, tt := range tests {
		got, ok := BranchKey(tt.name, keys)
		assert.Equal(t, tt.want, got, tt.name)
		assert.Equal(t, tt.want != "", ok, tt.name)
	}
	_, ok := BranchKey("GAME-1", nil)
	assert.False(t, ok)
}

func TestPatternCacheBounded(t *testing.T) {
	for i := 0; i < maxCachedRegexp+10; i++ {
		key := fmt.Sprintf("K%d", i)
		require.Len(t, Extract(key+"-1", []string{key}), 1)
	}
	patternMu.Lock()
	defer patternMu.Unlock()
	assert.LessOrEqual(t, len(patternCache), maxCachedRegexp)
}

func TestParseKey(t *testing.T) {
	keys := []string{"GAME", "art"}
	for raw, want := range map[string]string{
		"GAME-1":           "GAME-1",
		" game-42 ":        "GAME-42",
		"ART-7":            "ART-7",
		"OTHER-1":          "",
		"GAME-0":           "",
		"GAME-":            "",
		"-1":               "",
		"GAME":             "",
		"GAME-1a":          "",
		"G-A-M-E-1":        "",
		"GAME-12345678901": "",
	} {
		got, ok := ParseKey(raw, keys)
		if want == "" {
			if ok {
				t.Errorf("ParseKey(%q) = %q, want rejected", raw, got)
			}
			continue
		}
		if !ok || got != want {
			t.Errorf("ParseKey(%q) = %q, %v, want %q", raw, got, ok, want)
		}
	}
	if _, ok := ParseKey("GAME-1", nil); ok {
		t.Error("ParseKey without keys accepted a key")
	}
}
