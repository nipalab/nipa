package assetdiff

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// xlsx builds a workbook of inline-string sheets: name followed by rows.
func xlsx(t *testing.T, sheets ...any) []byte {
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
		n := i/2 + 1
		fmt.Fprintf(&list, `<sheet name="%s" sheetId="%d"/>`, sheets[i], n)
		var data strings.Builder
		for _, row := range sheets[i+1].([][]string) {
			data.WriteString("<row>")
			for _, v := range row {
				fmt.Fprintf(&data, `<c t="inlineStr"><is><t>%s</t></is></c>`, v)
			}
			data.WriteString("</row>")
		}
		put(fmt.Sprintf("xl/worksheets/sheet%d.xml", n), "<worksheet><sheetData>"+data.String()+"</sheetData></worksheet>")
	}
	put("xl/workbook.xml", "<workbook><sheets>"+list.String()+"</sheets></workbook>")
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func TestCompareWorkbook(t *testing.T) {
	f := files{
		"snapshot:items.xlsx": xlsx(t,
			"Readme", [][]string{{"about"}, {"v1"}},
			"Items", [][]string{{"id", "hp"}, {"1", "10"}, {"2", "8"}},
			"Old", [][]string{{"x"}},
		),
		"working:items.xlsx": xlsx(t,
			"Readme", [][]string{{"about"}, {"v1"}},
			"Items", [][]string{{"id", "hp"}, {"1", "12"}, {"2", "8"}},
			"New", [][]string{{"y"}, {"1"}},
		),
		"working:bad.xlsx": []byte("not a zip"),
	}
	r, err := Compare(ctx, f.load, oldRef("items.xlsx"), newRef("items.xlsx"), Options{})
	require.NoError(t, err)
	tb := r.Table
	require.NotNil(t, tb)
	assert.Equal(t, "Items", tb.Sheet)
	assert.Equal(t, 1, tb.CellsChanged)
	require.Len(t, tb.Rows, 1)
	assert.Equal(t, []string{"1", "12"}, tb.Rows[0].Cells)
	var statuses []string
	for _, s := range tb.Sheets {
		statuses = append(statuses, s.Name+":"+s.Status)
	}
	assert.Equal(t, []string{"Readme:same", "Items:changed", "New:added", "Old:removed"}, statuses)
	assert.Equal(t, 1, tb.Sheets[2].RowsAdded)
	assert.Equal(t, `"Items": 1 row changed (1 cell); sheet "New" added; sheet "Old" removed`, Headline(r))

	js, err := json.Marshal(tb.Sheets[1])
	require.NoError(t, err)
	assert.Contains(t, string(js), `"cells_changed":1`)

	r, err = Compare(ctx, f.load, oldRef("items.xlsx"), newRef("items.xlsx"), Options{Sheet: "New"})
	require.NoError(t, err)
	assert.Equal(t, "New", r.Table.Sheet)
	assert.Equal(t, 1, r.Table.RowsAdded)

	r, err = Compare(ctx, f.load, oldRef("items.xlsx"), newRef("items.xlsx"), Options{Sheet: "Nope"})
	require.NoError(t, err)
	assert.Equal(t, `no sheet named "Nope"`, r.Error)

	r, err = Compare(ctx, f.load, nil, newRef("bad.xlsx"), Options{})
	require.NoError(t, err)
	assert.Contains(t, r.Error, "invalid workbook")

	f["working:same.xlsx"] = xlsx(t, "A", [][]string{{"a"}})
	f["snapshot:same.xlsx"] = xlsx(t, "A", [][]string{{"a"}}, "B", [][]string{{"b"}})
	f["working:empty.xlsx"] = xlsx(t)
	r, err = Compare(ctx, f.load, oldRef("same.xlsx"), newRef("same.xlsx"), Options{})
	require.NoError(t, err)
	assert.Equal(t, "B", r.Table.Sheet)
	r, err = Compare(ctx, f.load, nil, newRef("empty.xlsx"), Options{})
	require.NoError(t, err)
	assert.Empty(t, r.Table.Sheets)
	assert.Equal(t, "", r.Table.Sheet)
	assert.Equal(t, "no cell changes", Headline(r))
}

func TestTableLineSheets(t *testing.T) {
	sheets := []TableSheet{{Name: "Same", Status: "same"}}
	for i := range 7 {
		sheets = append(sheets, TableSheet{Name: fmt.Sprint(i), Status: "changed", TableCounts: TableCounts{RowsAdded: 1}})
	}
	assert.Equal(t, `"0": +1 row; "1": +1 row; "2": +1 row; "3": +1 row; "4": +1 row; +2 more sheets`, tableLine(&TableResult{Sheets: sheets}))
	assert.Equal(t, "no cell changes", tableLine(&TableResult{Sheets: sheets[:1]}))
}
