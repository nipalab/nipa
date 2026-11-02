// Enterprise Edition: see ee/LICENSE.
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

	eedb "github.com/nipalab/nipa/ee/db"
	"github.com/nipalab/nipa/ee/repository/postgres"
	clientgrpc "github.com/nipalab/nipa/internal/client/grpc"
	"github.com/nipalab/nipa/internal/client/localrepo"
	clientusecase "github.com/nipalab/nipa/internal/client/usecase"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

const benchTreeDirs = 10

func benchTreePaths(total int) []string {
	paths := make([]string, 0, total)
	perDir := (total + benchTreeDirs - 1) / benchTreeDirs
	for i := 0; i < total; i++ {
		paths = append(paths, fmt.Sprintf("d%02d/f%06d.txt", i/perDir, i))
	}
	return paths
}

func timed(t *testing.T, label string, fn func()) {
	t.Helper()
	start := time.Now()
	fn()
	t.Logf("%-28s %s", label, time.Since(start).Round(time.Millisecond))
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

// TestEnterpriseWideTreeScale mirrors internal/e2e's wide-tree harness on
// postgres + S3, where every statement is a network round trip. It is skipped
// unless NIPA_BENCH_FILES is set.
func TestEnterpriseWideTreeScale(t *testing.T) {
	if testing.Short() {
		t.Skip("wide-tree harness skipped in short mode")
	}
	total, err := strconv.Atoi(os.Getenv("NIPA_BENCH_FILES"))
	if err != nil || total <= 0 {
		t.Skip("set NIPA_BENCH_FILES=<count> to run the wide-tree harness")
	}

	ctx := context.Background()
	dbConn, err := eedb.Open(testPGDSN)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dbConn.Close() })
	require.NoError(t, eedb.MigrateUp(dbConn))

	host, uc := startEnterpriseServerWithUsecases(t, dbConn, newS3ChunkStore(t), mustSnowNode(t, 0), nil)

	store := newMemoryStore()
	transport := clientgrpc.NewTransport()
	session := clientusecase.NewSession(store, transport, failPrompt{})
	grpcClient := clientgrpc.NewClient(transport, session)
	auth := clientusecase.NewAuth(grpcClient, store, failPrompt{})
	require.NoError(t, grpcClient.Connect(ctx, host))
	loginResult, err := grpcClient.LoginWithUsernamePassword(ctx, host, e2eSuperAdmin, e2eSuperPass)
	require.NoError(t, err)
	require.NoError(t, store.SaveToken(loginResult))

	admin, err := uc.User.GetByEmail(ctx, e2eSuperAdmin)
	require.NoError(t, err)
	serverCtx := serverDomain.ContextWithClaim(ctx, serverDomain.Claims{UserID: admin.ID, IsSuperAdmin: true, IsAdmin: true})
	_, project, err := uc.Common.ResolveBySlug(serverCtx, e2eOrgSlug, e2eProjectSlug)
	require.NoError(t, err)

	repo := clientusecase.NewRepo(auth, grpcClient, localrepo.NewLocalRepo())
	pusher := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())
	url := "http://" + host + "/" + e2eOrgSlug + "/" + e2eProjectSlug
	dir := filepath.Join(t.TempDir(), "work")
	require.NoError(t, repo.Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", nil, dir))

	paths := benchTreePaths(total)
	for i, p := range paths {
		writeFile(t, dir, p, fmt.Sprintf("wide tree bench record, unique id=%06d\n", i))
	}
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

	branchRepo := postgres.NewBranchRepository(dbConn)
	branch, err := branchRepo.GetBranchByName(ctx, project.ID, "main")
	require.NoError(t, err)
	require.NotNil(t, branch.CommitID)
	head, err := branchRepo.GetCommit(ctx, *branch.CommitID)
	require.NoError(t, err)
	require.NotNil(t, head.Parent1ID)

	timed(t, "commit diff", func() {
		files, _, derr := uc.Branch.CommitDiff(serverCtx, project.ID, head.ID, nil)
		require.NoError(t, derr)
		require.Len(t, files, 1)
	})
	timed(t, "merge request diff", func() {
		files, derr := uc.Branch.TreeDiffBetween(serverCtx, project.ID, head.Parent1ID, head.ID)
		require.NoError(t, derr)
		require.Len(t, files, 1)
	})
	timed(t, "clone", func() {
		require.NoError(t, repo.Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", nil, filepath.Join(t.TempDir(), "checkout")))
	})
}

func stagePaths(t *testing.T, dir string, paths []string) {
	t.Helper()
	lr := localrepo.NewLocalRepo()
	require.NoError(t, lr.Init(dir))
	defer func() { _ = lr.Close() }()
	require.NoError(t, lr.StageAddMany(paths))
}
