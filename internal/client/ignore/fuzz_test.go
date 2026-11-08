package ignore

import (
	"testing"

	"github.com/nipalab/nipa/internal/pathmatch"
)

// FuzzCompileAndMatch checks that arbitrary rule files compile and that
// matching any path is deterministic and never panics.
func FuzzCompileAndMatch(f *testing.F) {
	f.Add("*.log\n!keep.log\nbuild/\n", "app/debug.log", false)
	f.Add("/root-only\n**/cache/**\n", "a/b/cache/c", true)
	f.Add("# comment\n\n  \n[unclosed\n", "x", false)
	f.Add("a?c\n*\n!*.go\n", "dir/abc", true)
	f.Add("\\#literal\n!\n/\n", "#literal", false)

	f.Fuzz(func(t *testing.T, contents, path string, isDir bool) {
		m := &Matcher{patterns: pathmatch.Parse(contents)}
		first := m.Ignores(path, isDir)
		if second := m.Ignores(path, isDir); first != second {
			t.Fatalf("Ignores(%q, %v) is not deterministic", path, isDir)
		}
		if (&Matcher{}).Ignores(path, isDir) {
			t.Fatalf("a matcher without rules ignored %q", path)
		}
	})
}
