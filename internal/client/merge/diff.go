package merge

import (
	"sort"
	"strings"

	"github.com/nipalab/nipa/internal/diff"
)

const (
	mergeStart = "<<<<<<< ours\n"
	mergeMid   = "=======\n"
	mergeEnd   = ">>>>>>> theirs\n"
)

// MergeText performs a diff3-style three-way merge of two modified versions of
// a base text. Regions both sides changed differently are reported as conflicts
// with markers. Overlapping changes are rendered as one conflict over the whole
// overlap; disjoint changes in different parts of the file splice cleanly. A
// change made identically on both sides is not a conflict.
func MergeText(base, ours, theirs []byte) (merged []byte, conflicted bool) {
	out, conflicted := mergeLines(
		splitWithNewlines(string(base)),
		splitWithNewlines(string(ours)),
		splitWithNewlines(string(theirs)),
	)
	return []byte(strings.Join(out, "")), conflicted
}

func splitWithNewlines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.SplitAfter(s, "\n")
}

// srange is a changed region: base lines [aStart,aEnd) are replaced by side
// lines [bStart,bEnd).
type srange struct {
	aStart, aEnd, bStart, bEnd int
	side                       byte // 'A' = ours, 'B' = theirs
}

func mergeLines(base, ours, theirs []string) ([]string, bool) {
	if len(base) == 0 {
		switch {
		case len(ours) == 0:
			return append([]string(nil), theirs...), false
		case len(theirs) == 0:
			return append([]string(nil), ours...), false
		case equalLines(ours, theirs):
			return append([]string(nil), ours...), false
		default:
			return appendConflictMarkers(append([]string(nil), ours...), append([]string(nil), theirs...)), true
		}
	}

	cA := changeRanges(base, ours, 'A')
	cB := changeRanges(base, theirs, 'B')
	all := make([]srange, 0, len(cA)+len(cB))
	all = append(all, cA...)
	all = append(all, cB...)
	sort.Slice(all, func(i, j int) bool {
		if all[i].aStart != all[j].aStart {
			return all[i].aStart < all[j].aStart
		}
		// Tie-break so a pure insertion at a position sorts before a
		// replacement at the same position (smaller end = earlier).
		if all[i].aEnd != all[j].aEnd {
			return all[i].aEnd < all[j].aEnd
		}
		return all[i].side < all[j].side
	})

	var out []string
	conflicted := false
	pos := 0
	i := 0
	for i < len(all) {
		cStart := all[i].aStart
		for pos < cStart {
			out = append(out, base[pos])
			pos++
		}
		cEnd := all[i].aEnd
		j := i + 1
		for j < len(all) && rangesIntersect(cStart, cEnd, all[j]) {
			if all[j].aStart < cStart {
				cStart = all[j].aStart
			}
			if all[j].aEnd > cEnd {
				cEnd = all[j].aEnd
			}
			j++
		}
		cluster := all[i:j]

		hasA, hasB := false, false
		for k := range cluster {
			switch cluster[k].side {
			case 'A':
				hasA = true
			default:
				hasB = true
			}
		}

		switch {
		case hasA && hasB:
			o := sideView(cluster, 'A', base, ours, cStart, cEnd)
			t := sideView(cluster, 'B', base, theirs, cStart, cEnd)
			if equalLines(o, t) {
				out = append(out, o...)
			} else {
				out = append(out, appendConflictMarkers(o, t)...)
				conflicted = true
			}
		case hasA:
			out = append(out, sideView(cluster, 'A', base, ours, cStart, cEnd)...)
		default:
			out = append(out, sideView(cluster, 'B', base, theirs, cStart, cEnd)...)
		}

		pos = cEnd
		i = j
	}
	for pos < len(base) {
		out = append(out, base[pos])
		pos++
	}
	return out, conflicted
}

// rangesIntersect reports whether a change range overlaps the base span
// [cStart,cEnd) of a cluster. Two changes conflict when their base regions
// overlap each other. Pure insertions (zero-width base ranges) interact only
// with other insertions at the exact same position or with a replacement that
// strictly contains the insertion point; insertions sitting on a cluster
// boundary are clean and stay separate.
func rangesIntersect(cStart, cEnd int, r srange) bool {
	if cStart == cEnd && r.aStart == r.aEnd {
		return cStart == r.aStart
	}
	if cStart == cEnd {
		return r.aStart < cStart && cStart < r.aEnd
	}
	if r.aStart == r.aEnd {
		return cStart < r.aStart && r.aStart < cEnd
	}
	return r.aStart < cEnd && cStart < r.aEnd
}

// sideView builds one side's view of the cluster span [cStart,cEnd): kept base
// lines where the side did not change them, replaced by the side's lines where
// it did. Pure insertions (zero-width base ranges) contribute their lines
// directly.
func sideView(cluster []srange, side byte, base, sideLines []string, cStart, cEnd int) []string {
	var out []string
	pos := cStart
	for k := range cluster {
		r := &cluster[k]
		if r.side != side {
			continue
		}
		for pos < r.aStart {
			out = append(out, base[pos])
			pos++
		}
		out = append(out, sideLines[r.bStart:r.bEnd]...)
		pos = r.aEnd
		if pos > cEnd {
			pos = cEnd
		}
	}
	for pos < cEnd {
		out = append(out, base[pos])
		pos++
	}
	return out
}

func appendConflictMarkers(ours, theirs []string) []string {
	ours = ensureTrailingNL(ours)
	theirs = ensureTrailingNL(theirs)
	out := make([]string, 0, len(ours)+len(theirs)+3)
	out = append(out, mergeStart)
	out = append(out, ours...)
	out = append(out, mergeMid)
	out = append(out, theirs...)
	out = append(out, mergeEnd)
	return out
}

// ensureTrailingNL guarantees the slice ends with a newline so the following
// conflict marker starts on its own line even if the changed lines lost the
// file's final newline.
func ensureTrailingNL(lines []string) []string {
	if len(lines) == 0 {
		return []string{""}
	}
	last := len(lines) - 1
	if strings.HasSuffix(lines[last], "\n") {
		return lines
	}
	cp := append([]string(nil), lines...)
	cp[last] += "\n"
	return cp
}

// changeRanges converts an edit script into the base ranges changed relative to
// the side, in base order. Pure insertions produce zero-width ranges
// (aStart == aEnd) at the insertion point.
func changeRanges(base, side []string, which byte) []srange {
	ops := diff.Lines(base, side)
	var out []srange
	aPos, bPos := 0, 0
	i := 0
	for i < len(ops) {
		if ops[i].Kind == ' ' {
			aPos = ops[i].A + 1
			bPos = ops[i].B + 1
			i++
			continue
		}
		aStart, bStart := aPos, bPos
		for i < len(ops) && ops[i].Kind != ' ' {
			if ops[i].Kind == '-' {
				aPos++
			} else {
				bPos++
			}
			i++
		}
		out = append(out, srange{
			aStart: aStart, aEnd: aPos,
			bStart: bStart, bEnd: bPos,
			side: which,
		})
	}
	return out
}

func equalLines(a, b []string) bool {
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
