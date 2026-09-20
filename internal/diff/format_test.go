package diff

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNameOnlyAndStatus(t *testing.T) {
	files := []FileDiff{
		{Change: Change{Path: "a.txt", Status: Added}},
		{Change: Change{Path: "b.txt", Status: Modified}},
		{Change: Change{Path: "c.txt", Status: Deleted}},
	}
	require.Equal(t, []string{"a.txt", "b.txt", "c.txt"}, NameOnly(files))
	require.Equal(t, []string{"A\ta.txt", "M\tb.txt", "D\tc.txt"}, NameStatus(files))
}

func TestNameStatus_Renamed(t *testing.T) {
	f := renamed("old.txt", "new.txt", "same\n", "same\n", 100)
	require.Equal(t, []string{"R100\told.txt\tnew.txt"}, NameStatus([]FileDiff{f}))
}

func TestStat(t *testing.T) {
	files := []FileDiff{modified("one\ntwo\n", "one\nTWO\n")}
	got := Stat(files)
	require.Equal(t, []string{
		" a.txt | 2 +-",
		" 1 file changed, 1 insertion(+), 1 deletion(-)",
	}, got)
}

func TestStat_RenamedUsesArrowPath(t *testing.T) {
	f := renamed("old.txt", "new.txt", "one\n", "one\ntwo\n", 80)
	got := Stat([]FileDiff{f})
	require.Equal(t, " old.txt => new.txt | 1 +", got[0])
}
