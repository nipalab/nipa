package usecase

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
)

func newConflicts(t *testing.T, local *stubLocalRepo, root string) *Conflicts {
	t.Helper()
	local.loadConfig = &domain.Config{Branch: "main"}
	return NewConflicts(local, newWorkingCopy(t, local, root), root)
}

func pendingMerge() *domain.MergeState {
	return &domain.MergeState{
		SourceBranch:   "feature",
		SourceCommitID: "src",
		BaseCommitID:   "base",
		TargetCommitID: "tgt",
		Conflicts:      []string{"a.txt", "art/hero.png", "gone.txt", "old.txt"},
		ConflictKinds: map[string]string{
			"a.txt":        domain.ConflictText,
			"art/hero.png": domain.ConflictBinary,
			"gone.txt":     domain.ConflictDeleteModify,
		},
		ConflictBinary: []string{"art/hero.png"},
	}
}

func TestConflicts_ListMerge(t *testing.T) {
	local := &stubLocalRepo{mergeState: pendingMerge()}
	local.mergeState.Resolved = []string{"a.txt"}
	set, err := newConflicts(t, local, t.TempDir()).List()
	require.NoError(t, err)
	require.Equal(t, "merge", set.Op)
	require.Equal(t, domain.ConflictRevision{ID: "base", Label: "merge base"}, set.Base)
	require.Equal(t, domain.ConflictRevision{ID: "tgt", Label: "main"}, set.Ours)
	require.Equal(t, domain.ConflictRevision{ID: "src", Label: "feature"}, set.Theirs)
	require.Equal(t, []domain.ConflictFile{
		{Path: "a.txt", Kind: domain.ConflictText, Resolved: true},
		{Path: "art/hero.png", Kind: domain.ConflictBinary, Binary: true},
		{Path: "gone.txt", Kind: domain.ConflictDeleteModify},
		{Path: "old.txt", Kind: domain.ConflictText},
	}, set.Files)
}

func TestConflicts_ListRevert(t *testing.T) {
	local := &stubLocalRepo{revertState: &domain.RevertState{
		Targets:         []domain.CommitRef{{ID: "c1", Subject: "Add hero\n\nbody"}},
		CurrentCommitID: "head",
		TheirsCommitID:  "c0",
		Conflicts:       []string{"a.txt"},
	}}
	set, err := newConflicts(t, local, t.TempDir()).List()
	require.NoError(t, err)
	require.Equal(t, "revert", set.Op)
	require.Equal(t, domain.ConflictRevision{ID: "c1", Label: "Add hero"}, set.Base)
	require.Equal(t, domain.ConflictRevision{ID: "head", Label: "main"}, set.Ours)
	require.Equal(t, domain.ConflictRevision{ID: "c0", Label: "before Add hero"}, set.Theirs)

	local.revertState.Targets = []domain.CommitRef{{ID: "c1"}}
	set, err = newConflicts(t, local, t.TempDir()).List()
	require.NoError(t, err)
	require.Equal(t, "before c1", set.Theirs.Label)

	local.revertState.Targets = nil
	set, err = newConflicts(t, local, t.TempDir()).List()
	require.NoError(t, err)
	require.Empty(t, set.Base.ID)
}

func TestConflicts_ListNothingPending(t *testing.T) {
	set, err := newConflicts(t, &stubLocalRepo{}, t.TempDir()).List()
	require.NoError(t, err)
	require.Nil(t, set)
}

func TestConflicts_ListErrors(t *testing.T) {
	boom := errors.New("boom")
	local := &stubLocalRepo{}
	c := newConflicts(t, local, t.TempDir())
	local.loadConfig = nil
	local.configLoadErr = boom
	_, err := c.List()
	require.ErrorIs(t, err, boom)

	local.configLoadErr = nil
	local.mergeStateErr = boom
	_, err = c.List()
	require.ErrorIs(t, err, boom)

	local.mergeStateErr = nil
	local.revertStateErr = boom
	_, err = c.List()
	require.ErrorIs(t, err, boom)
}

func TestConflicts_ResolveWithContent(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "<<<<<<< ours\nA\n=======\nB\n>>>>>>> theirs\n")
	local := &stubLocalRepo{mergeState: pendingMerge(), snapshot: &domain.Snapshot{}}
	c := newConflicts(t, local, root)

	err := c.Resolve(context.Background(), "a.txt", Resolution{Content: strings.NewReader("x\n<<<<<<< ours\n")})
	require.ErrorContains(t, err, "still has conflict markers")

	require.NoError(t, c.Resolve(context.Background(), "a.txt", Resolution{Content: strings.NewReader("A\nB\n")}))
	data, err := os.ReadFile(filepath.Join(root, "a.txt"))
	require.NoError(t, err)
	require.Equal(t, "A\nB\n", string(data))
	require.Contains(t, local.stageAdd, "a.txt")
	require.Equal(t, []string{"a.txt"}, local.savedMerge.Resolved)

	require.NoError(t, c.Resolve(context.Background(), "./a.txt", Resolution{Content: strings.NewReader(">>>>>>> theirs\n"), AllowMarkers: true}))
	require.Equal(t, []string{"a.txt"}, local.savedMerge.Resolved, "resolving twice records the path once")
}

func TestConflicts_ResolveAppliesChosenMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not preserved on windows")
	}
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "old\n")
	require.NoError(t, os.Chmod(filepath.Join(root, "a.txt"), 0o644))
	local := &stubLocalRepo{mergeState: pendingMerge(), snapshot: &domain.Snapshot{}}
	c := newConflicts(t, local, root)

	require.NoError(t, c.Resolve(context.Background(), "a.txt", Resolution{Content: strings.NewReader("run\n"), Mode: 3}))
	info, err := os.Stat(filepath.Join(root, "a.txt"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o755), info.Mode().Perm())
}

func TestConflicts_ResolveKeepsWorkingFile(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "A\n<<<<<<< ours\nA\n=======\nB\n>>>>>>> theirs\n")
	writeRepoFile(t, root, "art/hero.png", "<<<<<<< ours\n")
	local := &stubLocalRepo{mergeState: pendingMerge(), snapshot: &domain.Snapshot{}}
	c := newConflicts(t, local, root)

	require.ErrorContains(t, c.Resolve(context.Background(), "a.txt", Resolution{}), "still has conflict markers")
	writeRepoFile(t, root, "a.txt", "fixed\n")
	require.NoError(t, c.Resolve(context.Background(), "a.txt", Resolution{}))
	require.NoError(t, c.Resolve(context.Background(), "art/hero.png", Resolution{}), "binary files are never scanned for markers")
	require.Equal(t, []string{"a.txt", "art/hero.png"}, local.savedMerge.Resolved)
}

func TestConflicts_ResolveWritesNewFileInNewDirectory(t *testing.T) {
	root := t.TempDir()
	local := &stubLocalRepo{mergeState: pendingMerge(), snapshot: &domain.Snapshot{}}
	local.mergeState.Conflicts = append(local.mergeState.Conflicts, "deep/dir/new.bin")
	local.mergeState.ConflictBinary = append(local.mergeState.ConflictBinary, "deep/dir/new.bin")
	c := newConflicts(t, local, root)

	require.NoError(t, c.Resolve(context.Background(), "deep/dir/new.bin", Resolution{Content: strings.NewReader("<<<<<<< ours\n")}))
	info, err := os.Stat(filepath.Join(root, "deep", "dir", "new.bin"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o644), info.Mode().Perm())
}

func TestConflicts_ResolveDelete(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "old.txt", "ours\n")
	local := &stubLocalRepo{
		mergeState: pendingMerge(),
		snapshot:   &domain.Snapshot{Files: []domain.SnapshotFile{{Path: "old.txt"}}},
		staged:     []string{"gone.txt"},
	}
	c := newConflicts(t, local, root)

	require.NoError(t, c.Resolve(context.Background(), "old.txt", Resolution{Delete: true}))
	_, err := os.Stat(filepath.Join(root, "old.txt"))
	require.ErrorIs(t, err, os.ErrNotExist)
	require.Contains(t, local.stageAdd, "old.txt", "a tracked file removed is staged as a deletion")

	require.NoError(t, c.Resolve(context.Background(), "gone.txt", Resolution{Delete: true}))
	require.Equal(t, []string{"gone.txt"}, local.stageRemove, "an untracked file kept deleted is unstaged")
	require.Equal(t, []string{"old.txt", "gone.txt"}, local.savedMerge.Resolved)
}

func TestConflicts_ResolveRevert(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "fixed\n")
	local := &stubLocalRepo{revertState: &domain.RevertState{Targets: []domain.CommitRef{{ID: "c1"}}, Conflicts: []string{"a.txt"}}, snapshot: &domain.Snapshot{}}
	c := newConflicts(t, local, root)

	require.NoError(t, c.Resolve(context.Background(), "a.txt", Resolution{}))
	require.Equal(t, []string{"a.txt"}, local.savedRevert.Resolved)
	require.NoError(t, c.Resolve(context.Background(), "a.txt", Resolution{}))
	require.Equal(t, []string{"a.txt"}, local.savedRevert.Resolved)
}

func TestConflicts_ResolveRejects(t *testing.T) {
	root := t.TempDir()
	local := &stubLocalRepo{snapshot: &domain.Snapshot{}}
	c := newConflicts(t, local, root)

	require.ErrorContains(t, c.Resolve(context.Background(), "", Resolution{}), "repository-relative path")
	require.ErrorContains(t, c.Resolve(context.Background(), "a.txt", Resolution{}), "no merge or revert is waiting")
	local.mergeState = pendingMerge()
	require.ErrorContains(t, c.Resolve(context.Background(), "b.txt", Resolution{}), "b.txt has no conflict to resolve")
}

func TestConflicts_ResolveErrors(t *testing.T) {
	boom := errors.New("boom")
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "fixed\n")

	local := &stubLocalRepo{mergeState: pendingMerge(), snapshot: &domain.Snapshot{}, saveMergeErr: boom}
	require.ErrorIs(t, newConflicts(t, local, root).Resolve(context.Background(), "a.txt", Resolution{}), boom)

	local = &stubLocalRepo{mergeState: pendingMerge(), snapshot: &domain.Snapshot{}, stageAddErr: boom}
	require.ErrorIs(t, newConflicts(t, local, root).Resolve(context.Background(), "a.txt", Resolution{}), boom)

	local = &stubLocalRepo{mergeState: pendingMerge(), snapshotErr: boom}
	require.ErrorIs(t, newConflicts(t, local, root).Resolve(context.Background(), "gone.txt", Resolution{}), boom)

	local = &stubLocalRepo{mergeState: pendingMerge(), mergeStateErr: boom}
	require.ErrorIs(t, newConflicts(t, local, root).Resolve(context.Background(), "a.txt", Resolution{}), boom)

	local = &stubLocalRepo{mergeState: pendingMerge(), snapshot: &domain.Snapshot{}}
	require.ErrorIs(t, newConflicts(t, local, root).Resolve(context.Background(), "a.txt", Resolution{Content: failingReader{boom}}), boom)
	require.ErrorIs(t, newConflicts(t, local, root).Resolve(context.Background(), "art/hero.png", Resolution{Content: failingReader{boom}}), boom)
}

type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

func TestHasMarkers(t *testing.T) {
	require.True(t, hasMarkers([]byte("<<<<<<< ours\n")))
	require.True(t, hasMarkers([]byte("a\n>>>>>>> theirs\n")))
	require.False(t, hasMarkers([]byte("a <<<<<<< ours\n")))
	require.False(t, hasMarkers([]byte("plain\n")))
}
