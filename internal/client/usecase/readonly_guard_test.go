package usecase

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
)

func TestTrackedModeFromPerm(t *testing.T) {
	cases := []struct {
		name    string
		perm    os.FileMode
		tracked int
		want    int
	}{
		{"writable stays writable", 0o644, 2, 2},
		{"guarded writable file", 0o444, 2, 2},
		{"guarded file of an unspecified mode", 0o444, 0, 2},
		{"guarded executable keeps its exec bits", 0o555, 3, 3},
		{"tracked read-only stays read-only", 0o444, 1, 1},
		{"tracked read-only made writable", 0o644, 1, 2},
		{"exec bit added", 0o755, 2, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, trackedModeFromPerm(tc.perm, tc.tracked))
		})
	}
}

func TestPushPerm(t *testing.T) {
	require.Equal(t, os.FileMode(0o644), pushPerm(0o444, 2))
	require.Equal(t, os.FileMode(0o755), pushPerm(0o555, 3))
	require.Equal(t, os.FileMode(0o444), pushPerm(0o444, 1), "a tracked read-only file stays read-only")
	require.Equal(t, os.FileMode(0o444), pushPerm(0o444, 0), "a new read-only file keeps its mode")
	require.Equal(t, os.FileMode(0o640), pushPerm(0o640, 2))
}

func guardFile(t *testing.T, root, path string) {
	t.Helper()
	require.NoError(t, os.Chmod(filepath.Join(root, path), 0o444))
}

func TestWorkingCopy_Status_IgnoresReadOnlyGuard(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.png", "png")
	wc := newWorkingCopy(t, &stubLocalRepo{snapshot: trackedSnapshot(t, "a.png", "png")}, root)

	guardFile(t, root, "a.png")
	st, err := wc.Status(context.Background())
	require.NoError(t, err)
	require.Empty(t, st.Modified, "a cold status must not report the guard")

	st, err = wc.Status(context.Background())
	require.NoError(t, err)
	require.Empty(t, st.Modified, "nor a cached one")
}

func TestWorkingCopy_Status_GuardedContentChangeIsModified(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.png", "changed")
	wc := newWorkingCopy(t, &stubLocalRepo{snapshot: trackedSnapshot(t, "a.png", "png")}, root)
	guardFile(t, root, "a.png")

	st, err := wc.Status(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"a.png"}, st.Modified)
}

func TestWorkingCopy_Status_TrackedReadOnlyMadeWritable(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "hello")
	snapshot := trackedSnapshot(t, "a.txt", "hello")
	snapshot.Files[0].Mode = 1
	wc := newWorkingCopy(t, &stubLocalRepo{snapshot: snapshot}, root)

	st, err := wc.Status(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"a.txt"}, st.Modified, "a tracked read-only file made writable is a real mode change")
}

func TestWorkingCopy_Add_SkipsGuardedUnchangedFile(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.png", "png")
	local := &stubLocalRepo{snapshot: trackedSnapshot(t, "a.png", "png")}
	wc := newWorkingCopy(t, local, root)
	_, err := wc.Status(context.Background())
	require.NoError(t, err)
	guardFile(t, root, "a.png")

	require.NoError(t, wc.Add(context.Background(), []string{"a.png"}))
	require.Empty(t, local.stageAdd)
}

func TestWorkingCopy_Restore_SkipsGuardedUnchangedFile(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.png", "png")
	snapshot := trackedSnapshot(t, "a.png", "png")
	wc := newWorkingCopy(t, &stubLocalRepo{snapshot: snapshot}, root)
	guardFile(t, root, "a.png")

	require.True(t, wc.matchesSnapshot("a.png", snapshot.Files[0]))
}

func TestDiff_IgnoresReadOnlyGuard(t *testing.T) {
	root := t.TempDir()
	stub := newDiffStub()
	withSnapshotFile(t, stub, "a.txt", "a\n")
	writeRepoFile(t, root, "a.txt", "a\n")
	guardFile(t, root, "a.txt")

	files, err := NewDiff(nil, nil, stub).Run(context.Background(), root, nil, DiffOptions{NoCache: true})
	require.NoError(t, err)
	require.Empty(t, files)
}

func TestPush_GuardedFileKeepsTrackedMode(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.png", "new content")
	writeRepoFile(t, root, "new.png", "fresh")
	guardFile(t, root, "a.png")
	guardFile(t, root, "new.png")
	base := map[string]domain.SnapshotFile{"a.png": {Path: "a.png", Mode: 2}}

	toRead, removed, err := classifyStaged(root, []string{"a.png", "new.png"}, base)
	require.NoError(t, err)
	require.Empty(t, removed)
	require.Len(t, toRead, 2)

	modes := map[string]int{}
	for _, sf := range toRead {
		sf.cfg = chunker.DefaultConfig
		file, err := scanPushFile(sf, newPlanCollector())
		require.NoError(t, err)
		modes[file.Path] = file.Mode
	}
	require.Equal(t, 0o644, modes["a.png"], "the guard must not reach the server")
	require.Equal(t, 0o444, modes["new.png"], "a new read-only file is pushed as read-only")
}
