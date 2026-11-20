package issuetracker

import (
	"regexp"
	"strings"
	"testing"
)

var fuzzKeys = []string{"GAME", "ART_2"}

var refShape = regexp.MustCompile(`^(GAME|ART_2)-[1-9][0-9]*$`)

func FuzzExtract(f *testing.F) {
	f.Add("Fixes GAME-1, see ART_2-30")
	f.Add("closes: GAME-1 GAME-1 game-2 UTF-8")
	f.Add(strings.Repeat("GAME-1 ", 30))
	f.Fuzz(func(t *testing.T, text string) {
		refs := Extract(text, fuzzKeys)
		if len(refs) > MaxRefs {
			t.Fatalf("%d refs over the cap", len(refs))
		}
		seen := map[string]bool{}
		for _, ref := range refs {
			if !refShape.MatchString(ref.Key) || !strings.Contains(text, ref.Key) {
				t.Fatalf("bad key %q", ref.Key)
			}
			if seen[ref.Key] {
				t.Fatalf("duplicate key %q", ref.Key)
			}
			seen[ref.Key] = true
			if ref.Relation != RelationMentions && ref.Relation != RelationCloses {
				t.Fatalf("bad relation %q", ref.Relation)
			}
		}
	})
}

func FuzzBranchKey(f *testing.F) {
	f.Add("feature/GAME-123-new-rig")
	f.Add("art_2-9")
	f.Add("GAME-")
	f.Fuzz(func(t *testing.T, name string) {
		key, ok := BranchKey(name, fuzzKeys)
		if ok != (key != "") {
			t.Fatalf("ok %v with key %q", ok, key)
		}
		if ok && !refShape.MatchString(key) {
			t.Fatalf("bad key %q from %q", key, name)
		}
	})
}
