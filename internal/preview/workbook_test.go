package preview

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	xlsxWorkbook = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets>%s</sheets></workbook>`
	xlsxRels = `<?xml version="1.0" encoding="UTF-8"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">%s</Relationships>`
	xlsxSheet = `<?xml version="1.0" encoding="UTF-8"?>
<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>%s</sheetData></worksheet>`
	relWorksheet  = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet"
	relChartsheet = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/chartsheet"
)

type xlsxSpec struct {
	sheets map[string]string // sheet name → sheetData inner XML
	order  []string
	shared []string
	extra  map[string]string
}

func zipFiles(t testing.TB, files map[string]string, order ...string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	seen := map[string]bool{}
	write := func(name string) {
		if seen[name] {
			return
		}
		seen[name] = true
		w, err := zw.Create(name)
		require.NoError(t, err)
		_, err = w.Write([]byte(files[name]))
		require.NoError(t, err)
	}
	for _, name := range order {
		write(name)
	}
	for name := range files {
		write(name)
	}
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func (s xlsxSpec) bytes(t testing.TB) []byte {
	files := map[string]string{}
	var sheets, rels strings.Builder
	for i, name := range s.order {
		fmt.Fprintf(&sheets, `<sheet name="%s" sheetId="%d" r:id="rId%d"/>`, name, i+1, i+1)
		fmt.Fprintf(&rels, `<Relationship Id="rId%d" Type="%s" Target="worksheets/sheet%d.xml"/>`, i+1, relWorksheet, i+1)
		files[fmt.Sprintf("xl/worksheets/sheet%d.xml", i+1)] = fmt.Sprintf(xlsxSheet, s.sheets[name])
	}
	files["xl/workbook.xml"] = fmt.Sprintf(xlsxWorkbook, sheets.String())
	files["xl/_rels/workbook.xml.rels"] = fmt.Sprintf(xlsxRels, rels.String())
	if s.shared != nil {
		var sst strings.Builder
		sst.WriteString(`<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">`)
		for _, v := range s.shared {
			sst.WriteString(v)
		}
		sst.WriteString(`</sst>`)
		files["xl/sharedStrings.xml"] = sst.String()
	}
	for k, v := range s.extra {
		files[k] = v
	}
	return zipFiles(t, files, "xl/workbook.xml")
}

func TestParseWorkbook(t *testing.T) {
	data := xlsxSpec{
		order: []string{"Items", "Loc &amp; text"},
		shared: []string{
			`<si><t>id</t></si>`,
			`<si><t>name</t></si>`,
			`<si><r><t>Iron </t></r><r><rPr><b/></rPr><t>Sword</t></r><rPh sb="0" eb="1"><t>ignored</t></rPh></si>`,
			`<si><t/></si>`,
		},
		sheets: map[string]string{
			"Items": `<row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c><c r="C1" t="inlineStr"><is><t>ok</t></is></c></row>` +
				`<row r="2"><c r="A2"><v>1001</v></c><c r="B2" t="s"><v>2</v></c><c r="C2" t="b"><v>1</v></c><c r="E2" t="str"><f>A2*2</f><v>2002</v></c></row>` +
				`<row r="4"><c t="b"><v>0</v></c><c t="e"><v>#DIV/0!</v></c><c r="D4" t="s"><v>3</v></c><c t="s"/></row>`,
			"Loc &amp; text": `<row><c><v>1.5</v></c></row><row><c/></row>`,
		},
	}.bytes(t)
	sheets, err := ParseWorkbook(data)
	require.NoError(t, err)
	require.Len(t, sheets, 2)
	assert.Equal(t, "Items", sheets[0].Name)
	assert.Equal(t, [][]string{
		{"id", "name", "ok"},
		{"1001", "Iron Sword", "TRUE", "", "2002"},
		{},
		{"FALSE", "#DIV/0!"},
	}, sheets[0].Table.Rows)
	assert.Equal(t, "Loc & text", sheets[1].Name)
	assert.Equal(t, [][]string{{"1.5"}}, sheets[1].Table.Rows)

	via, err := ParseSheets("Data/Items.XLSX", data)
	require.NoError(t, err)
	assert.Equal(t, sheets, via)
	assert.Equal(t, KindTable, Classify("a.xlsx", true))
}

func TestParseWorkbookRelationships(t *testing.T) {
	files := map[string]string{
		"xl/workbook.xml": fmt.Sprintf(xlsxWorkbook, `<sheet name="Abs" r:id="a"/><sheet name="Chart" r:id="c"/><sheet name="Fallback"/><sheet name="Gone" r:id="g"/>`),
		"xl/_rels/workbook.xml.rels": fmt.Sprintf(xlsxRels, `<Relationship Id="a" Type="`+relWorksheet+`" Target="/xl/data/one.xml"/>`+
			`<Relationship Id="c" Type="`+relChartsheet+`" Target="chartsheets/sheet1.xml"/>`+
			`<Relationship Id="g" Type="`+relWorksheet+`" Target="worksheets/missing.xml"/>`),
		"xl/data/one.xml":           fmt.Sprintf(xlsxSheet, `<row><c><v>abs</v></c></row>`),
		"xl/worksheets/sheet3.xml":  fmt.Sprintf(xlsxSheet, `<row><c><v>third</v></c></row>`),
		"xl/chartsheets/sheet1.xml": `<chartsheet/>`,
	}
	sheets, err := ParseWorkbook(zipFiles(t, files))
	require.NoError(t, err)
	require.Len(t, sheets, 3)
	assert.Equal(t, [][]string{{"abs"}}, sheets[0].Table.Rows)
	assert.Equal(t, "Fallback", sheets[1].Name)
	assert.Equal(t, [][]string{{"third"}}, sheets[1].Table.Rows)
	assert.Empty(t, sheets[2].Table.Rows)

	delete(files, "xl/_rels/workbook.xml.rels")
	sheets, err = ParseWorkbook(zipFiles(t, files))
	require.NoError(t, err)
	require.Len(t, sheets, 4)
	assert.Equal(t, [][]string{{"third"}}, sheets[2].Table.Rows)
}

func TestParseWorkbookErrors(t *testing.T) {
	one := func(data string, shared ...string) []byte {
		return xlsxSpec{order: []string{"S"}, sheets: map[string]string{"S": data}, shared: shared}.bytes(t)
	}
	for name, data := range map[string][]byte{
		"not a zip":         []byte("PK nope"),
		"no workbook":       zipFiles(t, map[string]string{"xl/other.xml": "<x/>"}),
		"bad workbook xml":  zipFiles(t, map[string]string{"xl/workbook.xml": "<workbook><sheets>"}),
		"rows out of order": one(`<row r="2"/><row r="1"/>`),
		"row zero":          one(`<row r="0"/>`),
		"cell outside row":  one(`<c r="A1"><v>1</v></c>`),
		"cell in other row": one(`<row r="1"><c r="A2"><v>1</v></c></row>`),
		"cells backwards":   one(`<row><c r="B1"><v>1</v></c><c r="A1"><v>1</v></c></row>`),
		"column too wide":   one(`<row><c r="XFE1"><v>1</v></c></row>`),
		"row too far":       one(`<row r="1048577"/>`),
		"bad shared index":  one(`<row><c t="s"><v>5</v></c></row>`, `<si><t>a</t></si>`),
		"bad shared value":  one(`<row><c t="s"><v>x</v></c></row>`),
		"bad sheet xml":     one(`<row><c>`),
	} {
		_, err := ParseWorkbook(data)
		assert.Error(t, err, name)
	}
	_, err := ParseSheets("a.xlsx", []byte("a,b\n"))
	assert.ErrorIs(t, err, errWorkbook)
	_, err = ParseSheets("a.csv", []byte("\xff\x00"))
	assert.ErrorIs(t, err, errTable)
}

func TestCellRef(t *testing.T) {
	for ref, want := range map[string][2]int{"A1": {0, 0}, "z9": {25, 8}, "AA10": {26, 9}, "XFD1048576": {16383, 1048575}, "B$3": {1, 2}} {
		c, r, ok := cellRef(ref)
		require.True(t, ok, ref)
		assert.Equal(t, want, [2]int{c, r}, ref)
	}
	for _, ref := range []string{"", "A", "1", "A0", "ABCDE1", "A-1"} {
		_, _, ok := cellRef(ref)
		assert.False(t, ok, ref)
	}
}

func TestParseWorkbookLimits(t *testing.T) {
	defer func(sheets int, bytes int64, cells int) {
		MaxWorkbookSheets, MaxWorkbookXMLBytes, MaxTableCells = sheets, bytes, cells
	}(MaxWorkbookSheets, MaxWorkbookXMLBytes, MaxTableCells)

	MaxWorkbookSheets = 1
	_, err := ParseWorkbook(xlsxSpec{order: []string{"a", "b"}, sheets: map[string]string{}}.bytes(t))
	assert.ErrorIs(t, err, errTableTooLarge)
	MaxWorkbookSheets = 256

	MaxTableCells = 10
	_, err = ParseWorkbook(xlsxSpec{order: []string{"a"}, sheets: map[string]string{"a": `<row r="20"><c><v>1</v></c></row>`}}.bytes(t))
	assert.ErrorIs(t, err, errTableTooLarge)
	_, err = ParseWorkbook(xlsxSpec{order: []string{"a"}, sheets: map[string]string{"a": `<row><c r="Z1"><v>1</v></c></row>`}}.bytes(t))
	assert.ErrorIs(t, err, errTableTooLarge)
	_, err = ParseWorkbook(xlsxSpec{order: []string{"a"}, shared: []string{strings.Repeat("<si/>", 11)}, sheets: map[string]string{}}.bytes(t))
	assert.ErrorIs(t, err, errTableTooLarge)
	MaxTableCells = 2_000_000

	MaxWorkbookXMLBytes = 1 << 20
	bomb := xlsxSpec{order: []string{"a"}, sheets: map[string]string{"a": strings.Repeat("<row/>", 1<<20)}}.bytes(t)
	require.Less(t, len(bomb), 64<<10)
	var parseErr error
	alloc := allocatedBy(func() { _, parseErr = ParseWorkbook(bomb) })
	assert.ErrorIs(t, parseErr, errTableTooLarge)
	assert.Less(t, alloc, uint64(64<<20))
}

func TestParseWorkbookTooManyParts(t *testing.T) {
	files := map[string]string{}
	for i := range maxWorkbookEntries + 1 {
		files[fmt.Sprintf("x/%d", i)] = ""
	}
	_, err := ParseWorkbook(zipFiles(t, files))
	assert.ErrorIs(t, err, errTableTooLarge)
}

func TestDiffSheets(t *testing.T) {
	tbl := func(rows ...[]string) *Table { return &Table{Rows: rows} }
	old := []Sheet{
		{Name: "Items", Table: tbl([]string{"id", "v"}, []string{"1", "a"})},
		{Name: "Old", Table: tbl([]string{"x"})},
		{Name: "Same", Table: tbl([]string{"k"}, []string{"1"})},
	}
	next := []Sheet{
		{Name: "Same", Table: tbl([]string{"k"}, []string{"1"})},
		{Name: "Items", Table: tbl([]string{"id", "v"}, []string{"1", "b"})},
		{Name: "New", Table: tbl([]string{"y"}, []string{"2"})},
	}
	d := DiffSheets(old, next)
	var got []string
	for _, s := range d {
		got = append(got, s.Name+":"+s.Status)
	}
	assert.Equal(t, []string{"Same:same", "Items:changed", "New:added", "Old:removed"}, got)
	assert.Equal(t, 1, d[1].Diff.CellsChanged)
	assert.Equal(t, 1, d[2].Diff.RowsAdded)

	csv, err := ParseSheets("a.csv", []byte("a,b\n1,2\n"))
	require.NoError(t, err)
	d = DiffSheets(nil, csv)
	require.Len(t, d, 1)
	assert.Equal(t, "", d[0].Name)
	assert.Equal(t, "added", d[0].Status)
}

func FuzzParseWorkbook(f *testing.F) {
	f.Add(xlsxSpec{order: []string{"S"}, shared: []string{"<si><t>a</t></si>"}, sheets: map[string]string{"S": `<row><c t="s"><v>0</v></c><c><v>2</v></c></row>`}}.bytes(f))
	f.Add([]byte("PK\x03\x04"))
	f.Fuzz(func(t *testing.T, data []byte) {
		sheets, err := ParseWorkbook(data)
		if err != nil {
			return
		}
		for _, d := range DiffSheets(sheets, sheets) {
			if d.Status != "same" {
				t.Fatalf("sheet %q differs from itself", d.Name)
			}
		}
	})
}
