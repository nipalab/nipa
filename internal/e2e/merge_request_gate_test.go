package e2e

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/client/localrepo"
	clientusecase "github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/snow"
)

// TestEndToEnd_MergeRequestApprovalGate drives the review-policy gate over the
// real gRPC surface: branch approvals, objection blocks, the reopened/merged
// lifecycle events and the diff/commits RPCs.
func TestEndToEnd_MergeRequestApprovalGate(t *testing.T) {
	ctx := context.Background()
	dbConn := openTestDB(t)
	host := startTestServer(t, dbConn)
	grpcClient, auth := newLoggedInClient(t, host)
	pusher := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())

	mainDir := cloneWorktree(t, grpcClient, auth, host, "main")
	writeFile(t, mainDir, "a.txt", "base\n")
	stagePath(t, mainDir, "a.txt")
	require.NoError(t, pusher.Run(ctx, mainDir, "seed main"))

	_, err := grpcClient.CreateBranch(ctx, e2eOrgSlug, e2eProjectSlug, "feature", "main", "", "")
	require.NoError(t, err)
	featDir := cloneWorktree(t, grpcClient, auth, host, "feature")
	writeFile(t, featDir, "b.txt", "feature\n")
	stagePath(t, featDir, "b.txt")
	require.NoError(t, pusher.Run(ctx, featDir, "work on feature"))

	login, err := grpcClient.LoginWithUsernamePassword(ctx, host, e2eSuperAdminEmail, e2eSuperAdminPass)
	require.NoError(t, err)
	conn, err := grpc.NewClient(host, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()
	rpc := pb.NewNipaServiceClient(conn)
	adminCtx := metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+login.AccessToken)
	projectCtx := &pb.ProjectContext{Org: e2eOrgSlug, Project: e2eProjectSlug}

	reviewer := snow.ID(2)
	seedReviewer(t, dbConn, reviewer, "Gate Reviewer", "gate-reviewer@example.com")
	reviewerCtx := metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+mintToken(t, reviewer))

	one := int64(1)
	protection, err := rpc.SetBranchProtection(adminCtx, &pb.SetBranchProtectionRequest{
		Context: projectCtx, Name: "main", IsProtected: true, RequiredApprovals: &one,
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, protection.Branch.RequiredApprovals)

	mr, err := rpc.CreateMergeRequest(adminCtx, &pb.CreateMergeRequestRequest{
		Context: projectCtx, Title: "Gated", SourceBranch: "feature", TargetBranch: "main",
	})
	require.NoError(t, err)
	number := mr.MergeRequest.Number

	// close and reopen exercise the new RPC and record lifecycle events
	_, err = rpc.CloseMergeRequest(adminCtx, &pb.CloseMergeRequestRequest{Context: projectCtx, Number: number})
	require.NoError(t, err)
	reopened, err := rpc.ReopenMergeRequest(adminCtx, &pb.ReopenMergeRequestRequest{Context: projectCtx, Number: number})
	require.NoError(t, err)
	require.Equal(t, "open", reopened.MergeRequest.Status)

	check, err := rpc.CheckMergeRequest(adminCtx, &pb.CheckMergeRequestRequest{Context: projectCtx, Number: number})
	require.NoError(t, err)
	require.Equal(t, "mergeable", check.Mergeability.Status)
	require.Equal(t, "insufficient_approvals", check.Mergeability.BlockedBy)

	_, err = rpc.MergeMergeRequest(adminCtx, &pb.MergeMergeRequestRequest{Context: projectCtx, Number: number})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))

	// a live objection blocks even when the approval count is met
	_, err = rpc.SubmitMergeRequestReview(reviewerCtx, &pb.SubmitMergeRequestReviewRequest{
		Context: projectCtx, Number: number, State: "changes_requested", Body: "hold",
	})
	require.NoError(t, err)
	check, err = rpc.CheckMergeRequest(adminCtx, &pb.CheckMergeRequestRequest{Context: projectCtx, Number: number})
	require.NoError(t, err)
	require.Equal(t, "changes_requested", check.Mergeability.BlockedBy)

	// re-reviewing the same head replaces the decision and clears the block
	_, err = rpc.SubmitMergeRequestReview(reviewerCtx, &pb.SubmitMergeRequestReviewRequest{
		Context: projectCtx, Number: number, State: "approved", Body: "ok",
	})
	require.NoError(t, err)
	check, err = rpc.CheckMergeRequest(adminCtx, &pb.CheckMergeRequestRequest{Context: projectCtx, Number: number})
	require.NoError(t, err)
	require.Empty(t, check.Mergeability.BlockedBy)

	diff, err := rpc.GetMergeRequestDiff(adminCtx, &pb.GetMergeRequestDiffRequest{Context: projectCtx, Number: number})
	require.NoError(t, err)
	require.Len(t, diff.Files, 1)
	require.Equal(t, "b.txt", diff.Files[0].Path)
	require.NotEmpty(t, diff.Files[0].Hunks)

	commits, err := rpc.ListMergeRequestCommits(adminCtx, &pb.ListMergeRequestCommitsRequest{Context: projectCtx, Number: number})
	require.NoError(t, err)
	require.Len(t, commits.Commits, 1)

	merged, err := rpc.MergeMergeRequest(adminCtx, &pb.MergeMergeRequestRequest{Context: projectCtx, Number: number})
	require.NoError(t, err)
	require.Equal(t, "merged", merged.MergeRequest.Status)

	timeline, err := rpc.ListMergeRequestTimeline(adminCtx, &pb.ListMergeRequestTimelineRequest{Context: projectCtx, Number: number})
	require.NoError(t, err)
	kinds := make([]string, 0, len(timeline.Items))
	for _, item := range timeline.Items {
		kinds = append(kinds, item.Kind)
	}
	require.Contains(t, kinds, "opened")
	require.Contains(t, kinds, "closed")
	require.Contains(t, kinds, "reopened")
	require.Contains(t, kinds, "review_submitted")
	require.Contains(t, kinds, "merged")
}
