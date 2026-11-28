package diff

import (
	"bytes"
	"fmt"
	"strings"
)

// Hunk is one unified diff hunk with its line numbers resolved, so callers such
// as review threads can anchor a comment to a concrete line instead of parsing
// the rendered patch.
type Hunk struct {
	OldStart int
	OldLines int
	NewStart int
	NewLines int
	Lines    []HunkLine
}

// HunkLine is one line of a Hunk. Kind is ' ' for context, '-' for removed and
// '+' for added. Old is 0 on added lines and New is 0 on removed lines, which
// is what tells a review thread which side of the diff it belongs to.
type HunkLine struct {
	Kind byte
	Old  int
	New  int
	Text string
	// NoNewline marks the last line of a file that has no trailing newline.
	NoNewline bool
}

// LineKind names a HunkLine kind for API responses: add, remove or context.
func LineKind(kind byte) string {
	switch kind {
	case '+':
		return "add"
	case '-':
		return "remove"
	default:
		return "context"
	}
}

// Header renders the git-style "@@ -a,b +c,d @@" header of the hunk.
func (h Hunk) Header() string {
	return fmt.Sprintf("@@ -%d,%d +%d,%d @@", h.OldStart, h.OldLines, h.NewStart, h.NewLines)
}

// Render renders the hunk as unified patch lines, header included.
func (h Hunk) Render() []string {
	out := make([]string, 0, len(h.Lines)+1)
	out = append(out, h.Header())
	for _, line := range h.Lines {
		text, _ := strings.CutSuffix(line.Text, "\n")
		out = append(out, string(line.Kind)+text)
		if line.NoNewline {
			out = append(out, `\ No newline at end of file`)
		}
	}
	return out
}

// FileHunks returns the structured hunks of one change. It returns nil when the
// change has no renderable text, matching FilePatch: binary or unavailable
// content, a rename or mode change that leaves the content identical, and
// modifications whose differences are all ignored by the whitespace options.
// It has no budget; callers that diff content they do not control use
// FileHunksBudget.
func FileHunks(f FileDiff, opts Options) []Hunk {
	return FileHunksBudget(f, opts, Budget{}).Hunks
}

// FileHunksBudget is FileHunks under a budget. A change without renderable
// text yields an empty result (no hunks, zero counts, not omitted). A
// truncated FileDiff is always omitted, with counts estimated from the loaded
// prefixes.
func FileHunksBudget(f FileDiff, opts Options, b Budget) HunkResult {
	c := f.Change
	switch c.Status {
	case Renamed:
		if c.Old.Hash == c.New.Hash {
			return HunkResult{}
		}
	case Modified:
		if c.Old.Mode != c.New.Mode && c.Old.Hash == c.New.Hash {
			return HunkResult{}
		}
	}
	if c.Old.IsBinary || c.New.IsBinary || f.OldUnavailable || f.NewUnavailable {
		return HunkResult{}
	}
	if f.Truncated {
		// Only prefixes were loaded: estimate from them and diff nothing.
		return omittedResult(f.Old, f.New, opts.equalFunc())
	}
	return HunksBudget(f.Old, f.New, opts, b)
}

// Sides of a diff, matching the review thread anchor semantics.
const (
	SideLeft  = "left"
	SideRight = "right"
)

// HunkLineAt returns the hunk and the line at lineNo on the given side, so a
// caller can check that a review comment is anchored to a line the diff really
// shows. A context line is part of both files and so is reachable from both
// sides; removed lines only from SideLeft and added lines only from SideRight.
func HunkLineAt(hunks []Hunk, side string, lineNo int) (Hunk, HunkLine, bool) {
	for _, hunk := range hunks {
		for _, line := range hunk.Lines {
			if side == SideLeft {
				if line.Kind != '+' && line.Old == lineNo {
					return hunk, line, true
				}
				continue
			}
			if line.Kind != '-' && line.New == lineNo {
				return hunk, line, true
			}
		}
	}
	return Hunk{}, HunkLine{}, false
}

// Budget bounds the work of a line diff. A zero field means no limit, so the
// zero Budget is unlimited.
type Budget struct {
	// MaxLines is the most lines either side may have.
	MaxLines int
	// MaxBytes is the most bytes either side may have.
	MaxBytes int
	// MaxCost is the largest edit distance (added plus removed lines) the
	// diff computes before giving up.
	MaxCost int
}

// DefaultBudget is the budget for diffs of content nobody chose to open, such
// as every file of a commit listing: up to 200k lines or 16 MiB per side and
// 5000 changed lines. Linear-space Myers needs O(lines) memory and about
// O(lines × changed lines) time; at these limits a diff, or giving up on one,
// takes well under a second.
var DefaultBudget = Budget{MaxLines: 200_000, MaxBytes: 16 << 20, MaxCost: 5000}

// HunkResult is the outcome of a budgeted diff.
type HunkResult struct {
	Hunks []Hunk
	// Additions and Deletions count the added and removed lines.
	Additions int
	Deletions int
	// Omitted is set when the diff exceeded the budget and no hunks were
	// computed.
	Omitted bool
	// Approx is set when the counts are estimates rather than exact: with
	// Omitted, they are the lines left on each side after trimming the common
	// prefix and suffix, an upper bound of the real counts.
	Approx bool
}

// Hunks returns the hunks between two file versions. An empty side is diffed as
// an empty file, so additions and deletions produce a single hunk. It has no
// budget; see HunksBudget.
func Hunks(old, new []byte, opts Options) []Hunk {
	return HunksBudget(old, new, opts, Budget{}).Hunks
}

// HunksBudget returns the hunks between two file versions and their exact
// counts, unless a side has more lines or bytes than the budget allows or the
// edit distance exceeds b.MaxCost. Then it computes no hunks, sets Omitted and
// returns approximate counts that cost one linear scan. MaxCost does not apply
// when the change is one block of added or removed lines, which is found by
// trimming alone.
func HunksBudget(old, new []byte, opts Options, b Budget) HunkResult {
	eq := opts.equalFunc()
	if (b.MaxBytes > 0 && (len(old) > b.MaxBytes || len(new) > b.MaxBytes)) ||
		(b.MaxLines > 0 && (countLines(old) > b.MaxLines || countLines(new) > b.MaxLines)) {
		return omittedResult(old, new, eq)
	}
	oldLines, newLines := splitLines(old), splitLines(new)
	maxCost := Unlimited
	if b.MaxCost > 0 && !pureInsertOrDelete(oldLines, newLines, eq) {
		maxCost = b.MaxCost
	}
	ops, ok := LinesBudget(oldLines, newLines, eq, maxCost)
	if !ok {
		return omittedResult(old, new, eq)
	}
	res := HunkResult{Hunks: hunksFromOps(ops, opts.Context)}
	for _, op := range ops {
		switch op.Kind {
		case '+':
			res.Additions++
		case '-':
			res.Deletions++
		}
	}
	return res
}

// pureInsertOrDelete reports whether a and b differ only by lines added or
// removed in one block, which the diff finds by trimming alone, so the edit
// cost cap does not apply (an added or deleted file, an appended log).
func pureInsertOrDelete(a, b []string, eq EqualFunc) bool {
	if eq == nil {
		eq = func(x, y string) bool { return x == y }
	}
	pre, suf := commonAffixes(a, b, eq)
	return pre+suf == len(a) || pre+suf == len(b)
}

func omittedResult(old, new []byte, eq EqualFunc) HunkResult {
	deletions, additions := trimmedLineCounts(old, new, eq)
	return HunkResult{Additions: additions, Deletions: deletions, Omitted: true, Approx: true}
}

// countLines counts lines the way splitLines splits them.
func countLines(data []byte) int {
	n := bytes.Count(data, []byte{'\n'})
	if len(data) > 0 && data[len(data)-1] != '\n' {
		n++
	}
	return n
}

// trimmedLineCounts returns how many lines of old and of new remain once the
// common prefix and suffix lines are trimmed, without splitting either side.
func trimmedLineCounts(old, new []byte, eq EqualFunc) (oldLeft, newLeft int) {
	same := func(x, y []byte) bool {
		if eq == nil {
			return bytes.Equal(x, y)
		}
		return eq(string(x), string(y))
	}
	nOld, nNew := countLines(old), countLines(new)
	pre := 0
	for i, j := 0, 0; pre < nOld && pre < nNew; pre++ {
		ei, ej := lineEnd(old, i), lineEnd(new, j)
		if !same(old[i:ei], new[j:ej]) {
			break
		}
		i, j = ei, ej
	}
	suf := 0
	for i, j := len(old), len(new); suf < nOld-pre && suf < nNew-pre; suf++ {
		si, sj := lineStart(old, i), lineStart(new, j)
		if !same(old[si:i], new[sj:j]) {
			break
		}
		i, j = si, sj
	}
	return nOld - pre - suf, nNew - pre - suf
}

// lineEnd returns the end of the line starting at start, past its newline.
func lineEnd(data []byte, start int) int {
	if i := bytes.IndexByte(data[start:], '\n'); i >= 0 {
		return start + i + 1
	}
	return len(data)
}

// lineStart returns the start of the line ending at end (end > 0).
func lineStart(data []byte, end int) int {
	return bytes.LastIndexByte(data[:end-1], '\n') + 1
}

func hunksFromOps(ops []Op, context int) []Hunk {
	ranges := hunkRanges(ops, context)
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

	out := make([]Hunk, 0, len(ranges))
	for _, r := range ranges {
		aStart, aCount := hunkStartCount(preA, r[0], r[1])
		bStart, bCount := hunkStartCount(preB, r[0], r[1])
		hunk := Hunk{
			OldStart: aStart,
			OldLines: aCount,
			NewStart: bStart,
			NewLines: bCount,
			Lines:    make([]HunkLine, 0, r[1]-r[0]),
		}
		for i := r[0]; i < r[1]; i++ {
			op := ops[i]
			line := HunkLine{Kind: op.Kind, Text: op.Line}
			if op.Kind != '+' {
				line.Old = preA[i] + 1
			}
			if op.Kind != '-' {
				line.New = preB[i] + 1
			}
			_, hasNewline := strings.CutSuffix(op.Line, "\n")
			line.NoNewline = !hasNewline && op.Kind != ' '
			hunk.Lines = append(hunk.Lines, line)
		}
		out = append(out, hunk)
	}
	return out
}
