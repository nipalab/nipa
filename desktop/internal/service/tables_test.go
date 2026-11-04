package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestService_TableDiff(t *testing.T) {
	svc, d := newModels(t, map[string]string{
		"loc/en.csv":     "key;text\nhello;Hi\nbye;Bye now\nnew;New\n",
		"loc/old/en.csv": "key;text\nhello;Hello\nbye;Bye now\ngone;Gone\n",
		"loc/bad.csv":    "\xff\x00\x80",
	})
	out, err := svc.TableDiff(ctx, "/work/game", &ImageRef{Source: "snapshot", Path: "loc/old/en.csv"}, &ImageRef{Source: "working", Path: "loc/en.csv"})
	require.NoError(t, err)
	assert.Equal(t, ";", out.Delimiter)
	assert.Equal(t, 0, out.KeyColumn)
	assert.Equal(t, []TableColumn{{Name: "key", OldName: "key", Old: 0, New: 0}, {Name: "text", OldName: "text", Old: 1, New: 1}}, out.Columns)
	require.Len(t, out.Rows, 4)
	assert.Equal(t, TableRow{Status: "changed", OldLine: 2, NewLine: 2, Cells: []string{"hello", "Hi"}, Old: []string{"hello", "Hello"}, Changed: []int{1}}, out.Rows[0])
	assert.Equal(t, 1, out.RowsAdded)
	assert.Equal(t, 1, out.RowsRemoved)
	assert.Equal(t, 1, out.RowsChanged)
	assert.Equal(t, 1, out.CellsChanged)

	out, err = svc.TableDiff(ctx, "/work/game", nil, &ImageRef{Source: "working", Path: "loc/en.csv"})
	require.NoError(t, err)
	assert.Equal(t, 3, out.RowsAdded)
	assert.Equal(t, 2, out.ColumnsAdded)

	out, err = svc.TableDiff(ctx, "/work/game", &ImageRef{Source: "snapshot", Path: "loc/missing.csv"}, &ImageRef{Source: "working", Path: "loc/en.csv"})
	require.NoError(t, err)
	assert.Equal(t, 3, out.RowsAdded)

	out, err = svc.TableDiff(ctx, "/work/game", nil, &ImageRef{Source: "working", Path: "loc/bad.csv"})
	require.NoError(t, err)
	assert.Contains(t, out.Error, "invalid table")
	assert.Empty(t, out.Rows)
	assert.Equal(t, -1, out.KeyColumn)

	d.sizes["loc/en.csv"] = maxTableBytes + 1
	out, err = svc.TableDiff(ctx, "/work/game", nil, &ImageRef{Source: "working", Path: "loc/en.csv"})
	require.NoError(t, err)
	assert.Contains(t, out.Error, "too large")

	_, err = svc.TableDiff(ctx, "/work/game", nil, nil)
	assert.Error(t, err)

	d.err = status.Error(codes.Internal, "boom")
	_, err = svc.TableDiff(ctx, "/work/game", nil, &ImageRef{Source: "working", Path: "loc/en.csv"})
	assert.ErrorContains(t, err, "boom")
}
