package diff

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
)

func addedFile(path, content string) FileDiff {
	return FileDiff{
		Change: Change{
			Path:   path,
			Status: Added,
			New:    Entry{Path: path, Mode: 2, Hash: chunker.Sum([]byte(content))},
		},
		New: []byte(content),
	}
}

func binaryModified() FileDiff {
	f := modified("one\n", "two\n")
	f.Change.Old.IsBinary = true
	f.Change.New.IsBinary = true
	return f
}

func TestHunks_ModifiedResolvesLineNumbers(t *testing.T) {
	hunks := Hunks([]byte("one\ntwo\nthree\n"), []byte("one\nTWO\nthree\nfour\n"), Options{Context: 3})
	require.Len(t, hunks, 1)

	hunk := hunks[0]
	require.Equal(t, 1, hunk.OldStart)
	require.Equal(t, 3, hunk.OldLines)
	require.Equal(t, 1, hunk.NewStart)
	require.Equal(t, 4, hunk.NewLines)
	require.Equal(t, []HunkLine{
		{Kind: ' ', Old: 1, New: 1, Text: "one\n"},
		{Kind: '-', Old: 2, New: 0, Text: "two\n"},
		{Kind: '+', Old: 0, New: 2, Text: "TWO\n"},
		{Kind: ' ', Old: 3, New: 3, Text: "three\n"},
		{Kind: '+', Old: 0, New: 4, Text: "four\n"},
	}, hunk.Lines)
}

func TestHunks_RenderMatchesPatchSection(t *testing.T) {
	f := modified("one\ntwo\nthree\nfour\nfive\n", "one\ntwo\nTHREE\nfour\nFIVE\n")
	patch := FilePatch(f, Options{Context: 1})
	// the patch is "diff --nipa", "---", "+++" and then the hunk sections
	require.Equal(t, []string{"diff --nipa a/a.txt b/a.txt", "--- a/a.txt", "+++ b/a.txt"}, patch[:3])
	require.Equal(t, renderHunks(Hunks(f.Old, f.New, Options{Context: 1})), patch[3:])
}

func TestHunks_TwoHunks(t *testing.T) {
	hunks := Hunks(
		[]byte("one\ntwo\nthree\nfour\nfive\nsix\nseven\n"),
		[]byte("one\nTWO\nthree\nfour\nfive\nsix\nSEVEN\n"),
		Options{Context: 1},
	)
	require.Len(t, hunks, 2)
	require.Equal(t, "@@ -1,3 +1,3 @@", hunks[0].Header())
	require.Equal(t, "@@ -6,2 +6,2 @@", hunks[1].Header())
}

func TestHunks_AddedFileHasNoOldLines(t *testing.T) {
	hunks := Hunks(nil, []byte("one\ntwo\n"), Options{Context: 3})
	require.Len(t, hunks, 1)
	require.Equal(t, 0, hunks[0].OldStart)
	require.Equal(t, 0, hunks[0].OldLines)
	require.Equal(t, 1, hunks[0].NewStart)
	require.Equal(t, 2, hunks[0].NewLines)
	for _, line := range hunks[0].Lines {
		require.Equal(t, byte('+'), line.Kind)
		require.Equal(t, 0, line.Old)
	}
}

func TestHunks_DeletedFileHasNoNewLines(t *testing.T) {
	hunks := Hunks([]byte("one\ntwo\n"), nil, Options{Context: 3})
	require.Len(t, hunks, 1)
	require.Equal(t, 1, hunks[0].OldStart)
	require.Equal(t, 2, hunks[0].OldLines)
	require.Equal(t, 0, hunks[0].NewStart)
	require.Equal(t, 0, hunks[0].NewLines)
	for _, line := range hunks[0].Lines {
		require.Equal(t, byte('-'), line.Kind)
		require.Equal(t, 0, line.New)
	}
}

func TestHunks_MissingTrailingNewline(t *testing.T) {
	hunks := Hunks([]byte("a\nb"), []byte("a\nB"), Options{Context: 3})
	require.Len(t, hunks, 1)
	require.False(t, hunks[0].Lines[0].NoNewline)
	require.True(t, hunks[0].Lines[1].NoNewline)
	require.True(t, hunks[0].Lines[2].NoNewline)
	require.Equal(t, []string{
		"@@ -1,2 +1,2 @@",
		" a",
		"-b",
		`\ No newline at end of file`,
		"+B",
		`\ No newline at end of file`,
	}, hunks[0].Render())
}

func TestHunks_IdenticalContentHasNoHunks(t *testing.T) {
	require.Empty(t, Hunks([]byte("same\n"), []byte("same\n"), Options{Context: 3}))
}

func TestFileHunks_SkipsUnrenderableChanges(t *testing.T) {
	require.NotEmpty(t, FileHunks(modified("one\n", "two\n"), Options{Context: 3}))

	require.Empty(t, FileHunks(binaryModified(), Options{Context: 3}))

	unavailable := modified("one\n", "two\n")
	unavailable.NewUnavailable = true
	require.Empty(t, FileHunks(unavailable, Options{Context: 3}))

	// a rename or mode change that leaves the content identical
	require.Empty(t, FileHunks(renamed("a.txt", "b.txt", "same\n", "same\n", 100), Options{Context: 3}))

	modeOnly := modified("same\n", "same\n")
	modeOnly.Change.New.Mode = 3
	require.Empty(t, FileHunks(modeOnly, Options{Context: 3}))

	// every difference ignored by the whitespace options
	require.Empty(t, FileHunks(modified("a b\n", "a  b\n"), Options{Context: 3, IgnoreSpaceChange: true}))
	require.NotEmpty(t, FileHunks(addedFile("new.txt", "one\n"), Options{Context: 3}))
}

func TestHunkLineAt(t *testing.T) {
	f := modified("one\ntwo\nthree\n", "one\nTWO\nthree\nfour\n")
	hunks := FileHunks(f, Options{Context: 3})

	hunk, line, ok := HunkLineAt(hunks, SideRight, 2)
	require.True(t, ok)
	require.Equal(t, byte('+'), line.Kind)
	require.Equal(t, "TWO\n", line.Text)
	require.Equal(t, hunks[0].OldStart, hunk.OldStart)

	// the removed line only exists on the left side
	_, line, ok = HunkLineAt(hunks, SideLeft, 2)
	require.True(t, ok)
	require.Equal(t, byte('-'), line.Kind)
	require.Equal(t, "two\n", line.Text)
	_, _, ok = HunkLineAt(hunks, SideRight, 99)
	require.False(t, ok)
	_, _, ok = HunkLineAt(hunks, SideLeft, 99)
	require.False(t, ok)
	// an added line is not part of the old file
	_, _, ok = HunkLineAt(hunks, SideLeft, 4)
	require.False(t, ok)
}

func TestHunkLineAt_ContextLineIsAnchorableOnBothSides(t *testing.T) {
	hunks := Hunks([]byte("one\ntwo\nthree\n"), []byte("one\nTWO\nthree\n"), Options{Context: 3})
	_, right, ok := HunkLineAt(hunks, SideRight, 3)
	require.True(t, ok)
	require.Equal(t, "three\n", right.Text)
	_, left, ok := HunkLineAt(hunks, SideLeft, 3)
	require.True(t, ok)
	require.Equal(t, "three\n", left.Text)
}
