package diff

import (
	"fmt"
	"strings"
)

// DefaultContext is the default number of context lines around each hunk.
const DefaultContext = 3

// Options controls FilePatch rendering.
type Options struct {
	// Context is the number of context lines around each hunk.
	// Values <= 0 select DefaultContext.
	Context int
	// AHeader and BHeader prefix the displayed ---/+++ paths.
	// Empty values select "a/" and "b/".
	AHeader string
	BHeader string
}

func (o Options) context() int {
	if o.Context <= 0 {
		return DefaultContext
	}
	return o.Context
}

func (o Options) headers() (string, string) {
	a, b := o.AHeader, o.BHeader
	if a == "" {
		a = "a/"
	}
	if b == "" {
		b = "b/"
	}
	return a, b
}

// FilePatch renders one file change as git-style unified patch lines
// (a "diff --nipa" header, optional mode lines, ---/+++ file lines, and
// @@ hunks). Returned lines carry no trailing newline; a missing final
// newline of file content is reported with a "\ No newline at end of file"
// marker, so the output can be printed line by line.
func FilePatch(c Change, old, new []byte, opts Options) []string {
	aHeader, bHeader := opts.headers()
	lines := []string{fmt.Sprintf("diff --nipa %s%s %s%s", aHeader, c.Path, bHeader, c.Path)}

	switch c.Status {
	case Added:
		lines = append(lines, fmt.Sprintf("new file mode %d", c.New.Mode))
	case Deleted:
		lines = append(lines, fmt.Sprintf("deleted file mode %d", c.Old.Mode))
	case Modified:
		if c.Old.Mode != c.New.Mode {
			if bytesEqual(old, new) {
				// mode-only change carries no hunks
				return append(lines,
					fmt.Sprintf("old mode %d", c.Old.Mode),
					fmt.Sprintf("new mode %d", c.New.Mode),
				)
			}
			lines = append(lines,
				fmt.Sprintf("old mode %d", c.Old.Mode),
				fmt.Sprintf("new mode %d", c.New.Mode),
			)
		}
	}

	if c.Old.IsBinary || c.New.IsBinary {
		return append(lines, fmt.Sprintf("Binary files %s%s and %s%s differ", aHeader, c.Path, bHeader, c.Path))
	}

	oldPath := aHeader + c.Path
	newPath := bHeader + c.Path
	if c.Status == Added {
		oldPath = "/dev/null"
	}
	if c.Status == Deleted {
		newPath = "/dev/null"
	}
	lines = append(lines, "--- "+oldPath, "+++ "+newPath)
	lines = append(lines, hunks(old, new, opts.context())...)
	return lines
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// hunks renders the unified hunks between two file contents.
func hunks(old, new []byte, context int) []string {
	ops := Lines(splitLines(old), splitLines(new))
	ranges := hunkRanges(ops, context)
	if len(ranges) == 0 {
		return nil
	}
	// prefix counts of consumed a/b lines, for hunk headers
	preA := make([]int, len(ops)+1)
	preB := make([]int, len(ops)+1)
	for i, op := range ops {
		preA[i+1], preB[i+1] = preA[i], preB[i]
		if op.Kind == '=' || op.Kind == '-' {
			preA[i+1]++
		}
		if op.Kind == '=' || op.Kind == '+' {
			preB[i+1]++
		}
	}
	var out []string
	for _, r := range ranges {
		aStart, aCount := hunkStartCount(preA, r[0], r[1])
		bStart, bCount := hunkStartCount(preB, r[0], r[1])
		out = append(out, fmt.Sprintf("@@ -%d,%d +%d,%d @@", aStart, aCount, bStart, bCount))
		for _, op := range ops[r[0]:r[1]] {
			prefix := " "
			switch op.Kind {
			case '-':
				prefix = "-"
			case '+':
				prefix = "+"
			}
			line, hasNL := strings.CutSuffix(op.Line, "\n")
			out = append(out, prefix+line)
			if !hasNL {
				out = append(out, `\ No newline at end of file`)
			}
		}
	}
	return out
}

// hunkStartCount renders a git-style hunk range: for an empty side the
// start is the line the change follows (e.g. -0,0 for a pure addition at
// the start of the file).
func hunkStartCount(prefix []int, start, end int) (int, int) {
	count := prefix[end] - prefix[start]
	if count == 0 {
		return prefix[start], 0
	}
	return prefix[start] + 1, count
}

// hunkRanges groups ops into hunk index ranges (in the original ops
// coordinates), each padded with up to context context lines. Change
// groups separated by more than 2*context context lines start a new hunk.
func hunkRanges(ops []Op, context int) [][2]int {
	first, last := -1, -1
	for i, op := range ops {
		if op.Kind != '=' {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 {
		return nil
	}
	base := max(0, first-context)
	ops = ops[base:min(len(ops), last+1+context)]
	var ranges [][2]int
	start := 0
	for i := 0; i < len(ops); i++ {
		if ops[i].Kind == '=' {
			continue
		}
		// ops[i] is the first change of a new group: count the context
		// run before it within the current hunk candidate.
		gap := 0
		for j := i - 1; j >= start && ops[j].Kind == '='; j-- {
			gap++
		}
		if gap > 2*context {
			// close the previous hunk keeping trailing context, and
			// start a new one context lines before this change.
			ranges = append(ranges, [2]int{base + start, base + i - (gap - context)})
			start = i - context
		}
	}
	return append(ranges, [2]int{base + start, base + len(ops)})
}

// splitLines splits content into lines keeping their trailing newlines. An
// empty file yields no lines. A trailing newline does not produce an extra
// empty line.
func splitLines(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	lines := strings.SplitAfter(string(data), "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
