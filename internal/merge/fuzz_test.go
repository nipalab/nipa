package merge

import (
	"bytes"
	"testing"
)

// FuzzMergeTextUnchangedSide checks the merge identities: when one side keeps
// the base, or both sides made the same change, the result is that change and
// is never a conflict.
func FuzzMergeTextUnchangedSide(f *testing.F) {
	f.Add("", "")
	f.Add("a\nb\nc\n", "a\nx\nc\n")
	f.Add("a\nb\nc\n", "")
	f.Add("a\nb", "a\nb\nc")
	f.Add("one\n", "one\ntwo\nthree\n")

	f.Fuzz(func(t *testing.T, base, changed string) {
		b, c := []byte(base), []byte(changed)

		check := func(name string, merged []byte, conflicted bool) {
			t.Helper()
			if conflicted {
				t.Fatalf("%s reported a conflict", name)
			}
			if !bytes.Equal(merged, c) {
				t.Fatalf("%s = %q, want %q", name, merged, c)
			}
		}
		merged, conflicted := MergeText(b, b, c)
		check("theirs-only change", merged, conflicted)
		merged, conflicted = MergeText(b, c, b)
		check("ours-only change", merged, conflicted)
		merged, conflicted = MergeText(b, c, c)
		check("identical change", merged, conflicted)
	})
}
