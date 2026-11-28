package diff

import (
	"math/rand"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHunksBudget_ExactWithinBudget(t *testing.T) {
	old := []byte("one\ntwo\nthree\n")
	new := []byte("one\nTWO\nthree\nfour\n")
	res := HunksBudget(old, new, Options{Context: 3}, DefaultBudget)
	require.False(t, res.Omitted)
	require.False(t, res.Approx)
	require.Equal(t, 2, res.Additions)
	require.Equal(t, 1, res.Deletions)
	require.Equal(t, Hunks(old, new, Options{Context: 3}), res.Hunks)

	res = HunksBudget(old, old, Options{Context: 3}, DefaultBudget)
	require.Equal(t, HunkResult{}, res)
}

func TestHunksBudget_OverBytes(t *testing.T) {
	old := []byte("head\nold body\ntail\n")
	new := []byte("head\nnew body\nmore\ntail\n")
	res := HunksBudget(old, new, Options{Context: 3}, Budget{MaxBytes: 10})
	require.Equal(t, HunkResult{Additions: 2, Deletions: 1, Omitted: true, Approx: true}, res)
}

func TestHunksBudget_OverLines(t *testing.T) {
	old := []byte("a\nb\nc")
	new := []byte("a\nx\ny\nc")
	res := HunksBudget(old, new, Options{}, Budget{MaxLines: 3})
	require.Equal(t, HunkResult{Additions: 2, Deletions: 1, Omitted: true, Approx: true}, res)
	res = HunksBudget(old, new, Options{}, Budget{MaxLines: 4})
	require.False(t, res.Omitted)
	require.Equal(t, 2, res.Additions)
	require.Equal(t, 1, res.Deletions)
}

func TestHunksBudget_OverCost(t *testing.T) {
	// Moving the first line to the end costs 2 edits; the trimmed estimate
	// overcounts it as the whole file on both sides.
	old := []byte("1\n2\n3\n4\n")
	new := []byte("2\n3\n4\n1\n")
	res := HunksBudget(old, new, Options{}, Budget{MaxCost: 1})
	require.Equal(t, HunkResult{Additions: 4, Deletions: 4, Omitted: true, Approx: true}, res)
	res = HunksBudget(old, new, Options{}, Budget{MaxCost: 2})
	require.False(t, res.Omitted)
	require.Equal(t, 1, res.Additions)
	require.Equal(t, 1, res.Deletions)
}

func TestHunksBudget_CostCapSkipsPureInsertOrDelete(t *testing.T) {
	block := strings.Repeat("x\n", 50)
	for _, tc := range []struct{ old, new string }{
		{"", block},
		{block, ""},
		{"head\ntail\n", "head\n" + block + "tail\n"},
		{"head\n" + block + "tail\n", "head\ntail\n"},
	} {
		res := HunksBudget([]byte(tc.old), []byte(tc.new), Options{}, Budget{MaxCost: 10})
		require.False(t, res.Omitted)
		require.Equal(t, 50, res.Additions+res.Deletions)
	}
	// Two blocks need the search, so the cap applies.
	res := HunksBudget([]byte("a\nb\nc\n"), []byte("a\n"+block+"b\n"+block+"c\n"), Options{}, Budget{MaxCost: 10})
	require.True(t, res.Omitted)
	require.Equal(t, 101, res.Additions)
}

func TestHunksBudget_WhitespaceOptionsApplyToEstimate(t *testing.T) {
	old := []byte("a  b\nold\nc d\n")
	new := []byte("a b\nnew\nc\td\n")
	res := HunksBudget(old, new, Options{IgnoreSpaceChange: true}, Budget{MaxBytes: 1})
	require.Equal(t, HunkResult{Additions: 1, Deletions: 1, Omitted: true, Approx: true}, res)
}

func TestFileHunksBudget(t *testing.T) {
	res := FileHunksBudget(modified("one\n", "two\n"), Options{Context: 3}, DefaultBudget)
	require.Len(t, res.Hunks, 1)
	require.Equal(t, 1, res.Additions)
	require.Equal(t, 1, res.Deletions)

	require.Equal(t, HunkResult{}, FileHunksBudget(binaryModified(), Options{}, DefaultBudget))
	modeOnly := modified("same\n", "same\n")
	modeOnly.Change.New.Mode = 3
	require.Equal(t, HunkResult{}, FileHunksBudget(modeOnly, Options{}, DefaultBudget))
	require.Equal(t, HunkResult{}, FileHunksBudget(renamed("a.txt", "b.txt", "same\n", "same\n", 100), Options{}, DefaultBudget))

	res = FileHunksBudget(addedFile("big.txt", "x\ny\nz\n"), Options{}, Budget{MaxLines: 2})
	require.Equal(t, HunkResult{Additions: 3, Omitted: true, Approx: true}, res)
}

func TestHunksBudget_DefaultOmitsHugeFile(t *testing.T) {
	a, b := benchLines(DefaultBudget.MaxLines+1, 10)
	res := HunksBudget([]byte(strings.Join(a, "\n")), []byte(strings.Join(b, "\n")), Options{}, DefaultBudget)
	require.True(t, res.Omitted)
	require.True(t, res.Approx)
	require.Nil(t, res.Hunks)
	require.Greater(t, res.Additions, 0)
	require.Equal(t, res.Additions, res.Deletions)
}

func TestTrimmedLineCounts_MatchesSplitLines(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	pieces := []string{"a", "b", "\n", " ", "c\n"}
	gen := func() []byte {
		var sb strings.Builder
		for n := rng.Intn(12); n > 0; n-- {
			sb.WriteString(pieces[rng.Intn(len(pieces))])
		}
		return []byte(sb.String())
	}
	for _, opts := range []Options{{}, {IgnoreAllSpace: true}} {
		eq := opts.equalFunc()
		equal := eq
		if equal == nil {
			equal = func(x, y string) bool { return x == y }
		}
		for i := 0; i < 2000; i++ {
			old, new := gen(), gen()
			oldLines, newLines := splitLines(old), splitLines(new)
			require.Equal(t, len(oldLines), countLines(old))
			pre, suf := commonAffixes(oldLines, newLines, equal)
			gotOld, gotNew := trimmedLineCounts(old, new, eq)
			require.Equal(t, len(oldLines)-pre-suf, gotOld, "old=%q new=%q", old, new)
			require.Equal(t, len(newLines)-pre-suf, gotNew, "old=%q new=%q", old, new)
		}
	}
}

func TestFilePatchHunks_MatchesFilePatch(t *testing.T) {
	opts := Options{Context: 3}
	for _, f := range []FileDiff{
		modified("one\ntwo\nthree\n", "one\nTWO\nthree\nfour\n"),
		addedFile("new.txt", "a\nb\n"),
		renamed("a.txt", "b.txt", "same\n", "same\n", 100),
		binaryModified(),
	} {
		require.Equal(t, FilePatch(f, opts), FilePatchHunks(f, FileHunksBudget(f, opts, DefaultBudget).Hunks))
	}
}

func TestSameLines_AgreesWithDiff(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	words := []string{"a b", "a  b", "ab", "c", " c"}
	gen := func() []string {
		out := make([]string, rng.Intn(5))
		for i := range out {
			out[i] = words[rng.Intn(len(words))]
		}
		return out
	}
	for _, opts := range []Options{{IgnoreAllSpace: true}, {IgnoreSpaceChange: true}} {
		eq := opts.equalFunc()
		for i := 0; i < 2000; i++ {
			a, b := gen(), gen()
			require.Equal(t, editCount(LinesWith(a, b, eq)) == 0, sameLines(a, b, eq), "a=%q b=%q", a, b)
		}
	}
}
