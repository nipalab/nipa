package usecase

import (
	"context"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/diff"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

const scaleBinaryPath = "big.bin"

// newDiffScaleRepo is a scale clone of n tracked text files, all on disk with
// fingerprints that match and a cached object each, plus one tracked binary
// of mb MiB that was modified after the sync and whose new fingerprint status
// already recorded, as a desktop FileDiff click finds it.
func newDiffScaleRepo(tb testing.TB, n, mb int) *scaleRepo {
	tb.Helper()
	oldHash := scaleHash(0xabcdef, 7)
	fx := newScaleRepo(tb, n, true, &serverDomain.File{
		Name: scaleBinaryPath, Hash: oldHash, Mode: 2, SizeBytes: int64(mb) << 20, IsBinary: true,
		Chunks: []serverDomain.Chunk{{Hash: scaleHash(0xabcdef, 8), SizeBytes: int64(mb) << 20}},
	})
	stats, err := fx.lr.LoadStatCache()
	require.NoError(tb, err)
	content := make([]byte, 64)
	for p, e := range stats {
		abs := filepath.Join(fx.root, filepath.FromSlash(p))
		require.NoError(tb, os.MkdirAll(filepath.Dir(abs), 0o755))
		require.NoError(tb, os.WriteFile(abs, content, 0o644))
		mtime := time.Unix(0, e.MtimeNS)
		require.NoError(tb, os.Chtimes(abs, mtime, mtime))
	}

	abs := filepath.Join(fx.root, scaleBinaryPath)
	f, err := os.Create(abs)
	require.NoError(tb, err)
	block := make([]byte, 1<<20)
	rng := rand.New(rand.NewSource(1))
	_, _ = rng.Read(block)
	block[0] = 0
	for i := 0; i < mb; i++ {
		block[1] = byte(i)
		_, err := f.Write(block)
		require.NoError(tb, err)
	}
	require.NoError(tb, f.Close())
	hash, err := hashWorkingPath(fx.root, scaleBinaryPath, "")
	require.NoError(tb, err)
	require.NotEqual(tb, oldHash, hash)
	info, err := os.Stat(abs)
	require.NoError(tb, err)
	require.NoError(tb, fx.lr.SaveStatEntries(map[string]domain.StatEntry{scaleBinaryPath: {
		SizeBytes: info.Size(), MtimeNS: info.ModTime().UnixNano(), Mode: 2, Hash: hash,
		CachedAt: info.ModTime().Add(time.Second).UnixNano(),
	}}))
	return fx
}

func runScaleFileDiff(tb testing.TB, fx *scaleRepo, opts DiffOptions) []diff.FileDiff {
	tb.Helper()
	lr := localrepo.NewLocalRepo()
	defer func() { _ = lr.Close() }()
	files, err := NewDiff(nil, nil, lr).Run(context.Background(), fx.root, nil, opts)
	require.NoError(tb, err)
	return files
}

// BenchmarkFileDiffModifiedBinary is the desktop FileDiff click on one
// modified binary in a clone of ~10k files: DiffFiles with one path. Size it
// with NIPA_BENCH_DIFF_MB (default 256) and NIPA_BENCH_DIFF_FILES (default
// 10000).
func BenchmarkFileDiffModifiedBinary(b *testing.B) {
	mb, _ := strconv.Atoi(os.Getenv("NIPA_BENCH_DIFF_MB"))
	if mb <= 0 {
		mb = 256
	}
	n, _ := strconv.Atoi(os.Getenv("NIPA_BENCH_DIFF_FILES"))
	if n <= 0 {
		n = 10_000
	}
	fx := newDiffScaleRepo(b, n, mb)
	for _, bc := range []struct {
		name string
		opts DiffOptions
	}{
		{"one-path", DiffOptions{Paths: []string{scaleBinaryPath}}},
		{"one-path-nocache", DiffOptions{Paths: []string{scaleBinaryPath}, NoCache: true}},
		{"whole-tree", DiffOptions{}},
	} {
		b.Run(bc.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				files := runScaleFileDiff(b, fx, bc.opts)
				if len(files) != 1 || files[0].Change.Status != diff.Modified {
					b.Fatalf("unexpected diff: %+v", files)
				}
			}
		})
	}
}
