package service

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strings"
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
	out, err := svc.TableDiff(ctx, "/work/game", &ImageRef{Source: "snapshot", Path: "loc/old/en.csv"}, &ImageRef{Source: "working", Path: "loc/en.csv"}, "")
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

	out, err = svc.TableDiff(ctx, "/work/game", nil, &ImageRef{Source: "working", Path: "loc/en.csv"}, "")
	require.NoError(t, err)
	assert.Equal(t, 3, out.RowsAdded)
	assert.Equal(t, 2, out.ColumnsAdded)

	out, err = svc.TableDiff(ctx, "/work/game", &ImageRef{Source: "snapshot", Path: "loc/missing.csv"}, &ImageRef{Source: "working", Path: "loc/en.csv"}, "")
	require.NoError(t, err)
	assert.Equal(t, 3, out.RowsAdded)

	out, err = svc.TableDiff(ctx, "/work/game", nil, &ImageRef{Source: "working", Path: "loc/bad.csv"}, "")
	require.NoError(t, err)
	assert.Contains(t, out.Error, "invalid table")
	assert.Empty(t, out.Rows)
	assert.Equal(t, -1, out.KeyColumn)

	d.sizes["loc/en.csv"] = maxTableBytes + 1
	out, err = svc.TableDiff(ctx, "/work/game", nil, &ImageRef{Source: "working", Path: "loc/en.csv"}, "")
	require.NoError(t, err)
	assert.Contains(t, out.Error, "too large")

	_, err = svc.TableDiff(ctx, "/work/game", nil, nil, "")
	assert.Error(t, err)

	d.err = status.Error(codes.Internal, "boom")
	_, err = svc.TableDiff(ctx, "/work/game", nil, &ImageRef{Source: "working", Path: "loc/en.csv"}, "")
	assert.ErrorContains(t, err, "boom")
}

func workbook(t *testing.T, sheets ...string) string {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	put := func(name, body string) {
		w, err := zw.Create(name)
		require.NoError(t, err)
		_, err = w.Write([]byte(body))
		require.NoError(t, err)
	}
	var list strings.Builder
	for i := 0; i < len(sheets); i += 2 {
		fmt.Fprintf(&list, `<sheet name="%s"/>`, sheets[i])
		var rows strings.Builder
		for _, line := range strings.Split(sheets[i+1], "\n") {
			rows.WriteString("<row>")
			for _, v := range strings.Split(line, ",") {
				fmt.Fprintf(&rows, `<c t="inlineStr"><is><t>%s</t></is></c>`, v)
			}
			rows.WriteString("</row>")
		}
		put(fmt.Sprintf("xl/worksheets/sheet%d.xml", i/2+1), "<worksheet><sheetData>"+rows.String()+"</sheetData></worksheet>")
	}
	put("xl/workbook.xml", "<workbook><sheets>"+list.String()+"</sheets></workbook>")
	require.NoError(t, zw.Close())
	return buf.String()
}

func TestService_TableDiffWorkbook(t *testing.T) {
	svc, _ := newModels(t, map[string]string{
		"data/old/items.xlsx": workbook(t, "Readme", "about", "Items", "id,hp\n1,10", "Gone", "x"),
		"data/items.xlsx":     workbook(t, "Readme", "about", "Items", "id,hp\n1,12", "Drops", "id\n7"),
		"data/bad.xlsx":       "PK nope",
	})
	old, next := &ImageRef{Source: "snapshot", Path: "data/old/items.xlsx"}, &ImageRef{Source: "working", Path: "data/items.xlsx"}
	out, err := svc.TableDiff(ctx, "/work/game", old, next, "")
	require.NoError(t, err)
	assert.Equal(t, "Items", out.Sheet)
	assert.Equal(t, "", out.Delimiter)
	assert.Equal(t, 1, out.CellsChanged)
	assert.Equal(t, []TableSheet{
		{Name: "Readme", Status: "same"},
		{Name: "Items", Status: "changed", RowsChanged: 1, CellsChanged: 1},
		{Name: "Drops", Status: "added", RowsAdded: 1},
		{Name: "Gone", Status: "removed"},
	}, out.Sheets)

	out, err = svc.TableDiff(ctx, "/work/game", old, next, "Drops")
	require.NoError(t, err)
	assert.Equal(t, "Drops", out.Sheet)
	assert.Equal(t, 1, out.RowsAdded)

	out, err = svc.TableDiff(ctx, "/work/game", old, next, "Renamed away")
	require.NoError(t, err)
	assert.Equal(t, "Items", out.Sheet)

	out, err = svc.TableDiff(ctx, "/work/game", nil, &ImageRef{Source: "working", Path: "data/bad.xlsx"}, "")
	require.NoError(t, err)
	assert.Contains(t, out.Error, "invalid workbook")
	assert.Empty(t, out.Sheets)

	out, err = svc.TableDiff(ctx, "/work/game", nil, &ImageRef{Source: "working", Path: "data/missing.xlsx"}, "")
	require.NoError(t, err)
	assert.Equal(t, "", out.Sheet)
	assert.Empty(t, out.Rows)
}
