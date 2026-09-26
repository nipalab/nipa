package e2e

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	clientgrpc "github.com/nipalab/nipa/internal/client/grpc"
	"github.com/nipalab/nipa/internal/client/localrepo"
	clientusecase "github.com/nipalab/nipa/internal/client/usecase"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/snow"
)

// seedReviewer inserts a second user so review requests can name a real account.
func seedReviewer(t *testing.T, dbConn *sql.DB, id snow.ID, name, email string) {
	t.Helper()

	_, err := dbConn.ExecContext(context.Background(),
		`INSERT INTO users (id, name, email, password, is_admin) VALUES (?, ?, ?, 'x', 1)`,
		id.Int64(), name, email)
	require.NoError(t, err)
}

// mintToken signs the JWT the gRPC interceptor accepts for the given user.
func mintToken(t *testing.T, userID snow.ID) string {
	t.Helper()

	claims := serverDomain.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID.Base36(),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
		UserID:  userID,
		IsAdmin: true,
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(e2eJWTSecret))
	require.NoError(t, err)
	return token
}

// TestEndToEnd_MergeRequestReviewRPCs drives the whole review gRPC surface
// against a real server: decisions, threads, comments, review requests and the
// timeline, including the permission and validation errors.
func TestEndToEnd_MergeRequestReviewRPCs(t *testing.T) {
	ctx := context.Background()
	dbConn := openTestDB(t)
	host := startTestServer(t, dbConn)

	store := newMemoryStore()
	transport := clientgrpc.NewTransport()
	grpcClient := clientgrpc.NewClient(transport, clientusecase.NewSession(store, transport, failPrompt{}))
	auth := clientusecase.NewAuth(grpcClient, store, failPrompt{})
	repo := clientusecase.NewRepo(auth, grpcClient, localrepo.NewLocalRepo())
	pusher := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())
	updater := clientusecase.NewUpdate(auth, grpcClient, localrepo.NewLocalRepo())

	require.NoError(t, grpcClient.Connect(ctx, host))
	login, err := grpcClient.LoginWithUsernamePassword(ctx, host, e2eSuperAdminEmail, e2eSuperAdminPass)
	require.NoError(t, err)
	require.NoError(t, store.SaveToken(login))

	url := "http://" + host + "/" + e2eOrgSlug + "/" + e2eProjectSlug
	work := filepath.Join(t.TempDir(), "work")
	require.NoError(t, repo.Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", nil, work))
	writeFile(t, work, "base.txt", "one\ntwo\nthree\n")
	stagePath(t, work, "base.txt")
	require.NoError(t, pusher.Run(ctx, work, "seed main"))

	created, err := grpcClient.CreateBranch(ctx, e2eOrgSlug, e2eProjectSlug, "feature", "main", "", "")
	require.NoError(t, err)
	require.Equal(t, "feature", created.Name)

	checkout := filepath.Join(t.TempDir(), "checkout")
	require.NoError(t, repo.Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", nil, checkout))
	require.NoError(t, updater.Switch(ctx, checkout, "feature"))
	writeFile(t, checkout, "feature.txt", "feature line\n")
	stagePath(t, checkout, "feature.txt")
	require.NoError(t, pusher.Run(ctx, checkout, "feature change"))

	conn, err := grpc.NewClient(host, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()
	rpc := pb.NewNipaServiceClient(conn)
	adminCtx := metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+login.AccessToken)
	projectCtx := &pb.ProjectContext{Org: e2eOrgSlug, Project: e2eProjectSlug}

	reviewer := snow.ID(2)
	seedReviewer(t, dbConn, reviewer, "Reviewer", "reviewer@example.com")
	reviewerID := reviewer.Base36()
	reviewerCtx := metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+mintToken(t, reviewer))

	mr, err := rpc.CreateMergeRequest(adminCtx, &pb.CreateMergeRequestRequest{
		Context: projectCtx, Title: "Feature", SourceBranch: "feature", TargetBranch: "main",
	})
	require.NoError(t, err)
	number := mr.MergeRequest.Number
	require.NotZero(t, number)

	empty, err := rpc.ListMergeRequestReviews(adminCtx, &pb.ListMergeRequestReviewsRequest{Context: projectCtx, Number: number})
	require.NoError(t, err)
	require.Empty(t, empty.Reviews)

	// a review request resolves the reviewer and shows up for read
	requested, err := rpc.RequestMergeRequestReview(adminCtx, &pb.RequestMergeRequestReviewRequest{
		Context: projectCtx, Number: number, UserId: reviewerID,
	})
	require.NoError(t, err)
	require.Equal(t, reviewerID, requested.ReviewRequest.Reviewer.UserId)

	requests, err := rpc.ListMergeRequestReviewRequests(adminCtx, &pb.ListMergeRequestReviewRequestsRequest{Context: projectCtx, Number: number})
	require.NoError(t, err)
	require.Len(t, requests.ReviewRequests, 1)

	// the author cannot review their own merge request
	_, err = rpc.SubmitMergeRequestReview(adminCtx, &pb.SubmitMergeRequestReviewRequest{
		Context: projectCtx, Number: number, State: "approved", Body: "self",
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	// a reviewer decides with an inline and a top-level comment
	line := int64(1)
	submitted, err := rpc.SubmitMergeRequestReview(reviewerCtx, &pb.SubmitMergeRequestReviewRequest{
		Context: projectCtx, Number: number, State: "changes_requested", Body: "please fix",
		Comments: []*pb.ReviewCommentInput{
			{FilePath: "feature.txt", NewLine: &line, Body: "rename this"},
			{Body: "overall note"},
		},
	})
	require.NoError(t, err)
	require.Equal(t, "changes_requested", submitted.Review.State)
	require.Equal(t, reviewerID, submitted.Review.Reviewer.UserId)
	require.NotEmpty(t, submitted.Review.HeadCommitId)
	reviewID := submitted.Review.Id

	// an unknown file is not a valid anchor
	_, err = rpc.AddMergeRequestComment(reviewerCtx, &pb.AddMergeRequestCommentRequest{
		Context: projectCtx, Number: number, FilePath: "ghost.txt", NewLine: &line, Body: "nope",
	})
	require.Equal(t, codes.NotFound, status.Code(err))

	state, err := rpc.GetMergeRequestReviewState(adminCtx, &pb.GetMergeRequestReviewStateRequest{Context: projectCtx, Number: number})
	require.NoError(t, err)
	require.EqualValues(t, 1, state.State.ChangesRequested)
	require.NotEmpty(t, state.State.HeadCommitId)

	reviews, err := rpc.ListMergeRequestReviews(adminCtx, &pb.ListMergeRequestReviewsRequest{Context: projectCtx, Number: number})
	require.NoError(t, err)
	require.Len(t, reviews.Reviews, 1)
	require.False(t, reviews.Reviews[0].Stale)

	threads, err := rpc.ListMergeRequestThreads(adminCtx, &pb.ListMergeRequestThreadsRequest{Context: projectCtx, Number: number})
	require.NoError(t, err)
	require.Len(t, threads.Threads, 2)
	var inline *pb.MergeRequestThreadDetail
	for _, thread := range threads.Threads {
		if thread.FilePath == "feature.txt" {
			inline = thread
		}
	}
	require.NotNil(t, inline)
	require.Equal(t, "right", inline.Side)
	require.EqualValues(t, 1, inline.GetNewLine())
	require.Len(t, inline.Comments, 1)

	resolvedOnly := true
	unresolved, err := rpc.ListMergeRequestThreads(adminCtx, &pb.ListMergeRequestThreadsRequest{
		Context: projectCtx, Number: number, Resolved: &resolvedOnly,
	})
	require.NoError(t, err)
	require.Empty(t, unresolved.Threads)

	// reply, edit and resolve a new inline thread
	added, err := rpc.AddMergeRequestComment(reviewerCtx, &pb.AddMergeRequestCommentRequest{
		Context: projectCtx, Number: number, FilePath: "feature.txt", NewLine: &line, Body: "one more",
	})
	require.NoError(t, err)
	threadID := added.Thread.Id
	require.Len(t, added.Thread.Comments, 1)
	commentID := added.Thread.Comments[0].Id
	require.NotEmpty(t, commentID)

	replied, err := rpc.ReplyMergeRequestThread(reviewerCtx, &pb.ReplyMergeRequestThreadRequest{
		Context: projectCtx, Number: number, ThreadId: threadID, Body: "and another",
	})
	require.NoError(t, err)
	require.Equal(t, threadID, replied.Comment.ThreadId)

	edited, err := rpc.UpdateMergeRequestComment(reviewerCtx, &pb.UpdateMergeRequestCommentRequest{
		Context: projectCtx, Number: number, ThreadId: threadID, CommentId: commentID, Body: "edited body",
	})
	require.NoError(t, err)
	require.Equal(t, "edited body", edited.Comment.Body)

	_, err = rpc.DeleteMergeRequestComment(reviewerCtx, &pb.DeleteMergeRequestCommentRequest{
		Context: projectCtx, Number: number, ThreadId: threadID, CommentId: replied.Comment.Id,
	})
	require.NoError(t, err)

	resolved, err := rpc.ResolveMergeRequestThread(reviewerCtx, &pb.ResolveMergeRequestThreadRequest{
		Context: projectCtx, Number: number, ThreadId: threadID, Resolved: true,
	})
	require.NoError(t, err)
	require.True(t, resolved.Thread.Resolved)
	require.NotNil(t, resolved.Thread.ResolvedBy)

	_, err = rpc.DeleteMergeRequestThread(reviewerCtx, &pb.DeleteMergeRequestThreadRequest{
		Context: projectCtx, Number: number, ThreadId: threadID,
	})
	require.NoError(t, err)

	// the reviewer withdraws their decision, then approves and is dismissed
	_, err = rpc.WithdrawMergeRequestReview(reviewerCtx, &pb.WithdrawMergeRequestReviewRequest{
		Context: projectCtx, Number: number, ReviewId: reviewID,
	})
	require.NoError(t, err)

	approved, err := rpc.SubmitMergeRequestReview(reviewerCtx, &pb.SubmitMergeRequestReviewRequest{
		Context: projectCtx, Number: number, State: "approved", Body: "looks good",
	})
	require.NoError(t, err)
	dismissed, err := rpc.DismissMergeRequestReview(adminCtx, &pb.DismissMergeRequestReviewRequest{
		Context: projectCtx, Number: number, ReviewId: approved.Review.Id,
	})
	require.NoError(t, err)
	require.Equal(t, "manual", dismissed.Review.DismissedReason)
	require.NotNil(t, dismissed.Review.DismissedAt)

	state, err = rpc.GetMergeRequestReviewState(adminCtx, &pb.GetMergeRequestReviewStateRequest{Context: projectCtx, Number: number})
	require.NoError(t, err)
	require.EqualValues(t, 1, state.State.DismissedApprovals)

	// the request can be raised again and withdrawn
	_, err = rpc.RequestMergeRequestReview(adminCtx, &pb.RequestMergeRequestReviewRequest{
		Context: projectCtx, Number: number, UserId: reviewerID,
	})
	require.NoError(t, err)
	_, err = rpc.RemoveMergeRequestReviewRequest(adminCtx, &pb.RemoveMergeRequestReviewRequestRequest{
		Context: projectCtx, Number: number, UserId: reviewerID,
	})
	require.NoError(t, err)
	requests, err = rpc.ListMergeRequestReviewRequests(adminCtx, &pb.ListMergeRequestReviewRequestsRequest{Context: projectCtx, Number: number})
	require.NoError(t, err)
	require.Empty(t, requests.ReviewRequests)

	timeline, err := rpc.ListMergeRequestTimeline(adminCtx, &pb.ListMergeRequestTimelineRequest{Context: projectCtx, Number: number})
	require.NoError(t, err)
	kinds := make([]string, 0, len(timeline.Items))
	for _, item := range timeline.Items {
		kinds = append(kinds, item.Kind)
	}
	require.Contains(t, kinds, "review_requested")
	require.Contains(t, kinds, "review_submitted")
	require.Contains(t, kinds, "review_dismissed")

	// validation and lookup errors
	_, err = rpc.ListMergeRequestReviews(adminCtx, &pb.ListMergeRequestReviewsRequest{Context: projectCtx, Number: 424242})
	require.Equal(t, codes.NotFound, status.Code(err))

	_, err = rpc.WithdrawMergeRequestReview(adminCtx, &pb.WithdrawMergeRequestReviewRequest{
		Context: projectCtx, Number: number, ReviewId: "not-an-id!",
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}
