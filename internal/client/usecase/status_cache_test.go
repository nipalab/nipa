package usecase

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

type hashCount struct{ n atomic.Int64 }

func (h *hashCount) Load() int { return int(h.n.Load()) }

func countHashes(wc *WorkingCopy) *hashCount {
	calls := &hashCount{}
	orig := wc.hashFile
	wc.hashFile = func(path, encoding string) (serverDomain.Hash, error) {
		calls.n.Add(1)
		return orig(path, encoding)
	}
	return calls
}

func trackedSnapshot(t *testing.T, path, content string) *domain.Snapshot {
	t.Helper()
	return &domain.Snapshot{Files: []domain.SnapshotFile{{
		Path:      path,
		Hash:      contentHash(t, content),
		Mode:      2,
		SizeBytes: int64(len(content)),
	}}}
}

func TestWorkingCopy_Status_SecondRunUsesStatCache(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "hello")
	local := &stubLocalRepo{snapshot: trackedSnapshot(t, "a.txt", "hello")}
	wc := newWorkingCopy(t, local, root)
	calls := countHashes(wc)

	st, err := wc.Status(context.Background())
	require.NoError(t, err)
	require.Empty(t, st.Modified)
	require.Equal(t, 1, calls.Load(), "a cold status hashes the tracked file once")
	require.Contains(t, local.savedStats, "a.txt", "the validated fingerprint is cached")

	st, err = wc.Status(context.Background())
	require.NoError(t, err)
	require.Empty(t, st.Modified)
	require.Equal(t, 1, calls.Load(), "a warm status trusts the stat cache instead of rehashing")
}

func TestWorkingCopy_Status_ModifiedFileStaysFast(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "hello")
	local := &stubLocalRepo{snapshot: trackedSnapshot(t, "a.txt", "hello")}
	wc := newWorkingCopy(t, local, root)
	calls := countHashes(wc)

	_, err := wc.Status(context.Background())
	require.NoError(t, err)

	writeRepoFile(t, root, "a.txt", "hello world")
	st, err := wc.Status(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"a.txt"}, st.Modified)
	require.Equal(t, 2, calls.Load(), "the change is hashed once")

	st, err = wc.Status(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"a.txt"}, st.Modified, "a still-modified file is reported from the cache")
	require.Equal(t, 2, calls.Load(), "a modified file does not get rehashed on every status")
}

func TestWorkingCopy_Status_TouchOnlyRehashesOnce(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "hello")
	local := &stubLocalRepo{snapshot: trackedSnapshot(t, "a.txt", "hello")}
	wc := newWorkingCopy(t, local, root)
	calls := countHashes(wc)

	_, err := wc.Status(context.Background())
	require.NoError(t, err)

	fp := filepath.Join(root, "a.txt")
	info, err := os.Stat(fp)
	require.NoError(t, err)
	touched := info.ModTime().Add(-2 * time.Second)
	require.NoError(t, os.Chtimes(fp, touched, touched))

	st, err := wc.Status(context.Background())
	require.NoError(t, err)
	require.Empty(t, st.Modified, "a touch does not change content")
	require.Equal(t, 2, calls.Load(), "the touched file is rehashed once")

	st, err = wc.Status(context.Background())
	require.NoError(t, err)
	require.Empty(t, st.Modified)
	require.Equal(t, 2, calls.Load(), "the refreshed fingerprint makes later statuses stat-only")
}

func TestWorkingCopy_Status_DetectsModeChangeWithoutRehash(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "hello")
	local := &stubLocalRepo{snapshot: trackedSnapshot(t, "a.txt", "hello")}
	wc := newWorkingCopy(t, local, root)
	calls := countHashes(wc)

	_, err := wc.Status(context.Background())
	require.NoError(t, err)

	require.NoError(t, os.Chmod(filepath.Join(root, "a.txt"), 0o755))
	st, err := wc.Status(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"a.txt"}, st.Modified, "chmod is a working-copy change")
	require.Equal(t, 1, calls.Load(), "mode changes are visible from stat alone")
}

func TestWorkingCopy_Status_UnspecifiedBaseModeIsReadWrite(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "hello")
	snapshot := trackedSnapshot(t, "a.txt", "hello")
	snapshot.Files[0].Mode = 0
	wc := newWorkingCopy(t, &stubLocalRepo{snapshot: snapshot}, root)

	st, err := wc.Status(context.Background())
	require.NoError(t, err)
	require.Empty(t, st.Modified, "an old snapshot without a mode must not read as chmodded")
}

func TestWorkingCopy_Status_RacyFingerprintIsRehashed(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "hello")
	info, err := os.Stat(filepath.Join(root, "a.txt"))
	require.NoError(t, err)

	mtimeNS := info.ModTime().UnixNano()
	local := &stubLocalRepo{
		snapshot: trackedSnapshot(t, "a.txt", "hello"),
		statCache: map[string]domain.StatEntry{
			"a.txt": {
				SizeBytes: info.Size(),
				MtimeNS:   mtimeNS,
				Mode:      2,
				Hash:      contentHash(t, "hello"),
				CachedAt:  mtimeNS,
			},
		},
	}
	wc := newWorkingCopy(t, local, root)
	calls := countHashes(wc)

	st, err := wc.Status(context.Background())
	require.NoError(t, err)
	require.Empty(t, st.Modified)
	require.Equal(t, 1, calls.Load(), "a fingerprint recorded in the file's own clock tick is revalidated")
}

func TestWorkingCopy_Status_HashErrorSurfaces(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "hello")
	local := &stubLocalRepo{snapshot: trackedSnapshot(t, "a.txt", "hello")}
	wc := newWorkingCopy(t, local, root)
	wc.hashFile = func(string, string) (serverDomain.Hash, error) {
		return serverDomain.Hash{}, errors.New("permission denied")
	}

	_, err := wc.Status(context.Background())
	require.ErrorContains(t, err, "a.txt")
	require.ErrorContains(t, err, "permission denied")
}

func TestWorkingCopy_Status_FileVanishingDuringHashIsMissing(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "hello")
	local := &stubLocalRepo{snapshot: trackedSnapshot(t, "a.txt", "hello")}
	wc := newWorkingCopy(t, local, root)
	wc.hashFile = func(string, string) (serverDomain.Hash, error) {
		return serverDomain.Hash{}, fs.ErrNotExist
	}

	st, err := wc.Status(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"a.txt"}, st.Missing)
	require.Empty(t, st.Modified)
}

func TestWorkingCopy_Status_MultipleFilesStaySortedAcrossWorkers(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "aaa")
	writeRepoFile(t, root, "b/c.txt", "ccc")
	writeRepoFile(t, root, "z.txt", "zzz")
	local := &stubLocalRepo{snapshot: &domain.Snapshot{Files: []domain.SnapshotFile{
		{Path: "a.txt", Hash: contentHash(t, "old-a"), Mode: 2, SizeBytes: 3},
		{Path: "b/c.txt", Hash: contentHash(t, "ccc"), Mode: 2, SizeBytes: 3},
		{Path: "z.txt", Hash: contentHash(t, "old-z"), Mode: 2, SizeBytes: 3},
	}}}
	wc := newWorkingCopy(t, local, root)
	calls := countHashes(wc)

	st, err := wc.Status(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"a.txt", "z.txt"}, st.Modified, "parallel rehashing must keep the path order")
	require.Equal(t, 3, calls.Load())

	st, err = wc.Status(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"a.txt", "z.txt"}, st.Modified)
	require.Equal(t, 3, calls.Load(), "both modified files are served from the cache")
}

func TestWorkingCopy_Status_NoCacheRehashesEverything(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "aaa")
	writeRepoFile(t, root, "b.txt", "bbb")
	local := &stubLocalRepo{snapshot: &domain.Snapshot{Files: []domain.SnapshotFile{
		{Path: "a.txt", Hash: contentHash(t, "aaa"), Mode: 2, SizeBytes: 3},
		{Path: "b.txt", Hash: contentHash(t, "bbb"), Mode: 2, SizeBytes: 3},
	}}}
	wc := newWorkingCopy(t, local, root)
	calls := countHashes(wc)

	st, err := wc.Status(context.Background())
	require.NoError(t, err)
	require.Empty(t, st.Modified)
	require.Equal(t, 2, calls.Load(), "cold status hashes both files")

	_, err = wc.Status(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, calls.Load(), "warm status trusts the fingerprints")

	st, err = wc.Status(context.Background(), StatusOptions{NoCache: true})
	require.NoError(t, err)
	require.Empty(t, st.Modified)
	require.Equal(t, 4, calls.Load(), "--no-cache rehashes every tracked file")
	require.Contains(t, local.savedStats, "a.txt", "the forced rehash refreshes the cache")
}

func TestWorkingCopy_Status_StatCacheErrors(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "hello")

	loadErr := errors.New("cache unreadable")
	wc := newWorkingCopy(t, &stubLocalRepo{snapshot: trackedSnapshot(t, "a.txt", "hello"), statCacheErr: loadErr}, root)
	_, err := wc.Status(context.Background())
	require.ErrorIs(t, err, loadErr)

	saveErr := errors.New("cache write failed")
	wc = newWorkingCopy(t, &stubLocalRepo{snapshot: trackedSnapshot(t, "a.txt", "hello"), saveStatsErr: saveErr}, root)
	_, err = wc.Status(context.Background())
	require.ErrorIs(t, err, saveErr)
}
