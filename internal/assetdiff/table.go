package assetdiff

import (
	"fmt"
	"strings"

	"github.com/nipalab/nipa/internal/diff"
	"github.com/nipalab/nipa/internal/preview"
)

type TableResult struct {
	Columns   []TableColumn `json:"columns"`
	KeyColumn string        `json:"key_column,omitempty"`
	TableCounts
	Rows      []TableRow   `json:"rows"`
	Truncated bool         `json:"truncated,omitempty"`
	Sheet     string       `json:"sheet,omitempty"`
	Sheets    []TableSheet `json:"sheets,omitempty"`
}

type TableCounts struct {
	RowsAdded      int `json:"rows_added"`
	RowsRemoved    int `json:"rows_removed"`
	RowsChanged    int `json:"rows_changed"`
	RowsMoved      int `json:"rows_moved"`
	CellsChanged   int `json:"cells_changed"`
	ColumnsAdded   int `json:"columns_added"`
	ColumnsRemoved int `json:"columns_removed"`
}

// TableSheet is one workbook sheet; Status is same, added, removed or
// changed. Rows and columns are only returned for the selected sheet.
type TableSheet struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	TableCounts
}

type TableColumn struct {
	Name    string `json:"name"`
	OldName string `json:"old_name,omitempty"`
	Status  string `json:"status"`
}

// TableRow is one differing row: Cells hold the new values (the old ones
// for removed rows), Old the previous values of changed rows.
type TableRow struct {
	Status  string   `json:"status"`
	OldLine int      `json:"old_line,omitempty"`
	NewLine int      `json:"new_line,omitempty"`
	Cells   []string `json:"cells"`
	Old     []string `json:"old,omitempty"`
	Changed []int    `json:"changed,omitempty"`
	Moved   bool     `json:"moved,omitempty"`
}

func countsOf(d *preview.TableDiff) TableCounts {
	return TableCounts{
		RowsAdded: d.RowsAdded, RowsRemoved: d.RowsRemoved, RowsChanged: d.RowsChanged, RowsMoved: d.RowsMoved,
		CellsChanged: d.CellsChanged, ColumnsAdded: d.ColumnsAdded, ColumnsRemoved: d.ColumnsRemoved,
	}
}

// PickSheet returns the index of the named sheet, or of the first changed
// sheet when name is empty; -1 when there is none.
func PickSheet(sheets []preview.SheetDiff, name string) int {
	if name != "" {
		for i, s := range sheets {
			if s.Name == name {
				return i
			}
		}
		return -1
	}
	for i, s := range sheets {
		if s.Status != "same" {
			return i
		}
	}
	return min(len(sheets), 1) - 1
}

func compareTable(sides [2]side, opts Options) (*TableResult, error) {
	var parsed [2][]preview.Sheet
	workbook := false
	for i, s := range sides {
		if !s.present {
			continue
		}
		workbook = workbook || preview.IsWorkbook(s.ref.Path)
		sheets, err := preview.ParseSheets(s.ref.Path, s.data)
		if err != nil {
			return nil, err
		}
		parsed[i] = sheets
	}
	diffs := preview.DiffSheets(parsed[0], parsed[1])
	pick := PickSheet(diffs, opts.Sheet)
	if pick < 0 && opts.Sheet != "" {
		return nil, fmt.Errorf("no sheet named %q", opts.Sheet)
	}
	d := preview.DiffTables(nil, nil)
	if pick >= 0 {
		d = diffs[pick].Diff
	}
	out := &TableResult{Columns: make([]TableColumn, len(d.Columns)), Rows: []TableRow{}, TableCounts: countsOf(d)}
	if workbook {
		out.Sheets = make([]TableSheet, len(diffs))
		for i, s := range diffs {
			out.Sheets[i] = TableSheet{Name: s.Name, Status: s.Status, TableCounts: countsOf(s.Diff)}
		}
		if pick >= 0 {
			out.Sheet = diffs[pick].Name
		}
	}
	for i, c := range d.Columns {
		col := TableColumn{Name: c.Name, Status: "same"}
		switch {
		case c.Old < 0:
			col.Status = "added"
		case c.New < 0:
			col.Status = "removed"
		case c.OldName != "" && c.OldName != c.Name:
			col.Status, col.OldName = "renamed", c.OldName
		}
		out.Columns[i] = col
	}
	if d.KeyColumn >= 0 && d.KeyColumn < len(d.Columns) {
		out.KeyColumn = d.Columns[d.KeyColumn].Name
	}
	for _, r := range d.Rows {
		if r.Status == "same" && !r.Moved {
			continue
		}
		if len(out.Rows) >= opts.MaxRows {
			out.Truncated = true
			break
		}
		out.Rows = append(out.Rows, TableRow{Status: r.Status, OldLine: r.OldLine, NewLine: r.NewLine, Cells: r.Cells, Old: r.Old, Changed: r.Changed, Moved: r.Moved})
	}
	return out, nil
}

type TextResult struct {
	LinesAdded   int `json:"lines_added"`
	LinesRemoved int `json:"lines_removed"`
}

func compareText(sides [2]side) *TextResult {
	var lines [2][]string
	for i, s := range sides {
		if int64(len(s.data)) > MaxTextBytes {
			return nil
		}
		if len(s.data) > 0 {
			lines[i] = strings.Split(strings.TrimSuffix(string(s.data), "\n"), "\n")
		}
	}
	out := &TextResult{}
	for _, op := range diff.Lines(lines[0], lines[1]) {
		switch op.Kind {
		case '+':
			out.LinesAdded++
		case '-':
			out.LinesRemoved++
		}
	}
	return out
}
