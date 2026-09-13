package merge

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/domain"
)

func file(t *testing.T, path string, data []byte) File {
	t.Helper()
	chunks, err := chunker.ChunkAll(data)
	require.NoError(t, err)
	hashes := make([]domain.Hash, len(chunks))
	for i, c := range chunks {
		hashes[i] = c.Hash
	}
	return File{
		Path:        path,
		Mode:        0o644,
		SizeBytes:   int64(len(data)),
		IsBinary:    chunker.IsBinary(data),
		Hash:        chunker.FileHash(hashes),
		ChunkHashes: hashes,
	}
}

func TestMergeText_CleanDisjointEdits(t *testing.T) {
	base := []byte("a\nb\nc\nd\n")
	ours := []byte("a\nX\nc\nd\n")
	theirs := []byte("a\nb\nc\nY\n")
	merged, conflicted := MergeText(base, ours, theirs)
	require.False(t, conflicted)
	require.Equal(t, "a\nX\nc\nY\n", string(merged))
}

func TestMergeText_TakeTheirsUnchangedOnOurs(t *testing.T) {
	base := []byte("a\nb\nc\n")
	ours := []byte("a\nb\nc\n")
	theirs := []byte("a\nB\nc\n")
	merged, conflicted := MergeText(base, ours, theirs)
	require.False(t, conflicted)
	require.Equal(t, string(theirs), string(merged))
}

func TestMergeText_TakeOursUnchangedOnTheirs(t *testing.T) {
	base := []byte("a\nb\nc\n")
	ours := []byte("a\nB\nc\n")
	theirs := []byte("a\nb\nc\n")
	merged, conflicted := MergeText(base, ours, theirs)
	require.False(t, conflicted)
	require.Equal(t, string(ours), string(merged))
}

func TestMergeText_SameEditOnBothSides(t *testing.T) {
	base := []byte("a\nb\n")
	ours := []byte("a\nB\n")
	theirs := []byte("a\nB\n")
	merged, conflicted := MergeText(base, ours, theirs)
	require.False(t, conflicted)
	require.Equal(t, "a\nB\n", string(merged))
}

func TestMergeText_ConflictSameRegion(t *testing.T) {
	base := []byte("a\nb\nc\n")
	ours := []byte("a\nX\nc\n")
	theirs := []byte("a\nY\nc\n")
	merged, conflicted := MergeText(base, ours, theirs)
	require.True(t, conflicted)
	require.Equal(t, "a\n<<<<<<< ours\nX\n=======\nY\n>>>>>>> theirs\nc\n", string(merged))
}

func TestMergeText_NoTrailingNewlineStrict(t *testing.T) {
	// Missing vs present trailing newline makes the final line a different
	// token, so changing it on one side and appending on the other conflicts.
	base := []byte("a\nb")
	ours := []byte("a\nB")
	theirs := []byte("a\nb\nc")
	merged, conflicted := MergeText(base, ours, theirs)
	require.True(t, conflicted)
	require.Equal(t, "a\n<<<<<<< ours\nB\n=======\nb\nc\n>>>>>>> theirs\n", string(merged))
}

func TestMergeText_NoTrailingNewlineTakenFromSide(t *testing.T) {
	base := []byte("a\nb")
	ours := []byte("a\nB")
	theirs := []byte("a\nB")
	merged, conflicted := MergeText(base, ours, theirs)
	require.False(t, conflicted)
	require.Equal(t, "a\nB", string(merged))
}

func TestMergeText_MultiLineReplacementConflict(t *testing.T) {
	base := []byte("one\ntwo\nthree\n")
	ours := []byte("uno\ndos\ntres\n")
	theirs := []byte("1\n2\n3\n")
	merged, conflicted := MergeText(base, ours, theirs)
	require.True(t, conflicted)
	require.Equal(t, "<<<<<<< ours\nuno\ndos\ntres\n=======\n1\n2\n3\n>>>>>>> theirs\n", string(merged))
}

func TestMergeText_AppendDifferentTrailers(t *testing.T) {
	base := []byte("line1\nline2\n")
	ours := []byte("line1\nline2\nc\n")
	theirs := []byte("line1\nline2\nd\n")
	merged, conflicted := MergeText(base, ours, theirs)
	require.True(t, conflicted)
	require.Equal(t, "line1\nline2\n<<<<<<< ours\nc\n=======\nd\n>>>>>>> theirs\n", string(merged))
}

func TestMergeText_AdjacentEditsConflict(t *testing.T) {
	base := []byte("a\nb\nc\n")
	ours := []byte("a2\nb\nc\n")
	theirs := []byte("a\nb\nc2\n")
	merged, conflicted := MergeText(base, ours, theirs)
	require.False(t, conflicted)
	require.Equal(t, "a2\nb\nc2\n", string(merged))
}

func TestMergeText_EmptyBase(t *testing.T) {
	merged, conflicted := MergeText(nil, []byte("only ours\n"), []byte("only ours\n"))
	require.False(t, conflicted)
	require.Equal(t, "only ours\n", string(merged))

	merged, conflicted = MergeText(nil, []byte("a\n"), []byte("b\n"))
	require.True(t, conflicted)
}

func ThreeWayFromStrings(t *testing.T, base, ours, theirs map[string]string) *Result {
	t.Helper()
	bm := make(map[string]File, len(base))
	om := make(map[string]File, len(base))
	tm := make(map[string]File, len(base))
	for p, s := range base {
		bm[p] = file(t, p, []byte(s))
	}
	for p, s := range ours {
		om[p] = file(t, p, []byte(s))
	}
	for p, s := range theirs {
		tm[p] = file(t, p, []byte(s))
	}
	return ThreeWay(bm, om, tm)
}

func TestThreeWay_NoChanges(t *testing.T) {
	res := ThreeWayFromStrings(t, map[string]string{"f.txt": "x"}, map[string]string{"f.txt": "x"}, map[string]string{"f.txt": "x"})
	require.Empty(t, res.Entries)
	require.Empty(t, res.Deleted)
	require.Empty(t, res.Conflicts)
}

func TestThreeWay_OursOnlyChanged(t *testing.T) {
	res := ThreeWayFromStrings(t,
		map[string]string{"f.txt": "x"},
		map[string]string{"f.txt": "y"},
		map[string]string{"f.txt": "x"})
	require.Equal(t, KeepOurs, res.Entries["f.txt"].Decision)
	require.Empty(t, res.Conflicts)
}

func TestThreeWay_SameChangeOnBothSides(t *testing.T) {
	res := ThreeWayFromStrings(t,
		map[string]string{"f.txt": "x"},
		map[string]string{"f.txt": "y"},
		map[string]string{"f.txt": "y"})
	require.Equal(t, KeepOurs, res.Entries["f.txt"].Decision)
	require.Empty(t, res.Conflicts)
}

func TestThreeWay_OursDeletedTheirsUnchanged(t *testing.T) {
	res := ThreeWayFromStrings(t,
		map[string]string{"f.txt": "base"},
		map[string]string{},
		map[string]string{"f.txt": "base"})
	require.Empty(t, res.Entries)
	require.Equal(t, []string{"f.txt"}, res.Deleted)
	require.Empty(t, res.Conflicts)
}

func TestThreeWay_AddedOnlyOnOurs(t *testing.T) {
	res := ThreeWayFromStrings(t,
		map[string]string{},
		map[string]string{"new.txt": "content"},
		map[string]string{})
	require.Equal(t, KeepOurs, res.Entries["new.txt"].Decision)
	require.Empty(t, res.Conflicts)
}

func TestThreeWay_TheirsOnlyChanged(t *testing.T) {
	res := ThreeWayFromStrings(t,
		map[string]string{"f.txt": "x"},
		map[string]string{"f.txt": "x"},
		map[string]string{"f.txt": "z"})
	require.Equal(t, KeepTheirs, res.Entries["f.txt"].Decision)
	require.Empty(t, res.Conflicts)
}

func TestThreeWay_TextConflict(t *testing.T) {
	res := ThreeWayFromStrings(t,
		map[string]string{"f.txt": "a\nb\nc\n"},
		map[string]string{"f.txt": "a\nX\nc\n"},
		map[string]string{"f.txt": "a\nY\nc\n"})
	require.Equal(t, TextMerge, res.Entries["f.txt"].Decision)
	// TextMerge reservations are marked as entries; whether they conflict is
	// decided when the content is diff3-merged by the caller.
	require.Empty(t, res.Conflicts)
}

func TestThreeWay_AddAdd(t *testing.T) {
	res := ThreeWayFromStrings(t,
		map[string]string{},
		map[string]string{"new.txt": "ours"},
		map[string]string{"new.txt": "theirs"})
	require.Equal(t, AddAddConflict, res.Entries["new.txt"].Decision)
	require.Len(t, res.Conflicts, 1)
}

func TestThreeWay_AddAddSame(t *testing.T) {
	res := ThreeWayFromStrings(t,
		map[string]string{},
		map[string]string{"new.txt": "same"},
		map[string]string{"new.txt": "same"})
	require.Equal(t, KeepOurs, res.Entries["new.txt"].Decision)
	require.Empty(t, res.Conflicts)
}

func TestThreeWay_ModifyDelete(t *testing.T) {
	res := ThreeWayFromStrings(t,
		map[string]string{"f.txt": "base"},
		map[string]string{"f.txt": "ours"},
		map[string]string{})
	require.Equal(t, ModifyDeleteConflict, res.Entries["f.txt"].Decision)
	require.Len(t, res.Conflicts, 1)
}

func TestThreeWay_DeleteModify(t *testing.T) {
	res := ThreeWayFromStrings(t,
		map[string]string{"f.txt": "base"},
		map[string]string{},
		map[string]string{"f.txt": "theirs"})
	require.Equal(t, DeleteModifyConflict, res.Entries["f.txt"].Decision)
	require.Len(t, res.Conflicts, 1)
}

func TestThreeWay_DeletedOnBoth(t *testing.T) {
	res := ThreeWayFromStrings(t,
		map[string]string{"f.txt": "base"},
		map[string]string{},
		map[string]string{})
	require.Empty(t, res.Entries)
	require.Equal(t, []string{"f.txt"}, res.Deleted)
}

func TestThreeWay_TheirsDeletedOursUnchanged(t *testing.T) {
	res := ThreeWayFromStrings(t,
		map[string]string{"f.txt": "base"},
		map[string]string{"f.txt": "base"},
		map[string]string{})
	require.Empty(t, res.Entries)
	require.Equal(t, []string{"f.txt"}, res.Deleted)
	require.Empty(t, res.Conflicts)
}

func TestThreeWay_AddedOnlyOnTheirs(t *testing.T) {
	res := ThreeWayFromStrings(t,
		map[string]string{},
		map[string]string{},
		map[string]string{"new.txt": "content"})
	require.Equal(t, KeepTheirs, res.Entries["new.txt"].Decision)
}

func TestThreeWay_BinaryConflict(t *testing.T) {
	base := map[string]File{"a.bin": file(t, "a.bin", []byte{0x00, 0x01})}
	ours := map[string]File{"a.bin": file(t, "a.bin", []byte{0x00, 0x02})}
	theirs := map[string]File{"a.bin": file(t, "a.bin", []byte{0x00, 0x03})}
	res := ThreeWay(base, ours, theirs)
	require.Equal(t, BinaryConflict, res.Entries["a.bin"].Decision)
	require.Len(t, res.Conflicts, 1)
}
