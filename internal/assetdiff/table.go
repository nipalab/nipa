package assetdiff

import (
	"strings"

	"github.com/nipalab/nipa/internal/diff"
	"github.com/nipalab/nipa/internal/preview"
)

type TableResult struct {
	Columns        []TableColumn `json:"columns"`
	KeyColumn      string        `json:"key_column,omitempty"`
	RowsAdded      int           `json:"rows_added"`
	RowsRemoved    int           `json:"rows_removed"`
	RowsChanged    int           `json:"rows_changed"`
	RowsMoved      int           `json:"rows_moved"`
	CellsChanged   int           `json:"cells_changed"`
	ColumnsAdded   int           `json:"columns_added"`
	ColumnsRemoved int           `json:"columns_removed"`
	Rows           []TableRow    `json:"rows"`
	Truncated      bool          `json:"truncated,omitempty"`
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

func compareTable(sides [2]side, opts Options) (*TableResult, error) {
	var tables [2]*preview.Table
	for i, s := range sides {
		tables[i] = &preview.Table{}
		if !s.present {
			continue
		}
		t, err := preview.ParseTable(s.ref.Path, s.data)
		if err != nil {
			return nil, err
		}
		tables[i] = t
	}
	d := preview.DiffTables(tables[0], tables[1])
	out := &TableResult{
		Columns: make([]TableColumn, len(d.Columns)), Rows: []TableRow{},
		RowsAdded: d.RowsAdded, RowsRemoved: d.RowsRemoved, RowsChanged: d.RowsChanged, RowsMoved: d.RowsMoved,
		CellsChanged: d.CellsChanged, ColumnsAdded: d.ColumnsAdded, ColumnsRemoved: d.ColumnsRemoved,
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
