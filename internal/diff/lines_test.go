package diff

import (
	"math/rand"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLines_Equal(t *testing.T) {
	ops := Lines([]string{"a\n", "b\n"}, []string{"a\n", "b\n"})
	require.Len(t, ops, 2)
	for _, op := range ops {
		require.Equal(t, byte(' '), op.Kind)
	}
}

func TestLines_AddedLines(t *testing.T) {
	ops := Lines(nil, []string{"a\n", "b\n"})
	require.Equal(t, []Op{
		{Kind: '+', Line: "a\n", A: 0, B: 0},
		{Kind: '+', Line: "b\n", A: 0, B: 1},
	}, ops)
}

func TestLines_DeletedLines(t *testing.T) {
	ops := Lines([]string{"a\n", "b\n"}, nil)
	require.Equal(t, []Op{
		{Kind: '-', Line: "a\n", A: 0, B: 0},
		{Kind: '-', Line: "b\n", A: 1, B: 0},
	}, ops)
}

func TestLines_ModifiedLine(t *testing.T) {
	ops := Lines([]string{"a\n", "b\n", "c\n"}, []string{"a\n", "B\n", "c\n"})
	require.Equal(t, []Op{
		{Kind: ' ', Line: "a\n", A: 0, B: 0},
		{Kind: '-', Line: "b\n", A: 1, B: 1},
		{Kind: '+', Line: "B\n", A: 2, B: 1},
		{Kind: ' ', Line: "c\n", A: 2, B: 2},
	}, ops)
}

// checkScript asserts that ops is a valid forward edit script from a to b with
// consistent A/B indices, and returns its number of added plus removed lines.
func checkScript(t *testing.T, a, b []string, ops []Op, eq EqualFunc) int {
	t.Helper()
	if eq == nil {
		eq = func(x, y string) bool { return x == y }
	}
	x, y, edits := 0, 0, 0
	for i, op := range ops {
		require.Equal(t, x, op.A, "op %d A", i)
		require.Equal(t, y, op.B, "op %d B", i)
		switch op.Kind {
		case ' ':
			require.Less(t, x, len(a))
			require.Less(t, y, len(b))
			require.Equal(t, a[x], op.Line)
			require.True(t, eq(a[x], b[y]), "op %d pairs unequal lines", i)
			x++
			y++
		case '-':
			require.Equal(t, a[x], op.Line)
			x++
			edits++
		case '+':
			require.Equal(t, b[y], op.Line)
			y++
			edits++
		default:
			t.Fatalf("unexpected op kind %q", op.Kind)
		}
	}
	require.Equal(t, len(a), x)
	require.Equal(t, len(b), y)
	return edits
}

func editCount(ops []Op) int {
	n := 0
	for _, op := range ops {
		if op.Kind != ' ' {
			n++
		}
	}
	return n
}

func TestLines_MatchesReferenceEditCount(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	alphabet := []string{"a", "b", "c", "d", "e"}
	randLines := func(n, k int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = alphabet[rng.Intn(k)]
		}
		return out
	}
	for i := 0; i < 3000; i++ {
		k := 1 + rng.Intn(len(alphabet))
		a := randLines(rng.Intn(40), k)
		var b []string
		if rng.Intn(2) == 0 {
			b = randLines(rng.Intn(40), k)
		} else {
			b = mutate(rng, a, alphabet[:k])
		}
		ops := Lines(a, b)
		got := checkScript(t, a, b, ops, nil)
		require.Equal(t, editCount(referenceLines(a, b, nil)), got, "a=%q b=%q", a, b)

		budget := rng.Intn(got + 3)
		bOps, ok := LinesBudget(a, b, nil, budget)
		require.Equal(t, got <= budget, ok, "budget %d cost %d", budget, got)
		if ok {
			require.Equal(t, ops, bOps)
		} else {
			require.Nil(t, bOps)
		}
	}
}

func mutate(rng *rand.Rand, a, alphabet []string) []string {
	b := append([]string(nil), a...)
	for n := rng.Intn(6); n > 0; n-- {
		switch pos := rng.Intn(len(b) + 1); rng.Intn(3) {
		case 0:
			b = append(b[:pos], append([]string{alphabet[rng.Intn(len(alphabet))]}, b[pos:]...)...)
		case 1:
			if pos < len(b) {
				b = append(b[:pos], b[pos+1:]...)
			}
		default:
			if pos < len(b) {
				b[pos] = alphabet[rng.Intn(len(alphabet))]
			}
		}
	}
	return b
}

func TestLinesWith_CustomEqual(t *testing.T) {
	fold := func(x, y string) bool { return strings.EqualFold(x, y) }
	a := []string{"Head", "one", "TWO", "three", "Tail"}
	b := []string{"head", "ONE", "zwei", "three", "tail"}
	ops := LinesWith(a, b, fold)
	require.Equal(t, 2, checkScript(t, a, b, ops, fold))
	require.Equal(t, editCount(referenceLines(a, b, fold)), editCount(ops))
}

func TestLinesBudget(t *testing.T) {
	a := []string{"a", "b", "c", "d"}
	ops, ok := LinesBudget(a, a, nil, 0)
	require.True(t, ok)
	require.Len(t, ops, 4)

	b := []string{"a", "x", "c", "y"}
	_, ok = LinesBudget(a, b, nil, 3)
	require.False(t, ok)
	ops, ok = LinesBudget(a, b, nil, 4)
	require.True(t, ok)
	require.Equal(t, 4, checkScript(t, a, b, ops, nil))

	// The length difference alone proves the cost.
	_, ok = LinesBudget(nil, []string{"1", "2", "3"}, nil, 2)
	require.False(t, ok)

	// Nothing in common.
	_, ok = LinesBudget([]string{"p", "q"}, []string{"r", "s"}, nil, 3)
	require.False(t, ok)
	ops, ok = LinesBudget([]string{"p", "q"}, []string{"r", "s"}, nil, Unlimited)
	require.True(t, ok)
	require.Equal(t, []Op{
		{Kind: '-', Line: "p", A: 0, B: 0},
		{Kind: '-', Line: "q", A: 1, B: 0},
		{Kind: '+', Line: "r", A: 2, B: 0},
		{Kind: '+', Line: "s", A: 2, B: 1},
	}, ops)
}

func TestLinesBudget_LargeInputGivesUpEarly(t *testing.T) {
	a, b := benchLines(200000, 2000)
	_, ok := LinesBudget(a, b, nil, 100)
	require.False(t, ok)
	ops, ok := LinesBudget(a, b, nil, 4000)
	require.True(t, ok)
	require.Equal(t, 4000, checkScript(t, a, b, ops, nil))
}
