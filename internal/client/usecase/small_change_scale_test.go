package usecase

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

const (
	scaleFilesPerDir = 100
	scaleDirFanout   = 32
	scaleBaseCommit  = "scalebase"
)

func scaleHash(a, b uint32) serverDomain.Hash {
	var h serverDomain.Hash
	binary.BigEndian.PutUint32(h[:4], a)
	binary.BigEndian.PutUint32(h[4:8], b)
	h[31] = 1
	return h
}

// scalePath is the working-copy path of synthetic file i: two directory
// levels, scaleFilesPerDir text files per leaf directory.
func scalePath(i int) string {
	dir := i / scaleFilesPerDir
	return fmt.Sprintf("d%02d/s%03d/f%03d.txt", dir%scaleDirFanout, dir/scaleDirFanout, i%scaleFilesPerDir)
}

// scaleRepo is a real localrepo holding a synthetic full checkout of n
// tracked text files: rows, a fresh fingerprint for each and, when asked, one
// cached object per file, as a clone of a text repository leaves them. Only
// the files a scenario touches exist on disk.
type scaleRepo struct {
	root string
	lr   *localrepo.LocalRepo
}

func newScaleRepo(t testing.TB, n int, objects bool) *scaleRepo {
	t.Helper()
	fx := &scaleRepo{root: t.TempDir(), lr: localrepo.NewLocalRepo()}
	t.Cleanup(func() { _ = fx.lr.Close() })
	require.NoError(t, fx.lr.Init(fx.root))
	require.NoError(t, fx.lr.SaveConfig(domain.Config{Url: "http://example.com/org/project", Branch: "main"}))

	root := &serverDomain.TreeNode{Name: "root", Hash: scaleHash(0xffffffff, 0)}
	tops := map[string]*serverDomain.TreeNode{}
	leaves := map[string]*serverDomain.TreeNode{}
	stats := make(map[string]domain.StatEntry, n)
	var cached []*serverDomain.ChunkData
	now := time.Now().UnixNano()
	for i := 0; i < n; i++ {
		p := scalePath(i)
		topName, leafName, name := p[:3], p[4:8], p[9:]
		top := tops[topName]
		if top == nil {
			top = &serverDomain.TreeNode{Name: topName, Hash: scaleHash(0xfffffffe, uint32(len(tops)))}
			tops[topName] = top
			root.TreeChildren = append(root.TreeChildren, top)
		}
		leaf := leaves[topName+"/"+leafName]
		if leaf == nil {
			leaf = &serverDomain.TreeNode{Name: leafName, Hash: scaleHash(0xfffffffd, uint32(len(leaves)))}
			leaves[topName+"/"+leafName] = leaf
			top.TreeChildren = append(top.TreeChildren, leaf)
		}
		chunk := scaleHash(uint32(i), 1)
		fileHash := chunker.FileHash([]serverDomain.Hash{chunk})
		leaf.FileChildren = append(leaf.FileChildren, &serverDomain.File{
			Name: name, Hash: fileHash, Mode: 2, SizeBytes: 64, Encoding: chunker.EncodingZstd,
			Chunks: []serverDomain.Chunk{{Hash: chunk, SizeBytes: 64}},
		})
		stats[p] = domain.StatEntry{SizeBytes: 64, MtimeNS: now - int64(time.Hour), Mode: 2, Hash: fileHash, CachedAt: now}
		if objects {
			cached = append(cached, &serverDomain.ChunkData{Hash: chunk, Data: []byte("x")})
		}
	}
	require.NoError(t, fx.lr.SaveSyncedTree(root, scaleBaseCommit, nil))
	require.NoError(t, fx.lr.SaveCommit(scaleBaseCommit, ""))
	require.NoError(t, fx.lr.SaveStatEntries(stats))
	require.NoError(t, fx.lr.StoreChunks(cached))
	return fx
}

func (fx *scaleRepo) write(t testing.TB, path, content string) {
	t.Helper()
	abs := filepath.Join(fx.root, filepath.FromSlash(path))
	require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
	require.NoError(t, os.WriteFile(abs, []byte(content), 0o644))
}

// scalePushClient is the server of the scale harness: it accepts every push
// and answers with the root hash the push leads to, which it learns from a
// local apply that is asked for an impossible hash. Its time is network time.
type scalePushClient struct {
	lr      *localrepo.LocalRepo
	commits int
	spent   time.Duration
	mu      sync.Mutex

	// The last push, as it was sent.
	files    []*serverDomain.PushFile
	removed  []string
	baseTree string
	base     string
}

func (c *scalePushClient) timed(start time.Time) {
	c.mu.Lock()
	c.spent += time.Since(start)
	c.mu.Unlock()
}

func (c *scalePushClient) Connect(context.Context, string) error { return nil }

func (c *scalePushClient) Push(_ context.Context, _, _, _, baseTreeHash, _ string, files []*serverDomain.PushFile, removed []string, _, baseCommitID string) (*serverDomain.PushResult, error) {
	defer c.timed(time.Now())
	c.files, c.removed, c.baseTree, c.base = files, removed, baseTreeHash, baseCommitID
	c.commits++
	id := snow.ID(1000 + c.commits)
	err := c.lr.ApplyPush(domain.PushApply{Files: files, Removed: removed, BaseCommitID: baseCommitID, CommitID: id.Base36()})
	var mismatch *domain.PushApplyMismatchError
	if !errors.As(err, &mismatch) {
		return nil, fmt.Errorf("scale server could not derive the tree hash: %w", err)
	}
	return &serverDomain.PushResult{CommitID: id, TreeHash: mismatch.Computed}, nil
}

func (c *scalePushClient) UploadChunks(_ context.Context, _ domain.ChunkScope, chunks []*serverDomain.ChunkData, _ ...func(*serverDomain.ChunkData)) (int, int, error) {
	defer c.timed(time.Now())
	return len(chunks), 0, nil
}

func (c *scalePushClient) GetTreeNodeManifest(context.Context, string, string, string, []string) (*serverDomain.TreeNode, error) {
	return nil, errors.New("the scale harness expects every push to be applied locally")
}

// phaseRepo times the local repository calls that can cost whole-repository
// work, so a run can be split into phases.
type phaseRepo struct {
	*localrepo.LocalRepo
	mu     sync.Mutex
	phases map[string]time.Duration
}

func (p *phaseRepo) record(name string, start time.Time) {
	p.mu.Lock()
	p.phases[name] += time.Since(start)
	p.mu.Unlock()
}

func (p *phaseRepo) Init(target string) error {
	defer p.record("open (Init)", time.Now())
	return p.LocalRepo.Init(target)
}

func (p *phaseRepo) Snapshot() (*domain.Snapshot, error) {
	defer p.record("Snapshot (all rows)", time.Now())
	return p.LocalRepo.Snapshot()
}

func (p *phaseRepo) LoadStatCache() (map[string]domain.StatEntry, error) {
	defer p.record("LoadStatCache (all rows)", time.Now())
	return p.LocalRepo.LoadStatCache()
}

func (p *phaseRepo) ApplyPush(in domain.PushApply) error {
	defer p.record("ApplyPush", time.Now())
	return p.LocalRepo.ApplyPush(in)
}

func (p *phaseRepo) PruneObjects(keep map[serverDomain.Hash]struct{}) (int, int64, error) {
	defer p.record("PruneObjects (list cache)", time.Now())
	return p.LocalRepo.PruneObjects(keep)
}

func (p *phaseRepo) StageAddMany(paths []string) error {
	defer p.record("StageAddMany", time.Now())
	return p.LocalRepo.StageAddMany(paths)
}

func (p *phaseRepo) SaveStatEntries(entries map[string]domain.StatEntry) error {
	defer p.record("SaveStatEntries", time.Now())
	return p.LocalRepo.SaveStatEntries(entries)
}

func (p *phaseRepo) StoreChunks(chunks []*serverDomain.ChunkData) error {
	defer p.record("StoreChunks", time.Now())
	return p.LocalRepo.StoreChunks(chunks)
}

func (p *phaseRepo) SnapshotFiles(paths []string) (map[string]domain.SnapshotFile, error) {
	defer p.record("SnapshotFiles (by path)", time.Now())
	return p.LocalRepo.SnapshotFiles(paths)
}

func (p *phaseRepo) StatEntries(paths []string) (map[string]domain.StatEntry, error) {
	defer p.record("StatEntries (by path)", time.Now())
	return p.LocalRepo.StatEntries(paths)
}

func (p *phaseRepo) FilesWithChunk(hash serverDomain.Hash) ([]domain.SnapshotFile, error) {
	defer p.record("FilesWithChunk (SQL scan)", time.Now())
	return p.LocalRepo.FilesWithChunk(hash)
}

func (p *phaseRepo) RemoveObjects(hashes []serverDomain.Hash) (int, int64, error) {
	defer p.record("RemoveObjects", time.Now())
	return p.LocalRepo.RemoveObjects(hashes)
}

func logPhases(t *testing.T, label string, total time.Duration, phases map[string]time.Duration) {
	t.Helper()
	names := make([]string, 0, len(phases))
	for name := range phases {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool { return phases[names[i]] > phases[names[j]] })
	t.Logf("%s: %s", label, total.Round(100*time.Microsecond))
	var known time.Duration
	for _, name := range names {
		known += phases[name]
		t.Logf("    %-28s %s", name, phases[name].Round(10*time.Microsecond))
	}
	t.Logf("    %-28s %s", "everything else", (total - known).Round(10*time.Microsecond))
}

// TestSmallChangeScale times `nipa add` and the local side of `nipa push` for
// one changed file in a working copy of NIPA_BENCH_FILES tracked files
// (make bench-small-change files=100000). It reports the median of
// NIPA_BENCH_RUNS runs (default 5) with the costliest local calls.
func TestSmallChangeScale(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("NIPA_BENCH_FILES"))
	if n <= 0 {
		t.Skip("set NIPA_BENCH_FILES to run the small-change scale harness")
	}
	runs, _ := strconv.Atoi(os.Getenv("NIPA_BENCH_RUNS"))
	if runs <= 0 {
		runs = 5
	}
	fx := newScaleRepo(t, n, true)
	target := scalePath(n / 2)
	client := &scalePushClient{lr: fx.lr}

	type sample struct {
		total  time.Duration
		phases map[string]time.Duration
	}
	var adds, pushes []sample
	for run := 0; run < runs; run++ {
		fx.write(t, target, fmt.Sprintf("edit %d of the scale harness\n", run))

		// nipa add <one file>: a fresh process opens the repository, stages.
		repo := &phaseRepo{LocalRepo: localrepo.NewLocalRepo(), phases: map[string]time.Duration{}}
		start := time.Now()
		wc, err := NewWorkingCopy(repo, fx.root)
		require.NoError(t, err)
		require.NoError(t, wc.Add(context.Background(), []string{target}))
		adds = append(adds, sample{time.Since(start), repo.phases})
		staged, err := repo.ListStaged()
		require.NoError(t, err)
		require.Equal(t, []string{target}, staged)
		require.NoError(t, repo.Close())

		// nipa push: everything but the server's own time.
		repo = &phaseRepo{LocalRepo: localrepo.NewLocalRepo(), phases: map[string]time.Duration{}}
		client.spent = 0
		start = time.Now()
		require.NoError(t, newTestPush(t, repo, client).Run(context.Background(), fx.root, "scale"))
		pushes = append(pushes, sample{time.Since(start) - client.spent, repo.phases})
		require.NoError(t, repo.Close())
	}
	median := func(samples []sample) sample {
		sort.Slice(samples, func(i, j int) bool { return samples[i].total < samples[j].total })
		return samples[len(samples)/2]
	}
	add, push := median(adds), median(pushes)
	logPhases(t, fmt.Sprintf("add one file, %d tracked files", n), add.total, add.phases)
	logPhases(t, fmt.Sprintf("push one file (local side), %d tracked files", n), push.total, push.phases)
}
