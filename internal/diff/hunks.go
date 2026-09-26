package diff

import (
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
func FileHunks(f FileDiff, opts Options) []Hunk {
	c := f.Change
	switch c.Status {
	case Renamed:
		if c.Old.Hash == c.New.Hash {
			return nil
		}
	case Modified:
		if c.Old.Mode != c.New.Mode && c.Old.Hash == c.New.Hash {
			return nil
		}
	}
	if c.Old.IsBinary || c.New.IsBinary || f.OldUnavailable || f.NewUnavailable {
		return nil
	}
	return Hunks(f.Old, f.New, opts)
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

// Hunks returns the hunks between two file versions. An empty side is diffed as
// an empty file, so additions and deletions produce a single hunk.
func Hunks(old, new []byte, opts Options) []Hunk {
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
