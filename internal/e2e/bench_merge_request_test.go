package e2e

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sort"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	clientgrpc "github.com/nipalab/nipa/internal/client/grpc"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/repository/sqlite"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/treehash"
	serverusecase "github.com/nipalab/nipa/internal/usecase"
)

// treeReadCounter wraps the branch repository and counts the tree and commit
// reads the server issues: batched tree reads (one statement group per level),
// single-directory or single-node reads, commit reads, the directory ids the
// batches carry and the rows (files, chunks, child nodes) they return.
type treeReadCounter struct {
	*sqlite.BranchRepository
	batches, singles, commits, ids, rows atomic.Int64
}

func (c *treeReadCounter) single(rows int) {
	c.singles.Add(1)
	c.rows.Add(int64(rows))
}

func (c *treeReadCounter) batch(ids, rows int) {
	c.batches.Add(1)
	c.ids.Add(int64(ids))
	c.rows.Add(int64(rows))
}

func (c *treeReadCounter) GetTreeNode(ctx context.Context, id int64) (*serverDomain.TreeNode, error) {
	c.single(1)
	return c.BranchRepository.GetTreeNode(ctx, id)
}

func (c *treeReadCounter) GetTreeChildByName(ctx context.Context, parent int64, name string) (*serverDomain.TreeNode, error) {
	c.single(1)
	return c.BranchRepository.GetTreeChildByName(ctx, parent, name)
}

func (c *treeReadCounter) ListTreeChildren(ctx context.Context, id int64) ([]*serverDomain.TreeNode, error) {
	out, err := c.BranchRepository.ListTreeChildren(ctx, id)
	c.single(len(out))
	return out, err
}

func countFileRows(files []*serverDomain.File) int {
	n := len(files)
	for _, f := range files {
		n += len(f.Chunks)
	}
	return n
}

func (c *treeReadCounter) ListFilesByTree(ctx context.Context, id int64) ([]*serverDomain.File, error) {
	out, err := c.BranchRepository.ListFilesByTree(ctx, id)
	c.single(countFileRows(out))
	return out, err
}

func (c *treeReadCounter) ListFileMetaByTree(ctx context.Context, id int64) ([]*serverDomain.File, error) {
	out, err := c.BranchRepository.ListFileMetaByTree(ctx, id)
	c.single(len(out))
	return out, err
}

func (c *treeReadCounter) ListTreeChildrenByTrees(ctx context.Context, ids []int64) (map[int64][]*serverDomain.TreeNode, error) {
	out, err := c.BranchRepository.ListTreeChildrenByTrees(ctx, ids)
	n := 0
	for _, list := range out {
		n += len(list)
	}
	c.batch(len(ids), n)
	return out, err
}

func (c *treeReadCounter) ListFilesByTrees(ctx context.Context, ids []int64) (map[int64][]*serverDomain.File, error) {
	out, err := c.BranchRepository.ListFilesByTrees(ctx, ids)
	n := 0
	for _, list := range out {
		n += countFileRows(list)
	}
	c.batch(len(ids), n)
	return out, err
}

func (c *treeReadCounter) ListFileMetaByTrees(ctx context.Context, ids []int64) (map[int64][]*serverDomain.File, error) {
	out, err := c.BranchRepository.ListFileMetaByTrees(ctx, ids)
	n := 0
	for _, list := range out {
		n += len(list)
	}
	c.batch(len(ids), n)
	return out, err
}

func (c *treeReadCounter) ListChunksByFiles(ctx context.Context, ids []int64) (map[int64][]serverDomain.Chunk, error) {
	out, err := c.BranchRepository.ListChunksByFiles(ctx, ids)
	n := 0
	for _, list := range out {
		n += len(list)
	}
	c.batches.Add(1)
	c.rows.Add(int64(n))
	return out, err
}

func (c *treeReadCounter) GetCommit(ctx context.Context, id snow.ID) (*serverDomain.Commit, error) {
	c.commits.Add(1)
	return c.BranchRepository.GetCommit(ctx, id)
}

func (c *treeReadCounter) reset() {
	c.batches.Store(0)
	c.singles.Store(0)
	c.commits.Store(0)
	c.ids.Store(0)
	c.rows.Store(0)
}

// mrSample is one measured server call: wall time over loopback gRPC, the
// branch repository calls and rows behind it, and the bytes and objects
// allocated in the process meanwhile (client decoding included).
type mrSample struct {
	elapsed                        time.Duration
	batches, singles, commits, ids int64
	rows                           int64
	bytes, mallo                   uint64
}

func medianMRSample(samples []mrSample) mrSample {
	sort.Slice(samples, func(i, j int) bool { return samples[i].elapsed < samples[j].elapsed })
	return samples[len(samples)/2]
}

// mrBenchPush pushes files (path -> content) and removed paths onto branch as
// one commit and returns its id.
func mrBenchPush(t *testing.T, ctx context.Context, c *clientgrpc.Client, branch, base, message string, files map[string]string, removed []string) string {
	t.Helper()
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	push := make([]*serverDomain.PushFile, 0, len(paths))
	var chunks []*serverDomain.ChunkData
	for _, path := range paths {
		content := files[path]
		encoding, encoded, err := chunker.EncodeBytes([]byte(content), path, false, "")
		require.NoError(t, err)
		hashes := make([]serverDomain.Hash, 0, len(encoded))
		for _, e := range encoded {
			hashes = append(hashes, e.Hash)
			chunks = append(chunks, &serverDomain.ChunkData{Hash: e.Hash, Data: e.Data, RawSize: e.RawSize})
		}
		push = append(push, &serverDomain.PushFile{
			Path: path, Mode: 0o644, SizeBytes: int64(len(content)), Encoding: encoding,
			FileHash: treehash.FileHash(hashes), ChunkHashes: hashes,
		})
	}
	scope := domain.ChunkScope{Org: e2eOrgSlug, Project: e2eProjectSlug}
	for start := 0; start < len(chunks); start += 5000 {
		end := min(start+5000, len(chunks))
		_, _, err := c.UploadChunks(ctx, scope, chunks[start:end])
		require.NoError(t, err)
	}
	res, err := c.Push(ctx, e2eOrgSlug, e2eProjectSlug, branch, "", message, push, removed, "", base)
	require.NoError(t, err)
	return res.CommitID.Base36()
}

// TestEndToEnd_MergeRequestScale measures the merge request paths that
// compare commit trees (diff, mergeability, detail, create, the merge
// strategies, the commit diff) in a repository of NIPA_BENCH_FILES files
// spread like the load test's (1,676 directories) when the source branch
// differs from the merge base by NIPA_BENCH_CHANGED files (default 20) and the
// target moved on by one file. Each figure is the median of NIPA_BENCH_RUNS
// runs (default 5). NIPA_BENCH_HISTORY adds that many one-file commits before
// the branch point (default 20). With NIPA_BENCH_PROFILE=<dir> it writes CPU
// profiles of the diff and check loops there.
func TestEndToEnd_MergeRequestScale(t *testing.T) {
	if testing.Short() {
		t.Skip("merge request harness skipped in short mode")
	}
	total, err := strconv.Atoi(os.Getenv("NIPA_BENCH_FILES"))
	if err != nil || total <= 0 {
		t.Skip("set NIPA_BENCH_FILES=<count> to run the merge request harness")
	}
	runs := envInt("NIPA_BENCH_RUNS", 5)
	changed := envInt("NIPA_BENCH_CHANGED", 20)
	history := envInt("NIPA_BENCH_HISTORY", 20)
	profileDir := os.Getenv("NIPA_BENCH_PROFILE")

	ctx := context.Background()
	dbConn := openTestDB(t)
	counter := &treeReadCounter{BranchRepository: sqlite.NewBranchRepository(dbConn)}
	host, uc := startTestServerWithRepos(t, dbConn, func(r *serverusecase.Repositories) { r.Branch = counter })
	grpcClient, _ := newLoggedInClient(t, host)
	login, err := grpcClient.LoginWithUsernamePassword(ctx, host, e2eSuperAdminEmail, e2eSuperAdminPass)
	require.NoError(t, err)
	conn, err := grpc.NewClient(host, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	rpc := pb.NewNipaServiceClient(conn)
	adminCtx := metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+login.AccessToken)
	project := &pb.ProjectContext{Org: e2eOrgSlug, Project: e2eProjectSlug}
	admin, err := uc.User.GetByEmail(ctx, e2eSuperAdminEmail)
	require.NoError(t, err)
	serverCtx := serverDomain.ContextWithClaim(ctx, serverDomain.Claims{UserID: admin.ID, IsSuperAdmin: true, IsAdmin: true})
	_, proj, err := uc.Common.ResolveBySlug(serverCtx, e2eOrgSlug, e2eProjectSlug)
	require.NoError(t, err)

	paths := make([]string, total)
	seed := make(map[string]string, total)
	for i := range paths {
		paths[i] = smallUpdatePath(i)
		seed[paths[i]] = fmt.Sprintf(benchTreeFilePayload, i)
	}
	var head string
	timed(t, "initial push", func() { head = mrBenchPush(t, ctx, grpcClient, "main", "", "seed", seed, nil) })
	for i := 0; i < history; i++ {
		p := paths[(i*104729)%total]
		head = mrBenchPush(t, ctx, grpcClient, "main", head, fmt.Sprintf("history %d", i), map[string]string{p: fmt.Sprintf("history %d\n", i)}, nil)
	}
	branchPoint := head
	t.Logf("tree: %d files in %d directories, %d commits of history", total, 1+smallUpdateTops+smallUpdateTops*smallUpdateLeaves, history+1)

	// The source changes `changed` files spread over the tree: all but four
	// modified, two added and two removed.
	_, err = grpcClient.CreateBranch(ctx, e2eOrgSlug, e2eProjectSlug, "feature", "main", "", "")
	require.NoError(t, err)
	change := map[string]string{}
	var removed []string
	for k := 0; k < changed; k++ {
		p := paths[(k*7919+k*total/changed+13)%total]
		switch {
		case k < 2 && changed >= 4:
			removed = append(removed, p)
		case k < 4 && changed >= 4:
			change[fmt.Sprintf("d%02d/s%02d/new-%d.txt", k, k, k)] = fmt.Sprintf("added %d\n", k)
		default:
			change[p] = fmt.Sprintf("source change %d of %s\n", k, p)
		}
	}
	featureHead := mrBenchPush(t, ctx, grpcClient, "feature", branchPoint, "feature change", change, removed)
	// The target moves on by one unrelated file, so the request is diverged
	// and the merge base is not the target head.
	mainHead := mrBenchPush(t, ctx, grpcClient, "main", branchPoint, "target change", map[string]string{"d24/s65/target-only.txt": "target\n"}, nil)

	created, err := rpc.CreateMergeRequest(adminCtx, &pb.CreateMergeRequestRequest{
		Context: project, Title: "bench", SourceBranch: "feature", TargetBranch: "main",
	})
	require.NoError(t, err)
	number := created.GetMergeRequest().GetNumber()

	measure := func(op func()) mrSample {
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		counter.reset()
		start := time.Now()
		op()
		elapsed := time.Since(start)
		runtime.ReadMemStats(&after)
		return mrSample{
			elapsed: elapsed, batches: counter.batches.Load(), singles: counter.singles.Load(), commits: counter.commits.Load(),
			ids: counter.ids.Load(), rows: counter.rows.Load(),
			bytes: after.TotalAlloc - before.TotalAlloc, mallo: after.Mallocs - before.Mallocs,
		}
	}
	samples := map[string][]mrSample{}
	var order []string
	record := func(name string, s mrSample) {
		if _, ok := samples[name]; !ok {
			order = append(order, name)
		}
		samples[name] = append(samples[name], s)
	}
	profile := func(name string, fn func()) {
		if profileDir == "" {
			fn()
			return
		}
		f, err := os.Create(filepath.Join(profileDir, name+".pprof"))
		require.NoError(t, err)
		require.NoError(t, pprof.StartCPUProfile(f))
		fn()
		pprof.StopCPUProfile()
		require.NoError(t, f.Close())
	}

	featureID := mustParseBase36(t, featureHead)
	profile("mr_diff", func() {
		for run := 0; run < runs; run++ {
			record("mr diff (stream)", measure(func() {
				files, err := grpcClient.GetMergeRequestDiff(ctx, e2eOrgSlug, e2eProjectSlug, number)
				require.NoError(t, err)
				require.Len(t, files, changed)
			}))
		}
	})
	profile("mr_check", func() {
		for run := 0; run < runs; run++ {
			record("mr check", measure(func() {
				res, err := rpc.CheckMergeRequest(adminCtx, &pb.CheckMergeRequestRequest{Context: project, Number: number})
				require.NoError(t, err)
				require.Equal(t, serverDomain.MergeabilityBehind, res.GetMergeability().GetStatus())
			}))
		}
	})
	for run := 0; run < runs; run++ {
		record("mr get (detail + check)", measure(func() {
			_, err := rpc.GetMergeRequest(adminCtx, &pb.GetMergeRequestRequest{Context: project, Number: number})
			require.NoError(t, err)
		}))
		record("mr commits", measure(func() {
			_, err := rpc.ListMergeRequestCommits(adminCtx, &pb.ListMergeRequestCommitsRequest{Context: project, Number: number})
			require.NoError(t, err)
		}))
		record("path commit log (blame's server step)", measure(func() {
			_, err := uc.Branch.PathCommitLog(serverCtx, proj.ID, "main", paths[0], nil, 500)
			require.NoError(t, err)
		}))
		record("commit diff (source head vs parent)", measure(func() {
			files, _, err := uc.Branch.CommitDiff(serverCtx, proj.ID, featureID, nil)
			require.NoError(t, err)
			require.Len(t, files, changed)
		}))
	}

	// Every merge lands a fresh pair of branches: the target at the main
	// head (diverged) or at the branch point (ff), the source at the feature
	// head.
	for run := 0; run < runs; run++ {
		// Each run's diverged targets start from their own commit, so the
		// commits a rebase replays do not repeat an earlier run's hashes.
		runBase := fmt.Sprintf("main-run-%d", run)
		_, err = grpcClient.CreateBranch(ctx, e2eOrgSlug, e2eProjectSlug, runBase, "main", "", "")
		require.NoError(t, err)
		runHead := mrBenchPush(t, ctx, grpcClient, runBase, mainHead, "run "+runBase, map[string]string{"d23/s64/run.txt": runBase + "\n"}, nil)
		for _, strategy := range []string{"ff", "merge", "squash", "rebase"} {
			target := fmt.Sprintf("tgt-%s-%d", strategy, run)
			source := fmt.Sprintf("src-%s-%d", strategy, run)
			if strategy == "ff" {
				_, err = grpcClient.CreateBranch(ctx, e2eOrgSlug, e2eProjectSlug, target, "", branchPoint, "")
			} else {
				_, err = grpcClient.CreateBranch(ctx, e2eOrgSlug, e2eProjectSlug, target, "", runHead, "")
			}
			require.NoError(t, err)
			_, err = grpcClient.CreateBranch(ctx, e2eOrgSlug, e2eProjectSlug, source, "feature", "", "")
			require.NoError(t, err)
			var n int64
			record("mr create", measure(func() {
				res, err := rpc.CreateMergeRequest(adminCtx, &pb.CreateMergeRequestRequest{
					Context: project, Title: fmt.Sprintf("bench %s %d", strategy, run), SourceBranch: source, TargetBranch: target,
				})
				require.NoError(t, err)
				n = res.GetMergeRequest().GetNumber()
			}))
			record("mr merge --strategy "+strategy, measure(func() {
				_, err := rpc.MergeMergeRequest(adminCtx, &pb.MergeMergeRequestRequest{Context: project, Number: n, Strategy: strategy})
				require.NoError(t, err)
			}))
		}
	}

	t.Logf("%d files, %d changed on the source, median of %d runs", total, changed, runs)
	t.Logf("%-38s %9s %7s %7s %7s %7s %8s %9s %9s", "operation", "time", "batches", "dir-ids", "singles", "commits", "rows", "alloc", "objects")
	for _, name := range order {
		s := medianMRSample(samples[name])
		t.Logf("%-38s %9s %7d %7d %7d %7d %8d %7.1fMB %9d", name, s.elapsed.Round(100*time.Microsecond), s.batches, s.ids, s.singles, s.commits, s.rows, float64(s.bytes)/(1<<20), s.mallo)
	}
}

func envInt(name string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v >= 0 {
		return v
	}
	return def
}
