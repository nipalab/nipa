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

// Lines computes a forward-ordered Myers edit script for a to b.
func Lines(a, b []string) []Op {
	return LinesWith(a, b, nil)
}

// LinesWith computes the edit script using eq to compare lines. A nil eq uses
// exact string equality.
func LinesWith(a, b []string, eq EqualFunc) []Op {
	equal := eq
	if equal == nil {
		equal = func(x, y string) bool { return x == y }
	}
	n, m := len(a), len(b)
	if n == 0 {
		ops := make([]Op, 0, m)
		for y := 0; y < m; y++ {
			ops = append(ops, Op{Kind: '+', Line: b[y], A: 0, B: y})
		}
		return ops
	}
	if m == 0 {
		ops := make([]Op, 0, n)
		for x := 0; x < n; x++ {
			ops = append(ops, Op{Kind: '-', Line: a[x], A: x, B: 0})
		}
		return ops
	}

	max := n + m
	v := make([]int, 2*max+1)
	trace := make([][]int, 0, max+1)
	d, found := 0, false
	for d = 0; d <= max; d++ {
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[k-1+max] < v[k+1+max]) {
				x = v[k+1+max]
			} else {
				x = v[k-1+max] + 1
			}
			y := x - k
			for x < n && y < m && equal(a[x], b[y]) {
				x++
				y++
			}
			v[k+max] = x
			if x >= n && y >= m {
				found = true
				break
			}
		}
		trace = append(trace, append([]int{}, v...))
		if found {
			break
		}
	}

	x, y := n, m
	ops := make([]Op, 0, n+m)
	for ; d > 0; d-- {
		k := x - y
		prevV := trace[d-1]
		var prevK int
		if k == -d || (k != d && prevV[k-1+max] < prevV[k+1+max]) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevX := prevV[prevK+max]
		prevY := prevX - prevK
		for x > prevX && y > prevY {
			ops = append(ops, Op{Kind: ' ', Line: a[x-1], A: x - 1, B: y - 1})
			x--
			y--
		}
		if x == prevX {
			ops = append(ops, Op{Kind: '+', Line: b[y-1], A: x, B: y - 1})
			y--
		} else {
			ops = append(ops, Op{Kind: '-', Line: a[x-1], A: x - 1, B: y})
			x--
		}
	}
	for x > 0 && y > 0 && equal(a[x-1], b[y-1]) {
		ops = append(ops, Op{Kind: ' ', Line: a[x-1], A: x - 1, B: y - 1})
		x--
		y--
	}
	for i, j := 0, len(ops)-1; i < j; i, j = i+1, j-1 {
		ops[i], ops[j] = ops[j], ops[i]
	}
	return ops
}
