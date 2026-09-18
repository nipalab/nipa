package diff

// Op is one edit operation in a line-level edit script between two line
// slices. Kind is one of '=', '-' or '+'.
type Op struct {
	Kind byte
	Line string
	// A is the index of the line in the first slice ('=' and '-' ops).
	// For insertions it is the position in the first slice at which the
	// line is inserted.
	A int
	// B is the index of the line in the second slice ('=' and '+' ops).
	// For deletions it is the position in the second slice the line was
	// deleted from.
	B int
}

// Lines computes a forward-ordered Myers edit script transforming a into b.
func Lines(a, b []string) []Op {
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
			for x < n && y < m && a[x] == b[y] {
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
			ops = append(ops, Op{Kind: '=', Line: a[x-1], A: x - 1, B: y - 1})
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
	for x > 0 && y > 0 && a[x-1] == b[y-1] {
		ops = append(ops, Op{Kind: '=', Line: a[x-1], A: x - 1, B: y - 1})
		x--
		y--
	}
	for i, j := 0, len(ops)-1; i < j; i, j = i+1, j-1 {
		ops[i], ops[j] = ops[j], ops[i]
	}
	return ops
}
