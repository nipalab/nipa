package e2e

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	clientgrpc "github.com/nipalab/nipa/internal/client/grpc"
	"github.com/nipalab/nipa/internal/client/localrepo"
	clientusecase "github.com/nipalab/nipa/internal/client/usecase"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/treedelta"
)

// The small-update harness tree: 25 top directories of 66 leaf directories
// each, which with the root makes the 1,676 directories of the reference
// repository; the files are spread evenly over the leaves.
const (
	smallUpdateTops   = 25
	smallUpdateLeaves = 66
)

func smallUpdatePath(i int) string {
	leaf := i % (smallUpdateTops * smallUpdateLeaves)
	return fmt.Sprintf("d%02d/s%02d/f%06d.txt", leaf/smallUpdateLeaves, leaf%smallUpdateLeaves, i)
}

// rpcClock adds up the time spent inside calls to the server.
type rpcClock struct {
	mu    sync.Mutex
	spent time.Duration
	calls map[string]time.Duration
}

func (c *rpcClock) timed(name string, start time.Time) {
	elapsed := time.Since(start)
	c.mu.Lock()
	c.spent += elapsed
	if c.calls == nil {
		c.calls = map[string]time.Duration{}
	}
	c.calls[name] += elapsed
	c.mu.Unlock()
}

func (c *rpcClock) reset() (time.Duration, map[string]time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	spent, calls := c.spent, c.calls
	c.spent, c.calls = 0, nil
	return spent, calls
}

// fullManifestClient is the real client without the delta manifest call: an
// update through it fetches the whole manifest and syncs the whole working
// copy, as every update did before delta manifests.
type fullManifestClient struct {
	c     *clientgrpc.Client
	clock *rpcClock
}

func (f *fullManifestClient) Connect(ctx context.Context, host string) error {
	return f.c.Connect(ctx, host)
}

func (f *fullManifestClient) GetBranchByName(ctx context.Context, org, project, name string) (*serverDomain.Branch, error) {
	defer f.clock.timed("GetBranchByName", time.Now())
	return f.c.GetBranchByName(ctx, org, project, name)
}

func (f *fullManifestClient) GetTagByName(ctx context.Context, org, project, name string) (*domain.Tag, error) {
	defer f.clock.timed("GetTagByName", time.Now())
	return f.c.GetTagByName(ctx, org, project, name)
}

func (f *fullManifestClient) GetTreeNodeManifest(ctx context.Context, org, project, branch string, paths []string) (*serverDomain.TreeNode, error) {
	defer f.clock.timed("manifest (full)", time.Now())
	return f.c.GetTreeNodeManifest(ctx, org, project, branch, paths)
}

func (f *fullManifestClient) GetCommitTreeManifest(ctx context.Context, org, project, commitID string, paths []string) (*serverDomain.TreeNode, error) {
	defer f.clock.timed("manifest (full)", time.Now())
	return f.c.GetCommitTreeManifest(ctx, org, project, commitID, paths)
}

func (f *fullManifestClient) DownloadChunks(ctx context.Context, scope domain.ChunkScope, hashes []serverDomain.Hash, onChunk func(h serverDomain.Hash, data []byte) error) error {
	defer f.clock.timed("chunk download", time.Now())
	return f.c.DownloadChunks(ctx, scope, hashes, onChunk)
}

func (f *fullManifestClient) DownloadChunksStream(ctx context.Context, scope domain.ChunkScope, hashes []serverDomain.Hash, stream *domain.ChunkStream, onChunk func(h serverDomain.Hash, data []byte) error) error {
	defer f.clock.timed("chunk download", time.Now())
	return f.c.DownloadChunksStream(ctx, scope, hashes, stream, onChunk)
}

// deltaManifestClient adds the delta manifest call, as the shipped client has
// it.
type deltaManifestClient struct {
	fullManifestClient
}

func (d *deltaManifestClient) GetTreeManifestDelta(ctx context.Context, org, project string, target treedelta.Target, paths []string, base treedelta.Base) (*treedelta.Manifest, error) {
	name := "manifest (full)"
	if base.CommitID != "" {
		name = "manifest (delta)"
	}
	defer d.clock.timed(name, time.Now())
	return d.c.GetTreeManifestDelta(ctx, org, project, target, paths, base)
}

// phasedRepo times the local repository calls whose cost can follow the size
// of the repository.
type phasedRepo struct {
	*localrepo.LocalRepo
	mu     sync.Mutex
	phases map[string]time.Duration
}

func (p *phasedRepo) record(name string, start time.Time) {
	p.mu.Lock()
	p.phases[name] += time.Since(start)
	p.mu.Unlock()
}

func (p *phasedRepo) Snapshot() (*domain.Snapshot, error) {
	defer p.record("Snapshot (all rows)", time.Now())
	return p.LocalRepo.Snapshot()
}

func (p *phasedRepo) LoadStatCache() (map[string]domain.StatEntry, error) {
	defer p.record("LoadStatCache (all rows)", time.Now())
	return p.LocalRepo.LoadStatCache()
}

func (p *phasedRepo) SaveSyncedTree(root *serverDomain.TreeNode, commitID string, sparse []string) error {
	defer p.record("SaveSyncedTree (diff all rows)", time.Now())
	return p.LocalRepo.SaveSyncedTree(root, commitID, sparse)
}

func (p *phasedRepo) SaveSyncedTreeView(root *serverDomain.TreeNode, commitID string, sparse []string, view []byte) error {
	defer p.record("SaveSyncedTree (diff all rows)", time.Now())
	return p.LocalRepo.SaveSyncedTreeView(root, commitID, sparse, view)
}

func (p *phasedRepo) PlanTreeDelta(in domain.TreeDeltaApply) (*domain.TreeDeltaChanges, error) {
	defer p.record("PlanTreeDelta", time.Now())
	return p.LocalRepo.PlanTreeDelta(in)
}

func (p *phasedRepo) ApplyTreeDelta(in domain.TreeDeltaApply, planned *domain.TreeDeltaChanges) (*domain.TreeDeltaChanges, error) {
	defer p.record("ApplyTreeDelta", time.Now())
	return p.LocalRepo.ApplyTreeDelta(in, planned)
}

func (p *phasedRepo) PruneObjects(keep map[serverDomain.Hash]struct{}) (int, int64, error) {
	defer p.record("PruneObjects (list cache)", time.Now())
	return p.LocalRepo.PruneObjects(keep)
}

func (p *phasedRepo) FilesWithChunk(hash serverDomain.Hash) ([]domain.SnapshotFile, error) {
	defer p.record("FilesWithChunk (SQL scan)", time.Now())
	return p.LocalRepo.FilesWithChunk(hash)
}

func (p *phasedRepo) Init(target string) error {
	defer p.record("open (Init)", time.Now())
	return p.LocalRepo.Init(target)
}

type updateSample struct {
	total, server time.Duration
	rpcs, phases  map[string]time.Duration
}

func medianSample(samples []updateSample) updateSample {
	sort.Slice(samples, func(i, j int) bool { return samples[i].total < samples[j].total })
	return samples[len(samples)/2]
}

func logUpdateSample(t *testing.T, label string, s updateSample) {
	t.Helper()
	t.Logf("%-44s total %8s = server+transfer %8s + client-local %8s", label,
		s.total.Round(100*time.Microsecond), s.server.Round(100*time.Microsecond), (s.total - s.server).Round(100*time.Microsecond))
	for _, group := range []map[string]time.Duration{s.rpcs, s.phases} {
		names := make([]string, 0, len(group))
		for name := range group {
			names = append(names, name)
		}
		sort.Slice(names, func(i, j int) bool { return group[names[i]] > group[names[j]] })
		for _, name := range names {
			t.Logf("        %-32s %s", name, group[name].Round(10*time.Microsecond))
		}
	}
}

// TestEndToEnd_SmallUpdateScale times `nipa update` and `nipa switch` in a
// clone of NIPA_BENCH_FILES files (make bench-small-update files=100000) when
// the target differs from what the clone holds by a handful of files: once
// through a client without delta manifests (the full manifest and the full
// working-copy sync, as before them) and once through the shipped client.
// Each figure is the median of NIPA_BENCH_RUNS runs (default 5), split into
// the time spent inside calls to the server (server work, transfer and
// decoding, over loopback) and everything else (client-local). Unforced and
// forced (`--force`, the daemon) are timed apart.
func TestEndToEnd_SmallUpdateScale(t *testing.T) {
	if testing.Short() {
		t.Skip("small-update harness skipped in short mode")
	}
	total, err := strconv.Atoi(os.Getenv("NIPA_BENCH_FILES"))
	if err != nil || total <= 0 {
		t.Skip("set NIPA_BENCH_FILES=<count> to run the small-update harness")
	}
	runs, _ := strconv.Atoi(os.Getenv("NIPA_BENCH_RUNS"))
	if runs <= 0 {
		runs = 5
	}

	ctx := context.Background()
	host := startTestServer(t, openTestDB(t))
	grpcClient, auth, _ := newBenchClient(t, host)
	pusher := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())

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
	stagePaths(t, seed, paths)
	timed(t, "initial push", func() { require.NoError(t, pusher.Run(ctx, seed, "seed")) })
	t.Logf("tree: %d files in %d directories", total, 1+smallUpdateTops+smallUpdateTops*smallUpdateLeaves)

	// change commits a handful of files: three modified, one added and the
	// file added by the previous change removed. Its pushes are timed: a push
	// of a handful of files into a clone of N files.
	generation, added := 0, ""
	var pushes []time.Duration
	change := func() {
		generation++
		var staged []string
		for k := 0; k < 3; k++ {
			p := paths[(generation*7919+k*total/3)%total]
			writeFile(t, seed, p, fmt.Sprintf("generation %d of %s\n", generation, p))
			staged = append(staged, p)
		}
		if added != "" {
			require.NoError(t, os.Remove(filepath.Join(seed, filepath.FromSlash(added))))
			staged = append(staged, added)
		}
		added = fmt.Sprintf("d%02d/s%02d/added-%d.txt", generation%smallUpdateTops, generation%smallUpdateLeaves, generation)
		writeFile(t, seed, added, fmt.Sprintf("added in generation %d\n", generation))
		staged = append(staged, added)
		stagePaths(t, seed, staged)
		start := time.Now()
		require.NoError(t, pusher.Run(ctx, seed, fmt.Sprintf("change %d", generation)))
		pushes = append(pushes, time.Since(start))
	}

	// The seed is a clone of an empty project that only ever pushed: its
	// first switch away from its own commits is what every project creator
	// does first. It used to find no view token in the stored tree (no
	// manifest had ever been fetched) and fetch the full manifest; the token
	// now comes with the push response.
	_, err = grpcClient.CreateBranch(ctx, e2eOrgSlug, e2eProjectSlug, "first", "main", "", "")
	require.NoError(t, err)
	change()
	firstClock := &rpcClock{}
	firstRepo := &phasedRepo{LocalRepo: localrepo.NewLocalRepo(), phases: map[string]time.Duration{}}
	firstUpdate := clientusecase.NewUpdate(auth, &deltaManifestClient{fullManifestClient{c: grpcClient, clock: firstClock}}, firstRepo)
	start := time.Now()
	require.NoError(t, firstUpdate.Switch(ctx, seed, "first"))
	firstTotal := time.Since(start)
	firstServer, firstRPCs := firstClock.reset()
	require.NoError(t, firstRepo.Close())
	logUpdateSample(t, "first switch after an empty-clone push (5 files apart)", updateSample{total: firstTotal, server: firstServer, rpcs: firstRPCs, phases: firstRepo.phases})
	require.NoError(t, clientusecase.NewUpdate(auth, grpcClient, localrepo.NewLocalRepo()).Switch(ctx, seed, "main"))

	type clone struct {
		label  string
		dir    string
		update func(repo *phasedRepo, force bool) *clientusecase.Update
		clock  *rpcClock
	}
	newClone := func(label string, delta bool) *clone {
		c := &clone{label: label, clock: &rpcClock{}}
		timed(t, "clone ("+label+")", func() { c.dir = cloneWorktree(t, grpcClient, auth, host, "main") })
		full := fullManifestClient{c: grpcClient, clock: c.clock}
		c.update = func(repo *phasedRepo, force bool) *clientusecase.Update {
			if delta {
				return clientusecase.NewUpdate(auth, &deltaManifestClient{full}, repo).WithForce(force)
			}
			return clientusecase.NewUpdate(auth, &full, repo).WithForce(force)
		}
		return c
	}
	clones := []*clone{newClone("full manifest", false), newClone("delta manifest", true)}

	// measure runs op in a fresh "process": a new repository handle, as a
	// CLI invocation has.
	measure := func(c *clone, force bool, op func(u *clientusecase.Update) error) updateSample {
		repo := &phasedRepo{LocalRepo: localrepo.NewLocalRepo(), phases: map[string]time.Duration{}}
		defer func() { _ = repo.Close() }()
		c.clock.reset()
		start := time.Now()
		require.NoError(t, op(c.update(repo, force)))
		elapsed := time.Since(start)
		server, rpcs := c.clock.reset()
		return updateSample{total: elapsed, server: server, rpcs: rpcs, phases: repo.phases}
	}

	samples := map[string][]updateSample{}
	record := func(scenario string, c *clone, s updateSample) {
		key := scenario + ", " + c.label
		samples[key] = append(samples[key], s)
	}
	scenarios := []string{"update (5 files changed)", "update --force (5 files changed)", "switch (5 files apart)", "switch --force (5 files apart)", "update (already at the head)"}

	untimed := func(c *clone) {
		repo := &phasedRepo{LocalRepo: localrepo.NewLocalRepo(), phases: map[string]time.Duration{}}
		defer func() { _ = repo.Close() }()
		require.NoError(t, c.update(repo, false).Run(ctx, c.dir))
	}
	for run := 0; run < runs; run++ {
		// Every timed update is the first request for its commit, as it is
		// for a user whose teammate just pushed: the server answers "which
		// chunks may this caller download" from a cache for two minutes, so
		// the clone that is not being timed catches up afterwards.
		for _, force := range []bool{false, true} {
			scenario := scenarios[0]
			if force {
				scenario = scenarios[1]
			}
			for i, c := range clones {
				change()
				record(scenario, c, measure(c, force, func(u *clientusecase.Update) error { return u.Run(ctx, c.dir) }))
				untimed(clones[1-i])
			}
		}
		// The side branch stays one change behind main, so a switch moves
		// the clone across a handful of files, forth and back. The clones
		// take turns going first.
		_ = grpcClient.DeleteBranch(ctx, e2eOrgSlug, e2eProjectSlug, "side")
		_, err := grpcClient.CreateBranch(ctx, e2eOrgSlug, e2eProjectSlug, "side", "main", "", "")
		require.NoError(t, err)
		change()
		order := clones
		if run%2 == 1 {
			order = []*clone{clones[1], clones[0]}
		}
		for _, c := range order {
			untimed(c)
		}
		for _, force := range []bool{false, true} {
			scenario := scenarios[2]
			if force {
				scenario = scenarios[3]
			}
			for _, branch := range []string{"side", "main"} {
				for _, c := range order {
					record(scenario, c, measure(c, force, func(u *clientusecase.Update) error { return u.Switch(ctx, c.dir, branch) }))
				}
			}
		}
		for _, c := range order {
			record(scenarios[4], c, measure(c, false, func(u *clientusecase.Update) error { return u.Run(ctx, c.dir) }))
		}
	}

	// Both clones must hold the same thing in the end.
	require.Equal(t, withoutView(storedTree(t, clones[0].dir)), withoutView(storedTree(t, clones[1].dir)))
	require.Equal(t, workingTree(t, clones[0].dir), workingTree(t, clones[1].dir))

	sort.Slice(pushes, func(i, j int) bool { return pushes[i] < pushes[j] })
	t.Logf("push of a handful of files (%d pushes): median %s, min %s, max %s", len(pushes),
		pushes[len(pushes)/2].Round(100*time.Microsecond), pushes[0].Round(100*time.Microsecond), pushes[len(pushes)-1].Round(100*time.Microsecond))
	t.Logf("%d files, median of %d runs (switch: %d)", total, runs, 2*runs)
	for _, scenario := range scenarios {
		for _, c := range clones {
			logUpdateSample(t, scenario+", "+c.label, medianSample(samples[scenario+", "+c.label]))
		}
	}
}
