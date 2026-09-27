package usecase

import (
	"context"
	"errors"
	"io/fs"
	"testing"

	"github.com/stretchr/testify/require"

	serverDomain "github.com/nipalab/nipa/internal/domain"
)

func TestWorkingCopy_RefreshStatEntries(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "hello")
	local := &stubLocalRepo{snapshot: trackedSnapshot(t, "a.txt", "old")}
	wc := newWorkingCopy(t, local, root)
	calls := countHashes(wc)

	require.NoError(t, wc.RefreshStatEntries(nil))
	require.NoError(t, wc.RefreshStatEntries([]string{"a.txt"}))
	require.Equal(t, 1, calls.Load(), "the dirty file is hashed once")
	entry, ok := local.savedStats["a.txt"]
	require.True(t, ok)
	require.Equal(t, contentHash(t, "hello"), entry.Hash)

	st, err := wc.Status(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"a.txt"}, st.Modified)
	require.Equal(t, 1, calls.Load(), "status must trust the reconciled fingerprint")

	require.NoError(t, wc.RefreshStatEntries([]string{"a.txt"}))
	require.Equal(t, 1, calls.Load(), "a fresh fingerprint skips the rehash")
}

func TestWorkingCopy_RefreshStatEntries_SkipsUntrackedAndMissing(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "untracked.txt", "new")
	local := &stubLocalRepo{snapshot: trackedSnapshot(t, "a.txt", "hello")}
	wc := newWorkingCopy(t, local, root)
	calls := countHashes(wc)

	require.NoError(t, wc.RefreshStatEntries([]string{"untracked.txt", "a.txt", "tracked.txt"}))
	require.Empty(t, local.savedStats, "untracked and missing paths are never reconciled")
	require.Zero(t, calls.Load())
}

func TestWorkingCopy_RefreshStatEntries_Errors(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "hello")

	snapErr := errors.New("snapshot unreadable")
	wc := newWorkingCopy(t, &stubLocalRepo{snapshotErr: snapErr}, root)
	require.ErrorIs(t, wc.RefreshStatEntries([]string{"a.txt"}), snapErr)

	loadErr := errors.New("cache unreadable")
	wc = newWorkingCopy(t, &stubLocalRepo{
		snapshot:     trackedSnapshot(t, "a.txt", "hello"),
		statCacheErr: loadErr,
	}, root)
	require.ErrorIs(t, wc.RefreshStatEntries([]string{"a.txt"}), loadErr)

	wc = newWorkingCopy(t, &stubLocalRepo{snapshot: trackedSnapshot(t, "a.txt", "hello")}, root)
	wc.hashFile = func(string, string) (serverDomain.Hash, error) {
		return serverDomain.Hash{}, fs.ErrPermission
	}
	require.ErrorContains(t, wc.RefreshStatEntries([]string{"a.txt"}), "a.txt")

	wc = newWorkingCopy(t, &stubLocalRepo{
		snapshot:     trackedSnapshot(t, "a.txt", "hello"),
		saveStatsErr: errors.New("cache write failed"),
	}, root)
	require.ErrorContains(t, wc.RefreshStatEntries([]string{"a.txt"}), "cache write failed")
}
