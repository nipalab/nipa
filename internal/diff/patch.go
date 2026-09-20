package diff

import (
	"bytes"
	"fmt"
	"strings"
)

// FileDiff pairs one change with the contents needed to render it.
type FileDiff struct {
	Change         Change
	Old            []byte
	New            []byte
	OldUnavailable bool
	NewUnavailable bool
}

// DefaultContext is the default number of context lines around a hunk.
const DefaultContext = 3

const contentUnavailable = "content not available locally (run 'nipa update' to fetch it)"

// Options controls rendering of diffs.
type Options struct {
	Context          int
	InterHunkContext int
	SrcPrefix        string
	DstPrefix        string
	NoPrefix         bool
	NewIndicator     string
	OldIndicator     string
	ContextIndicator string
	LinePrefix       string
	Text             bool
}

func (o Options) prefixes() (string, string) {
	if o.NoPrefix {
		return "", ""
	}
	src, dst := o.SrcPrefix, o.DstPrefix
	if src == "" {
		src = "a/"
	}
	if dst == "" {
		dst = "b/"
	}
	return src, dst
}

func (o Options) indicators() (byte, byte, byte) {
	old, new, ctx := o.OldIndicator, o.NewIndicator, o.ContextIndicator
	return indicator(old, '-'), indicator(new, '+'), indicator(ctx, ' ')
}

func indicator(s string, fallback byte) byte {
	if s == "" {
		return fallback
	}
	return s[0]
}

func (o Options) applyPrefix(lines []string) []string {
	if o.LinePrefix == "" {
		return lines
	}
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = o.LinePrefix + line
	}
	return out
}

// HeaderLine returns the "diff --nipa" line for a change.
func HeaderLine(c Change, opts Options) string {
	src, dst := opts.prefixes()
	return fmt.Sprintf("diff --nipa %s%s %s%s", src, c.Path, dst, c.Path)
}

// FilePatch renders one change as unified patch lines.
func FilePatch(f FileDiff, opts Options) []string {
	lines := []string{HeaderLine(f.Change, opts)}
	if f.OldUnavailable || f.NewUnavailable {
		lines = append(lines, contentUnavailable)
		return opts.applyPrefix(lines)
	}

	switch f.Change.Status {
	case Added:
		lines = append(lines, "new file mode "+modeString(f.Change.New.Mode))
	case Deleted:
		lines = append(lines, "deleted file mode "+modeString(f.Change.Old.Mode))
	case Modified:
		if f.Change.Old.Mode != f.Change.New.Mode {
			lines = append(lines,
				"old mode "+modeString(f.Change.Old.Mode),
				"new mode "+modeString(f.Change.New.Mode),
			)
			if bytes.Equal(f.Old, f.New) {
				return opts.applyPrefix(lines)
			}
		}
	}

	src, dst := opts.prefixes()
	oldPath, newPath := src+f.Change.Path, dst+f.Change.Path
	if f.Change.Status == Added {
		oldPath = "/dev/null"
	}
	if f.Change.Status == Deleted {
		newPath = "/dev/null"
	}
	if (f.Change.Old.IsBinary || f.Change.New.IsBinary) && !opts.Text {
		return opts.applyPrefix(append(lines, fmt.Sprintf("Binary files %s and %s differ", oldPath, newPath)))
	}

	lines = append(lines, "--- "+oldPath, "+++ "+newPath)
	return opts.applyPrefix(append(lines, hunks(f.Old, f.New, opts)...))
}

// Patch renders a batch of file changes as one patch.
func Patch(files []FileDiff, opts Options) []string {
	var out []string
	for _, f := range files {
		out = append(out, FilePatch(f, opts)...)
	}
	return out
}

func hunks(old, new []byte, opts Options) []string {
	ops := Lines(splitLines(old), splitLines(new))
	ranges := hunkRanges(ops, opts.Context, opts.InterHunkContext)
	if len(ranges) == 0 {
		return nil
	}

	preA := make([]int, len(ops)+1)
	preB := make([]int, len(ops)+1)
	for i, op := range ops {
		preA[i+1], preB[i+1] = preA[i], preB[i]
		if op.Kind != '+' {
			preA[i+1]++
		}
		if op.Kind != '-' {
			preB[i+1]++
		}
	}

	oldIndicator, newIndicator, contextIndicator := opts.indicators()
	var out []string
	for _, r := range ranges {
		aStart, aCount := hunkStartCount(preA, r[0], r[1])
		bStart, bCount := hunkStartCount(preB, r[0], r[1])
		out = append(out, fmt.Sprintf("@@ -%d,%d +%d,%d @@", aStart, aCount, bStart, bCount))
		for _, op := range ops[r[0]:r[1]] {
			prefix := contextIndicator
			switch op.Kind {
			case '-':
				prefix = oldIndicator
			case '+':
				prefix = newIndicator
			}
			line, hasNewline := strings.CutSuffix(op.Line, "\n")
			out = append(out, string(prefix)+line)
			if !hasNewline && op.Kind != ' ' {
				out = append(out, `\ No newline at end of file`)
			}
		}
	}
	return out
}

// hunkRanges groups changed ops into hunk index ranges, each padded with
// context lines. Change groups separated by more than twice the context
// (plus inter-hunk context) start a new hunk.
func hunkRanges(ops []Op, context, interHunk int) [][2]int {
	var changes []int
	for i, op := range ops {
		if op.Kind != ' ' {
			changes = append(changes, i)
		}
	}
	if len(changes) == 0 {
		return nil
	}
	fuse := 2*context + interHunk
	var ranges [][2]int
	start := max(0, changes[0]-context)
	for i := 1; i < len(changes); i++ {
		if changes[i]-changes[i-1]-1 > fuse {
			ranges = append(ranges, [2]int{start, min(len(ops), changes[i-1]+context+1)})
			start = max(0, changes[i]-context)
		}
	}
	return append(ranges, [2]int{start, min(len(ops), changes[len(changes)-1]+context+1)})
}

// hunkStartCount renders a git-style hunk range. For an empty side the start
// is the line the change follows (e.g. -0,0 for an insertion at the top).
func hunkStartCount(prefix []int, start, end int) (int, int) {
	count := prefix[end] - prefix[start]
	if count == 0 {
		return prefix[start], 0
	}
	return prefix[start] + 1, count
}

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

func modeString(mode int) string {
	switch mode {
	case 1:
		return "100444"
	case 3:
		return "100755"
	default:
		return "100644"
	}
}
