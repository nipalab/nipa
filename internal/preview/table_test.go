package preview

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustTable(t *testing.T, name, text string) *Table {
	t.Helper()
	tb, err := ParseTable(name, []byte(text))
	require.NoError(t, err)
	return tb
}

func utf16Bytes(text string, bigEndian bool) []byte {
	out := []byte{0xff, 0xfe}
	if bigEndian {
		out = []byte{0xfe, 0xff}
	}
	for _, u := range utf16.Encode([]rune(text)) {
		if bigEndian {
			out = append(out, byte(u>>8), byte(u))
		} else {
			out = append(out, byte(u), byte(u>>8))
		}
	}
	return out
}

func TestParseTable(t *testing.T) {
	tb := mustTable(t, "a.csv", "\xef\xbb\xbfkey,text\nhello,\"Hi, there\"\nbye,\"say \"\"bye\"\"\nnow\"\n")
	assert.Equal(t, ',', tb.Delimiter)
	assert.Equal(t, [][]string{{"key", "text"}, {"hello", "Hi, there"}, {"bye", "say \"bye\"\nnow"}}, tb.Rows)

	tb = mustTable(t, "a.csv", "id;name;\"a,b\"\n1;x;2\n")
	assert.Equal(t, ';', tb.Delimiter)
	assert.Equal(t, []string{"id", "name", "a,b"}, tb.Rows[0])

	tb = mustTable(t, "a.csv", "id\tname\n1\tx")
	assert.Equal(t, '\t', tb.Delimiter)

	tb = mustTable(t, "a.TSV", "a,b\tc\n")
	assert.Equal(t, []string{"a,b", "c"}, tb.Rows[0])

	tb = mustTable(t, "ragged.csv", "a,b\n1\n1,2,3\n")
	assert.Equal(t, [][]string{{"a", "b"}, {"1"}, {"1", "2", "3"}}, tb.Rows)

	for _, be := range []bool{false, true} {
		tb, err := ParseTable("u.csv", utf16Bytes("k,v\nä,ß\n", be))
		require.NoError(t, err)
		assert.Equal(t, [][]string{{"k", "v"}, {"ä", "ß"}}, tb.Rows)
	}

	tb = mustTable(t, "e.csv", "")
	assert.Empty(t, tb.Rows)

	_, err := ParseTable("b.csv", []byte{0xff, 0x00, 0x80})
	assert.ErrorIs(t, err, errTable)
	_, err = ParseTable("q.csv", []byte("a,b\n\"x\"y\"z,1\n"))
	assert.NoError(t, err)

	old := MaxTableCells
	MaxTableCells = 3
	defer func() { MaxTableCells = old }()
	_, err = ParseTable("big.csv", []byte("a,b\n1,2\n"))
	assert.ErrorIs(t, err, errTableTooLarge)
}

func TestDiffTablesByKey(t *testing.T) {
	old := mustTable(t, "loc.csv", "key,en,fr\nhello,Hello,Bonjour\nbye,Bye,Au revoir\nyes,Yes,Oui\nno,No,Non\n")
	next := mustTable(t, "loc.csv", "key,en,de,fr\nhello,Hello,Hallo,Salut\nyes,Yes,Ja,Oui\nmaybe,Maybe,Vielleicht,Peut-être\nno,No,Nein,Non\n")
	d := DiffTables(old, next)

	require.Len(t, d.Columns, 4)
	assert.Equal(t, TableColumn{Name: "de", Old: -1, New: 2}, d.Columns[2])
	assert.Equal(t, TableColumn{Name: "fr", OldName: "fr", Old: 2, New: 3}, d.Columns[3])
	assert.Equal(t, 0, d.KeyColumn)
	assert.Equal(t, 1, d.ColumnsAdded)
	assert.Zero(t, d.ColumnsRemoved)

	status := make([]string, len(d.Rows))
	for i, r := range d.Rows {
		status[i] = r.Status + ":" + r.Cells[0]
	}
	assert.Equal(t, []string{"changed:hello", "removed:bye", "same:yes", "added:maybe", "same:no"}, status)
	hello := d.Rows[0]
	assert.Equal(t, []int{3}, hello.Changed)
	assert.Equal(t, "Bonjour", hello.Old[3])
	assert.Equal(t, "Salut", hello.Cells[3])
	assert.Equal(t, "Hallo", hello.Cells[2])
	assert.Equal(t, 2, hello.OldLine)
	assert.Equal(t, 2, hello.NewLine)
	assert.Equal(t, "Au revoir", d.Rows[1].Cells[3])
	assert.Equal(t, 0, d.Rows[1].NewLine)
	assert.Equal(t, 1, d.RowsAdded)
	assert.Equal(t, 1, d.RowsRemoved)
	assert.Equal(t, 1, d.RowsChanged)
	assert.Equal(t, 1, d.CellsChanged)
}

func TestDiffTablesMovedRowsAndColumns(t *testing.T) {
	old := mustTable(t, "t.csv", "id,hp,dmg\n1,10,1\n2,20,2\n3,30,3\n4,40,4\n")
	next := mustTable(t, "t.csv", "id,dmg,hp\n1,1,10\n3,3,30\n4,4,45\n2,2,20\n")
	d := DiffTables(old, next)
	names := []string{}
	for _, c := range d.Columns {
		names = append(names, c.Name)
	}
	assert.Equal(t, []string{"id", "dmg", "hp"}, names)
	assert.Zero(t, d.ColumnsAdded+d.ColumnsRemoved)
	var got []string
	for _, r := range d.Rows {
		s := r.Status + ":" + r.Cells[0]
		if r.Moved {
			s += "*"
		}
		got = append(got, s)
	}
	assert.Equal(t, []string{"same:1", "same:3", "changed:4", "same:2*"}, got)
	assert.Equal(t, 1, d.RowsMoved)
	assert.Equal(t, []int{2}, d.Rows[2].Changed)
}

func TestDiffTablesByContent(t *testing.T) {
	old := mustTable(t, "t.csv", "a,b,c\nx,1,1\nx,1,1\ny,2,2\nz,3,3\nw,9,9\n")
	next := mustTable(t, "t.csv", "a,b,c\nx,1,1\ny,2,5\nz,3,3\nq,7,8\nv,0,0\n")
	d := DiffTables(old, next)
	assert.Equal(t, -1, d.KeyColumn)
	var got []string
	for _, r := range d.Rows {
		got = append(got, r.Status+":"+strings.Join(r.Cells, ""))
	}
	assert.Equal(t, []string{"same:x11", "removed:x11", "changed:y25", "same:z33", "removed:w99", "added:q78", "added:v00"}, got)
}

func TestDiffTablesByIndexAndSmallTables(t *testing.T) {
	old := mustTable(t, "t.csv", "a,a\n1,2\n3,4\n")
	next := mustTable(t, "t.csv", "a,a,\n1,5,x\n6,7,y\n")
	d := DiffTables(old, next)
	require.Len(t, d.Columns, 3)
	assert.Equal(t, TableColumn{Name: "", Old: -1, New: 2}, d.Columns[2])
	assert.Equal(t, 0, d.KeyColumn)
	assert.Equal(t, "changed", d.Rows[0].Status)

	d = DiffTables(mustTable(t, "l.csv", "item\nsword\nshield\n"), mustTable(t, "l.csv", "item\nsword\nbow\nbow\n"))
	var got []string
	for _, r := range d.Rows {
		got = append(got, r.Status+":"+r.Cells[0])
	}
	assert.Equal(t, []string{"same:sword", "changed:bow", "added:bow"}, got)

	d = DiffTables(nil, mustTable(t, "n.csv", "a,b\n1,2\n"))
	assert.Equal(t, 2, d.ColumnsAdded)
	require.Len(t, d.Rows, 1)
	assert.Equal(t, "added", d.Rows[0].Status)

	d = DiffTables(mustTable(t, "n.csv", "a,b\n1,2\n"), &Table{})
	assert.Equal(t, 2, d.ColumnsRemoved)
	assert.Equal(t, "removed", d.Rows[0].Status)
	assert.Equal(t, []string{"1", "2"}, d.Rows[0].Cells)

	d = DiffTables(mustTable(t, "w.csv", "a,b\n1,2,3\n"), mustTable(t, "w.csv", "a,b\n1,2,4\n"))
	assert.Len(t, d.Columns, 3)
	assert.Equal(t, []int{2}, d.Rows[0].Changed)
}

func TestLongestIncreasing(t *testing.T) {
	js := []int{0, 1, 2, 3, 4, 5}
	value := []int{3, 1, 4, 0, 5, 2}
	got := longestIncreasing(js, value)
	assert.Len(t, got, 3)
	for i := 1; i < len(got); i++ {
		assert.Less(t, value[got[i-1]], value[got[i]])
	}
	assert.Empty(t, longestIncreasing(nil, nil))
}

func TestDiffTablesLargeUnmatchedBlockIsBounded(t *testing.T) {
	const n = 20000
	oldT := &Table{Rows: [][]string{{"x", "key", "a", "b", "c"}}}
	newT := &Table{Rows: [][]string{{"x", "key", "a", "b", "c"}}}
	for i := 0; i < n; i++ {
		oldT.Rows = append(oldT.Rows, []string{"o", "k", fmt.Sprint("a", i), fmt.Sprint("b", i), fmt.Sprint("c", i)})
		newT.Rows = append(newT.Rows, []string{"n", "k", fmt.Sprint("p", i), fmt.Sprint("q", i), fmt.Sprint("r", i)})
	}
	start := time.Now()
	d := DiffTables(oldT, newT)
	require.Less(t, time.Since(start), 60*time.Second)
	require.Equal(t, n, d.RowsAdded)
	require.Equal(t, n, d.RowsRemoved)
}

func TestDiffTablesDisjointHeadersCompareByPosition(t *testing.T) {
	oldT := &Table{Rows: [][]string{{"a", "b"}, {"1", "2"}, {"3", "4"}}}
	newT := &Table{Rows: [][]string{{"x", "y"}, {"1", "9"}, {"3", "4"}}}
	d := DiffTables(oldT, newT)
	require.Len(t, d.Rows, 2)
	assert.Equal(t, 1, d.RowsChanged)
	assert.Equal(t, "changed", d.Rows[0].Status)
	assert.Equal(t, []int{1}, d.Rows[0].Changed)
	assert.Equal(t, "same", d.Rows[1].Status)
}
