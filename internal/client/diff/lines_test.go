package diff

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func applyOps(t *testing.T, a []string, ops []Op) []string {
	t.Helper()
	var out []string
	ai, bi := 0, 0
	for _, op := range ops {
		switch op.Kind {
		case '=':
			require.Equal(t, ai, op.A, "equal op A index")
			require.Equal(t, bi, op.B, "equal op B index")
			require.Equal(t, a[op.A], op.Line)
			out = append(out, op.Line)
			ai++
			bi++
		case '-':
			require.Equal(t, ai, op.A, "delete op A index")
			require.Equal(t, a[op.A], op.Line)
			ai++
		case '+':
			require.Equal(t, bi, op.B, "insert op B index")
			out = append(out, op.Line)
			bi++
		default:
			t.Fatalf("unknown op kind %q", op.Kind)
		}
	}
	require.Equal(t, len(a), ai, "script must consume all of a")
	return out
}

func TestLines_Empty(t *testing.T) {
	require.Empty(t, Lines(nil, nil))
}

func TestLines_AllAdded(t *testing.T) {
	ops := Lines(nil, []string{"a\n", "b\n"})
	require.Equal(t, []Op{
		{Kind: '+', Line: "a\n", A: 0, B: 0},
		{Kind: '+', Line: "b\n", A: 0, B: 1},
	}, ops)
}

func TestLines_AllDeleted(t *testing.T) {
	ops := Lines([]string{"a\n", "b\n"}, nil)
	require.Equal(t, []Op{
		{Kind: '-', Line: "a\n", A: 0, B: 0},
		{Kind: '-', Line: "b\n", A: 1, B: 0},
	}, ops)
}

func TestLines_Equal(t *testing.T) {
	a := []string{"a\n", "b\n", "c\n"}
	ops := Lines(a, a)
	require.Len(t, ops, 3)
	for i, op := range ops {
		require.Equal(t, Op{Kind: '=', Line: a[i], A: i, B: i}, op)
	}
}

func TestLines_RoundTrip(t *testing.T) {
	a := []string{"l1\n", "l2\n", "l3\n", "l4\n", "l5\n", "l6\n"}
	b := []string{"l1\n", "changed\n", "l3\n", "l4\n", "added\n", "l5\n", "l6\n", "tail\n"}
	ops := Lines(a, b)
	require.Equal(t, b, applyOps(t, a, ops))

	deletes := 0
	inserts := 0
	for _, op := range ops {
		switch op.Kind {
		case '-':
			deletes++
		case '+':
			inserts++
		}
	}
	require.Equal(t, 1, deletes)
	require.Equal(t, 3, inserts)
}

func TestLines_Substitution(t *testing.T) {
	a := []string{"x\n"}
	b := []string{"y\n"}
	ops := Lines(a, b)
	require.Equal(t, b, applyOps(t, a, ops))
	require.Len(t, ops, 2)
}
