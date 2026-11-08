package usecase

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

func requireReadOnly(t *testing.T, path string, want bool) {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, want, info.Mode().Perm()&0o222 == 0, "read-only state of %s (%v)", path, info.Mode())
}

func makeReadOnly(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, os.Chmod(path, 0o444))
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })
}

func TestUpdate_Run_ReplacesReadOnlyFile(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "art/hero.png", "old content")
	makeReadOnly(t, filepath.Join(root, "art", "hero.png"))
	old, oldStored := snapshotFileWithContent(t, "art/hero.png", "old content", chunker.EncodingRaw)
	fileHash, chunks, stored, fileEncoding := testEncodedFile(t, "art/hero.png", "new content")

	local := &stubLocalRepo{
		loadConfig:    &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		snapshot:      &domain.Snapshot{Files: []domain.SnapshotFile{old}},
		storedChunks:  oldStored,
		missingChunks: []serverDomain.Hash{chunks[0].Hash},
	}
	client := &stubUpdateClient{
		manifest: &serverDomain.TreeNode{
			Name: "root",
			TreeChildren: []*serverDomain.TreeNode{{
				Name: "art",
				FileChildren: []*serverDomain.File{{
					Name: "hero.png", Mode: 2, SizeBytes: int64(len("new content")),
					Encoding: fileEncoding, Hash: fileHash, Chunks: chunks,
				}},
			}},
		},
		downloadData: stored,
	}
	require.NoError(t, newTestUpdate(t, local, client).Run(context.Background(), root))

	require.Equal(t, "new content", string(readRepoFile(t, root, "art/hero.png")))
	requireReadOnly(t, filepath.Join(root, "art", "hero.png"), true)
}

func TestWorkingCopy_Restore_ReadOnlyFile(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "local edit")
	writeRepoFile(t, root, "b.txt", "local edit")
	makeReadOnly(t, filepath.Join(root, "a.txt"))
	a, aStored := snapshotFileWithContent(t, "a.txt", "original", chunker.EncodingRaw)
	b, bStored := snapshotFileWithContent(t, "b.txt", "original", chunker.EncodingRaw)
	local := &stubLocalRepo{
		snapshot:     &domain.Snapshot{Files: []domain.SnapshotFile{a, b}},
		storedChunks: mergeStored(aStored, bStored),
	}
	wc := newWorkingCopy(t, local, root)

	require.NoError(t, wc.Restore(context.Background(), []string{"a.txt", "b.txt"}))
	require.Equal(t, "original", string(readRepoFile(t, root, "a.txt")))
	require.Equal(t, "original", string(readRepoFile(t, root, "b.txt")))
	requireReadOnly(t, filepath.Join(root, "a.txt"), true)
	requireReadOnly(t, filepath.Join(root, "b.txt"), false)
}

func TestMaterializeFile_ReadOnlyDestination(t *testing.T) {
	data := []byte("payload")
	open := func(serverDomain.Hash) (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(data)), nil }
	file := materializedFile{Path: "x.bin", Mode: 2, SizeBytes: int64(len(data)), ChunkHashes: []serverDomain.Hash{chunker.Sum(data)}}

	root := t.TempDir()
	fp := filepath.Join(root, "x.bin")
	require.NoError(t, os.WriteFile(fp, []byte("old"), 0o644))
	makeReadOnly(t, fp)
	require.NoError(t, materializeFile(root, file, 0, open))
	require.Equal(t, data, readRepoFile(t, root, "x.bin"))
	requireReadOnly(t, fp, true)

	require.NoError(t, os.Chmod(fp, 0o644))
	require.NoError(t, materializeFile(root, file, 0, open))
	requireReadOnly(t, fp, false)
}

func TestReplaceFile_MissingSourceOverReadOnly(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "dst")
	require.NoError(t, os.WriteFile(dst, []byte("keep"), 0o644))
	makeReadOnly(t, dst)
	require.Error(t, replaceFile(filepath.Join(dir, "missing"), dst))
	require.Equal(t, "keep", string(readRepoFile(t, dir, "dst")))
	requireReadOnly(t, dst, true)
}

func TestIsReadOnlyFile(t *testing.T) {
	dir := t.TempDir()
	require.False(t, isReadOnlyFile(filepath.Join(dir, "absent")))
	require.False(t, isReadOnlyFile(dir))
	fp := filepath.Join(dir, "f")
	require.NoError(t, os.WriteFile(fp, nil, 0o644))
	require.False(t, isReadOnlyFile(fp))
	makeReadOnly(t, fp)
	require.True(t, isReadOnlyFile(fp))
}

func TestMaterializeFile_TrackedModeTransitions(t *testing.T) {
	data := []byte("payload")
	open := func(serverDomain.Hash) (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(data)), nil }
	hash := chunker.Sum(data)
	root := t.TempDir()
	fp := filepath.Join(root, "run.py")

	require.NoError(t, os.WriteFile(fp, []byte("old"), 0o444))
	t.Cleanup(func() { _ = os.Chmod(fp, 0o644) })
	file := materializedFile{Path: "run.py", Mode: 2, SizeBytes: int64(len(data)), ChunkHashes: []serverDomain.Hash{hash}}
	require.NoError(t, materializeFile(root, file, 1, open))
	requireReadOnly(t, fp, false)

	file.Mode = 1
	require.NoError(t, materializeFile(root, file, 2, open))
	requireReadOnly(t, fp, true)
}

func storeObjectChunks(t *testing.T, dir string, content []byte, isBinary bool) materializedFile {
	t.Helper()
	var hashes []serverDomain.Hash
	encoding, err := chunker.Encode(bytes.NewReader(content), "f", isBinary, "", func(c chunker.EncodedChunk) error {
		hashes = append(hashes, c.Hash)
		return os.WriteFile(filepath.Join(dir, c.Hash.String()), c.Data, 0o644)
	})
	require.NoError(t, err)
	return materializedFile{
		Mode:        2,
		SizeBytes:   int64(len(content)),
		Encoding:    encoding,
		FileHash:    chunker.FileHash(hashes),
		ChunkHashes: hashes,
	}
}

func TestMaterializeAll_WritesFilesFromStoredChunks(t *testing.T) {
	objects := t.TempDir()
	root := t.TempDir()
	open := func(h serverDomain.Hash) (io.ReadCloser, error) {
		return os.Open(filepath.Join(objects, h.String()))
	}

	binary := make([]byte, 3<<20)
	x := uint32(7)
	for i := range binary {
		x = x*1664525 + 1013904223
		binary[i] = byte(x >> 24)
	}
	text := bytes.Repeat([]byte("line of text\n"), 200000)

	want := map[string][]byte{}
	var jobs []materializeJob
	for i := range 12 {
		content, isBinary := text, false
		if i%2 == 0 {
			content, isBinary = binary, true
		}
		f := storeObjectChunks(t, objects, content, isBinary)
		f.Path = filepath.ToSlash(filepath.Join("dir", string(rune('a'+i)), "file"))
		want[f.Path] = content
		jobs = append(jobs, materializeJob{file: f})
	}

	entries, err := materializeAll(context.Background(), root, jobs, open)
	require.NoError(t, err)
	require.Len(t, entries, len(jobs))
	for path, content := range want {
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		require.NoError(t, err)
		require.Equal(t, content, got, path)
		require.Equal(t, int64(len(content)), entries[path].SizeBytes)
	}
}

func TestMaterializeAll_ReportsTheFailedPath(t *testing.T) {
	root := t.TempDir()
	open := func(serverDomain.Hash) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader([]byte("abc"))), nil
	}
	jobs := []materializeJob{
		{file: materializedFile{Path: "ok.txt", SizeBytes: 3, ChunkHashes: []serverDomain.Hash{{0x01}}}},
		{file: materializedFile{Path: "bad.txt", SizeBytes: 2, ChunkHashes: []serverDomain.Hash{{0x02}}}},
	}

	_, err := materializeAll(context.Background(), root, jobs, open)
	var failed *materializeError
	require.ErrorAs(t, err, &failed)
	require.Equal(t, "bad.txt", failed.path)
	require.ErrorContains(t, err, "content length mismatch")
}

func TestMaterializeAll_StopsOnCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	open := func(serverDomain.Hash) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(nil)), nil
	}
	jobs := make([]materializeJob, 50)
	for i := range jobs {
		jobs[i] = materializeJob{file: materializedFile{Path: filepath.ToSlash(filepath.Join("d", string(rune('a'+i))))}}
	}

	_, err := materializeAll(ctx, t.TempDir(), jobs, open)
	require.ErrorIs(t, err, context.Canceled)
}

func TestReadAllInto_GrowsAndReusesTheBuffer(t *testing.T) {
	data := bytes.Repeat([]byte("x"), 10000)
	got, err := readAllInto(make([]byte, 0, 4), bytes.NewReader(data))
	require.NoError(t, err)
	require.Equal(t, data, got)

	reused, err := readAllInto(got[:0], bytes.NewReader([]byte("short")))
	require.NoError(t, err)
	require.Equal(t, "short", string(reused))
	require.Equal(t, &got[:1][0], &reused[:1][0], "a large enough buffer is reused")
}

func TestRestore_TrustsAFreshStatFingerprint(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "same")
	snapshot := trackedSnapshot(t, "a.txt", "same")
	info, err := os.Stat(filepath.Join(root, "a.txt"))
	require.NoError(t, err)
	local := &stubLocalRepo{
		snapshot:  snapshot,
		statCache: map[string]domain.StatEntry{"a.txt": statEntryFromInfo(info, snapshot.Files[0].Hash)},
	}
	wc := newWorkingCopy(t, local, root)
	wc.hashFile = func(string, string) (serverDomain.Hash, error) {
		t.Fatal("a fresh fingerprint must not be rehashed")
		return serverDomain.Hash{}, nil
	}

	require.NoError(t, wc.Restore(context.Background(), []string{"a.txt"}))
}
