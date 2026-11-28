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
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	eedb "github.com/nipalab/nipa/ee/db"
	"github.com/nipalab/nipa/internal/chunker"
	clientdomain "github.com/nipalab/nipa/internal/client/domain"
	clientgrpc "github.com/nipalab/nipa/internal/client/grpc"
	clientusecase "github.com/nipalab/nipa/internal/client/usecase"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/treehash"
)

// eeMRPath spreads files like the load test's repository: 25 top directories
// of 66 leaves, 1,676 directories with the root.
func eeMRPath(i int) string {
	leaf := i % (25 * 66)
	return fmt.Sprintf("d%02d/s%02d/f%06d.txt", leaf/66, leaf%66, i)
}

func eeMRPush(t *testing.T, ctx context.Context, c *clientgrpc.Client, branch, base, message string, files map[string]string, removed []string) string {
	t.Helper()
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	push := make([]*serverDomain.PushFile, 0, len(paths))
	var chunks []*serverDomain.ChunkData
	for _, path := range paths {
		encoding, encoded, err := chunker.EncodeBytes([]byte(files[path]), path, false, "")
		require.NoError(t, err)
		hashes := make([]serverDomain.Hash, 0, len(encoded))
		for _, e := range encoded {
			hashes = append(hashes, e.Hash)
			chunks = append(chunks, &serverDomain.ChunkData{Hash: e.Hash, Data: e.Data, RawSize: e.RawSize})
		}
		push = append(push, &serverDomain.PushFile{
			Path: path, Mode: 0o644, SizeBytes: int64(len(files[path])), Encoding: encoding,
			FileHash: treehash.FileHash(hashes), ChunkHashes: hashes,
		})
	}
	scope := clientdomain.ChunkScope{Org: e2eOrgSlug, Project: e2eProjectSlug}
	for start := 0; start < len(chunks); start += 5000 {
		_, _, err := c.UploadChunks(ctx, scope, chunks[start:min(start+5000, len(chunks))])
		require.NoError(t, err)
	}
	res, err := c.Push(ctx, e2eOrgSlug, e2eProjectSlug, branch, "", message, push, removed, "", base)
	require.NoError(t, err)
	return res.CommitID.Base36()
}

func eeEnvInt(name string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v >= 0 {
		return v
	}
	return def
}

// TestEnterpriseMergeRequestScale is the postgres + S3 twin of the free
// harness TestEndToEnd_MergeRequestScale: NIPA_BENCH_FILES files over 1,676
// directories, a source NIPA_BENCH_CHANGED (default 20) files ahead of the
// merge base, a target one file ahead; median of NIPA_BENCH_RUNS (default 5)
// for the merge request diff, check, detail, create and every merge strategy.
// NIPA_BENCH_PROFILE=<dir> writes a CPU profile of the diff loop there.
func TestEnterpriseMergeRequestScale(t *testing.T) {
	if testing.Short() {
		t.Skip("merge request harness skipped in short mode")
	}
	total, err := strconv.Atoi(os.Getenv("NIPA_BENCH_FILES"))
	if err != nil || total <= 0 {
		t.Skip("set NIPA_BENCH_FILES=<count> to run the merge request harness")
	}
	runs := eeEnvInt("NIPA_BENCH_RUNS", 5)
	changed := eeEnvInt("NIPA_BENCH_CHANGED", 20)

	ctx := context.Background()
	dbConn, err := eedb.Open(testPGDSN, eedb.PoolConfig{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = dbConn.Close() })
	require.NoError(t, eedb.MigrateUp(dbConn))
	host, uc := startEnterpriseServerWithUsecases(t, dbConn, newS3ChunkStore(t), mustSnowNode(t, 0), nil)

	store := newMemoryStore()
	transport := clientgrpc.NewTransport()
	session := clientusecase.NewSession(store, transport, failPrompt{})
	c := clientgrpc.NewClient(transport, session)
	require.NoError(t, c.Connect(ctx, host))
	login, err := c.LoginWithUsernamePassword(ctx, host, e2eSuperAdmin, e2eSuperPass)
	require.NoError(t, err)
	require.NoError(t, store.SaveToken(login))
	admin, err := uc.User.GetByEmail(ctx, e2eSuperAdmin)
	require.NoError(t, err)
	serverCtx := serverDomain.ContextWithClaim(ctx, serverDomain.Claims{UserID: admin.ID, IsSuperAdmin: true, IsAdmin: true})
	_, project, err := uc.Common.ResolveBySlug(serverCtx, e2eOrgSlug, e2eProjectSlug)
	require.NoError(t, err)

	paths := make([]string, total)
	seed := make(map[string]string, total)
	for i := range paths {
		paths[i] = eeMRPath(i)
		seed[paths[i]] = fmt.Sprintf("wide tree bench record, unique id=%06d\n", i)
	}
	start := time.Now()
	branchPoint := eeMRPush(t, ctx, c, "main", "", "seed", seed, nil)
	t.Logf("initial push of %d files: %s", total, time.Since(start).Round(time.Millisecond))

	_, err = c.CreateBranch(ctx, e2eOrgSlug, e2eProjectSlug, "feature", "main", "", "")
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
	featureHead := eeMRPush(t, ctx, c, "feature", branchPoint, "feature change", change, removed)
	mainHead := eeMRPush(t, ctx, c, "main", branchPoint, "target change", map[string]string{"d24/s65/target-only.txt": "target\n"}, nil)
	created, err := c.CreateMergeRequest(ctx, e2eOrgSlug, e2eProjectSlug, clientdomain.NewMergeRequest{Title: "bench", SourceBranch: "feature", TargetBranch: "main"})
	require.NoError(t, err)
	number := created.Number

	type sample struct {
		elapsed      time.Duration
		bytes, mallo uint64
	}
	samples := map[string][]sample{}
	var order []string
	measure := func(name string, op func()) {
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		begin := time.Now()
		op()
		elapsed := time.Since(begin)
		runtime.ReadMemStats(&after)
		if _, ok := samples[name]; !ok {
			order = append(order, name)
		}
		samples[name] = append(samples[name], sample{elapsed, after.TotalAlloc - before.TotalAlloc, after.Mallocs - before.Mallocs})
	}

	featureID, err := snow.ParseBase36(featureHead)
	require.NoError(t, err)
	diffLoop := func() {
		for run := 0; run < runs; run++ {
			measure("mr diff (stream)", func() {
				files, err := c.GetMergeRequestDiff(ctx, e2eOrgSlug, e2eProjectSlug, number)
				require.NoError(t, err)
				require.Len(t, files, changed)
			})
		}
	}
	if dir := os.Getenv("NIPA_BENCH_PROFILE"); dir != "" {
		f, err := os.Create(filepath.Join(dir, "ee_mr_diff.pprof"))
		require.NoError(t, err)
		require.NoError(t, pprof.StartCPUProfile(f))
		diffLoop()
		pprof.StopCPUProfile()
		require.NoError(t, f.Close())
	} else {
		diffLoop()
	}
	for run := 0; run < runs; run++ {
		measure("mr check", func() {
			m, err := c.CheckMergeRequest(ctx, e2eOrgSlug, e2eProjectSlug, number)
			require.NoError(t, err)
			require.Equal(t, serverDomain.MergeabilityBehind, m.Status)
		})
		measure("mr get (detail + check)", func() {
			_, _, err := c.GetMergeRequest(ctx, e2eOrgSlug, e2eProjectSlug, number)
			require.NoError(t, err)
		})
		measure("commit diff (source head vs parent)", func() {
			files, _, err := uc.Branch.CommitDiff(serverCtx, project.ID, featureID, nil)
			require.NoError(t, err)
			require.Len(t, files, changed)
		})
		measure("path commit log (blame's server step)", func() {
			_, err := uc.Branch.PathCommitLog(serverCtx, project.ID, "main", paths[0], nil, 500)
			require.NoError(t, err)
		})
	}
	for run := 0; run < runs; run++ {
		runBase := fmt.Sprintf("main-run-%d", run)
		_, err = c.CreateBranch(ctx, e2eOrgSlug, e2eProjectSlug, runBase, "main", "", "")
		require.NoError(t, err)
		runHead := eeMRPush(t, ctx, c, runBase, mainHead, "run "+runBase, map[string]string{"d23/s64/run.txt": runBase + "\n"}, nil)
		for _, strategy := range []string{"ff", "merge", "squash", "rebase"} {
			target := fmt.Sprintf("tgt-%s-%d", strategy, run)
			source := fmt.Sprintf("src-%s-%d", strategy, run)
			from := runHead
			if strategy == "ff" {
				from = branchPoint
			}
			_, err = c.CreateBranch(ctx, e2eOrgSlug, e2eProjectSlug, target, "", from, "")
			require.NoError(t, err)
			_, err = c.CreateBranch(ctx, e2eOrgSlug, e2eProjectSlug, source, "feature", "", "")
			require.NoError(t, err)
			var n int64
			measure("mr create", func() {
				mr, err := c.CreateMergeRequest(ctx, e2eOrgSlug, e2eProjectSlug, clientdomain.NewMergeRequest{
					Title: fmt.Sprintf("bench %s %d", strategy, run), SourceBranch: source, TargetBranch: target,
				})
				require.NoError(t, err)
				n = mr.Number
			})
			measure("mr merge --strategy "+strategy, func() {
				_, _, err := c.MergeMergeRequest(ctx, e2eOrgSlug, e2eProjectSlug, n, strategy, false)
				require.NoError(t, err)
			})
		}
	}

	t.Logf("%d files, %d changed on the source, median of %d runs (postgres + S3)", total, changed, runs)
	t.Logf("%-40s %10s %10s %10s", "operation", "time", "alloc", "objects")
	for _, name := range order {
		list := samples[name]
		sort.Slice(list, func(i, j int) bool { return list[i].elapsed < list[j].elapsed })
		s := list[len(list)/2]
		t.Logf("%-40s %10s %8.1fMB %10d", name, s.elapsed.Round(100*time.Microsecond), float64(s.bytes)/(1<<20), s.mallo)
	}
}
