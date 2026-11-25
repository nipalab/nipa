package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
)

// failingScopedRepo is a real repository whose by-path reads can be made to
// fail one at a time.
type failingScopedRepo struct {
	*readCountingRepo
	filesErr, underErr, tracksErr, treeHashErr, statErr error
}

func (f *failingScopedRepo) SnapshotFiles(paths []string) (map[string]domain.SnapshotFile, error) {
	if f.filesErr != nil {
		return nil, f.filesErr
	}
	return f.readCountingRepo.SnapshotFiles(paths)
}

func (f *failingScopedRepo) SnapshotFilesUnder(dir string) ([]domain.SnapshotFile, error) {
	if f.underErr != nil {
		return nil, f.underErr
	}
	return f.readCountingRepo.SnapshotFilesUnder(dir)
}

func (f *failingScopedRepo) TracksUnder(dir string) (bool, error) {
	if f.tracksErr != nil {
		return false, f.tracksErr
	}
	return f.readCountingRepo.TracksUnder(dir)
}

func (f *failingScopedRepo) SnapshotTreeHash() (string, error) {
	if f.treeHashErr != nil {
		return "", f.treeHashErr
	}
	return f.readCountingRepo.SnapshotTreeHash()
}

func (f *failingScopedRepo) StatEntries(paths []string) (map[string]domain.StatEntry, error) {
	if f.statErr != nil {
		return nil, f.statErr
	}
	return f.readCountingRepo.StatEntries(paths)
}

func newFailingRepo(t *testing.T) (*smallRepo, *failingScopedRepo) {
	t.Helper()
	fx := newSmallRepo(t, smallChangeFiles)
	smallChange(t, fx)
	return fx, &failingScopedRepo{readCountingRepo: &readCountingRepo{LocalRepo: fx.lr}}
}

func TestTrackedScope_ReadsOnlyWhatItIsAskedAbout(t *testing.T) {
	fx, repo := newFailingRepo(t)
	scope, err := newTrackedScope(repo)
	require.NoError(t, err)
	require.False(t, scope.all)

	f, ok, err := scope.get("a.txt")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, fx.chunks["a.txt"], f.Chunks)
	_, ok, err = scope.get("nope.txt")
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, 2, repo.pathReads)
	_, _, _ = scope.get("a.txt")
	_, _, _ = scope.get("nope.txt")
	require.Equal(t, 2, repo.pathReads, "answers are remembered")

	under, err := scope.under("src")
	require.NoError(t, err)
	require.Equal(t, []string{"src/main.go", "src/util/data.bin", "src/util/helper.go"}, under)
	_, ok, err = scope.get("src/new.txt")
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, scope.prefetch([]string{"src/main.go", "src/util/none.go", "a.txt", "nope.txt"}))
	require.Equal(t, 2, repo.pathReads, "a loaded directory answers for everything below it")

	// Exemptions from the ignore rules: tracked files and their directories.
	for rel, want := range map[string]bool{
		"":             false,
		"a.txt":        true,
		"src":          true,
		"src/util":     true,
		"src/other":    false,
		"docs":         true,
		"docs/gone.md": true,
		"srcx":         true,
		"sr":           false,
		"build":        true,
		"debug.log":    false,
	} {
		require.Equal(t, want, scope.protects(rel), rel)
	}
	require.NoError(t, scope.err)

	untracked, err := scope.untracked([]string{"a.txt", "zzz.txt", "src/new.txt", "docs/readme.md"})
	require.NoError(t, err)
	require.Equal(t, []string{"zzz.txt", "src/new.txt"}, untracked)
	require.Zero(t, repo.snapshots)

	// The root is the whole snapshot, loaded once.
	all, err := scope.under("")
	require.NoError(t, err)
	require.Len(t, all, len(smallChangeFiles))
	require.True(t, scope.all)
	require.True(t, scope.protects("ab"))
	require.False(t, scope.protects("zzz"))
	docs, err := scope.under("docs")
	require.NoError(t, err)
	require.Equal(t, []string{"docs/gone.md", "docs/readme.md"}, docs)
	require.NoError(t, scope.prefetch([]string{"never-asked.txt"}))
	require.Equal(t, 1, repo.snapshots)
}

func TestTrackedScope_ReturnsReadErrors(t *testing.T) {
	boom := errors.New("boom")

	_, repo := newFailingRepo(t)
	repo.filesErr = boom
	scope, err := newTrackedScope(repo)
	require.NoError(t, err)
	_, _, err = scope.get("a.txt")
	require.ErrorIs(t, err, boom)
	require.ErrorIs(t, scope.prefetch([]string{"a.txt"}), boom)
	_, err = scope.untracked([]string{"a.txt"})
	require.ErrorIs(t, err, boom)
	require.True(t, scope.protects("a.txt"), "nothing is reported as ignored on the way to the error")
	require.ErrorIs(t, scope.err, boom)

	_, repo = newFailingRepo(t)
	repo.underErr = boom
	scope, err = newTrackedScope(repo)
	require.NoError(t, err)
	_, err = scope.under("src")
	require.ErrorIs(t, err, boom)

	_, repo = newFailingRepo(t)
	repo.tracksErr = boom
	scope, err = newTrackedScope(repo)
	require.NoError(t, err)
	require.True(t, scope.protects("src"))
	require.ErrorIs(t, scope.err, boom)

	local := &stubLocalRepo{snapshotErr: boom}
	_, err = newTrackedScope(local)
	require.ErrorIs(t, err, boom)
	local = &stubLocalRepo{snapshot: &domain.Snapshot{}}
	scope, err = newTrackedScope(local)
	require.NoError(t, err)
	scope.all = false
	local.snapshotErr = boom
	_, err = scope.under("")
	require.ErrorIs(t, err, boom)
}

func TestAdd_ReturnsScopedReadErrors(t *testing.T) {
	boom := errors.New("boom")
	add := func(repo *failingScopedRepo, root string, targets ...string) error {
		wc, err := NewWorkingCopy(repo, root)
		require.NoError(t, err)
		return wc.Add(context.Background(), targets)
	}
	cases := map[string]struct {
		fail    func(*failingScopedRepo)
		targets []string
	}{
		"row of a file target":           {func(r *failingScopedRepo) { r.filesErr = boom }, []string{"src/main.go"}},
		"row of an ignored file target":  {func(r *failingScopedRepo) { r.filesErr = boom }, []string{"debug.log"}},
		"row of a missing target":        {func(r *failingScopedRepo) { r.filesErr = boom }, []string{"docs/gone.md"}},
		"rows below a missing target":    {func(r *failingScopedRepo) { r.underErr = boom }, []string{"docs/gone.md"}},
		"rows below a directory":         {func(r *failingScopedRepo) { r.underErr = boom }, []string{"src"}},
		"row of an ignored directory":    {func(r *failingScopedRepo) { r.filesErr = boom }, []string{"ignored-dir"}},
		"files below an ignored file":    {func(r *failingScopedRepo) { r.tracksErr = boom }, []string{"debug.log"}},
		"fingerprints":                   {func(r *failingScopedRepo) { r.statErr = boom }, []string{"src/main.go"}},
		"fingerprints below a directory": {func(r *failingScopedRepo) { r.statErr = boom }, []string{"src"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fx, repo := newFailingRepo(t)
			tc.fail(repo)
			require.ErrorIs(t, add(repo, fx.root, tc.targets...), boom)
			require.Empty(t, fx.staged(t))
		})
	}

	t.Run("refresh", func(t *testing.T) {
		fx, repo := newFailingRepo(t)
		wc, err := NewWorkingCopy(repo, fx.root)
		require.NoError(t, err)
		repo.statErr = boom
		require.ErrorIs(t, wc.RefreshStatEntries([]string{"src/main.go"}), boom)
		repo.statErr, repo.filesErr = nil, boom
		require.ErrorIs(t, wc.RefreshStatEntries([]string{"src/main.go"}), boom)
	})
}

func TestPush_ReturnsScopedReadErrors(t *testing.T) {
	boom := errors.New("boom")
	for name, fail := range map[string]func(*failingScopedRepo){
		"staged rows": func(r *failingScopedRepo) { r.filesErr = boom },
		"tree hash":   func(r *failingScopedRepo) { r.treeHashErr = boom },
	} {
		t.Run(name, func(t *testing.T) {
			fx, repo := newFailingRepo(t)
			require.NoError(t, fx.lr.StageAddMany([]string{"src/main.go"}))
			fail(repo)
			push := newTestPush(t, repo, &scalePushClient{lr: fx.lr})
			_, err := push.Plan(context.Background(), fx.root)
			require.ErrorIs(t, err, boom)
			require.ErrorIs(t, push.Run(context.Background(), fx.root, "msg"), boom)
			require.Equal(t, []string{"src/main.go"}, fx.staged(t))
		})
	}
}
