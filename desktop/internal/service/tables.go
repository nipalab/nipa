package service

import (
	"context"
	"errors"
	"strings"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/preview"
)

const maxTableBytes = 16 << 20

type TableColumn struct {
	Name    string `json:"name"`
	OldName string `json:"oldName"`
	Old     int    `json:"old"` // -1 when the column is new
	New     int    `json:"new"` // -1 when the column was removed
}

type TableRow struct {
	Status  string   `json:"status"` // same, added, removed, changed
	OldLine int      `json:"oldLine"`
	NewLine int      `json:"newLine"`
	Cells   []string `json:"cells"`
	Old     []string `json:"old"`     // changed rows only
	Changed []int    `json:"changed"` // changed rows only
	Moved   bool     `json:"moved,omitempty"`
}

// TableCompare is a cell-level diff of a CSV/TSV file. Error is set, and the
// rows are empty, when a side cannot be read as a table.
type TableCompare struct {
	Columns        []TableColumn `json:"columns"`
	Rows           []TableRow    `json:"rows"`
	KeyColumn      int           `json:"keyColumn"`
	Delimiter      string        `json:"delimiter"`
	RowsAdded      int           `json:"rowsAdded"`
	RowsRemoved    int           `json:"rowsRemoved"`
	RowsChanged    int           `json:"rowsChanged"`
	RowsMoved      int           `json:"rowsMoved"`
	CellsChanged   int           `json:"cellsChanged"`
	ColumnsAdded   int           `json:"columnsAdded"`
	ColumnsRemoved int           `json:"columnsRemoved"`
	Error          string        `json:"error,omitempty"`
}

// TableDiff compares two versions of a CSV/TSV file cell by cell.
func (s *Service) TableDiff(ctx context.Context, root string, old, new *ImageRef) (*TableCompare, error) {
	if old == nil && new == nil {
		return nil, errors.New("nothing to compare")
	}
	client, err := s.client(ctx, root)
	if err != nil {
		return nil, err
	}
	var tables [2]*preview.Table
	for i, ref := range []*ImageRef{old, new} {
		if ref == nil {
			continue
		}
		t, msg, err := s.loadTable(ctx, client, root, ref)
		if err != nil {
			return nil, err
		}
		if msg != "" {
			return &TableCompare{Columns: []TableColumn{}, Rows: []TableRow{}, KeyColumn: -1, Error: msg}, nil
		}
		tables[i] = t
	}
	d := preview.DiffTables(tables[0], tables[1])
	out := &TableCompare{
		Columns: make([]TableColumn, len(d.Columns)), Rows: make([]TableRow, len(d.Rows)), KeyColumn: d.KeyColumn,
		RowsAdded: d.RowsAdded, RowsRemoved: d.RowsRemoved, RowsChanged: d.RowsChanged, RowsMoved: d.RowsMoved,
		CellsChanged: d.CellsChanged, ColumnsAdded: d.ColumnsAdded, ColumnsRemoved: d.ColumnsRemoved,
	}
	for _, t := range tables {
		if t != nil {
			out.Delimiter = string(t.Delimiter)
		}
	}
	for i, c := range d.Columns {
		out.Columns[i] = TableColumn{Name: c.Name, OldName: c.OldName, Old: c.Old, New: c.New}
	}
	for i, r := range d.Rows {
		out.Rows[i] = TableRow{Status: r.Status, OldLine: r.OldLine, NewLine: r.NewLine, Cells: r.Cells, Old: r.Old, Changed: r.Changed, Moved: r.Moved}
	}
	return out, nil
}

func (s *Service) loadTable(ctx context.Context, client daemonpb.NipaDaemonClient, root string, ref *ImageRef) (*preview.Table, string, error) {
	_, data, err := s.readBlob(ctx, client, root, blobRef(ref), maxTableBytes)
	if errors.Is(err, errBlobTooLarge) {
		return nil, "the file is too large to compare as a table", nil
	}
	if errors.Is(err, errBlobMissing) {
		return &preview.Table{}, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	t, err := preview.ParseTable(ref.Path, data)
	if err != nil {
		return nil, strings.TrimPrefix(err.Error(), "preview: "), nil
	}
	return t, "", nil
}
