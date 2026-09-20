package diff

import (
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
