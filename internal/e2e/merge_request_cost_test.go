package e2e

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/repository/sqlite"
	serverusecase "github.com/nipalab/nipa/internal/usecase"
)

// TestEndToEnd_MergeRequestCostFollowsTheChange pins what the merge request
// paths read in a tree of the load test's shape (1,676 directories, here with
// 3,300 files) when the source changes three files in three leaf directories:
// the mergeability check reads no tree at all, and the diff and the lock scan
// at creation walk only the seven differing directories on each side, one
// file batch and one children batch per level plus one chunk batch.
func TestEndToEnd_MergeRequestCostFollowsTheChange(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbConn := openTestDB(t)
	counter := &treeReadCounter{BranchRepository: sqlite.NewBranchRepository(dbConn)}
	host, _ := startTestServerWithRepos(t, dbConn, func(r *serverusecase.Repositories) { r.Branch = counter })
	grpcClient, _ := newLoggedInClient(t, host)
	login, err := grpcClient.LoginWithUsernamePassword(ctx, host, e2eSuperAdminEmail, e2eSuperAdminPass)
	require.NoError(t, err)
	conn, err := grpc.NewClient(host, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	rpc := pb.NewNipaServiceClient(conn)
	adminCtx := metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+login.AccessToken)
	project := &pb.ProjectContext{Org: e2eOrgSlug, Project: e2eProjectSlug}

	const total = 3300
	seed := make(map[string]string, total)
	for i := 0; i < total; i++ {
		seed[smallUpdatePath(i)] = fmt.Sprintf(benchTreeFilePayload, i)
	}
	branchPoint := mrBenchPush(t, ctx, grpcClient, "main", "", "seed", seed, nil)
	_, err = grpcClient.CreateBranch(ctx, e2eOrgSlug, e2eProjectSlug, "feature", "main", "", "")
	require.NoError(t, err)
	// Two files of d00/s00, one of d01/s01, one new file in d07/s03.
	change := map[string]string{
		smallUpdatePath(0):    "changed 0\n",
		smallUpdatePath(1650): "changed 1650\n",
		smallUpdatePath(67):   "changed 67\n",
		"d07/s03/new.txt":     "new\n",
	}
	require.Equal(t, "d00/s00", smallUpdatePath(1650)[:7])
	require.Equal(t, "d01/s01", smallUpdatePath(67)[:7])
	mrBenchPush(t, ctx, grpcClient, "feature", branchPoint, "feature", change, nil)
	mrBenchPush(t, ctx, grpcClient, "main", branchPoint, "target", map[string]string{"d24/s65/target.txt": "t\n"}, nil)

	counter.reset()
	created, err := rpc.CreateMergeRequest(adminCtx, &pb.CreateMergeRequestRequest{
		Context: project, Title: "cost", SourceBranch: "feature", TargetBranch: "main",
	})
	require.NoError(t, err)
	number := created.GetMergeRequest().GetNumber()
	// The binary-lock scan: three levels of two batches each, plus one chunk
	// batch for the changed files.
	require.Equal(t, int64(7), counter.batches.Load(), "create: batched tree reads")
	require.Equal(t, int64(2*2*(1+3+3)), counter.ids.Load(), "create: directory ids read")
	require.Equal(t, int64(2), counter.singles.Load(), "create: the two root nodes")

	counter.reset()
	check, err := rpc.CheckMergeRequest(adminCtx, &pb.CheckMergeRequestRequest{Context: project, Number: number})
	require.NoError(t, err)
	require.Equal(t, serverDomain.MergeabilityBehind, check.GetMergeability().GetStatus())
	require.Zero(t, counter.batches.Load()+counter.singles.Load(), "the mergeability check reads no tree")

	counter.reset()
	_, err = rpc.GetMergeRequest(adminCtx, &pb.GetMergeRequestRequest{Context: project, Number: number})
	require.NoError(t, err)
	require.Zero(t, counter.batches.Load()+counter.singles.Load(), "the detail view reads no tree")

	counter.reset()
	files, err := grpcClient.GetMergeRequestDiff(ctx, e2eOrgSlug, e2eProjectSlug, number)
	require.NoError(t, err)
	require.Len(t, files, len(change))
	require.Equal(t, int64(7), counter.batches.Load(), "diff: batched tree reads")
	require.Equal(t, int64(2*2*(1+3+3)), counter.ids.Load(), "diff: directory ids read")
	require.Equal(t, int64(2), counter.singles.Load(), "diff: the two root nodes")
	// Root and three top directories list their children on both sides,
	// the three leaves their few files, and the changed files their chunks.
	t.Logf("diff read %d rows over %d directory ids", counter.rows.Load(), counter.ids.Load())
	require.Less(t, counter.rows.Load(), int64(500), "rows follow the changed directories, not the tree")
}
