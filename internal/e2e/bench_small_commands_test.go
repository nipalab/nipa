package e2e

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/metrics"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	clientusecase "github.com/nipalab/nipa/internal/client/usecase"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

// costRepo is the local repository with the reads whose cost can follow the
// size of the repository counted: the whole-table loads, the object cache
// listing and the chunk scans, plus the rows the by-path reads return.
type costRepo struct {
	*localrepo.LocalRepo
	c *repoCost
}

type repoCost struct {
	snapshotRows   atomic.Int64
	statCacheRows  atomic.Int64
	objectListings atomic.Int64
	chunkScans     atomic.Int64
	scopedRows     atomic.Int64
	// whole is the time spent in the reads above that can follow the size
	// of the clone (whole-table loads, cache listings, chunk scans).
	whole atomic.Int64
}

func (c *repoCost) timed(start time.Time) { c.whole.Add(int64(time.Since(start))) }

func (c *repoCost) String() string {
	return fmt.Sprintf("snapshot rows %d, stat_cache rows %d, cache listings %d, chunk scans %d, by-path rows %d",
		c.snapshotRows.Load(), c.statCacheRows.Load(), c.objectListings.Load(), c.chunkScans.Load(), c.scopedRows.Load())
}

func (r *costRepo) Snapshot() (*domain.Snapshot, error) {
	defer r.c.timed(time.Now())
	s, err := r.LocalRepo.Snapshot()
	if s != nil {
		r.c.snapshotRows.Add(int64(len(s.Files)))
	}
	return s, err
}

func (r *costRepo) LoadStatCache() (map[string]domain.StatEntry, error) {
	defer r.c.timed(time.Now())
	m, err := r.LocalRepo.LoadStatCache()
	r.c.statCacheRows.Add(int64(len(m)))
	return m, err
}

func (r *costRepo) PruneObjects(keep map[serverDomain.Hash]struct{}) (int, int64, error) {
	defer r.c.timed(time.Now())
	r.c.objectListings.Add(1)
	return r.LocalRepo.PruneObjects(keep)
}

func (r *costRepo) FilesWithChunk(hash serverDomain.Hash) ([]domain.SnapshotFile, error) {
	defer r.c.timed(time.Now())
	r.c.chunkScans.Add(1)
	return r.LocalRepo.FilesWithChunk(hash)
}

func (r *costRepo) FilesWithAnyChunk(hashes []serverDomain.Hash) ([]domain.SnapshotFile, error) {
	defer r.c.timed(time.Now())
	r.c.chunkScans.Add(1)
	return r.LocalRepo.FilesWithAnyChunk(hashes)
}

// legacyPruneRepo hides the one-scan chunk lookup, so the commands prune as
// they did before it: a few chunks looked up one by one, more by the full
// sweep (and blame and unlock always by the full sweep).
type legacyPruneRepo struct{ *costRepo }

func (legacyPruneRepo) FilesWithAnyChunk() {}

// legacyLockRepo also hides the by-directory and by-path fingerprint reads,
// so lock reads the snapshot and the whole stat cache as it did before.
type legacyLockRepo struct{ *costRepo }

func (legacyLockRepo) FilesWithAnyChunk()  {}
func (legacyLockRepo) SnapshotFilesUnder() {}
func (legacyLockRepo) StatEntries()        {}

func (r *costRepo) SnapshotFiles(paths []string) (map[string]domain.SnapshotFile, error) {
	m, err := r.LocalRepo.SnapshotFiles(paths)
	r.c.scopedRows.Add(int64(len(m)))
	return m, err
}

func (r *costRepo) SnapshotFilesUnder(dir string) ([]domain.SnapshotFile, error) {
	rows, err := r.LocalRepo.SnapshotFilesUnder(dir)
	r.c.scopedRows.Add(int64(len(rows)))
	return rows, err
}

func (r *costRepo) StatEntries(paths []string) (map[string]domain.StatEntry, error) {
	m, err := r.LocalRepo.StatEntries(paths)
	r.c.scopedRows.Add(int64(len(m)))
	return m, err
}

// heapPeak samples the heap objects (live and not yet collected) of the
// process every millisecond while an operation runs. The server runs in the
// same process, so the figure covers both sides.
type heapPeak struct {
	stop chan struct{}
	done sync.WaitGroup
	peak uint64
}

func startHeapPeak() *heapPeak {
	h := &heapPeak{stop: make(chan struct{})}
	sample := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
	read := func() {
		metrics.Read(sample)
		if v := sample[0].Value.Uint64(); v > h.peak {
			h.peak = v
		}
	}
	read()
	h.done.Add(1)
	go func() {
		defer h.done.Done()
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-h.stop:
				read()
				return
			case <-ticker.C:
				read()
			}
		}
	}()
	return h
}

func (h *heapPeak) end() uint64 {
	close(h.stop)
	h.done.Wait()
	return h.peak
}

// benchCPUProfile writes a CPU profile of every "after" run of a command into
// NIPA_BENCH_CPUPROFILE (a directory), one file per command, when it is set.
// Each run of the same command overwrites the file: the last run wins.
func benchCPUProfile(t *testing.T, name string, legacy bool) func() {
	dir := os.Getenv("NIPA_BENCH_CPUPROFILE")
	if dir == "" || legacy {
		return func() {}
	}
	file := strings.NewReplacer(" ", "-", ",", "", "(", "", ")", "").Replace(name) + ".pprof"
	f, err := os.Create(filepath.Join(dir, file))
	require.NoError(t, err)
	require.NoError(t, pprof.StartCPUProfile(f))
	return func() {
		pprof.StopCPUProfile()
		require.NoError(t, f.Close())
	}
}

type commandSample struct {
	elapsed time.Duration
	alloc   uint64
	peak    uint64
	cost    string
	whole   time.Duration
}

// TestEndToEnd_SmallCommandScale times the client commands that touch one or
// a few files in a clone of NIPA_BENCH_FILES files (make bench-small-commands
// files=100000): lock and unlock of one binary file, a push of one file and of
// 20 files (on the branch and on a fresh feature branch), a switch between
// two branches 20 files apart (delta manifest) and blame of a file with
// NIPA_BENCH_HISTORY versions (default 40). Each command runs "before"
// (through a repository that hides the reads the fix added, so the command
// takes the path it took before: the full cache sweep after a prune of more
// than 16 chunks, and for lock the whole snapshot and stat cache) and
// "after". Each figure is the median of NIPA_BENCH_RUNS runs (default 3):
// wall time, bytes allocated and the peak heap (objects, sampled every millisecond) above the heap before the
// command (client and the in-process server together), and the repository
// reads that can follow the size of the clone.
func TestEndToEnd_SmallCommandScale(t *testing.T) {
	if testing.Short() {
		t.Skip("small-command harness skipped in short mode")
	}
	total, err := strconv.Atoi(os.Getenv("NIPA_BENCH_FILES"))
	if err != nil || total <= 0 {
		t.Skip("set NIPA_BENCH_FILES=<count> to run the small-command harness")
	}
	runs := envInt("NIPA_BENCH_RUNS", 3)
	history := envInt("NIPA_BENCH_HISTORY", 40)

	ctx := context.Background()
	host := startTestServer(t, openTestDB(t))
	grpcClient, auth, _ := newBenchClient(t, host)
	seedPusher := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())

	seed := cloneWorktree(t, grpcClient, auth, host, "main")
	paths := make([]string, total)
	for i := range paths {
		paths[i] = smallUpdatePath(i)
		fp := filepath.Join(seed, filepath.FromSlash(paths[i]))
		if i < smallUpdateTops*smallUpdateLeaves {
			require.NoError(t, os.MkdirAll(filepath.Dir(fp), 0o755))
		}
		require.NoError(t, os.WriteFile(fp, []byte(fmt.Sprintf(benchTreeFilePayload, i)), 0o644))
	}
	const binary = "assets/model.bin"
	blob := make([]byte, 8<<20)
	_, _ = rand.Read(blob)
	require.NoError(t, os.MkdirAll(filepath.Join(seed, "assets"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(seed, filepath.FromSlash(binary)), blob, 0o644))
	stagePaths(t, seed, append(append([]string(nil), paths...), binary))
	timed(t, "initial push", func() { require.NoError(t, seedPusher.Run(ctx, seed, "seed")) })

	blamed := paths[1]
	lines := make([]string, 0, 200)
	for i := 0; i < 200; i++ {
		lines = append(lines, fmt.Sprintf("line %d of the blamed file", i))
	}
	for i := 0; i < history; i++ {
		lines[(i*37)%len(lines)] = fmt.Sprintf("line %d changed in version %d", (i*37)%len(lines), i)
		content := ""
		for _, l := range lines {
			content += l + "\n"
		}
		writeFile(t, seed, blamed, content)
		stagePaths(t, seed, []string{blamed})
		require.NoError(t, seedPusher.Run(ctx, seed, fmt.Sprintf("blame history %d", i)))
	}
	t.Logf("tree: %d files + %s (8 MiB) in %d directories, blamed file has %d versions", total, binary, 1+smallUpdateTops+smallUpdateTops*smallUpdateLeaves, history+1)

	work := cloneWorktree(t, grpcClient, auth, host, "main")

	samples := map[string][]commandSample{}
	var order []string
	measure := func(name string, legacy bool, op func(repo *costRepo, legacy bool)) {
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		repo := &costRepo{LocalRepo: localrepo.NewLocalRepo(), c: &repoCost{}}
		stopProfile := benchCPUProfile(t, name, legacy)
		heap := startHeapPeak()
		start := time.Now()
		op(repo, legacy)
		elapsed := time.Since(start)
		peak := heap.end()
		stopProfile()
		runtime.ReadMemStats(&after)
		_ = repo.Close()
		base := before.HeapAlloc
		if peak < base {
			peak = base
		}
		key := name + "\x00after"
		if legacy {
			key = name + "\x00before"
		}
		if _, ok := samples[name+"\x00after"]; !ok {
			if _, ok := samples[name+"\x00before"]; !ok {
				order = append(order, name)
			}
		}
		samples[key] = append(samples[key], commandSample{elapsed: elapsed, alloc: after.TotalAlloc - before.TotalAlloc, peak: peak - base, cost: repo.c.String(), whole: time.Duration(repo.c.whole.Load())})
	}
	edit := func(files []string, tag string) {
		for _, p := range files {
			writeFile(t, work, p, fmt.Sprintf("%s of %s\n", tag, p))
		}
		stagePaths(t, work, files)
	}
	lock := func(r *costRepo, legacy bool) *clientusecase.FileLock {
		if legacy {
			return clientusecase.NewFileLock(auth, grpcClient, legacyLockRepo{r})
		}
		return clientusecase.NewFileLock(auth, grpcClient, r)
	}
	push := func(r *costRepo, legacy bool) *clientusecase.Push {
		if legacy {
			return clientusecase.NewPush(auth, grpcClient, legacyPruneRepo{r})
		}
		return clientusecase.NewPush(auth, grpcClient, r)
	}
	update := func(r *costRepo, legacy bool) *clientusecase.Update {
		if legacy {
			return clientusecase.NewUpdate(auth, grpcClient, legacyPruneRepo{r})
		}
		return clientusecase.NewUpdate(auth, grpcClient, r)
	}
	blame := func(r *costRepo, legacy bool) *clientusecase.Blame {
		if legacy {
			return clientusecase.NewBlame(auth, grpcClient, legacyPruneRepo{r})
		}
		return clientusecase.NewBlame(auth, grpcClient, r)
	}

	step := 0
	for run := 0; run < runs; run++ {
		for _, legacy := range []bool{true, false} {
			step++
			measure("lock one binary file", legacy, func(r *costRepo, legacy bool) {
				_, err := lock(r, legacy).Lock(ctx, work, binary, clientusecase.LockOptions{})
				require.NoError(t, err)
			})
			measure("unlock it", legacy, func(r *costRepo, legacy bool) {
				require.NoError(t, lock(r, legacy).Unlock(ctx, work, binary, ""))
			})

			edit([]string{paths[(step*7919+11)%total]}, fmt.Sprintf("step %d one", step))
			measure("push 1 file", legacy, func(r *costRepo, legacy bool) {
				require.NoError(t, push(r, legacy).Run(ctx, work, "one file"))
			})

			twenty := make([]string, 20)
			for k := range twenty {
				twenty[k] = paths[(k*6007+step*131+17)%total]
			}
			edit(twenty, fmt.Sprintf("step %d main", step))
			measure("push 20 files", legacy, func(r *costRepo, legacy bool) {
				require.NoError(t, push(r, legacy).Run(ctx, work, "twenty files"))
			})

			feature := fmt.Sprintf("feature-%d", step)
			_, err := clientusecase.NewRepo(auth, grpcClient, localrepo.NewLocalRepo()).CreateBranch(ctx, work, host, e2eOrgSlug, e2eProjectSlug, feature)
			require.NoError(t, err)
			for k := range twenty {
				twenty[k] = paths[(k*4001+step*71+29)%total]
			}
			edit(twenty, fmt.Sprintf("step %d feature", step))
			measure("push 20 files, fresh feature branch", legacy, func(r *costRepo, legacy bool) {
				require.NoError(t, push(r, legacy).Run(ctx, work, "twenty files on a feature"))
			})

			for _, branch := range []string{"main", feature} {
				measure("switch, 20 files apart (delta)", legacy, func(r *costRepo, legacy bool) {
					require.NoError(t, update(r, legacy).Switch(ctx, work, branch))
				})
			}
			require.NoError(t, clientusecase.NewUpdate(auth, grpcClient, localrepo.NewLocalRepo()).Switch(ctx, work, "main"))

			measure(fmt.Sprintf("blame (%d versions)", history+1), legacy, func(r *costRepo, legacy bool) {
				res, err := blame(r, legacy).Run(ctx, work, blamed, "", nil)
				require.NoError(t, err)
				require.NotEmpty(t, res.Lines)
			})
		}
	}

	t.Logf("%d files, median of %d runs; before = without the one-scan chunk lookup and lock's scoped reads; heap = peak heap objects above the start (client and in-process server)", total, runs)
	t.Logf("%-36s %10s %10s %11s %11s %11s %11s %11s %11s", "command", "before", "after", "alloc bef.", "alloc aft.", "heap bef.", "heap aft.", "O(N) bef.", "O(N) aft.")
	median := func(key string) commandSample {
		list := append([]commandSample(nil), samples[key]...)
		sort.Slice(list, func(i, j int) bool { return list[i].elapsed < list[j].elapsed })
		return list[len(list)/2]
	}
	mib := func(b uint64) string { return fmt.Sprintf("%.1fMiB", float64(b)/(1<<20)) }
	for _, name := range order {
		b, a := median(name+"\x00before"), median(name+"\x00after")
		t.Logf("%-36s %10s %10s %11s %11s %11s %11s %11s %11s", name, b.elapsed.Round(100*time.Microsecond), a.elapsed.Round(100*time.Microsecond),
			mib(b.alloc), mib(a.alloc), mib(b.peak), mib(a.peak), b.whole.Round(100*time.Microsecond), a.whole.Round(100*time.Microsecond))
	}
	t.Logf("O(N) = time inside the reads that follow the size of the clone (snapshot, stat cache, cache listing, chunk scans)")
	t.Logf("repository reads that can follow the size of the clone (median run):")
	for _, name := range order {
		t.Logf("  %-34s before: %s", name, median(name+"\x00before").cost)
		t.Logf("  %-34s after:  %s", "", median(name+"\x00after").cost)
	}
}
