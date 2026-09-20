package diff

import (
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
	Context int
	// IgnoreAllSpace ignores all whitespace when comparing lines (-w).
	IgnoreAllSpace bool
	// IgnoreSpaceChange treats whitespace runs as equivalent (-b).
	IgnoreSpaceChange bool
}

func (o Options) equalFunc() EqualFunc {
	if !o.IgnoreAllSpace && !o.IgnoreSpaceChange {
		return nil
	}
	return func(a, b string) bool {
		return normalizeLine(a, o) == normalizeLine(b, o)
	}
}

func normalizeLine(line string, o Options) string {
	s := strings.TrimSuffix(line, "\n")
	switch {
	case o.IgnoreAllSpace:
		return strings.Join(strings.Fields(s), "")
	case o.IgnoreSpaceChange:
		return strings.Join(strings.Fields(s), " ")
	}
	return s
}

// HeaderLine returns the "diff --nipa" line for a change.
func HeaderLine(c Change) string {
	oldPath, newPath := c.Path, c.Path
	if c.Status == Renamed {
		oldPath, newPath = c.Old.Path, c.Path
	}
	return fmt.Sprintf("diff --nipa a/%s b/%s", oldPath, newPath)
}

// FilePatch renders one change as unified patch lines.
func FilePatch(f FileDiff, opts Options) []string {
	c := f.Change
	lines := []string{HeaderLine(c)}
	oldPath, newPath := "a/"+c.Path, "b/"+c.Path

	switch c.Status {
	case Added:
		oldPath = "/dev/null"
		lines = append(lines, "new file mode "+ModeString(c.New.Mode))
	case Deleted:
		newPath = "/dev/null"
		lines = append(lines, "deleted file mode "+ModeString(c.Old.Mode))
	case Renamed:
		oldPath, newPath = "a/"+c.Old.Path, "b/"+c.Path
		if c.Old.Mode != c.New.Mode {
			lines = append(lines,
				"old mode "+ModeString(c.Old.Mode),
				"new mode "+ModeString(c.New.Mode),
			)
		}
		if c.Similarity > 0 {
			lines = append(lines, fmt.Sprintf("similarity index %d%%", c.Similarity))
		}
		lines = append(lines, "rename from "+c.Old.Path, "rename to "+c.Path)
		if c.Old.Hash == c.New.Hash {
			return lines
		}
	case Modified:
		if c.Old.Mode != c.New.Mode {
			lines = append(lines,
				"old mode "+ModeString(c.Old.Mode),
				"new mode "+ModeString(c.New.Mode),
			)
			if c.Old.Hash == c.New.Hash {
				return lines
			}
		}
	}

	if c.Old.IsBinary || c.New.IsBinary {
		return append(lines, fmt.Sprintf("Binary files %s and %s differ", oldPath, newPath))
	}
	if f.OldUnavailable || f.NewUnavailable {
		return append(lines, contentUnavailable)
	}

	hunkLines := hunks(f.Old, f.New, opts)
	switch {
	case len(hunkLines) > 0:
		lines = append(lines, "--- "+oldPath, "+++ "+newPath)
		lines = append(lines, hunkLines...)
	case c.Status == Added || c.Status == Deleted || c.Status == Renamed:
		// empty added/deleted files and renames without content changes
		// have no ---/+++ section
	default:
		// every difference is ignored by the whitespace options
		return nil
	}
	return lines
}

// Patch renders a batch of file changes as one patch.
func Patch(files []FileDiff, opts Options) []string {
	var out []string
	for _, f := range files {
		out = append(out, FilePatch(f, opts)...)
	}
	return out
}

// FilterIgnored drops Modified changes whose differences are entirely ignored
// by the whitespace options, so all output formats agree with the patch
// renderer. Renamed changes are always kept.
func FilterIgnored(files []FileDiff, opts Options) []FileDiff {
	if opts.equalFunc() == nil {
		return files
	}
	out := make([]FileDiff, 0, len(files))
	for _, f := range files {
		c := f.Change
		if c.Status != Modified || c.Old.Hash == c.New.Hash || c.Old.Mode != c.New.Mode {
			out = append(out, f)
			continue
		}
		if f.OldUnavailable || f.NewUnavailable || c.Old.IsBinary || c.New.IsBinary {
			out = append(out, f)
			continue
		}
		ops := LinesWith(splitLines(f.Old), splitLines(f.New), opts.equalFunc())
		if len(hunkRanges(ops, 0)) == 0 {
			continue
		}
		out = append(out, f)
	}
	return out
}

func hunks(old, new []byte, opts Options) []string {
	ops := LinesWith(splitLines(old), splitLines(new), opts.equalFunc())
	ranges := hunkRanges(ops, opts.Context)
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

	var out []string
	for _, r := range ranges {
		aStart, aCount := hunkStartCount(preA, r[0], r[1])
		bStart, bCount := hunkStartCount(preB, r[0], r[1])
		out = append(out, fmt.Sprintf("@@ -%d,%d +%d,%d @@", aStart, aCount, bStart, bCount))
		for _, op := range ops[r[0]:r[1]] {
			prefix := byte(' ')
			switch op.Kind {
			case '-':
				prefix = '-'
			case '+':
				prefix = '+'
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
// context lines. Change groups separated by more than twice the context start
// a new hunk.
func hunkRanges(ops []Op, context int) [][2]int {
	var changes []int
	for i, op := range ops {
		if op.Kind != ' ' {
			changes = append(changes, i)
		}
	}
	if len(changes) == 0 {
		return nil
	}
	fuse := 2 * context
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

// ModeString renders a Nipa file mode as a git-style octal mode.
func ModeString(mode int) string {
	switch mode {
	case 1:
		return "100444"
	case 3:
		return "100755"
	default:
		return "100644"
	}
}
