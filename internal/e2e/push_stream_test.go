package e2e

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/localrepo"
	clientusecase "github.com/nipalab/nipa/internal/client/usecase"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

// TestEndToEnd_ManifestsOverGRPCDefault pushes, clones and updates a tree
// over the streamed push and manifest RPCs. With NIPA_E2E_LARGE_MANIFEST=1 the
// tree has 25000 files, so the unary PushRequest and GetTreeManifestResponse
// would both exceed the 4 MiB gRPC message limit.
func TestEndToEnd_ManifestsOverGRPCDefault(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("large manifest skipped in short mode")
	}
	total := 1000
	if os.Getenv("NIPA_E2E_LARGE_MANIFEST") == "1" {
		total = 25000
	}
	ctx := context.Background()
	dbConn := openTestDB(t)
	host, uc := startTestServerWithUsecases(t, dbConn)
	grpcClient, auth, _ := newBenchClient(t, host)
	pusher := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())

	dir := cloneWorktree(t, grpcClient, auth, host, "main")
	paths := make([]string, 0, total)
	for i := 0; i < total; i++ {
		p := fmt.Sprintf("node_modules/package-%05d/dist/index.js", i)
		fp := filepath.Join(dir, filepath.FromSlash(p))
		require.NoError(t, os.MkdirAll(filepath.Dir(fp), 0o755))
		require.NoError(t, os.WriteFile(fp, []byte(fmt.Sprintf("module.exports = %d\n", i)), 0o644))
		paths = append(paths, p)
	}
	stagePaths(t, dir, paths)
	timed(t, "initial push", func() { require.NoError(t, pusher.Run(ctx, dir, "add node_modules")) })

	admin, err := uc.User.GetByEmail(ctx, e2eSuperAdminEmail)
	require.NoError(t, err)
	serverCtx := serverDomain.ContextWithClaim(ctx, serverDomain.Claims{UserID: admin.ID, IsSuperAdmin: true, IsAdmin: true})
	_, project, err := uc.Common.ResolveBySlug(serverCtx, e2eOrgSlug, e2eProjectSlug)
	require.NoError(t, err)
	manifest, err := uc.Branch.GetTreeManifest(serverCtx, project.ID, "main", nil, "", true)
	require.NoError(t, err)
	require.Equal(t, total, countTreeFiles(manifest))

	var other string
	timed(t, "clone", func() { other = cloneWorktree(t, grpcClient, auth, host, "main") })
	got, err := os.ReadFile(filepath.Join(other, filepath.FromSlash(paths[total-1])))
	require.NoError(t, err)
	require.Equal(t, fmt.Sprintf("module.exports = %d\n", total-1), string(got))

	for _, p := range paths[:total/2] {
		require.NoError(t, os.Remove(filepath.Join(dir, filepath.FromSlash(p))))
	}
	stagePaths(t, dir, paths[:total/2])
	timed(t, "push removing half", func() { require.NoError(t, pusher.Run(ctx, dir, "drop half")) })
	manifest, err = uc.Branch.GetTreeManifest(serverCtx, project.ID, "main", nil, "", true)
	require.NoError(t, err)
	require.Equal(t, total-total/2, countTreeFiles(manifest))

	timed(t, "update", func() {
		require.NoError(t, clientusecase.NewUpdate(auth, grpcClient, localrepo.NewLocalRepo()).Run(ctx, other))
	})
	_, err = os.Stat(filepath.Join(other, filepath.FromSlash(paths[0])))
	require.True(t, os.IsNotExist(err), "update must remove the dropped files")
	_, err = os.Stat(filepath.Join(other, filepath.FromSlash(paths[total-1])))
	require.NoError(t, err)
}
