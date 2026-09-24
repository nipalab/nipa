package usecase

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/diff"
)

func cacheTestEntry(t *testing.T, root, path string, hash [32]byte) domain.StatEntry {
	t.Helper()
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(path)))
	require.NoError(t, err)
	return domain.StatEntry{
		SizeBytes: info.Size(),
		MtimeNS:   info.ModTime().UnixNano(),
		Mode:      2,
		Hash:      hash,
		CachedAt:  info.ModTime().Add(time.Second).UnixNano(),
	}
}

func TestDiff_WorkingDiff_TrustsStatCacheWithoutReading(t *testing.T) {
	root := t.TempDir()
	stub := newDiffStub()
	withSnapshotFile(t, stub, "a.txt", "hello\n")
	writeRepoFile(t, root, "a.txt", "hello\n")
	stub.statCache = map[string]domain.StatEntry{
		"a.txt": cacheTestEntry(t, root, "a.txt", stub.snapshot.Files[0].Hash),
	}

	fp := filepath.Join(root, "a.txt")
	info, err := os.Stat(fp)
	require.NoError(t, err)
	writeRepoFile(t, root, "a.txt", "world\n")
	require.NoError(t, os.Chtimes(fp, info.ModTime(), info.ModTime()), "same size and mtime as the cached fingerprint")

	files, err := NewDiff(nil, nil, stub).Run(context.Background(), root, nil, DiffOptions{})
	require.NoError(t, err)
	require.Empty(t, files, "the fingerprint is trusted, so the content is not read")

	files, err = NewDiff(nil, nil, stub).Run(context.Background(), root, nil, DiffOptions{NoCache: true})
	require.NoError(t, err)
	require.Len(t, files, 1, "--no-cache reads the file and finds the edit")
	require.Equal(t, diff.Modified, files[0].Change.Status)
	require.Equal(t, []byte("world\n"), files[0].New)
}

func TestDiff_WorkingDiff_ModeChangeWithCache(t *testing.T) {
	root := t.TempDir()
	stub := newDiffStub()
	withSnapshotFile(t, stub, "run.sh", "echo\n")
	writeRepoFile(t, root, "run.sh", "echo\n")
	stub.statCache = map[string]domain.StatEntry{
		"run.sh": cacheTestEntry(t, root, "run.sh", stub.snapshot.Files[0].Hash),
	}
	require.NoError(t, os.Chmod(filepath.Join(root, "run.sh"), 0o755))

	files, err := NewDiff(nil, nil, stub).Run(context.Background(), root, nil, DiffOptions{})
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, diff.Modified, files[0].Change.Status)
	require.Equal(t, 2, files[0].Change.Old.Mode)
	require.Equal(t, 3, files[0].Change.New.Mode, "mode changes are visible from stat alone")
}

func TestDiff_WorkingDiff_StaleFingerprintReadsFile(t *testing.T) {
	root := t.TempDir()
	stub := newDiffStub()
	withSnapshotFile(t, stub, "a.txt", "hello\n")
	writeRepoFile(t, root, "a.txt", "world\n")

	entry := cacheTestEntry(t, root, "a.txt", stub.snapshot.Files[0].Hash)
	entry.MtimeNS-- // fingerprint no longer matches the file
	stub.statCache = map[string]domain.StatEntry{"a.txt": entry}

	files, err := NewDiff(nil, nil, stub).Run(context.Background(), root, nil, DiffOptions{})
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, diff.Modified, files[0].Change.Status)
	require.Equal(t, []byte("world\n"), files[0].New)
}

func TestDiff_WorkingDiff_StatCacheErrors(t *testing.T) {
	root := t.TempDir()
	stub := newDiffStub()
	withSnapshotFile(t, stub, "a.txt", "hello\n")
	writeRepoFile(t, root, "a.txt", "hello\n")
	stub.statErr = errors.New("cache unreadable")

	_, err := NewDiff(nil, nil, stub).Run(context.Background(), root, nil, DiffOptions{})
	require.ErrorIs(t, err, stub.statErr)

	_, err = NewDiff(nil, nil, stub).Run(context.Background(), root, nil, DiffOptions{NoCache: true})
	require.NoError(t, err, "--no-cache must not read the stat cache")
}
