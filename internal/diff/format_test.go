package diff

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
)

func TestNameOnlyAndStatus(t *testing.T) {
	files := []FileDiff{
		{Change: Change{Path: "a.txt", Status: Added}},
		{Change: Change{Path: "b.txt", Status: Modified}},
		{Change: Change{Path: "c.txt", Status: Deleted}},
	}
	require.Equal(t, []string{"a.txt", "b.txt", "c.txt"}, NameOnly(files, Options{}))
	require.Equal(t, []string{"A\ta.txt", "M\tb.txt", "D\tc.txt"}, NameStatus(files, Options{}))
}

func TestNumStat(t *testing.T) {
	files := []FileDiff{
		modified("one\ntwo\n", "one\nTWO\n"),
		{Change: Change{Path: "img.bin", Status: Modified, Old: Entry{IsBinary: true}, New: Entry{IsBinary: true}}},
	}
	require.Equal(t, []string{"1\t1\ta.txt", "-\t-\timg.bin"}, NumStat(files, Options{}))
}

func TestStat(t *testing.T) {
	files := []FileDiff{modified("one\ntwo\n", "one\nTWO\n")}
	got := Stat(files, Options{})
	require.Equal(t, []string{
		" a.txt | 2 +-",
		" 1 file changed, 1 insertion(+), 1 deletion(-)",
	}, got)
}

func TestShortStat(t *testing.T) {
	files := []FileDiff{
		modified("one\ntwo\n", "one\nTWO\n"),
		modified("x\n", "y\n"),
	}
	require.Equal(t, []string{" 2 files changed, 2 insertions(+), 2 deletions(-)"}, ShortStat(files, Options{}))
}

func TestSummary(t *testing.T) {
	f := modified("same\n", "same\n")
	f.Change.New.Mode = 3
	require.Equal(t, []string{" mode change 100644 => 100755 a.txt"}, Summary([]FileDiff{f}, Options{}))
}

func TestRaw(t *testing.T) {
	oldHash := chunker.Sum([]byte("old"))
	newHash := chunker.Sum([]byte("new"))
	f := FileDiff{Change: Change{
		Path:   "a.txt",
		Status: Modified,
		Old:    Entry{Hash: oldHash, Mode: 2},
		New:    Entry{Hash: newHash, Mode: 3},
	}}
	require.Equal(t, []string{
		":100644 100755 " + oldHash.String() + " " + newHash.String() + " M\ta.txt",
	}, Raw([]FileDiff{f}, Options{}))

	added := FileDiff{Change: Change{Path: "n.txt", Status: Added, New: Entry{Hash: newHash, Mode: 2}}}
	require.Equal(t, []string{
		":000000 100644 0000000000000000000000000000000000000000000000000000000000000000 " + newHash.String() + " A\tn.txt",
	}, Raw([]FileDiff{added}, Options{}))
}
