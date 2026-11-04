package preview

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// MaxTableCells bounds how many cells ParseTable reads from one file.
var MaxTableCells = 2_000_000

var (
	errTable         = errors.New("preview: invalid table")
	errTableTooLarge = errors.New("preview: table too large")
)

// Table is a parsed CSV or TSV file. The first row is treated as the header.
type Table struct {
	Rows      [][]string
	Delimiter rune
}

// ParseTable reads CSV (comma, semicolon or tab separated, sniffed from the
// first line) or TSV, in UTF-8 or UTF-16 with a byte order mark.
func ParseTable(name string, data []byte) (*Table, error) {
	text, err := tableText(data)
	if err != nil {
		return nil, err
	}
	delim := sniffDelimiter(text)
	if strings.EqualFold(filepath.Ext(name), ".tsv") {
		delim = '\t'
	}
	r := csv.NewReader(strings.NewReader(text))
	r.Comma = delim
	r.LazyQuotes = true
	r.FieldsPerRecord = -1
	t := &Table{Delimiter: delim, Rows: [][]string{}}
	cells := 0
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			return t, nil
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %v", errTable, err)
		}
		cells += len(rec)
		if cells > MaxTableCells {
			return nil, fmt.Errorf("%w: more than %d cells", errTableTooLarge, MaxTableCells)
		}
		t.Rows = append(t.Rows, rec)
	}
}

func tableText(data []byte) (string, error) {
	switch {
	case bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}):
		data = data[3:]
	case bytes.HasPrefix(data, []byte{0xff, 0xfe}), bytes.HasPrefix(data, []byte{0xfe, 0xff}):
		units := make([]uint16, (len(data)-2)/2)
		for i := range units {
			lo, hi := data[2+2*i], data[3+2*i]
			if data[0] == 0xfe {
				lo, hi = hi, lo
			}
			units[i] = uint16(hi)<<8 | uint16(lo)
		}
		return string(utf16.Decode(units)), nil
	}
	if !utf8.Valid(data) {
		return "", fmt.Errorf("%w: not UTF-8 text", errTable)
	}
	return string(data), nil
}

func sniffDelimiter(text string) rune {
	counts := map[rune]int{}
	quoted := false
	for _, c := range text {
		if c == '"' {
			quoted = !quoted
		}
		if !quoted && (c == '\n' || c == '\r') {
			break
		}
		if !quoted && (c == ',' || c == ';' || c == '\t') {
			counts[c]++
		}
	}
	best := ','
	for _, c := range []rune{';', '\t'} {
		if counts[c] > counts[best] {
			best = c
		}
	}
	return best
}

type TableColumn struct {
	Name    string
	OldName string
	Old     int // -1 when the column is new
	New     int // -1 when the column was removed
}

type TableRow struct {
	Status  string   // same, added, removed, changed
	OldLine int      // 1-based record number in the old file, 0 when absent
	NewLine int      // 1-based record number in the new file, 0 when absent
	Cells   []string // one per column: the new value, or the old one where only that exists
	Old     []string // changed rows only: the old value per column
	Changed []int    // changed rows only: columns whose value differs
	Moved   bool
}

type TableDiff struct {
	Columns        []TableColumn
	Rows           []TableRow
	KeyColumn      int // column rows are matched by, -1 when matched by content
	RowsAdded      int
	RowsRemoved    int
	RowsChanged    int
	RowsMoved      int
	CellsChanged   int
	ColumnsAdded   int
	ColumnsRemoved int
}

type pair struct {
	old, new int
	moved    bool
}

// alignUnique orders the union of a and b for display. Keys must be unique on
// each side; matches outside the longest common subsequence are moves.
func alignUnique(a, b []string) []pair {
	at := make(map[string]int, len(a))
	for i, k := range a {
		at[k] = i
	}
	matchOf := make([]int, len(b))
	var seq []int
	for j, k := range b {
		matchOf[j] = -1
		if i, ok := at[k]; ok {
			matchOf[j] = i
			seq = append(seq, j)
		}
	}
	stable := map[int]bool{}
	for _, j := range longestIncreasing(seq, matchOf) {
		stable[j] = true
	}
	matchedOld := make([]bool, len(a))
	for _, i := range matchOf {
		if i >= 0 {
			matchedOld[i] = true
		}
	}
	var out []pair
	i := 0
	flush := func(until int) {
		for ; i < until; i++ {
			if !matchedOld[i] {
				out = append(out, pair{old: i, new: -1})
			}
		}
	}
	for j := range b {
		switch {
		case stable[j]:
			flush(matchOf[j])
			out = append(out, pair{old: matchOf[j], new: j})
			i = matchOf[j] + 1
		case matchOf[j] >= 0:
			out = append(out, pair{old: matchOf[j], new: j, moved: true})
		default:
			out = append(out, pair{old: -1, new: j})
		}
	}
	flush(len(a))
	return out
}

func longestIncreasing(js []int, value []int) []int {
	var tails []int
	prev := make([]int, len(js))
	for n, j := range js {
		v := value[j]
		k := sort.Search(len(tails), func(x int) bool { return value[js[tails[x]]] >= v })
		if k > 0 {
			prev[n] = tails[k-1]
		} else {
			prev[n] = -1
		}
		if k == len(tails) {
			tails = append(tails, n)
		} else {
			tails[k] = n
		}
	}
	out := make([]int, len(tails))
	for n, k := len(tails)-1, -1; n >= 0; n-- {
		if k == -1 {
			k = tails[len(tails)-1]
		} else {
			k = prev[k]
		}
		out[n] = js[k]
	}
	return out
}

func uniqueNonEmpty(values []string) bool {
	seen := make(map[string]bool, len(values))
	for _, v := range values {
		if v == "" || seen[v] {
			return false
		}
		seen[v] = true
	}
	return true
}

func header(t *Table) ([]string, [][]string) {
	if t == nil || len(t.Rows) == 0 {
		return nil, nil
	}
	return t.Rows[0], t.Rows[1:]
}

func widest(head []string, rows [][]string) int {
	w := len(head)
	for _, r := range rows {
		w = max(w, len(r))
	}
	return w
}

func cell(row []string, i int) string {
	if i < 0 || i >= len(row) {
		return ""
	}
	return row[i]
}

func alignColumns(oh, nh []string, oldRows, newRows [][]string) []TableColumn {
	ow, nw := widest(oh, oldRows), widest(nh, newRows)
	var cols []TableColumn
	if uniqueNonEmpty(oh) && uniqueNonEmpty(nh) && ow == len(oh) && nw == len(nh) {
		for _, p := range alignUnique(oh, nh) {
			cols = append(cols, TableColumn{Name: cell(nh, p.new), OldName: cell(oh, p.old), Old: p.old, New: p.new})
		}
		for i := range cols {
			if cols[i].Name == "" {
				cols[i].Name = cols[i].OldName
			}
		}
		return cols
	}
	for i := 0; i < max(ow, nw); i++ {
		c := TableColumn{Name: cell(nh, i), OldName: cell(oh, i), Old: -1, New: -1}
		if i < ow {
			c.Old = i
		}
		if i < nw {
			c.New = i
		}
		if c.Name == "" {
			c.Name = c.OldName
		}
		cols = append(cols, c)
	}
	return cols
}

// DiffTables compares two tables cell by cell. Columns are matched by header
// name when the headers are unique, rows by the first shared column when its
// values are unique on both sides, otherwise by content.
func DiffTables(old, new *Table) *TableDiff {
	oh, oldRows := header(old)
	nh, newRows := header(new)
	d := &TableDiff{Columns: alignColumns(oh, nh, oldRows, newRows), KeyColumn: -1, Rows: []TableRow{}}
	var common []int
	for i, c := range d.Columns {
		switch {
		case c.Old >= 0 && c.New >= 0:
			common = append(common, i)
		case c.Old >= 0:
			d.ColumnsRemoved++
		default:
			d.ColumnsAdded++
		}
	}
	project := func(rows [][]string, side func(TableColumn) int, cols []int) []string {
		out := make([]string, len(rows))
		for r, row := range rows {
			parts := make([]string, len(cols))
			for n, c := range cols {
				parts[n] = cell(row, side(d.Columns[c]))
			}
			out[r] = strings.Join(parts, "\x1f")
		}
		return out
	}
	oldSide := func(c TableColumn) int { return c.Old }
	newSide := func(c TableColumn) int { return c.New }
	byKey := false
	var oldKeys, newKeys []string
	if len(common) > 0 {
		oldKeys = project(oldRows, oldSide, common[:1])
		newKeys = project(newRows, newSide, common[:1])
		byKey = uniqueNonEmpty(oldKeys) && uniqueNonEmpty(newKeys)
	}
	if byKey {
		d.KeyColumn = common[0]
	} else {
		oldKeys = occurrences(project(oldRows, oldSide, common))
		newKeys = occurrences(project(newRows, newSide, common))
	}
	pairs := alignUnique(oldKeys, newKeys)
	if !byKey {
		pairs = pairChanged(pairs, oldRows, newRows, d.Columns, common)
	}
	for _, p := range pairs {
		d.Rows = append(d.Rows, d.row(p, oldRows, newRows))
	}
	return d
}

func occurrences(keys []string) []string {
	seen := map[string]int{}
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = fmt.Sprintf("%s\x00%d", k, seen[k])
		seen[k]++
	}
	return out
}

func similar(a, b []string, cols []TableColumn, common []int) bool {
	if len(common) <= 2 {
		return true
	}
	same := 0
	for _, c := range common {
		if cell(a, cols[c].Old) == cell(b, cols[c].New) {
			same++
		}
	}
	return 2*same >= len(common)
}

func pairChanged(pairs []pair, oldRows, newRows [][]string, cols []TableColumn, common []int) []pair {
	var out []pair
	for start := 0; start < len(pairs); {
		if pairs[start].old >= 0 && pairs[start].new >= 0 {
			out = append(out, pairs[start])
			start++
			continue
		}
		end := start
		var removed, added []int
		for ; end < len(pairs) && (pairs[end].old < 0 || pairs[end].new < 0); end++ {
			if pairs[end].new < 0 {
				removed = append(removed, pairs[end].old)
			} else {
				added = append(added, pairs[end].new)
			}
		}
		k := 0
		var rest []pair
		for _, j := range added {
			match := -1
			for x := k; x < len(removed) && match < 0; x++ {
				if similar(oldRows[removed[x]], newRows[j], cols, common) {
					match = x
				}
			}
			if match < 0 {
				rest = append(rest, pair{old: -1, new: j})
				continue
			}
			for ; k < match; k++ {
				out = append(out, pair{old: removed[k], new: -1})
			}
			out = append(out, pair{old: removed[match], new: j})
			k = match + 1
		}
		for _, i := range removed[k:] {
			out = append(out, pair{old: i, new: -1})
		}
		out = append(out, rest...)
		start = end
	}
	return out
}

func (d *TableDiff) row(p pair, oldRows, newRows [][]string) TableRow {
	r := TableRow{Moved: p.moved, Cells: make([]string, len(d.Columns))}
	var o, n []string
	if p.old >= 0 {
		o, r.OldLine = oldRows[p.old], p.old+2
	}
	if p.new >= 0 {
		n, r.NewLine = newRows[p.new], p.new+2
	}
	for i, c := range d.Columns {
		if p.new >= 0 && c.New >= 0 {
			r.Cells[i] = cell(n, c.New)
		} else {
			r.Cells[i] = cell(o, c.Old)
		}
		if p.old >= 0 && p.new >= 0 && c.Old >= 0 && c.New >= 0 && cell(o, c.Old) != cell(n, c.New) {
			r.Changed = append(r.Changed, i)
		}
	}
	switch {
	case p.old < 0:
		r.Status = "added"
		d.RowsAdded++
	case p.new < 0:
		r.Status = "removed"
		d.RowsRemoved++
	case len(r.Changed) > 0:
		r.Status = "changed"
		d.RowsChanged++
		d.CellsChanged += len(r.Changed)
		r.Old = make([]string, len(d.Columns))
		for i, c := range d.Columns {
			r.Old[i] = cell(o, c.Old)
		}
	default:
		r.Status = "same"
	}
	if r.Moved {
		d.RowsMoved++
	}
	return r
}
