package diff

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func modified(old, new string) FileDiff {
	return FileDiff{
		Change: Change{
			Path:   "a.txt",
			Status: Modified,
			Old:    Entry{Path: "a.txt", Mode: 2},
			New:    Entry{Path: "a.txt", Mode: 2},
		},
		Old: []byte(old),
		New: []byte(new),
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

func TestFilePatch_Options(t *testing.T) {
	got := FilePatch(modified("a\n", "b\n"), Options{
		Context:      3,
		NoPrefix:     true,
		OldIndicator: "!",
		NewIndicator: ">",
		LinePrefix:   "| ",
	})
	require.Equal(t, []string{
		"| diff --nipa a.txt a.txt",
		"| --- a.txt",
		"| +++ a.txt",
		"| @@ -1,1 +1,1 @@",
		"| !a",
		"| >b",
	}, got)
}

func TestFilePatch_TextOption(t *testing.T) {
	f := FileDiff{
		Change: Change{
			Path:   "img.bin",
			Status: Modified,
			Old:    Entry{Path: "img.bin", Mode: 2, IsBinary: true},
			New:    Entry{Path: "img.bin", Mode: 2, IsBinary: true},
		},
		Old: []byte{0x01, '\n'},
		New: []byte{0x02, '\n'},
	}
	got := FilePatch(f, Options{Context: 3, Text: true})
	require.Equal(t, []string{
		"diff --nipa a/img.bin b/img.bin",
		"--- a/img.bin",
		"+++ b/img.bin",
		"@@ -1,1 +1,1 @@",
		"-\x01",
		"+\x02",
	}, got)
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
