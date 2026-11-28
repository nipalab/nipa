package usecase

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/diff"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

// countingHash wraps hashWorkingFile and counts the files it reads.
type countingHash struct{ paths []string }

func (c *countingHash) hash(abs, rel string, size int64, encoding string) (hashedFile, error) {
	c.paths = append(c.paths, rel)
	return hashWorkingFile(abs, rel, size, encoding)
}

// wholeRepoGuard is a real localrepo whose whole-repository reads fail, so a
// test proves a filtered diff only reads by path.
type wholeRepoGuard struct {
	*localrepo.LocalRepo
}

func (g *wholeRepoGuard) Snapshot() (*domain.Snapshot, error) {
	return nil, errors.New("Snapshot read the whole repository")
}

func (g *wholeRepoGuard) LoadStatCache() (map[string]domain.StatEntry, error) {
	return nil, errors.New("LoadStatCache read the whole repository")
}

func TestDiff_ScopedPaths_ReadsOnlyTheRowsAndFilesTheyName(t *testing.T) {
	fx := newDiffScaleRepo(t, 300, 1)
	target := scalePath(150)
	other := scalePath(10)
	fx.write(t, target, "edited\n")
	fx.write(t, other, "edited too\n")

	if runtime.GOOS != "windows" && os.Geteuid() != 0 {
		// A walk of the whole tree would fail on this folder.
		locked := filepath.Join(fx.root, "locked")
		require.NoError(t, os.MkdirAll(filepath.Join(locked, "inner"), 0o755))
		require.NoError(t, os.Chmod(locked, 0))
		t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	}

	lr := localrepo.NewLocalRepo()
	t.Cleanup(func() { _ = lr.Close() })
	counter := &countingHash{}
	d := NewDiff(nil, nil, &wholeRepoGuard{LocalRepo: lr})
	d.hashFile = counter.hash
	files, err := d.Run(context.Background(), fx.root, nil, DiffOptions{Paths: []string{target}})
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, target, files[0].Change.Path)
	require.Equal(t, diff.Modified, files[0].Change.Status)
	require.Equal(t, []string{target}, counter.paths, "only the named file is read")

	// A folder filter is a prefix: it covers every file below it.
	dir := target[:strings.LastIndexByte(target, '/')]
	counter.paths = nil
	files, err = d.Run(context.Background(), fx.root, nil, DiffOptions{Paths: []string{"./" + dir + "/"}})
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, target, files[0].Change.Path)
	require.Equal(t, []string{target}, counter.paths, "unchanged fingerprints below the folder are trusted")

	files, err = d.Run(context.Background(), fx.root, nil, DiffOptions{Paths: []string{target, other}})
	require.NoError(t, err)
	require.Len(t, files, 2)

	// The modified binary is reported from its fingerprint.
	counter.paths = nil
	files, err = d.Run(context.Background(), fx.root, nil, DiffOptions{Paths: []string{scaleBinaryPath}})
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.True(t, files[0].Change.New.IsBinary)
	require.Empty(t, counter.paths)
}

func TestDiff_ScopedPaths_RootFilterReadsEverything(t *testing.T) {
	fx := newDiffScaleRepo(t, 20, 1)
	lr := localrepo.NewLocalRepo()
	t.Cleanup(func() { _ = lr.Close() })
	_, err := NewDiff(nil, nil, &wholeRepoGuard{LocalRepo: lr}).Run(context.Background(), fx.root, nil, DiffOptions{Paths: []string{"."}})
	require.NoError(t, err, `"." is not the root for pathMatches, so it reads by path`)
	_, err = NewDiff(nil, nil, &wholeRepoGuard{LocalRepo: lr}).Run(context.Background(), fx.root, nil, DiffOptions{Paths: []string{"./"}})
	require.ErrorContains(t, err, "whole repository", `"./" is the root and loads the snapshot`)

	files, err := NewDiff(nil, nil, lr).Run(context.Background(), fx.root, nil, DiffOptions{Paths: []string{"/"}})
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, scaleBinaryPath, files[0].Change.Path)
}

// scopeFixture is a working copy that exercises every case where the walk
// leaves a tracked or staged path out or keeps an ignored one in.
func scopeFixture(t *testing.T) (string, *stubDiffRepo) {
	t.Helper()
	root := t.TempDir()
	stub := newDiffStub()
	for _, p := range []string{"a/one.txt", "a/two.txt", "a/sub/three.txt", "build/out.txt", "gone.txt", "dirnow.txt", "ab.txt", "b/x.txt"} {
		withSnapshotFile(t, stub, p, "base "+p+"\n")
	}
	writeRepoFile(t, root, ".nipaignore", "build/\n*.log\n")
	writeRepoFile(t, root, "a/one.txt", "changed one\n")
	writeRepoFile(t, root, "a/two.txt", "base a/two.txt\n")
	writeRepoFile(t, root, "a/sub/three.txt", "changed three\n")
	writeRepoFile(t, root, "a/sub/untracked.log", "ignored\n")
	writeRepoFile(t, root, "a/untracked.txt", "untracked\n")
	writeRepoFile(t, root, "build/out.txt", "changed build\n")
	writeRepoFile(t, root, "build/other.txt", "ignored and untracked\n")
	writeRepoFile(t, root, "ab.txt", "changed ab\n")
	writeRepoFile(t, root, "a/staged.log", "staged although ignored\n")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "dirnow.txt"), 0o755))
	if runtime.GOOS != "windows" {
		// A tracked folder replaced by a link: the walk does not follow it.
		require.NoError(t, os.MkdirAll(filepath.Join(root, "elsewhere"), 0o755))
		writeRepoFile(t, root, "elsewhere/x.txt", "behind a link\n")
		require.NoError(t, os.Symlink(filepath.Join(root, "elsewhere"), filepath.Join(root, "b")))
	}
	stub.staged = []string{"a/staged.log", "staged-gone.txt"}
	return root, stub
}

func diffSummary(files []diff.FileDiff) []string {
	var out []string
	for _, f := range files {
		out = append(out, f.Change.Status.String()+" "+f.Change.Path+" "+string(f.New))
	}
	return out
}

func TestDiff_ScopedPaths_MatchesTheFilteredWalk(t *testing.T) {
	root, stub := scopeFixture(t)
	full, err := NewDiff(nil, nil, stub).Run(context.Background(), root, nil, DiffOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, full)

	for _, filters := range [][]string{
		{"a"}, {"a/"}, {"a/one.txt"}, {"a/sub"}, {"build"}, {"build/out.txt"}, {"gone.txt"},
		{"dirnow.txt"}, {"a", "ab.txt"}, {"b"}, {"b/x.txt"}, {"a/staged.log"}, {"missing"},
		{"staged-gone.txt"}, {".nipaignore"},
	} {
		want := []diff.FileDiff{}
		for _, f := range full {
			if pathMatches(f.Change.Path, filters) {
				want = append(want, f)
			}
		}
		got, err := NewDiff(nil, nil, stub).Run(context.Background(), root, nil, DiffOptions{Paths: filters})
		require.NoError(t, err, filters)
		require.Equal(t, diffSummary(want), diffSummary(got), "filters %v", filters)

		staged, err := NewDiff(nil, nil, stub).Run(context.Background(), root, nil, DiffOptions{Paths: filters, Staged: true})
		require.NoError(t, err)
		for _, f := range staged {
			require.Contains(t, stub.staged, f.Change.Path)
		}
	}
	// Ignore rules hold for paths that are neither tracked nor staged.
	got, err := NewDiff(nil, nil, stub).Run(context.Background(), root, nil, DiffOptions{Paths: []string{"build/other.txt", "a/sub/untracked.log"}})
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestWalkReaches(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a/b.txt", "x")
	writeRepoFile(t, root, ".nipa/x", "x")
	dirs := map[string]bool{}
	for path, want := range map[string]bool{
		"a/b.txt": true, "a": false, "a/missing": false, "missing/b.txt": false,
		".nipa/x": false, "a//b.txt": false, "a/b.txt/c": false,
	} {
		got, err := walkReaches(root, path, dirs)
		require.NoError(t, err, path)
		require.Equal(t, want, got, path)
	}
}

// binaryFixture is a tracked binary that was modified after the sync, with a
// fingerprint status recorded for its new content.
func binaryFixture(t *testing.T) (string, *stubDiffRepo, serverDomain.Hash) {
	t.Helper()
	root := t.TempDir()
	stub := newDiffStub()
	withSnapshotFile(t, stub, "model.bin", "\x00old binary")
	writeRepoFile(t, root, "model.bin", "\x00new binary!")
	hash, err := hashWorkingPath(root, "model.bin", "")
	require.NoError(t, err)
	stub.statCache = map[string]domain.StatEntry{"model.bin": cacheTestEntry(t, root, "model.bin", hash)}
	return root, stub, hash
}

func TestDiff_ModifiedBinary_UsesCachedHash(t *testing.T) {
	root, stub, hash := binaryFixture(t)
	counter := &countingHash{}
	d := NewDiff(nil, nil, stub)
	d.hashFile = counter.hash

	files, err := d.Run(context.Background(), root, nil, DiffOptions{Paths: []string{"model.bin"}})
	require.NoError(t, err)
	require.Empty(t, counter.paths, "a fresh fingerprint of a binary is not rehashed")
	require.Len(t, files, 1)
	c := files[0].Change
	require.Equal(t, diff.Modified, c.Status)
	require.Equal(t, hash, c.New.Hash)
	require.True(t, c.New.IsBinary)
	require.Equal(t, int64(len("\x00new binary!")), c.New.SizeBytes)
	require.Equal(t, chunker.EncodingRaw, c.New.Encoding)
	require.Empty(t, c.New.ChunkHashes)
	require.Equal(t, []string{"diff --nipa a/model.bin b/model.bin", "Binary files a/model.bin and b/model.bin differ"}, diff.FilePatch(files[0], diff.Options{}))

	// The whole-tree diff takes the same shortcut.
	files, err = d.Run(context.Background(), root, nil, DiffOptions{})
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Empty(t, counter.paths)

	// The rehash would agree with the cached hash.
	files, err = d.Run(context.Background(), root, nil, DiffOptions{NoCache: true})
	require.NoError(t, err)
	require.Equal(t, []string{"model.bin"}, counter.paths, "--no-cache always rehashes")
	require.Equal(t, hash, files[0].Change.New.Hash)
	require.NotEmpty(t, files[0].Change.New.ChunkHashes)
}

func TestDiff_ModifiedBinary_StaleFingerprintRehashes(t *testing.T) {
	root, stub, _ := binaryFixture(t)
	writeRepoFile(t, root, "model.bin", "\x00newer binary, other size")
	counter := &countingHash{}
	d := NewDiff(nil, nil, stub)
	d.hashFile = counter.hash
	files, err := d.Run(context.Background(), root, nil, DiffOptions{Paths: []string{"model.bin"}})
	require.NoError(t, err)
	require.Equal(t, []string{"model.bin"}, counter.paths)
	want, err := hashWorkingPath(root, "model.bin", "")
	require.NoError(t, err)
	require.Equal(t, want, files[0].Change.New.Hash)
}

func TestDiff_ModifiedBinaryNowText_Rehashes(t *testing.T) {
	root, stub, _ := binaryFixture(t)
	fp := filepath.Join(root, "model.bin")
	info, err := os.Stat(fp)
	require.NoError(t, err)
	// Same size and mtime, so the fingerprint still matches, but text now.
	require.Len(t, "text version", int(info.Size()))
	writeRepoFile(t, root, "model.bin", "text version")
	require.NoError(t, os.Chtimes(fp, info.ModTime(), info.ModTime()))

	counter := &countingHash{}
	d := NewDiff(nil, nil, stub)
	d.hashFile = counter.hash
	files, err := d.Run(context.Background(), root, nil, DiffOptions{Paths: []string{"model.bin"}})
	require.NoError(t, err)
	require.Equal(t, []string{"model.bin"}, counter.paths, "text content is read for its hunks")
	require.False(t, files[0].Change.New.IsBinary)
	require.Equal(t, []byte("text version"), files[0].New)
}

func TestDiff_ModifiedText_WithFreshFingerprintStillHashes(t *testing.T) {
	root := t.TempDir()
	stub := newDiffStub()
	withSnapshotFile(t, stub, "a.txt", "old\n")
	writeRepoFile(t, root, "a.txt", "new\n")
	hash, err := hashWorkingPath(root, "a.txt", "")
	require.NoError(t, err)
	stub.statCache = map[string]domain.StatEntry{"a.txt": cacheTestEntry(t, root, "a.txt", hash)}
	counter := &countingHash{}
	d := NewDiff(nil, nil, stub)
	d.hashFile = counter.hash
	files, err := d.Run(context.Background(), root, nil, DiffOptions{Paths: []string{"a.txt"}})
	require.NoError(t, err)
	require.Equal(t, []string{"a.txt"}, counter.paths)
	require.Equal(t, []byte("new\n"), files[0].New)
}

func TestDiff_ModifiedBinary_VanishedBeforeProbe(t *testing.T) {
	root, stub, _ := binaryFixture(t)
	d := NewDiff(nil, nil, stub)
	entry, _, ok, err := d.scanFile(root, "model.bin", diff.Entry{Path: "model.bin", Hash: stub.snapshot.Files[0].Hash}, true, stub.statCache, false)
	require.NoError(t, err)
	require.True(t, ok)
	require.True(t, entry.IsBinary)

	_, err = probeBinaryFile(filepath.Join(root, "missing"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestDiff_OversizedText_IsNotReadWhole(t *testing.T) {
	root := t.TempDir()
	stub := newDiffStub()
	old := strings.Repeat("old line\n", 400)
	withSnapshotFile(t, stub, "big.txt", old)
	big := bytes.Repeat([]byte("new line of a large text file\n"), (inlineContentMax/30)+1000)
	require.Greater(t, len(big), inlineContentMax, "streamed by the hash, not kept inline")
	writeRepoFile(t, root, "big.txt", string(big))
	writeRepoFile(t, root, "small.txt", "x\n")
	withSnapshotFile(t, stub, "small.txt", "y\n")

	const limit = 1024
	files, err := NewDiff(nil, nil, stub).Run(context.Background(), root, nil, DiffOptions{MaxTextBytes: limit})
	require.NoError(t, err)
	require.Len(t, files, 2)
	f := files[0]
	require.Equal(t, "big.txt", f.Change.Path)
	require.True(t, f.Truncated)
	require.Len(t, f.New, limit+1, "only enough to tell it is too large")
	require.Len(t, f.Old, limit+1)
	res := diff.FileHunksBudget(f, diff.Options{}, diff.DefaultBudget)
	require.True(t, res.Omitted)
	require.True(t, res.Approx)
	require.Empty(t, res.Hunks)
	require.Positive(t, res.Additions)
	require.Contains(t, diff.FilePatch(f, diff.Options{}), "content too large to diff")

	require.False(t, files[1].Truncated)
	require.Equal(t, []byte("x\n"), files[1].New)

	// A working text small enough to be kept inline is clipped to the cap too.
	mid := strings.Repeat("inline text line\n", 200)
	require.Less(t, len(mid), inlineContentMax)
	writeRepoFile(t, root, "mid.txt", mid)
	withSnapshotFile(t, stub, "mid.txt", "short\n")
	files, err = NewDiff(nil, nil, stub).Run(context.Background(), root, nil, DiffOptions{Paths: []string{"mid.txt"}, MaxTextBytes: limit})
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.True(t, files[0].Truncated)
	require.Len(t, files[0].New, limit+1)
	require.Equal(t, []byte(mid[:limit+1]), files[0].New)

	// Without a cap everything is read, as the CLI does.
	files, err = NewDiff(nil, nil, stub).Run(context.Background(), root, nil, DiffOptions{Paths: []string{"big.txt"}})
	require.NoError(t, err)
	require.False(t, files[0].Truncated)
	require.Len(t, files[0].New, len(big))
}

func TestReadFileLimit(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "f", "0123456789")
	fp := filepath.Join(root, "f")
	for limit, want := range map[int64]string{-1: "0123456789", 3: "0123", 10: "0123456789", 20: "0123456789"} {
		got, err := readFileLimit(fp, limit)
		require.NoError(t, err)
		require.Equal(t, want, string(got))
	}
	_, err := readFileLimit(filepath.Join(root, "missing"), 3)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestDiffScaleFixture_FingerprintsMatchDisk(t *testing.T) {
	fx := newDiffScaleRepo(t, 50, 1)
	stats, err := fx.lr.LoadStatCache()
	require.NoError(t, err)
	info, err := os.Stat(filepath.Join(fx.root, filepath.FromSlash(scalePath(3))))
	require.NoError(t, err)
	require.True(t, statMatches(stats[scalePath(3)], info))
	require.Less(t, info.ModTime(), time.Now())
}

func TestDiff_ScopedPaths_ManyFiltersBatchTheirReads(t *testing.T) {
	fx := newDiffScaleRepo(t, 300, 1)
	fx.write(t, scalePath(150), "edited\n")
	fx.write(t, scalePath(20), "edited too\n")

	lr := localrepo.NewLocalRepo()
	t.Cleanup(func() { _ = lr.Close() })
	counter := &readCountingRepo{LocalRepo: lr}
	// 100 tracked files, a folder, a filter the folder covers (twice, as
	// written two ways) and a path that is not tracked.
	var filters []string
	for i := 100; i < 200; i++ {
		filters = append(filters, scalePath(i))
	}
	dir := scalePath(0)[:strings.LastIndexByte(scalePath(0), '/')]
	filters = append(filters, "nope/x", dir+"/", "./"+dir, strings.Split(dir, "/")[0])

	files, err := NewDiff(nil, nil, counter).Run(context.Background(), fx.root, nil, DiffOptions{Paths: filters})
	require.NoError(t, err)
	require.Len(t, files, 2)
	require.Equal(t, scalePath(20), files[0].Change.Path)
	require.Equal(t, scalePath(150), files[1].Change.Path)
	require.Zero(t, counter.snapshots)
	require.Equal(t, 2, counter.dirReads, "only the folder and the untracked path are read as folders; the covered filter is not")
	// One batched snapshot read for every filter, plus the fingerprint reads.
	require.Equal(t, 3, counter.pathReads)
}

func TestDiff_SimilarityLoader_SkipsTextAboveTheCap(t *testing.T) {
	// No local repo: a load from the object cache would panic, so a decline
	// proves nothing was read.
	d := NewDiff(nil, nil, nil)
	big := diff.Entry{Path: "big.txt", SizeBytes: 11}
	_, ok := d.similarityLoader("", nil, 10)(big)
	require.False(t, ok, "a revision side above the cap falls back to chunk overlap")
	_, ok = d.similarityLoader("/root", map[string][]byte{"big.txt": []byte("01234567890")}, 10)(big)
	require.False(t, ok, "so does a working side above the cap, even when held inline")

	small := diff.Entry{Path: "small.txt", SizeBytes: 3}
	content, ok := d.similarityLoader("/root", map[string][]byte{"small.txt": []byte("abc")}, 10)(small)
	require.True(t, ok)
	require.Equal(t, []byte("abc"), content)
	content, ok = d.similarityLoader("/root", map[string][]byte{"big.txt": []byte("01234567890")}, 0)(big)
	require.True(t, ok, "without a cap the inline content is used")
	require.Equal(t, []byte("01234567890"), content)
}
