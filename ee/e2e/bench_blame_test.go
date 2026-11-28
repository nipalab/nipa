package e2e

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	eedb "github.com/nipalab/nipa/ee/db"
	clientgrpc "github.com/nipalab/nipa/internal/client/grpc"
	clientusecase "github.com/nipalab/nipa/internal/client/usecase"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	serverusecase "github.com/nipalab/nipa/internal/usecase"
)

// TestEnterpriseBlameScale is the postgres + S3 twin of the free harness
// TestEndToEnd_BlameScale: NIPA_BENCH_FILES files over 1,676 directories,
// NIPA_BENCH_HISTORY (default 500) one-file commits, the blamed file changed
// in every 25th; median of NIPA_BENCH_RUNS (default 5) for the per-path
// history of a file and of folders, a 20-file push and a merge commit of a
// 20-file branch, with the path change rows each write stored.
func TestEnterpriseBlameScale(t *testing.T) {
	if testing.Short() {
		t.Skip("blame harness skipped in short mode")
	}
	total, err := strconv.Atoi(os.Getenv("NIPA_BENCH_FILES"))
	if err != nil || total <= 0 {
		t.Skip("set NIPA_BENCH_FILES=<count> to run the blame harness")
	}
	runs := eeEnvInt("NIPA_BENCH_RUNS", 5)
	history := eeEnvInt("NIPA_BENCH_HISTORY", 500)

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
	blamed := paths[0]
	start := time.Now()
	head := eeMRPush(t, ctx, c, "main", "", "seed", seed, nil)
	t.Logf("initial push of %d files: %s", total, time.Since(start).Round(time.Millisecond))
	start = time.Now()
	for i := 0; i < history; i++ {
		p := paths[(i*104729+1)%total]
		if i%25 == 0 {
			p = blamed
		}
		head = eeMRPush(t, ctx, c, "main", head, fmt.Sprintf("history %d", i), map[string]string{p: fmt.Sprintf("history %d\n", i)}, nil)
	}
	t.Logf("history: %d one-file commits in %s", history, time.Since(start).Round(time.Millisecond))

	pathRows := func(commitID string) int64 {
		id, err := snow.ParseBase36(commitID)
		require.NoError(t, err)
		var n int64
		// The table does not exist before the path change index.
		if err := dbConn.QueryRowContext(ctx, "SELECT COUNT(*) FROM commit_path_changes WHERE commit_id = $1", id.Int64()).Scan(&n); err != nil {
			return -1
		}
		return n
	}

	samples := map[string][]time.Duration{}
	var order []string
	measure := func(name string, fn func()) {
		begin := time.Now()
		fn()
		if _, ok := samples[name]; !ok {
			order = append(order, name)
		}
		samples[name] = append(samples[name], time.Since(begin))
	}
	logOf := func(path string, limit int) func() {
		return func() {
			_, err := uc.Branch.PathCommitLog(serverCtx, project.ID, "main", path, nil, limit)
			require.NoError(t, err)
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
		measure("push 20 files", func() {
			commitID = eeMRPush(t, ctx, c, "main", head, fmt.Sprintf("push %d", run), change, nil)
		})
		head = commitID
		writeRows["push 20 files"] = pathRows(commitID)

		source := fmt.Sprintf("blame-src-%d", run)
		target := fmt.Sprintf("blame-tgt-%d", run)
		_, err := c.CreateBranch(ctx, e2eOrgSlug, e2eProjectSlug, source, "main", "", "")
		require.NoError(t, err)
		srcChange := map[string]string{}
		for k := 0; k < 20; k++ {
			srcChange[paths[(k*6007+run*17+9)%total]] = fmt.Sprintf("merge %d change %d\n", run, k)
		}
		eeMRPush(t, ctx, c, source, head, "source "+source, srcChange, nil)
		_, err = c.CreateBranch(ctx, e2eOrgSlug, e2eProjectSlug, target, "main", "", "")
		require.NoError(t, err)
		eeMRPush(t, ctx, c, target, head, "target "+target, map[string]string{fmt.Sprintf("d24/s65/target-%d.txt", run): "target\n"}, nil)
		measure("merge commit of a 20-file branch", func() {
			branch, err := uc.Branch.MergeForMergeRequest(serverCtx, project.ID, target, source, serverusecase.MergeCommitOptions{Strategy: serverDomain.MergeStrategyMerge, Author: admin.ID})
			require.NoError(t, err)
			writeRows["merge commit of a 20-file branch"] = pathRows(branch.CommitID.Base36())
		})
	}

	t.Logf("%d files, %d commits of history, median of %d runs", total, history, runs)
	t.Logf("%-40s %10s %10s", "operation", "time", "path rows")
	for _, name := range order {
		list := append([]time.Duration(nil), samples[name]...)
		sort.Slice(list, func(i, j int) bool { return list[i] < list[j] })
		rows := "-"
		if n, ok := writeRows[name]; ok {
			rows = strconv.FormatInt(n, 10)
		}
		t.Logf("%-40s %10s %10s", name, list[len(list)/2].Round(100*time.Microsecond), rows)
	}
}
