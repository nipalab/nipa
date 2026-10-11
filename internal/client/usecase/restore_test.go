package usecase

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

// snapshotFileWithContent builds a snapshot entry plus the stored chunk bytes
// needed to materialize it, using the given stored encoding.
func snapshotFileWithContent(t *testing.T, path, content, encoding string) (domain.SnapshotFile, map[serverDomain.Hash][]byte) {
	t.Helper()
	data := []byte(content)
	enc, chunks, err := chunker.EncodeBytes(data, path, chunker.IsBinary(data), encoding)
	require.NoError(t, err)
	require.Equal(t, encoding, enc)
	hashes := make([]serverDomain.Hash, 0, len(chunks))
	stored := make(map[serverDomain.Hash][]byte, len(chunks))
	for _, c := range chunks {
		hashes = append(hashes, c.Hash)
		stored[c.Hash] = c.Data
	}
	return domain.SnapshotFile{
		Path:      path,
		Hash:      chunker.FileHash(hashes),
		Encoding:  enc,
		SizeBytes: int64(len(data)),
		Chunks:    hashes,
	}, stored
}

func mergeStored(sets ...map[serverDomain.Hash][]byte) map[serverDomain.Hash][]byte {
	out := make(map[serverDomain.Hash][]byte)
	for _, set := range sets {
		for hash, data := range set {
			out[hash] = data
		}
	}
	return out
}

func TestWorkingCopy_Restore_ModifiedFile(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "local edit")
	snap, stored := snapshotFileWithContent(t, "a.txt", "original", chunker.EncodingRaw)
	local := &stubLocalRepo{
		snapshot:     &domain.Snapshot{Files: []domain.SnapshotFile{snap}},
		storedChunks: stored,
	}
	wc := newWorkingCopy(t, local, root)

	require.NoError(t, wc.Restore(context.Background(), []string{"a.txt"}))
	require.Equal(t, "original", string(readRepoFile(t, root, "a.txt")))
	entry, ok := local.savedStats["a.txt"]
	require.True(t, ok, "restore refreshes the stat cache")
	require.Equal(t, snap.Hash, entry.Hash)
	require.Equal(t, int64(len("original")), entry.SizeBytes)
}

func TestWorkingCopy_Restore_DeletedTrackedFile(t *testing.T) {
	root := t.TempDir()
	snap, stored := snapshotFileWithContent(t, "gone.txt", "restored content", chunker.EncodingRaw)
	local := &stubLocalRepo{
		snapshot:     &domain.Snapshot{Files: []domain.SnapshotFile{snap}},
		storedChunks: stored,
	}
	wc := newWorkingCopy(t, local, root)

	require.NoError(t, wc.Restore(context.Background(), []string{"gone.txt"}))
	require.Equal(t, "restored content", string(readRepoFile(t, root, "gone.txt")))
}

func TestWorkingCopy_Restore_Directory(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "docs/a.txt", "edit a")
	writeRepoFile(t, root, "docs/sub/b.txt", "edit b")
	writeRepoFile(t, root, "other.txt", "keep me")
	a, aStored := snapshotFileWithContent(t, "docs/a.txt", "a", chunker.EncodingRaw)
	b, bStored := snapshotFileWithContent(t, "docs/sub/b.txt", "b", chunker.EncodingRaw)
	other, otherStored := snapshotFileWithContent(t, "other.txt", "other", chunker.EncodingRaw)
	local := &stubLocalRepo{
		snapshot:     &domain.Snapshot{Files: []domain.SnapshotFile{a, b, other}},
		storedChunks: mergeStored(aStored, bStored, otherStored),
	}
	wc := newWorkingCopy(t, local, root)

	require.NoError(t, wc.Restore(context.Background(), []string{"docs"}))
	require.Equal(t, "a", string(readRepoFile(t, root, "docs/a.txt")))
	require.Equal(t, "b", string(readRepoFile(t, root, "docs/sub/b.txt")))
	require.Equal(t, "keep me", string(readRepoFile(t, root, "other.txt")), "paths outside the target are untouched")
}

func TestWorkingCopy_Restore_UntrackedPath(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "local.txt", "mine")
	wc := newWorkingCopy(t, &stubLocalRepo{snapshot: &domain.Snapshot{}}, root)

	err := wc.Restore(context.Background(), []string{"local.txt"})
	require.ErrorContains(t, err, "not tracked")
	require.Equal(t, "mine", string(readRepoFile(t, root, "local.txt")))
}

func TestWorkingCopy_Restore_RefusesStagedPaths(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "edited")
	writeRepoFile(t, root, "b.txt", "edited")
	a, aStored := snapshotFileWithContent(t, "a.txt", "a", chunker.EncodingRaw)
	b, bStored := snapshotFileWithContent(t, "b.txt", "b", chunker.EncodingRaw)
	local := &stubLocalRepo{
		snapshot:     &domain.Snapshot{Files: []domain.SnapshotFile{a, b}},
		storedChunks: mergeStored(aStored, bStored),
		staged:       []string{"b.txt"},
	}
	wc := newWorkingCopy(t, local, root)

	err := wc.Restore(context.Background(), []string{"a.txt", "b.txt"})
	require.ErrorContains(t, err, "staged changes")
	require.Equal(t, "edited", string(readRepoFile(t, root, "a.txt")), "nothing is restored when any target is staged")
	require.Equal(t, "edited", string(readRepoFile(t, root, "b.txt")))
}

func TestWorkingCopy_Restore_UnchangedFileIsNotRewritten(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "same")
	snap, _ := snapshotFileWithContent(t, "a.txt", "same", chunker.EncodingRaw)
	local := &stubLocalRepo{snapshot: &domain.Snapshot{Files: []domain.SnapshotFile{snap}}}
	wc := newWorkingCopy(t, local, root)

	before, err := os.Stat(filepath.Join(root, "a.txt"))
	require.NoError(t, err)

	require.NoError(t, wc.Restore(context.Background(), []string{"a.txt"}))
	after, err := os.Stat(filepath.Join(root, "a.txt"))
	require.NoError(t, err)
	require.Equal(t, before.ModTime(), after.ModTime(), "an unchanged file keeps its mtime")
	require.Empty(t, local.savedStats)
}

func TestWorkingCopy_Restore_ZstdContent(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "different")
	snap, stored := snapshotFileWithContent(t, "a.txt", "compressed text", chunker.EncodingZstd)
	local := &stubLocalRepo{
		snapshot:     &domain.Snapshot{Files: []domain.SnapshotFile{snap}},
		storedChunks: stored,
	}
	wc := newWorkingCopy(t, local, root)

	require.NoError(t, wc.Restore(context.Background(), []string{"a.txt"}))
	require.Equal(t, "compressed text", string(readRepoFile(t, root, "a.txt")))
}

func TestWorkingCopy_Restore_MissingChunk(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "edited")
	snap, _ := snapshotFileWithContent(t, "a.txt", "original", chunker.EncodingRaw)
	local := &stubLocalRepo{snapshot: &domain.Snapshot{Files: []domain.SnapshotFile{snap}}}
	wc := newWorkingCopy(t, local, root)

	err := wc.Restore(context.Background(), []string{"a.txt"})
	require.ErrorContains(t, err, "restore a.txt")
	require.ErrorContains(t, err, "chunk not found")
}

func TestWorkingCopy_Restore_RejectsNipaPath(t *testing.T) {
	wc := newWorkingCopy(t, &stubLocalRepo{snapshot: &domain.Snapshot{}}, t.TempDir())
	err := wc.Restore(context.Background(), []string{".nipa/config"})
	require.ErrorContains(t, err, ".nipa")
}

func TestWorkingCopy_Restore_RejectsEscapingPath(t *testing.T) {
	wc := newWorkingCopy(t, &stubLocalRepo{snapshot: &domain.Snapshot{}}, t.TempDir())
	err := wc.Restore(context.Background(), []string{"../outside"})
	require.ErrorContains(t, err, "outside the repository")
}

func TestWorkingCopy_Restore_RejectsEmptyTarget(t *testing.T) {
	wc := newWorkingCopy(t, &stubLocalRepo{snapshot: &domain.Snapshot{}}, t.TempDir())
	err := wc.Restore(context.Background(), []string{""})
	require.ErrorContains(t, err, "path is required")
}
