package e2e

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/sqlite"
	"github.com/nipalab/nipa/internal/snow"
	serverusecase "github.com/nipalab/nipa/internal/usecase"
)

// blameReadCounter extends treeReadCounter with the commit walk and the path
// change index reads, so a sample's statement count covers every repository
// call the per-path history makes after resolving the revision.
type blameReadCounter struct {
	*treeReadCounter
	other atomic.Int64
}

func (c *blameReadCounter) CommitLog(ctx context.Context, projectID, startID snow.ID, limit int) ([]*serverDomain.CommitLogEntry, error) {
	c.other.Add(1)
	return c.treeReadCounter.CommitLog(ctx, projectID, startID, limit)
}

func (c *blameReadCounter) PathChangeLog(ctx context.Context, q serverusecase.PathChangeQuery) ([]*serverDomain.CommitLogEntry, error) {
	c.other.Add(1)
	return c.treeReadCounter.PathChangeLog(ctx, q)
}

func (c *blameReadCounter) PathChangeRows(ctx context.Context, q serverusecase.PathChangeQuery, commitIDs []snow.ID) ([]serverusecase.PathChangeRow, error) {
	c.other.Add(1)
	return c.treeReadCounter.PathChangeRows(ctx, q, commitIDs)
}

func (c *blameReadCounter) UnindexedPathChangeCommits(ctx context.Context, projectID, startID snow.ID, scanLimit int) ([]serverusecase.UnindexedCommit, error) {
	c.other.Add(1)
	return c.treeReadCounter.UnindexedPathChangeCommits(ctx, projectID, startID, scanLimit)
}

func (c *blameReadCounter) RecordPathChanges(ctx context.Context, commitID snow.ID, changes []serverusecase.PathChange) error {
	c.other.Add(1)
	return c.treeReadCounter.RecordPathChanges(ctx, commitID, changes)
}

func (c *blameReadCounter) statements() int64 {
	return c.batches.Load() + c.singles.Load() + c.commits.Load() + c.other.Load()
}

func (c *blameReadCounter) resetAll() {
	c.reset()
	c.other.Store(0)
}

// TestEndToEnd_BlameScale measures the per-path history (PathCommitLog, the
// server step of blame and the web UI file and folder history) in a repository
// of NIPA_BENCH_FILES files spread like the load test's (1,676 directories)
// with NIPA_BENCH_HISTORY one-file commits on top (default 500). The blamed
// file changes in every 25th of them. It also measures the write side: a
// 20-file push and a merge commit of a 20-file branch, with the path change
// rows each wrote. Each figure is the median of NIPA_BENCH_RUNS runs
// (default 5).
func TestEndToEnd_BlameScale(t *testing.T) {
	if testing.Short() {
		t.Skip("blame harness skipped in short mode")
	}
	total, err := strconv.Atoi(os.Getenv("NIPA_BENCH_FILES"))
	if err != nil || total <= 0 {
		t.Skip("set NIPA_BENCH_FILES=<count> to run the blame harness")
	}
	runs := envInt("NIPA_BENCH_RUNS", 5)
	history := envInt("NIPA_BENCH_HISTORY", 500)

	ctx := context.Background()
	dbConn := openTestDB(t)
	counter := &blameReadCounter{treeReadCounter: &treeReadCounter{BranchRepository: sqlite.NewBranchRepository(dbConn)}}
	host, uc := startTestServerWithRepos(t, dbConn, func(r *serverusecase.Repositories) { r.Branch = counter })
	grpcClient, _ := newLoggedInClient(t, host)
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
	blamed := paths[0]
	var head string
	timed(t, "initial push", func() { head = mrBenchPush(t, ctx, grpcClient, "main", "", "seed", seed, nil) })
	start := time.Now()
	blamedChanges := 1
	for i := 0; i < history; i++ {
		p := paths[(i*104729+1)%total]
		if i%25 == 0 {
			p = blamed
			blamedChanges++
		}
		head = mrBenchPush(t, ctx, grpcClient, "main", head, fmt.Sprintf("history %d", i), map[string]string{p: fmt.Sprintf("history %d\n", i)}, nil)
	}
	t.Logf("history: %d one-file commits in %s, the blamed file changed in %d", history, time.Since(start).Round(time.Millisecond), blamedChanges)

	pathRows := func(commitID string) int64 {
		var n int64
		// The table does not exist before the path change index.
		if err := dbConn.QueryRowContext(ctx, "SELECT COUNT(*) FROM commit_path_changes WHERE commit_id = ?", mustParseBase36(t, commitID).Int64()).Scan(&n); err != nil {
			return -1
		}
		return n
	}

	type sample struct {
		elapsed    time.Duration
		statements int64
		results    int
	}
	samples := map[string][]sample{}
	var order []string
	record := func(name string, s sample) {
		if _, ok := samples[name]; !ok {
			order = append(order, name)
		}
		samples[name] = append(samples[name], s)
	}
	measure := func(name string, fn func() int) {
		counter.resetAll()
		begin := time.Now()
		n := fn()
		record(name, sample{elapsed: time.Since(begin), statements: counter.statements(), results: n})
	}
	logOf := func(path string, limit int) func() int {
		return func() int {
			entries, err := uc.Branch.PathCommitLog(serverCtx, proj.ID, "main", path, nil, limit)
			require.NoError(t, err)
			return len(entries)
		}
	}
	// A history stored before the path change index (the load test's
	// existing data) is indexed by the first read that walks it: drop the
	// index (nothing to drop before it existed) and time that first read
	// apart from the steady state.
	_, _ = dbConn.ExecContext(ctx, "DELETE FROM commit_path_changes WHERE commit_id > 0")
	_, _ = dbConn.ExecContext(ctx, "DELETE FROM commit_path_index WHERE commit_id > 0")
	measure("first call, history not indexed yet", logOf(blamed, 500))
	for run := 0; run < runs; run++ {
		measure("blame server step (file, limit 500)", logOf(blamed, 500))
		measure("file history page (limit 50)", logOf(paths[1], 50))
		measure("folder history d00 (limit 50)", logOf("d00", 50))
		measure("folder history d00/s00 (limit 50)", logOf("d00/s00", 50))
	}

	writeRows := map[string]int64{}
	for run := 0; run < runs; run++ {
		change := map[string]string{}
		for k := 0; k < 20; k++ {
			change[paths[(k*7919+run*31+5)%total]] = fmt.Sprintf("push %d change %d\n", run, k)
		}
		var commitID string
		measure("push 20 files", func() int {
			commitID = mrBenchPush(t, ctx, grpcClient, "main", head, fmt.Sprintf("push %d", run), change, nil)
			return 20
		})
		head = commitID
		writeRows["push 20 files"] = pathRows(commitID)

		source := fmt.Sprintf("blame-src-%d", run)
		target := fmt.Sprintf("blame-tgt-%d", run)
		_, err := grpcClient.CreateBranch(ctx, e2eOrgSlug, e2eProjectSlug, source, "main", "", "")
		require.NoError(t, err)
		srcChange := map[string]string{}
		for k := 0; k < 20; k++ {
			srcChange[paths[(k*6007+run*17+9)%total]] = fmt.Sprintf("merge %d change %d\n", run, k)
		}
		mrBenchPush(t, ctx, grpcClient, source, head, "source "+source, srcChange, nil)
		_, err = grpcClient.CreateBranch(ctx, e2eOrgSlug, e2eProjectSlug, target, "main", "", "")
		require.NoError(t, err)
		mrBenchPush(t, ctx, grpcClient, target, head, "target "+target, map[string]string{fmt.Sprintf("d24/s65/target-%d.txt", run): "target\n"}, nil)
		measure("merge commit of a 20-file branch", func() int {
			branch, err := uc.Branch.MergeForMergeRequest(serverCtx, proj.ID, target, source, serverusecase.MergeCommitOptions{Strategy: serverDomain.MergeStrategyMerge, Author: admin.ID})
			require.NoError(t, err)
			writeRows["merge commit of a 20-file branch"] = pathRows(branch.CommitID.Base36())
			return 20
		})
	}

	t.Logf("%d files, %d commits of history, median of %d runs", total, history, runs)
	t.Logf("%-40s %10s %10s %8s %10s", "operation", "time", "statements", "results", "path rows")
	for _, name := range order {
		list := samples[name]
		sorted := append([]sample(nil), list...)
		for i := range sorted {
			for j := i + 1; j < len(sorted); j++ {
				if sorted[j].elapsed < sorted[i].elapsed {
					sorted[i], sorted[j] = sorted[j], sorted[i]
				}
			}
		}
		s := sorted[len(sorted)/2]
		rows := "-"
		if n, ok := writeRows[name]; ok {
			rows = strconv.FormatInt(n, 10)
		}
		t.Logf("%-40s %10s %10d %8d %10s", name, s.elapsed.Round(100*time.Microsecond), s.statements, s.results, rows)
	}
}
