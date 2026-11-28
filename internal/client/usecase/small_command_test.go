package usecase

import (
	"context"
	"errors"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

// wholeRepoPins is the local repository as lock, unlock and blame used it
// before they read by path: no directory range read, no per-path
// fingerprints and no one-scan chunk lookup, so they load the snapshot and
// the stat cache and prune with the full sweep. It is the reference the
// scoped reads must match.
type wholeRepoPins struct{ *readCountingRepo }

func (wholeRepoPins) SnapshotFilesUnder() {}
func (wholeRepoPins) StatEntries()        {}
func (wholeRepoPins) FilesWithAnyChunk()  {}

// pinLayout has the paths a prefix match gets wrong when it is a plain string
// prefix or a LIKE pattern: siblings that share a prefix (a, a-b, a.bin,
// a/b vs a/bc and a/b-c), case variants, wildcard characters and text next
// to raw content.
var pinLayout = map[string]string{
	"a.bin":         "\x00a dot bin\x01",
	"a-b/x.bin":     "\x00a dash b\x02",
	"a/b.bin":       "\x00a slash b dot bin\x03",
	"a/b/c.bin":     "\x00a b c\x04",
	"a/b/d/e.bin":   "\x00deep\x05",
	"a/bc/y.bin":    "\x00a bc\x06",
	"a/b-c/z.bin":   "\x00a b-c\x07",
	"a/notes.txt":   "text under a\n",
	"A/x.bin":       "\x00upper case\x08",
	"keep/c.bin":    "\x00a b c\x04",
	"x%_/y.bin":     "\x00wildcards\x09",
	"x%_z/y.bin":    "\x00wildcard sibling\x0a",
	"z/b":           "\x00file named b\x0b",
	"z/b.txt":       "text named b\n",
	"top-level.bin": "\x00top\x0c",
}

var pinPrefixes = []string{"a", "a/b", "a-b", "A", "a/b/c.bin", "a.bin", "z/b", "x%_", "keep", "a/b/d", "missing", "a/bc", "z"}

// casedPinLayout is pinLayout without the case variant on file systems that
// cannot hold A and a side by side.
func casedPinLayout() map[string]string {
	out := make(map[string]string, len(pinLayout))
	for p, c := range pinLayout {
		if p == "A/x.bin" && (runtime.GOOS == "darwin" || runtime.GOOS == "windows") {
			continue
		}
		out[p] = c
	}
	return out
}

func TestPinnedRawFiles_ByPathEqualsTheSnapshotFilter(t *testing.T) {
	layout := casedPinLayout()
	fx := newSmallRepo(t, layout)
	for _, pin := range pinPrefixes {
		scoped := &readCountingRepo{LocalRepo: fx.lr}
		got, err := pinnedRawFiles(scoped, pin)
		require.NoError(t, err)
		whole := wholeRepoPins{&readCountingRepo{LocalRepo: fx.lr}}
		want, err := pinnedRawFiles(whole, pin)
		require.NoError(t, err)
		require.Equal(t, want, got, "pin %q", pin)
		require.Zero(t, scoped.snapshots, "pin %q", pin)
		require.Equal(t, 1, whole.snapshots)
		for _, f := range got {
			require.True(t, serverDomain.PrefixCovers(pin, f.Path))
			require.NotEqual(t, "zstd", f.Encoding)
		}
	}
	paths := func(pin string) []string {
		files, err := pinnedRawFiles(fx.lr, pin)
		require.NoError(t, err)
		var out []string
		for _, f := range files {
			out = append(out, f.Path)
		}
		return out
	}
	require.Equal(t, []string{"a/b-c/z.bin", "a/b.bin", "a/b/c.bin", "a/b/d/e.bin", "a/bc/y.bin"}, paths("a"))
	require.Equal(t, []string{"a/b/c.bin", "a/b/d/e.bin"}, paths("a/b"))
	require.Equal(t, []string{"a/b/c.bin"}, paths("a/b/c.bin"))
	require.Equal(t, []string{"z/b"}, paths("z/b"))
	require.Equal(t, []string{"x%_/y.bin"}, paths("x%_"))
	if _, ok := layout["A/x.bin"]; ok {
		require.Equal(t, []string{"A/x.bin"}, paths("A"))
	}
	require.Empty(t, paths("missing"))
}

// Pin and Unpin: lock pins one after another and then unlocks them in turn,
// once through the by-path reads and once through the whole-repository
// reference, and requires the same pins and the same cached objects after
// every step.
func TestLockPins_PinAndUnpinMatchTheWholeRepositoryReads(t *testing.T) {
	ctx := context.Background()
	steps := []struct {
		pin   bool
		path  string
		check func(t *testing.T, fx *smallRepo)
	}{
		{pin: true, path: "a", check: func(t *testing.T, fx *smallRepo) {
			require.True(t, fx.cached(t, fx.chunks["a/b/c.bin"]))
			require.True(t, fx.cached(t, fx.chunks["a/bc/y.bin"]))
			require.False(t, fx.cached(t, fx.chunks["a-b/x.bin"]), "a sibling prefix is not covered")
			require.False(t, fx.cached(t, fx.chunks["a.bin"]))
			if chunks, ok := fx.chunks["A/x.bin"]; ok {
				require.False(t, fx.cached(t, chunks), "case matters")
			}
		}},
		{pin: true, path: "a/b"},
		{pin: true, path: "x%_", check: func(t *testing.T, fx *smallRepo) {
			require.True(t, fx.cached(t, fx.chunks["x%_/y.bin"]))
			require.False(t, fx.cached(t, fx.chunks["x%_z/y.bin"]), "wildcards are plain bytes")
		}},
		{pin: true, path: "keep"},
		{pin: false, path: "a/b", check: func(t *testing.T, fx *smallRepo) {
			require.True(t, fx.cached(t, fx.chunks["a/b/c.bin"]), "the pin on a still keeps it")
		}},
		{pin: false, path: "a", check: func(t *testing.T, fx *smallRepo) {
			require.True(t, fx.cached(t, fx.chunks["a/b/c.bin"]), "keep/c.bin has the same content and is still pinned")
			require.False(t, fx.cached(t, fx.chunks["a/b/d/e.bin"]))
			require.False(t, fx.cached(t, fx.chunks["a/bc/y.bin"]))
			require.True(t, fx.cached(t, fx.chunks["a/notes.txt"]), "text stays cached")
		}},
		{pin: false, path: "keep", check: func(t *testing.T, fx *smallRepo) {
			require.False(t, fx.cached(t, fx.chunks["keep/c.bin"]))
			require.True(t, fx.cached(t, fx.chunks["x%_/y.bin"]))
		}},
		{pin: false, path: "missing"},
		{pin: false, path: "x%_"},
	}
	scopedFx, wholeFx := newSmallRepo(t, casedPinLayout()), newSmallRepo(t, casedPinLayout())
	for _, step := range steps {
		scoped := &readCountingRepo{LocalRepo: scopedFx.lr}
		whole := wholeRepoPins{&readCountingRepo{LocalRepo: wholeFx.lr}}
		if step.pin {
			require.NoError(t, NewLockPins(nil, nil, scoped).Pin(ctx, scopedFx.root, step.path))
			require.NoError(t, NewLockPins(nil, nil, whole).Pin(ctx, wholeFx.root, step.path))
			require.Equal(t, 1, whole.snapshots)
		} else {
			require.NoError(t, NewLockPins(nil, nil, scoped).Unpin(ctx, scopedFx.root, step.path))
			require.NoError(t, NewLockPins(nil, nil, whole).Unpin(ctx, wholeFx.root, step.path))
			require.Equal(t, 1, whole.sweeps)
		}
		require.Zero(t, scoped.snapshots, "%v %s", step.pin, step.path)
		require.Zero(t, scoped.statLoads)
		require.Zero(t, scoped.sweeps)
		require.LessOrEqual(t, scoped.chunkScans+scoped.setScans, 1)
		scopedPins, err := scopedFx.lr.ListPins()
		require.NoError(t, err)
		wholePins, err := wholeFx.lr.ListPins()
		require.NoError(t, err)
		require.Equal(t, wholePins, scopedPins)
		require.Equal(t, wholeFx.objects(t), scopedFx.objects(t), "after %v %s", step.pin, step.path)
		if step.check != nil {
			step.check(t, scopedFx)
		}
	}
}

func TestLockPins_UnpinOfTheRootSweeps(t *testing.T) {
	fx := newSmallRepo(t, casedPinLayout(), "a")
	stray := serverDomain.Hash{0xee}
	require.NoError(t, fx.lr.StoreChunk(stray, []byte("stray")))
	counting := &readCountingRepo{LocalRepo: fx.lr}
	require.NoError(t, NewLockPins(nil, nil, counting).Unpin(context.Background(), fx.root, "."))
	require.Equal(t, 1, counting.sweeps)
	require.False(t, fx.cached(t, []serverDomain.Hash{stray}))
}

func TestLockPins_UnpinErrors(t *testing.T) {
	fx := newSmallRepo(t, casedPinLayout(), "a")
	counting := &readCountingRepo{LocalRepo: fx.lr}
	require.Error(t, NewLockPins(nil, nil, &dirReadFailRepo{counting}).Unpin(context.Background(), fx.root, "a"))
	require.Zero(t, counting.sweeps, "a failed unpin prunes nothing")
	_, err := pinnedRawFiles(&pathReadFailRepo{counting}, "a")
	require.Error(t, err)
	_, err = pinnedRawFiles(&dirReadFailRepo{counting}, "a")
	require.Error(t, err)
	_, err = pinnedRawFiles(struct{}{}, "a")
	require.NoError(t, err, "a repository with no reads covers nothing")
}

type dirReadFailRepo struct{ *readCountingRepo }

func (dirReadFailRepo) SnapshotFilesUnder(string) ([]domain.SnapshotFile, error) {
	return nil, errors.New("range read failed")
}

type pathReadFailRepo struct{ *readCountingRepo }

func (pathReadFailRepo) SnapshotFiles([]string) (map[string]domain.SnapshotFile, error) {
	return nil, errors.New("path read failed")
}

func TestPruneCandidates(t *testing.T) {
	setup := func(t *testing.T) (*smallRepo, []serverDomain.Hash, serverDomain.Hash) {
		fx := newSmallRepo(t, smallChangeFiles)
		var orphans []serverDomain.Hash
		for i := 0; i < 3; i++ {
			h := serverDomain.Hash{0xab, byte(i)}
			require.NoError(t, fx.lr.StoreChunk(h, []byte("x")))
			orphans = append(orphans, h)
		}
		stray := serverDomain.Hash{0xcd}
		require.NoError(t, fx.lr.StoreChunk(stray, []byte("stray")))
		return fx, orphans, stray
	}
	t.Run("failed command prunes nothing", func(t *testing.T) {
		fx, orphans, _ := setup(t)
		counting := &readCountingRepo{LocalRepo: fx.lr}
		pruneCandidates(counting, orphans, errors.New("failed"))
		require.True(t, fx.cached(t, orphans))
		require.Zero(t, counting.sweeps)
	})
	t.Run("targeted", func(t *testing.T) {
		fx, orphans, stray := setup(t)
		kept := fx.chunks["a.txt"]
		counting := &readCountingRepo{LocalRepo: fx.lr}
		pruneCandidates(counting, append(append([]serverDomain.Hash(nil), orphans...), kept...), nil)
		require.Zero(t, counting.sweeps)
		require.Zero(t, counting.snapshots)
		require.Equal(t, 1, counting.setScans)
		require.False(t, fx.cached(t, orphans))
		require.True(t, fx.cached(t, kept))
		require.True(t, fx.cached(t, []serverDomain.Hash{stray}), "only candidates are looked at")
	})
	t.Run("nothing to prune", func(t *testing.T) {
		fx, _, stray := setup(t)
		counting := &readCountingRepo{LocalRepo: fx.lr}
		pruneCandidates(counting, nil, nil)
		require.Zero(t, counting.sweeps+counting.setScans+counting.chunkScans)
		require.True(t, fx.cached(t, []serverDomain.Hash{stray}))
	})
	t.Run("repository without the set scan sweeps", func(t *testing.T) {
		fx, orphans, stray := setup(t)
		whole := wholeRepoPins{&readCountingRepo{LocalRepo: fx.lr}}
		pruneCandidates(whole, orphans, nil)
		require.Equal(t, 1, whole.sweeps)
		require.False(t, fx.cached(t, append(orphans, stray)))
	})
	t.Run("lookup failure keeps everything", func(t *testing.T) {
		fx, orphans, _ := setup(t)
		counting := &readCountingRepo{LocalRepo: fx.lr, setScanErr: errors.New("scan failed")}
		pruneCandidates(counting, orphans, nil)
		require.Zero(t, counting.sweeps)
		require.True(t, fx.cached(t, orphans))
	})
	t.Run("busy repository skips", func(t *testing.T) {
		fx, orphans, _ := setup(t)
		other := localrepo.NewLocalRepo()
		require.NoError(t, other.Init(fx.root))
		defer func() { _ = other.Close() }()
		release, err := other.LockShared()
		require.NoError(t, err)
		defer release()
		counting := &readCountingRepo{LocalRepo: fx.lr}
		pruneCandidates(counting, orphans, nil)
		require.Zero(t, counting.sweeps)
		require.True(t, fx.cached(t, orphans))
	})
}
