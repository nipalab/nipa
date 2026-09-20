package diff

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
)

func modified(old, new string) FileDiff {
	return FileDiff{
		Change: Change{
			Path:   "a.txt",
			Status: Modified,
			Old:    Entry{Path: "a.txt", Mode: 2, Hash: chunker.Sum([]byte(old))},
			New:    Entry{Path: "a.txt", Mode: 2, Hash: chunker.Sum([]byte(new))},
		},
		Old: []byte(old),
		New: []byte(new),
	}
}

func renamed(oldPath, newPath, oldContent, newContent string, score int) FileDiff {
	return FileDiff{
		Change: Change{
			Path:       newPath,
			Status:     Renamed,
			Similarity: score,
			Old:        Entry{Path: oldPath, Mode: 2, Hash: chunker.Sum([]byte(oldContent))},
			New:        Entry{Path: newPath, Mode: 2, Hash: chunker.Sum([]byte(newContent))},
		},
		Old: []byte(oldContent),
		New: []byte(newContent),
	}
}

func TestFilePatch_Modified(t *testing.T) {
	got := FilePatch(modified("one\ntwo\nthree\n", "one\nTWO\nthree\nfour\n"), Options{Context: 3})
	require.Equal(t, []string{
		"diff --nipa a/a.txt b/a.txt",
		"--- a/a.txt",
		"+++ b/a.txt",
		"@@ -1,3 +1,4 @@",
		" one",
		"-two",
		"+TWO",
		" three",
		"+four",
	}, got)
}

func TestFilePatch_Added(t *testing.T) {
	f := FileDiff{
		Change: Change{Path: "new.txt", Status: Added, New: Entry{Path: "new.txt", Mode: 2}},
		New:    []byte("a\nb\n"),
	}
	require.Equal(t, []string{
		"diff --nipa a/new.txt b/new.txt",
		"new file mode 100644",
		"--- /dev/null",
		"+++ b/new.txt",
		"@@ -0,0 +1,2 @@",
		"+a",
		"+b",
	}, FilePatch(f, Options{Context: 3}))
}

func TestFilePatch_Deleted(t *testing.T) {
	f := FileDiff{
		Change: Change{Path: "gone.txt", Status: Deleted, Old: Entry{Path: "gone.txt", Mode: 2}},
		Old:    []byte("a\nb\n"),
	}
	require.Equal(t, []string{
		"diff --nipa a/gone.txt b/gone.txt",
		"deleted file mode 100644",
		"--- a/gone.txt",
		"+++ /dev/null",
		"@@ -1,2 +0,0 @@",
		"-a",
		"-b",
	}, FilePatch(f, Options{Context: 3}))
}

func TestFilePatch_Binary(t *testing.T) {
	f := FileDiff{
		Change: Change{
			Path:   "img.bin",
			Status: Modified,
			Old:    Entry{Path: "img.bin", Mode: 2, IsBinary: true},
			New:    Entry{Path: "img.bin", Mode: 2, IsBinary: true},
		},
		Old: []byte{0, 1},
		New: []byte{0, 2},
	}
	require.Equal(t, []string{
		"diff --nipa a/img.bin b/img.bin",
		"Binary files a/img.bin and b/img.bin differ",
	}, FilePatch(f, Options{Context: 3}))
}

func TestFilePatch_BinaryAdded(t *testing.T) {
	f := FileDiff{
		Change: Change{Path: "img.bin", Status: Added, New: Entry{Path: "img.bin", Mode: 2, IsBinary: true}},
		New:    []byte{0, 2},
	}
	require.Equal(t, []string{
		"diff --nipa a/img.bin b/img.bin",
		"new file mode 100644",
		"Binary files /dev/null and b/img.bin differ",
	}, FilePatch(f, Options{Context: 3}))
}

func TestFilePatch_BinaryUnavailable(t *testing.T) {
	f := FileDiff{
		Change: Change{
			Path:   "img.bin",
			Status: Modified,
			Old:    Entry{Path: "img.bin", Mode: 2, IsBinary: true},
			New:    Entry{Path: "img.bin", Mode: 2, IsBinary: true},
		},
		OldUnavailable: true,
		NewUnavailable: true,
	}
	require.Equal(t, []string{
		"diff --nipa a/img.bin b/img.bin",
		"Binary files a/img.bin and b/img.bin differ",
	}, FilePatch(f, Options{Context: 3}))
}

func TestFilePatch_ModeOnly(t *testing.T) {
	f := modified("same\n", "same\n")
	f.Change.New.Mode = 3
	require.Equal(t, []string{
		"diff --nipa a/a.txt b/a.txt",
		"old mode 100644",
		"new mode 100755",
	}, FilePatch(f, Options{Context: 3}))
}

func TestFilePatch_NoNewline(t *testing.T) {
	got := FilePatch(modified("a\nb", "a\nB"), Options{Context: 3})
	require.Contains(t, got, "-b")
	require.Contains(t, got, `\ No newline at end of file`)
	require.Contains(t, got, "+B")
}

func TestFilePatch_ContextZero(t *testing.T) {
	got := FilePatch(modified("one\ntwo\nthree\nfour\nfive\n", "one\ntwo\nTHREE\nfour\nfive\n"), Options{Context: 0})
	require.Equal(t, []string{
		"diff --nipa a/a.txt b/a.txt",
		"--- a/a.txt",
		"+++ b/a.txt",
		"@@ -3,1 +3,1 @@",
		"-three",
		"+THREE",
	}, got)
}

func TestFilePatch_TwoHunks(t *testing.T) {
	oldText := "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n"
	newText := "1\nX\n3\n4\n5\n6\n7\n8\nY\n10\n"
	got := FilePatch(modified(oldText, newText), Options{Context: 1})
	require.Equal(t, []string{
		"diff --nipa a/a.txt b/a.txt",
		"--- a/a.txt",
		"+++ b/a.txt",
		"@@ -1,3 +1,3 @@",
		" 1",
		"-2",
		"+X",
		" 3",
		"@@ -8,3 +8,3 @@",
		" 8",
		"-9",
		"+Y",
		" 10",
	}, got)
}

func TestFilePatch_Renamed(t *testing.T) {
	got := FilePatch(renamed("old.txt", "new.txt", "one\n", "one\ntwo\n", 80), Options{Context: 3})
	require.Equal(t, []string{
		"diff --nipa a/old.txt b/new.txt",
		"similarity index 80%",
		"rename from old.txt",
		"rename to new.txt",
		"--- a/old.txt",
		"+++ b/new.txt",
		"@@ -1,1 +1,2 @@",
		" one",
		"+two",
	}, got)
}

func TestFilePatch_RenamedPure(t *testing.T) {
	got := FilePatch(renamed("old.txt", "new.txt", "same\n", "same\n", 100), Options{Context: 3})
	require.Equal(t, []string{
		"diff --nipa a/old.txt b/new.txt",
		"similarity index 100%",
		"rename from old.txt",
		"rename to new.txt",
	}, got)
}

func TestFilePatch_AddedEmpty(t *testing.T) {
	f := FileDiff{Change: Change{
		Path:   "empty.txt",
		Status: Added,
		New:    Entry{Path: "empty.txt", Mode: 2, Hash: chunker.Sum(nil)},
	}}
	require.Equal(t, []string{
		"diff --nipa a/empty.txt b/empty.txt",
		"new file mode 100644",
	}, FilePatch(f, Options{Context: 3}))
}

func TestFilePatch_Unavailable(t *testing.T) {
	f := modified("a\n", "b\n")
	f.OldUnavailable = true
	got := FilePatch(f, Options{Context: 3})
	require.Equal(t, []string{
		"diff --nipa a/a.txt b/a.txt",
		contentUnavailable,
	}, got)
}

func TestFilterIgnored_WhitespaceOnly(t *testing.T) {
	f := modified("a b\nc\n", "a   b\nc\n")
	require.Empty(t, FilterIgnored([]FileDiff{f}, Options{IgnoreAllSpace: true}))
	require.Len(t, FilterIgnored([]FileDiff{f}, Options{}), 1)
	require.Empty(t, Patch(FilterIgnored([]FileDiff{f}, Options{IgnoreAllSpace: true}), Options{IgnoreAllSpace: true}))
}

func TestFilterIgnored_SpaceChangeKeepsRealChanges(t *testing.T) {
	f := modified("a b\n", "a   b\nc\n")
	opts := Options{Context: 3, IgnoreSpaceChange: true}
	got := Patch(FilterIgnored([]FileDiff{f}, opts), opts)
	require.Contains(t, got, "+c")
	require.Contains(t, got, " a b")
}

func TestFilterIgnored_KeepsRenames(t *testing.T) {
	f := renamed("old.txt", "new.txt", "same\n", "same\n", 100)
	require.Len(t, FilterIgnored([]FileDiff{f}, Options{IgnoreAllSpace: true}), 1)
}
