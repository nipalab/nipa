package service

import (
	"context"
	"errors"
	"strings"

	"github.com/nipalab/nipa/internal/assetdiff"
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

// TableSheet is one workbook sheet: Status is same, added, removed or
// changed.
type TableSheet struct {
	Name         string `json:"name"`
	Status       string `json:"status"`
	RowsAdded    int    `json:"rowsAdded"`
	RowsRemoved  int    `json:"rowsRemoved"`
	RowsChanged  int    `json:"rowsChanged"`
	CellsChanged int    `json:"cellsChanged"`
}

// TableCompare is a cell-level diff of a CSV/TSV file or of one sheet of an
// .xlsx workbook (Sheet, with every sheet listed in Sheets). Error is set,
// and the rows are empty, when a side cannot be read as a table.
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
	Sheet          string        `json:"sheet,omitempty"`
	Sheets         []TableSheet  `json:"sheets"`
	Error          string        `json:"error,omitempty"`
}

// TableDiff compares two versions of a CSV/TSV file cell by cell. For an
// .xlsx workbook it compares the named sheet, or the first changed one when
// sheet is empty or no longer exists.
func (s *Service) TableDiff(ctx context.Context, root string, old, new *ImageRef, sheet string) (*TableCompare, error) {
	if old == nil && new == nil {
		return nil, errors.New("nothing to compare")
	}
	client, err := s.client(ctx, root)
	if err != nil {
		return nil, err
	}
	var sides [2][]preview.Sheet
	workbook := false
	for i, ref := range []*ImageRef{old, new} {
		if ref == nil {
			continue
		}
		workbook = workbook || preview.IsWorkbook(ref.Path)
		sheets, msg, err := s.loadTable(ctx, client, root, ref)
		if err != nil {
			return nil, err
		}
		if msg != "" {
			return &TableCompare{Columns: []TableColumn{}, Rows: []TableRow{}, Sheets: []TableSheet{}, KeyColumn: -1, Error: msg}, nil
		}
		sides[i] = sheets
	}
	diffs := preview.DiffSheets(sides[0], sides[1])
	pick := assetdiff.PickSheet(diffs, sheet)
	if pick < 0 {
		pick = assetdiff.PickSheet(diffs, "")
	}
	d := preview.DiffTables(nil, nil)
	if pick >= 0 {
		d = diffs[pick].Diff
	}
	out := &TableCompare{
		Columns: make([]TableColumn, len(d.Columns)), Rows: make([]TableRow, len(d.Rows)), KeyColumn: d.KeyColumn, Sheets: []TableSheet{},
		RowsAdded: d.RowsAdded, RowsRemoved: d.RowsRemoved, RowsChanged: d.RowsChanged, RowsMoved: d.RowsMoved,
		CellsChanged: d.CellsChanged, ColumnsAdded: d.ColumnsAdded, ColumnsRemoved: d.ColumnsRemoved,
	}
	for _, side := range sides {
		if len(side) == 1 && side[0].Table.Delimiter != 0 {
			out.Delimiter = string(side[0].Table.Delimiter)
		}
	}
	if workbook {
		for _, sd := range diffs {
			out.Sheets = append(out.Sheets, TableSheet{Name: sd.Name, Status: sd.Status, RowsAdded: sd.Diff.RowsAdded, RowsRemoved: sd.Diff.RowsRemoved, RowsChanged: sd.Diff.RowsChanged, CellsChanged: sd.Diff.CellsChanged})
		}
		if pick >= 0 {
			out.Sheet = diffs[pick].Name
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

func (s *Service) loadTable(ctx context.Context, client daemonpb.NipaDaemonClient, root string, ref *ImageRef) ([]preview.Sheet, string, error) {
	_, data, err := s.readBlob(ctx, client, root, blobRef(ref), maxTableBytes)
	if errors.Is(err, errBlobTooLarge) {
		return nil, "the file is too large to compare as a table", nil
	}
	if errors.Is(err, errBlobMissing) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	sheets, err := preview.ParseSheets(ref.Path, data)
	if err != nil {
		return nil, strings.TrimPrefix(err.Error(), "preview: "), nil
	}
	return sheets, "", nil
}
