package e2e

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/localrepo"
	clientusecase "github.com/nipalab/nipa/internal/client/usecase"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

const (
	benchTreeDirs        = 10
	benchTreeFilePayload = "wide tree bench record, unique id=%06d\n"
)

func benchTreePaths(total int) []string {
	paths := make([]string, 0, total)
	perDir := (total + benchTreeDirs - 1) / benchTreeDirs
	for i := 0; i < total; i++ {
		paths = append(paths, fmt.Sprintf("d%02d/f%06d.txt", i/perDir, i))
	}
	return paths
}

func writeBenchTree(t *testing.T, dir string, paths []string) {
	t.Helper()
	for i, p := range paths {
		fp := filepath.Join(dir, filepath.FromSlash(p))
		require.NoError(t, os.MkdirAll(filepath.Dir(fp), 0o755))
		require.NoError(t, os.WriteFile(fp, []byte(fmt.Sprintf(benchTreeFilePayload, i)), 0o644))
	}
}

func stagePaths(t *testing.T, dir string, paths []string) {
	t.Helper()
	lr := localrepo.NewLocalRepo()
	require.NoError(t, lr.Init(dir))
	defer func() { _ = lr.Close() }()
	require.NoError(t, lr.StageAddMany(paths))
}

func timed(t *testing.T, label string, fn func()) time.Duration {
	t.Helper()
	start := time.Now()
	fn()
	elapsed := time.Since(start)
	t.Logf("%-28s %s", label, elapsed.Round(time.Millisecond))
	return elapsed
}

func countTreeFiles(node *serverDomain.TreeNode) int {
	if node == nil {
		return 0
	}
	n := len(node.FileChildren)
	for _, child := range node.TreeChildren {
		n += countTreeFiles(child)
	}
	return n
}

// TestEndToEnd_WideTreeScale baselines the server paths that grow with the
// number of tracked files: the initial push, a one-file change inside a wide
// directory, the recursive manifest and the commit/merge-request diff. It is
// skipped unless NIPA_BENCH_FILES is set.
func TestEndToEnd_WideTreeScale(t *testing.T) {
	if testing.Short() {
		t.Skip("wide-tree harness skipped in short mode")
	}
	total, err := strconv.Atoi(os.Getenv("NIPA_BENCH_FILES"))
	if err != nil || total <= 0 {
		t.Skip("set NIPA_BENCH_FILES=<count> to run the wide-tree harness")
	}

	ctx := context.Background()
	dbConn := openTestDB(t)
	host, uc := startTestServerWithUsecases(t, dbConn)
	grpcClient, auth, _ := newBenchClient(t, host)
	pusher := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())

	admin, err := uc.User.GetByEmail(ctx, e2eSuperAdminEmail)
	require.NoError(t, err)
	serverCtx := serverDomain.ContextWithClaim(ctx, serverDomain.Claims{UserID: admin.ID, IsSuperAdmin: true, IsAdmin: true})
	_, project, err := uc.Common.ResolveBySlug(serverCtx, e2eOrgSlug, e2eProjectSlug)
	require.NoError(t, err)

	dir := cloneWorktree(t, grpcClient, auth, host, "main")
	paths := benchTreePaths(total)
	writeBenchTree(t, dir, paths)
	stagePaths(t, dir, paths)
	t.Logf("tree: %d files across %d directories", total, benchTreeDirs)

	timed(t, "initial push", func() { require.NoError(t, pusher.Run(ctx, dir, "seed wide tree")) })

	writeFile(t, dir, paths[0], "changed content\n")
	stagePaths(t, dir, paths[:1])
	timed(t, "one-file change push", func() { require.NoError(t, pusher.Run(ctx, dir, "touch one file")) })

	var manifest *serverDomain.TreeNode
	timed(t, "recursive manifest", func() {
		manifest, err = uc.Branch.GetTreeManifest(serverCtx, project.ID, "main", nil, "", true)
		require.NoError(t, err)
	})
	require.Equal(t, total, countTreeFiles(manifest))

	head := branchHeadCommit(t, dbConn, "main")
	require.NotNil(t, head.Parent1ID)

	var commitDiff int
	timed(t, "commit diff", func() {
		files, _, derr := uc.Branch.CommitDiff(serverCtx, project.ID, head.ID, nil)
		require.NoError(t, derr)
		commitDiff = len(files)
	})
	require.Equal(t, 1, commitDiff)

	var mrDiff int
	timed(t, "merge request diff", func() {
		files, derr := uc.Branch.TreeDiffBetween(serverCtx, project.ID, head.Parent1ID, head.ID)
		require.NoError(t, derr)
		mrDiff = len(files)
	})
	require.Equal(t, 1, mrDiff)

	timed(t, "clone", func() { cloneWorktree(t, grpcClient, auth, host, "main") })
}
