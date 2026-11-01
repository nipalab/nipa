package diff

import (
	"slices"
	"strings"
	"testing"
)

// FuzzLinesScript checks the Myers edit script: the equal and removed lines
// rebuild the old side, the equal and added lines rebuild the new side.
func FuzzLinesScript(f *testing.F) {
	f.Add("", "")
	f.Add("a\nb\nc", "a\nx\nc")
	f.Add("same\nsame\nsame", "same")
	f.Add("one", "one\ntwo\nthree")
	f.Add("x\ny\nz", "")

	f.Fuzz(func(t *testing.T, oldText, newText string) {
		a := strings.Split(oldText, "\n")
		b := strings.Split(newText, "\n")

		var gotOld, gotNew []string
		for _, op := range Lines(a, b) {
			switch op.Kind {
			case ' ':
				gotOld = append(gotOld, op.Line)
				gotNew = append(gotNew, op.Line)
			case '-':
				gotOld = append(gotOld, op.Line)
			case '+':
				gotNew = append(gotNew, op.Line)
			default:
				t.Fatalf("unexpected op kind %q", op.Kind)
			}
		}
		if !slices.Equal(gotOld, a) {
			t.Fatalf("old side not reproduced: got %q want %q", gotOld, a)
		}
		if !slices.Equal(gotNew, b) {
			t.Fatalf("new side not reproduced: got %q want %q", gotNew, b)
		}
	})
}
