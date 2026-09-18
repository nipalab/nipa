package diff

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func modifiedChange(path string, oldMode, newMode int) Change {
	return Change{
		Path:   path,
		Status: Modified,
		Old:    testEntry(path, oldMode, "old"),
		New:    testEntry(path, newMode, "new"),
	}
}

func TestFilePatch_SingleChange(t *testing.T) {
	c := modifiedChange("f.txt", 2, 2)
	got := FilePatch(c, []byte("l1\nl2\nl3\nl4\nl5\n"), []byte("l1\nl2\nCHANGED\nl4\nl5\n"), Options{})
	require.Equal(t, []string{
		"diff --nipa a/f.txt b/f.txt",
		"--- a/f.txt",
		"+++ b/f.txt",
		"@@ -1,5 +1,5 @@",
		" l1",
		" l2",
		"-l3",
		"+CHANGED",
		" l4",
		" l5",
	}, got)
}

func TestFilePatch_TwoHunks(t *testing.T) {
	old := "a\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\n"
	new := "A\nb\nc\nd\ne\nf\ng\nh\ni\nj\nK\n"
	c := modifiedChange("f.txt", 2, 2)
	got := FilePatch(c, []byte(old), []byte(new), Options{})
	require.Equal(t, []string{
		"diff --nipa a/f.txt b/f.txt",
		"--- a/f.txt",
		"+++ b/f.txt",
		"@@ -1,4 +1,4 @@",
		"-a",
		"+A",
		" b",
		" c",
		" d",
		"@@ -8,4 +8,4 @@",
		" h",
		" i",
		" j",
		"-k",
		"+K",
	}, got)
}

func TestFilePatch_Added(t *testing.T) {
	c := Change{Path: "new.txt", Status: Added, New: testEntry("new.txt", 2, "one\ntwo\n")}
	got := FilePatch(c, nil, []byte("one\ntwo\n"), Options{})
	require.Equal(t, []string{
		"diff --nipa a/new.txt b/new.txt",
		"new file mode 2",
		"--- /dev/null",
		"+++ b/new.txt",
		"@@ -0,0 +1,2 @@",
		"+one",
		"+two",
	}, got)
}

func TestFilePatch_Deleted(t *testing.T) {
	c := Change{Path: "gone.txt", Status: Deleted, Old: testEntry("gone.txt", 3, "one\ntwo\n")}
	got := FilePatch(c, []byte("one\ntwo\n"), nil, Options{})
	require.Equal(t, []string{
		"diff --nipa a/gone.txt b/gone.txt",
		"deleted file mode 3",
		"--- a/gone.txt",
		"+++ /dev/null",
		"@@ -1,2 +0,0 @@",
		"-one",
		"-two",
	}, got)
}

func TestFilePatch_EmptyAdded(t *testing.T) {
	c := Change{Path: "empty.txt", Status: Added, New: testEntry("empty.txt", 2, "")}
	got := FilePatch(c, nil, nil, Options{})
	require.Equal(t, []string{
		"diff --nipa a/empty.txt b/empty.txt",
		"new file mode 2",
		"--- /dev/null",
		"+++ b/empty.txt",
	}, got)
}

func TestFilePatch_NoTrailingNewline(t *testing.T) {
	c := modifiedChange("f.txt", 2, 2)
	got := FilePatch(c, []byte("a\nb"), []byte("a\nc"), Options{})
	require.Equal(t, []string{
		"diff --nipa a/f.txt b/f.txt",
		"--- a/f.txt",
		"+++ b/f.txt",
		"@@ -1,2 +1,2 @@",
		" a",
		"-b",
		`\ No newline at end of file`,
		"+c",
		`\ No newline at end of file`,
	}, got)
}

func TestFilePatch_Binary(t *testing.T) {
	c := modifiedChange("img.png", 2, 2)
	c.Old.IsBinary = true
	c.New.IsBinary = true
	got := FilePatch(c, []byte{0x89, 0x50}, []byte{0x89, 0x51}, Options{})
	require.Equal(t, []string{
		"diff --nipa a/img.png b/img.png",
		"Binary files a/img.png and b/img.png differ",
	}, got)
}

func TestFilePatch_ModeOnly(t *testing.T) {
	c := modifiedChange("run.sh", 2, 3)
	got := FilePatch(c, []byte("echo hi\n"), []byte("echo hi\n"), Options{})
	require.Equal(t, []string{
		"diff --nipa a/run.sh b/run.sh",
		"old mode 2",
		"new mode 3",
	}, got)
}

func TestFilePatch_ModeAndContent(t *testing.T) {
	c := modifiedChange("run.sh", 2, 3)
	got := FilePatch(c, []byte("echo hi\n"), []byte("echo bye\n"), Options{})
	require.Equal(t, []string{
		"diff --nipa a/run.sh b/run.sh",
		"old mode 2",
		"new mode 3",
		"--- a/run.sh",
		"+++ b/run.sh",
		"@@ -1,1 +1,1 @@",
		"-echo hi",
		"+echo bye",
	}, got)
}

func TestFilePatch_CustomOptions(t *testing.T) {
	c := modifiedChange("f.txt", 2, 2)
	got := FilePatch(c, []byte("l1\nl2\nl3\n"), []byte("l1\nl2\nl3-changed\n"), Options{Context: 1, AHeader: "o/", BHeader: "n/"})
	require.Equal(t, []string{
		"diff --nipa o/f.txt n/f.txt",
		"--- o/f.txt",
		"+++ n/f.txt",
		"@@ -2,2 +2,2 @@",
		" l2",
		"-l3",
		"+l3-changed",
	}, got)
}
