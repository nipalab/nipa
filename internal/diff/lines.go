package diff

// Op is one edit operation in a Myers edit script between two line slices.
type Op struct {
	Kind byte // ' ' (equal), '-' (removed), '+' (added)
	Line string
	A    int // index in a for ' ' and '-'; insertion index for '+'
	B    int // index in b for ' ' and '+'; insertion index for '-'
}

// EqualFunc reports whether two lines are considered equal. It allows
// comparison modes that ignore whitespace differences.
type EqualFunc func(a, b string) bool

// Unlimited is the maxCost for LinesBudget that never gives up.
const Unlimited = -1

// Lines computes a forward-ordered Myers edit script for a to b.
func Lines(a, b []string) []Op {
	return LinesWith(a, b, nil)
}

// LinesWith computes the edit script using eq to compare lines. A nil eq uses
// exact string equality.
func LinesWith(a, b []string, eq EqualFunc) []Op {
	ops, _ := LinesBudget(a, b, eq, Unlimited)
	return ops
}

// LinesBudget computes a minimal edit script like LinesWith, but gives up and
// returns (nil, false) once the edit distance (the number of added plus
// removed lines) is known to exceed maxCost. A negative maxCost never gives
// up.
//
// The common prefix and suffix are trimmed first, then the remainder is
// diffed with the linear-space variant of Myers' algorithm (divide and
// conquer on the middle snake), so memory stays O(len(a)+len(b)) and time is
// O((len(a)+len(b))·D), with D capped by maxCost. Within a run of changes the
// removed lines come before the added ones.
func LinesBudget(a, b []string, eq EqualFunc, maxCost int) ([]Op, bool) {
	equal := eq
	if equal == nil {
		equal = func(x, y string) bool { return x == y }
	}
	pre, suf := commonAffixes(a, b, equal)
	aHi, bHi := len(a)-suf, len(b)-suf
	if maxCost >= 0 && abs((aHi-pre)-(bHi-pre)) > maxCost {
		return nil, false
	}

	s := &lineDiff{a: a, b: b, eq: equal, delA: make([]bool, len(a)), insB: make([]bool, len(b))}
	if aHi > pre && bHi > pre {
		size := (aHi - pre) + (bHi - pre) + 3 // 2*maxD+2 of the widest range
		s.v1 = make([]int, size)
		s.v2 = make([]int, size)
	}
	if !s.compare(pre, aHi, pre, bHi, maxCost) {
		return nil, false
	}
	ops, edits := s.script()
	if maxCost >= 0 && edits > maxCost {
		return nil, false
	}
	return ops, true
}

// commonAffixes returns the lengths of the common prefix and of the common
// suffix of a and b; the two never overlap.
func commonAffixes(a, b []string, eq EqualFunc) (pre, suf int) {
	n, m := len(a), len(b)
	for pre < n && pre < m && eq(a[pre], b[pre]) {
		pre++
	}
	for suf < n-pre && suf < m-pre && eq(a[n-1-suf], b[m-1-suf]) {
		suf++
	}
	return pre, suf
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// lineDiff holds the state of one linear-space Myers run: the inputs, the
// change marks it fills in and the two reusable diagonal arrays.
type lineDiff struct {
	a, b   []string
	eq     EqualFunc
	delA   []bool // delA[i]: a[i] is removed
	insB   []bool // insB[j]: b[j] is added
	v1, v2 []int
}

// compare marks the changes between a[aLo:aHi] and b[bLo:bHi]. limit caps the
// edit distance of this range (negative: no cap); it is only set on the top
// call, as the sub-ranges together cost exactly the edit distance found there.
func (s *lineDiff) compare(aLo, aHi, bLo, bHi, limit int) bool {
	for aLo < aHi && bLo < bHi && s.eq(s.a[aLo], s.b[bLo]) {
		aLo++
		bLo++
	}
	for aLo < aHi && bLo < bHi && s.eq(s.a[aHi-1], s.b[bHi-1]) {
		aHi--
		bHi--
	}
	if aLo == aHi {
		for j := bLo; j < bHi; j++ {
			s.insB[j] = true
		}
		return true
	}
	if bLo == bHi {
		for i := aLo; i < aHi; i++ {
			s.delA[i] = true
		}
		return true
	}
	x, y, found, ok := s.bisect(aLo, aHi, bLo, bHi, limit)
	if !ok {
		return false
	}
	if !found {
		// Nothing in common: everything is replaced.
		for i := aLo; i < aHi; i++ {
			s.delA[i] = true
		}
		for j := bLo; j < bHi; j++ {
			s.insB[j] = true
		}
		return true
	}
	s.compare(aLo, x, bLo, y, Unlimited)
	s.compare(x, aHi, y, bHi, Unlimited)
	return true
}

// bisect finds the middle snake of a[aLo:aHi] against b[bLo:bHi] by running
// the forward and the reverse Myers searches until they overlap, and returns
// the split point (x, y) on an optimal path. found is false when the ranges
// share no line at all; ok is false when the edit distance exceeds limit.
// Both ranges are non-empty and differ in their first and last lines.
func (s *lineDiff) bisect(aLo, aHi, bLo, bHi, limit int) (x, y int, found, ok bool) {
	n, m := aHi-aLo, bHi-bLo
	maxD := (n + m + 1) / 2
	vOff := maxD
	vLen := 2 * maxD
	v1 := s.v1[:vLen+2]
	v2 := s.v2[:vLen+2]
	for i := range v1 {
		v1[i] = -1
		v2[i] = -1
	}
	v1[vOff+1] = 0
	v2[vOff+1] = 0
	delta := n - m
	// With an odd delta the paths can only meet on a forward step, with an
	// even one only on a reverse step.
	front := delta%2 != 0
	// Diagonals that ran off the grid are skipped from then on.
	k1start, k1end, k2start, k2end := 0, 0, 0, 0
	for d := 0; d < maxD; d++ {
		// The searches meet by step ceil(D/2), so a later step proves D > limit.
		if limit >= 0 && d > limit/2+1 {
			return 0, 0, false, false
		}
		for k1 := -d + k1start; k1 <= d-k1end; k1 += 2 {
			k1Off := vOff + k1
			var x1 int
			if k1 == -d || (k1 != d && v1[k1Off-1] < v1[k1Off+1]) {
				x1 = v1[k1Off+1]
			} else {
				x1 = v1[k1Off-1] + 1
			}
			y1 := x1 - k1
			for x1 < n && y1 < m && s.eq(s.a[aLo+x1], s.b[bLo+y1]) {
				x1++
				y1++
			}
			v1[k1Off] = x1
			switch {
			case x1 > n:
				k1end += 2
			case y1 > m:
				k1start += 2
			case front:
				k2Off := vOff + delta - k1
				if k2Off >= 0 && k2Off < vLen && v2[k2Off] != -1 && x1 >= n-v2[k2Off] {
					return aLo + x1, bLo + y1, true, true
				}
			}
		}
		for k2 := -d + k2start; k2 <= d-k2end; k2 += 2 {
			k2Off := vOff + k2
			var x2 int
			if k2 == -d || (k2 != d && v2[k2Off-1] < v2[k2Off+1]) {
				x2 = v2[k2Off+1]
			} else {
				x2 = v2[k2Off-1] + 1
			}
			y2 := x2 - k2
			for x2 < n && y2 < m && s.eq(s.a[aHi-1-x2], s.b[bHi-1-y2]) {
				x2++
				y2++
			}
			v2[k2Off] = x2
			switch {
			case x2 > n:
				k2end += 2
			case y2 > m:
				k2start += 2
			case !front:
				k1Off := vOff + delta - k2
				if k1Off >= 0 && k1Off < vLen && v1[k1Off] != -1 {
					x1 := v1[k1Off]
					y1 := x1 - (k1Off - vOff)
					if x1 >= n-x2 {
						return aLo + x1, bLo + y1, true, true
					}
				}
			}
		}
	}
	if limit >= 0 && n+m > limit {
		return 0, 0, false, false
	}
	return 0, 0, false, true
}

// script turns the change marks into a forward-ordered edit script and
// returns it with its number of added plus removed lines.
func (s *lineDiff) script() ([]Op, int) {
	n, m := len(s.a), len(s.b)
	ins := 0
	for _, added := range s.insB {
		if added {
			ins++
		}
	}
	ops := make([]Op, 0, n+ins)
	edits := 0
	x, y := 0, 0
	for x < n || y < m {
		start := x + y
		if x < n && y < m && !s.delA[x] && !s.insB[y] {
			ops = append(ops, Op{Kind: ' ', Line: s.a[x], A: x, B: y})
			x++
			y++
			continue
		}
		for x < n && s.delA[x] {
			ops = append(ops, Op{Kind: '-', Line: s.a[x], A: x, B: y})
			x++
			edits++
		}
		for y < m && s.insB[y] {
			ops = append(ops, Op{Kind: '+', Line: s.b[y], A: x, B: y})
			y++
			edits++
		}
		if x+y == start {
			panic("diff: inconsistent change marks")
		}
	}
	return ops, edits
}
