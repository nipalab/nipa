package preview

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

var (
	// MaxWorkbookSheets bounds how many worksheets ParseWorkbook reads.
	MaxWorkbookSheets = 256
	// MaxWorkbookXMLBytes bounds the decompressed size of all parts read.
	MaxWorkbookXMLBytes int64 = 128 << 20
	// MaxWorkbookRows bounds the row index of a cell (the Excel limit).
	MaxWorkbookRows = 1 << 20
	// MaxWorkbookColumns bounds the column index of a cell (the Excel limit).
	MaxWorkbookColumns = 1 << 14
	maxWorkbookEntries = 10_000
)

var errWorkbook = errors.New("preview: invalid workbook")

// Sheet is one named table of a workbook; CSV and TSV files have a single
// sheet with an empty name.
type Sheet struct {
	Name  string
	Table *Table
}

// SheetDiff pairs the old and new versions of a sheet by name. Status is
// same, added, removed or changed.
type SheetDiff struct {
	Name   string
	Status string
	Diff   *TableDiff
}

// IsWorkbook reports whether name is parsed as an .xlsx workbook.
func IsWorkbook(name string) bool {
	return strings.EqualFold(filepath.Ext(name), ".xlsx")
}

// ParseSheets reads a table file: every worksheet of an .xlsx workbook, or
// the single sheet of a CSV/TSV file.
func ParseSheets(name string, data []byte) ([]Sheet, error) {
	if IsWorkbook(name) {
		return ParseWorkbook(data)
	}
	t, err := ParseTable(name, data)
	if err != nil {
		return nil, err
	}
	return []Sheet{{Table: t}}, nil
}

// DiffSheets matches sheets by name (new order, removed sheets after their
// old predecessor) and compares each pair.
func DiffSheets(old, new []Sheet) []SheetDiff {
	names := func(s []Sheet) []string {
		out := make([]string, len(s))
		for i, sh := range s {
			out[i] = sh.Name
		}
		return occurrences(out)
	}
	var out []SheetDiff
	for _, p := range alignUnique(names(old), names(new)) {
		var o, n *Table
		d := SheetDiff{Status: "changed"}
		if p.old >= 0 {
			o, d.Name = old[p.old].Table, old[p.old].Name
		}
		if p.new >= 0 {
			n, d.Name = new[p.new].Table, new[p.new].Name
		}
		d.Diff = DiffTables(o, n)
		switch {
		case p.old < 0:
			d.Status = "added"
		case p.new < 0:
			d.Status = "removed"
		case !d.Diff.Changed():
			d.Status = "same"
		}
		out = append(out, d)
	}
	return out
}

// Changed reports whether any row or column differs.
func (d *TableDiff) Changed() bool {
	return d.RowsAdded+d.RowsRemoved+d.RowsChanged+d.RowsMoved+d.ColumnsAdded+d.ColumnsRemoved > 0
}

type workbookReader struct {
	files  map[string]*zip.File
	budget int64
	cells  int
}

// ParseWorkbook reads the cached cell values of every worksheet of an .xlsx
// file. Formulas are not evaluated and number formats are not applied.
func ParseWorkbook(data []byte) ([]Sheet, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errWorkbook, err)
	}
	if len(zr.File) > maxWorkbookEntries {
		return nil, fmt.Errorf("%w: more than %d parts", errTableTooLarge, maxWorkbookEntries)
	}
	w := &workbookReader{files: make(map[string]*zip.File, len(zr.File)), budget: MaxWorkbookXMLBytes}
	for _, f := range zr.File {
		w.files[strings.TrimPrefix(f.Name, "/")] = f
	}
	refs, err := w.sheetRefs()
	if err != nil {
		return nil, err
	}
	shared, err := w.sharedStrings()
	if err != nil {
		return nil, err
	}
	out := make([]Sheet, 0, len(refs))
	for _, ref := range refs {
		t, err := w.sheet(ref.target, shared)
		if err != nil {
			return nil, fmt.Errorf("sheet %q: %w", ref.name, err)
		}
		out = append(out, Sheet{Name: ref.name, Table: t})
	}
	return out, nil
}

func (w *workbookReader) open(name string) (*xml.Decoder, func(), error) {
	f, ok := w.files[name]
	if !ok {
		return nil, nil, nil
	}
	rc, err := f.Open()
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", errWorkbook, err)
	}
	d := xml.NewDecoder(&budgetReader{r: rc, left: &w.budget})
	return d, func() { _ = rc.Close() }, nil
}

type budgetReader struct {
	r    io.Reader
	left *int64
}

func (b *budgetReader) Read(p []byte) (int, error) {
	if *b.left <= 0 {
		return 0, fmt.Errorf("%w: more than %d bytes of XML", errTableTooLarge, MaxWorkbookXMLBytes)
	}
	if int64(len(p)) > *b.left {
		p = p[:*b.left]
	}
	n, err := b.r.Read(p)
	*b.left -= int64(n)
	return n, err
}

type sheetRef struct{ name, id, target string }

func (w *workbookReader) sheetRefs() ([]sheetRef, error) {
	d, done, err := w.open("xl/workbook.xml")
	if err != nil {
		return nil, err
	}
	if d == nil {
		return nil, fmt.Errorf("%w: no xl/workbook.xml", errWorkbook)
	}
	defer done()
	var refs []sheetRef
	err = walkXML(d, func(se xml.StartElement) error {
		if se.Name.Local != "sheet" {
			return nil
		}
		if len(refs) >= MaxWorkbookSheets {
			return fmt.Errorf("%w: more than %d sheets", errTableTooLarge, MaxWorkbookSheets)
		}
		ref := sheetRef{name: attr(se, "name")}
		for _, a := range se.Attr {
			if a.Name.Local == "id" && a.Name.Space != "" {
				ref.id = a.Value
			}
		}
		refs = append(refs, ref)
		return nil
	}, nil)
	if err != nil {
		return nil, err
	}
	targets, err := w.relationships()
	if err != nil {
		return nil, err
	}
	out := refs[:0]
	for i, ref := range refs {
		rel, ok := targets[ref.id]
		switch {
		case !ok:
			ref.target = fmt.Sprintf("xl/worksheets/sheet%d.xml", i+1)
		case !strings.HasSuffix(rel.kind, "/worksheet"):
			continue
		default:
			ref.target = rel.target
		}
		out = append(out, ref)
	}
	return out, nil
}

type relationship struct{ target, kind string }

func (w *workbookReader) relationships() (map[string]relationship, error) {
	out := map[string]relationship{}
	d, done, err := w.open("xl/_rels/workbook.xml.rels")
	if err != nil || d == nil {
		return out, err
	}
	defer done()
	err = walkXML(d, func(se xml.StartElement) error {
		if se.Name.Local != "Relationship" {
			return nil
		}
		target := attr(se, "Target")
		if strings.HasPrefix(target, "/") {
			target = strings.TrimPrefix(path.Clean(target), "/")
		} else {
			target = path.Join("xl", target)
		}
		out[attr(se, "Id")] = relationship{target: target, kind: attr(se, "Type")}
		return nil
	}, nil)
	return out, err
}

func (w *workbookReader) sharedStrings() ([]string, error) {
	d, done, err := w.open("xl/sharedStrings.xml")
	if err != nil || d == nil {
		return nil, err
	}
	defer done()
	var out []string
	var cur strings.Builder
	inText, phonetic := false, 0
	err = walkXML(d, func(se xml.StartElement) error {
		switch se.Name.Local {
		case "si":
			if len(out) >= MaxTableCells {
				return fmt.Errorf("%w: more than %d shared strings", errTableTooLarge, MaxTableCells)
			}
			cur.Reset()
		case "rPh":
			phonetic++
		case "t":
			inText = phonetic == 0
		}
		return nil
	}, func(tok xml.Token) {
		switch t := tok.(type) {
		case xml.CharData:
			if inText {
				cur.Write(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "si":
				out = append(out, cur.String())
			case "rPh":
				phonetic--
			case "t":
				inText = false
			}
		}
	})
	return out, err
}

func (w *workbookReader) sheet(name string, shared []string) (*Table, error) {
	t := &Table{Rows: [][]string{}}
	d, done, err := w.open(name)
	if err != nil || d == nil {
		return t, err
	}
	defer done()
	var (
		row, col       = -1, -1
		kind, value    string
		inValue, inStr bool
		text           strings.Builder
	)
	put := func(r, c int, v string) error {
		for len(t.Rows) <= r {
			if w.cells++; w.cells > MaxTableCells {
				return fmt.Errorf("%w: more than %d cells", errTableTooLarge, MaxTableCells)
			}
			t.Rows = append(t.Rows, nil)
		}
		for len(t.Rows[r]) <= c {
			if w.cells++; w.cells > MaxTableCells {
				return fmt.Errorf("%w: more than %d cells", errTableTooLarge, MaxTableCells)
			}
			t.Rows[r] = append(t.Rows[r], "")
		}
		t.Rows[r][c] = v
		return nil
	}
	var cellErr error
	err = walkXML(d, func(se xml.StartElement) error {
		switch se.Name.Local {
		case "row":
			next := row + 1
			if r, err := strconv.Atoi(attr(se, "r")); err == nil {
				next = r - 1
			}
			if next <= row || next >= MaxWorkbookRows {
				return fmt.Errorf("%w: bad row %d", errWorkbook, next+1)
			}
			row, col = next, -1
		case "c":
			if row < 0 {
				return fmt.Errorf("%w: cell outside a row", errWorkbook)
			}
			next := col + 1
			if c, r, ok := cellRef(attr(se, "r")); ok {
				if r != row {
					return fmt.Errorf("%w: cell %s outside row %d", errWorkbook, attr(se, "r"), row+1)
				}
				next = c
			}
			if next <= col || next >= MaxWorkbookColumns {
				return fmt.Errorf("%w: bad cell %q", errWorkbook, attr(se, "r"))
			}
			col, kind, value = next, attr(se, "t"), ""
			text.Reset()
		case "v":
			inValue = true
		case "t":
			inStr = kind == "inlineStr"
		}
		return nil
	}, func(tok xml.Token) {
		switch t := tok.(type) {
		case xml.CharData:
			if inValue || inStr {
				text.Write(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "v":
				inValue, value = false, text.String()
			case "t":
				inStr = false
				if kind == "inlineStr" {
					value = text.String()
				}
			case "c":
				v, err := cellValue(kind, value, shared)
				if err == nil && v != "" {
					err = put(row, col, v)
				}
				if err != nil && cellErr == nil {
					cellErr = err
				}
			}
		}
	})
	if err == nil {
		err = cellErr
	}
	if err != nil {
		return nil, err
	}
	for i, r := range t.Rows {
		if r == nil {
			t.Rows[i] = []string{}
		}
	}
	return t, nil
}

func cellValue(kind, value string, shared []string) (string, error) {
	switch kind {
	case "s":
		if value == "" {
			return "", nil
		}
		i, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || i < 0 || i >= len(shared) {
			return "", fmt.Errorf("%w: bad shared string %q", errWorkbook, value)
		}
		return shared[i], nil
	case "b":
		switch strings.TrimSpace(value) {
		case "1":
			return "TRUE", nil
		case "0":
			return "FALSE", nil
		}
	}
	return value, nil
}

// cellRef parses an A1-style reference into zero-based column and row.
func cellRef(ref string) (col, row int, ok bool) {
	i := 0
	for ; i < len(ref) && i < 4; i++ {
		c := ref[i] | 0x20
		if c < 'a' || c > 'z' {
			break
		}
		col = col*26 + int(c-'a'+1)
	}
	if i == 0 || i == len(ref) {
		return 0, 0, false
	}
	r, err := strconv.Atoi(strings.TrimPrefix(ref[i:], "$"))
	if err != nil || r < 1 {
		return 0, 0, false
	}
	return col - 1, r - 1, true
}

func attr(se xml.StartElement, name string) string {
	for _, a := range se.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

func walkXML(d *xml.Decoder, start func(xml.StartElement) error, other func(xml.Token)) error {
	depth := 0
	for {
		tok, err := d.RawToken()
		if errors.Is(err, io.EOF) && depth == 0 {
			return nil
		}
		if err != nil {
			if errors.Is(err, errTableTooLarge) {
				return err
			}
			return fmt.Errorf("%w: %v", errWorkbook, err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if err := start(t); err != nil {
				return err
			}
		case xml.EndElement:
			depth--
		}
		if other != nil {
			other(tok)
		}
	}
}
