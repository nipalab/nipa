package grpc

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/snow"
)

func reviewDetail() *pb.MergeRequestReviewDetail {
	created := time.Unix(100, 0).UTC()
	return &pb.MergeRequestReviewDetail{
		Id:             snow.ID(7).Base36(),
		MergeRequestId: 5,
		Reviewer:       &pb.ReviewActor{UserId: snow.ID(8).Base36(), Name: "Rev", PhotoUrl: "p.png"},
		State:          "approved",
		Body:           "lgtm",
		HeadCommitId:   snow.ID(11).Base36(),
		CreatedAt:      timestamppb.New(created),
		UpdatedAt:      timestamppb.New(created),
	}
}

func TestClient_GetMergeRequest_Success(t *testing.T) {
	fs := &fakeMergeRequestServer{getResp: &pb.GetMergeRequestResponse{
		MergeRequest: mergeRequestDetail(),
		Mergeability: &pb.MergeabilityDetail{Status: "mergeable", BlockedBy: "insufficient_approvals"},
	}}
	c := newMergeRequestTestClient(t, fs)

	mr, info, err := c.GetMergeRequest(context.Background(), "default", "sample", 5)
	require.NoError(t, err)
	require.Equal(t, "default", fs.getReq.GetContext().GetOrg())
	require.Equal(t, int64(5), fs.getReq.GetNumber())
	require.Equal(t, int64(5), mr.Number)
	require.Equal(t, "mergeable", info.Status)
	require.Equal(t, "insufficient_approvals", info.BlockedBy)
}

func TestClient_SubmitMergeRequestReview_Success(t *testing.T) {
	fs := &fakeMergeRequestServer{submitResp: &pb.SubmitMergeRequestReviewResponse{Review: reviewDetail()}}
	c := newMergeRequestTestClient(t, fs)

	review, err := c.SubmitMergeRequestReview(context.Background(), "default", "sample", 5, "approved", "lgtm")
	require.NoError(t, err)
	require.Equal(t, int64(5), fs.submitReq.GetNumber())
	require.Equal(t, "approved", fs.submitReq.GetState())
	require.Equal(t, "lgtm", fs.submitReq.GetBody())
	require.Equal(t, "approved", review.State)
	require.Equal(t, snow.ID(8).Base36(), review.Reviewer.UserID)
	require.Equal(t, "Rev", review.Reviewer.Name)
	require.Equal(t, "lgtm", review.Body)
	require.Equal(t, snow.ID(11).Base36(), review.HeadCommitID)
	require.False(t, review.CreatedAt.IsZero())
}

func TestClient_ListMergeRequestThreads_Success(t *testing.T) {
	newLine := int64(2)
	at := time.Unix(300, 0).UTC()
	fs := &fakeMergeRequestServer{threadsResp: &pb.ListMergeRequestThreadsResponse{Threads: []*pb.MergeRequestThreadDetail{{
		Id:             snow.ID(9).Base36(),
		MergeRequestId: 5,
		FilePath:       "code.txt",
		NewLine:        &newLine,
		Side:           "right",
		Outdated:       true,
		Resolved:       true,
		ResolvedBy:     &pb.ReviewActor{UserId: snow.ID(8).Base36(), Name: "Rev"},
		ResolvedAt:     timestamppb.New(at),
		CreatedBy:      &pb.ReviewActor{UserId: snow.ID(7).Base36(), Name: "Alice"},
		CreatedAt:      timestamppb.New(at),
		Comments: []*pb.ReviewCommentDetail{{
			Id:        snow.ID(10).Base36(),
			ThreadId:  snow.ID(9).Base36(),
			User:      &pb.ReviewActor{UserId: snow.ID(8).Base36(), Name: "Rev"},
			Body:      "rename this",
			CreatedAt: timestamppb.New(at),
			UpdatedAt: timestamppb.New(at),
		}},
	}}}}
	c := newMergeRequestTestClient(t, fs)

	threads, err := c.ListMergeRequestThreads(context.Background(), "default", "sample", 5)
	require.NoError(t, err)
	require.Len(t, threads, 1)
	thread := threads[0]
	require.Equal(t, "code.txt", thread.FilePath)
	require.Equal(t, int64(2), *thread.NewLine)
	require.True(t, thread.Resolved)
	require.True(t, thread.Outdated)
	require.Equal(t, "right", thread.Side)
	require.NotNil(t, thread.ResolvedBy)
	require.NotNil(t, thread.ResolvedAt)
	require.Len(t, thread.Comments, 1)
	require.Equal(t, "rename this", thread.Comments[0].Body)
	require.Equal(t, "Rev", thread.Comments[0].User.Name)
}

func TestClient_ListMergeRequestTimeline_Success(t *testing.T) {
	at := time.Unix(200, 0).UTC()
	fs := &fakeMergeRequestServer{timelineResp: &pb.ListMergeRequestTimelineResponse{Items: []*pb.MergeRequestTimelineItem{{
		Id:        snow.ID(1).Base36(),
		Kind:      "review_requested",
		Actor:     &pb.ReviewActor{UserId: snow.ID(7).Base36(), Name: "Alice"},
		Subject:   &pb.ReviewActor{UserId: snow.ID(8).Base36(), Name: "Rev"},
		CreatedAt: timestamppb.New(at),
	}}}}
	c := newMergeRequestTestClient(t, fs)

	items, err := c.ListMergeRequestTimeline(context.Background(), "default", "sample", 5)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "review_requested", items[0].Kind)
	require.Equal(t, "Alice", items[0].Actor.Name)
	require.NotNil(t, items[0].Subject)
	require.Equal(t, "Rev", items[0].Subject.Name)
}

func TestClient_GetMergeRequestDiff_Success(t *testing.T) {
	fs := &fakeMergeRequestServer{diffResp: &pb.GetMergeRequestDiffResponse{Files: []*pb.DiffFileDetail{{
		Path: "a.txt", Status: "M", Additions: 1, Deletions: 1,
		Patch: []string{"@@ -1 +1 @@", "-one", "+two"},
	}}}}
	c := newMergeRequestTestClient(t, fs)

	files, err := c.GetMergeRequestDiff(context.Background(), "default", "sample", 5)
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, "a.txt", files[0].Path)
	require.Equal(t, "M", files[0].Status)
	require.Equal(t, int64(1), files[0].Additions)
	require.Len(t, files[0].Patch, 3)
}

func TestClient_ReviewRequestsAndRemove(t *testing.T) {
	at := time.Unix(200, 0).UTC()
	request := &pb.ReviewRequestDetail{
		Id:             snow.ID(3).Base36(),
		MergeRequestId: 5,
		Reviewer:       &pb.ReviewActor{UserId: snow.ID(8).Base36(), Name: "Rev"},
		RequestedBy:    &pb.ReviewActor{UserId: snow.ID(7).Base36(), Name: "Alice"},
		CreatedAt:      timestamppb.New(at),
	}
	fs := &fakeMergeRequestServer{
		requestsResp: &pb.ListMergeRequestReviewRequestsResponse{ReviewRequests: []*pb.ReviewRequestDetail{request}},
		requestResp:  &pb.RequestMergeRequestReviewResponse{ReviewRequest: request},
	}
	c := newMergeRequestTestClient(t, fs)

	requests, err := c.ListMergeRequestReviewRequests(context.Background(), "default", "sample", 5)
	require.NoError(t, err)
	require.Len(t, requests, 1)
	require.Equal(t, "Rev", requests[0].Reviewer.Name)

	created, err := c.RequestMergeRequestReview(context.Background(), "default", "sample", 5, snow.ID(8).Base36())
	require.NoError(t, err)
	require.Equal(t, snow.ID(8).Base36(), fs.requestReq.GetUserId())
	require.Equal(t, "Rev", created.Reviewer.Name)

	require.NoError(t, c.RemoveMergeRequestReviewRequest(context.Background(), "default", "sample", 5, snow.ID(8).Base36()))
	require.Equal(t, snow.ID(8).Base36(), fs.removeReq.GetUserId())
}

func TestClient_ListMergeRequestReviews_Success(t *testing.T) {
	dismissedAt := time.Unix(400, 0).UTC()
	review := reviewDetail()
	review.Stale = true
	review.DismissedBy = &pb.ReviewActor{UserId: snow.ID(9).Base36(), Name: "Admin"}
	review.DismissedAt = timestamppb.New(dismissedAt)
	review.DismissedReason = "new_commits"
	fs := &fakeMergeRequestServer{reviewsResp: &pb.ListMergeRequestReviewsResponse{
		Reviews: []*pb.MergeRequestReviewDetail{review},
	}}
	c := newMergeRequestTestClient(t, fs)

	reviews, err := c.ListMergeRequestReviews(context.Background(), "default", "sample", 5)
	require.NoError(t, err)
	require.Equal(t, "default", fs.reviewsReq.GetContext().GetOrg())
	require.Equal(t, int64(5), fs.reviewsReq.GetNumber())
	require.Len(t, reviews, 1)
	got := reviews[0]
	require.Equal(t, "approved", got.State)
	require.True(t, got.Stale)
	require.NotNil(t, got.DismissedBy)
	require.Equal(t, "Admin", got.DismissedBy.Name)
	require.NotNil(t, got.DismissedAt)
	require.Equal(t, "new_commits", got.DismissedReason)
}

func TestClient_ListMergeRequestReviews_Error(t *testing.T) {
	fs := &fakeMergeRequestServer{reviewsErr: status.Error(codes.NotFound, "merge request 5 not found")}
	c := newMergeRequestTestClient(t, fs)

	_, err := c.ListMergeRequestReviews(context.Background(), "default", "sample", 5)
	require.Error(t, err)

	var domErr *clientDomain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)
}

func TestClient_AddMergeRequestComment_Success(t *testing.T) {
	oldLine := int64(1)
	newLine := int64(2)
	at := time.Unix(500, 0).UTC()
	fs := &fakeMergeRequestServer{addCommentResp: &pb.AddMergeRequestCommentResponse{Thread: &pb.MergeRequestThreadDetail{
		Id:             snow.ID(9).Base36(),
		MergeRequestId: 5,
		FilePath:       "code.txt",
		OldLine:        &oldLine,
		NewLine:        &newLine,
		Side:           "right",
		Resolved:       true,
		CreatedAt:      timestamppb.New(at),
	}}}
	c := newMergeRequestTestClient(t, fs)

	thread, err := c.AddMergeRequestComment(context.Background(), "default", "sample", 5, "code.txt", &oldLine, &newLine, "rename this")
	require.NoError(t, err)
	require.Equal(t, "default", fs.addCommentReq.GetContext().GetOrg())
	require.Equal(t, int64(5), fs.addCommentReq.GetNumber())
	require.Equal(t, "code.txt", fs.addCommentReq.GetFilePath())
	require.NotNil(t, fs.addCommentReq.OldLine)
	require.Equal(t, oldLine, fs.addCommentReq.GetOldLine())
	require.NotNil(t, fs.addCommentReq.NewLine)
	require.Equal(t, newLine, fs.addCommentReq.GetNewLine())
	require.Equal(t, "rename this", fs.addCommentReq.GetBody())
	require.Equal(t, "code.txt", thread.FilePath)
	require.NotNil(t, thread.OldLine)
	require.Equal(t, int64(1), *thread.OldLine)
	require.NotNil(t, thread.NewLine)
	require.Equal(t, int64(2), *thread.NewLine)
	require.True(t, thread.Resolved)
}

func TestClient_AddMergeRequestComment_Error(t *testing.T) {
	fs := &fakeMergeRequestServer{addCommentErr: status.Error(codes.InvalidArgument, "line 99 is not in the diff")}
	c := newMergeRequestTestClient(t, fs)

	_, err := c.AddMergeRequestComment(context.Background(), "default", "sample", 5, "code.txt", nil, nil, "note")
	require.Error(t, err)

	var domErr *clientDomain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 400, domErr.Code)
}

func TestClient_ReplyMergeRequestThread_Success(t *testing.T) {
	at := time.Unix(600, 0).UTC()
	fs := &fakeMergeRequestServer{replyResp: &pb.ReplyMergeRequestThreadResponse{Comment: &pb.ReviewCommentDetail{
		Id:        snow.ID(10).Base36(),
		ThreadId:  snow.ID(9).Base36(),
		User:      &pb.ReviewActor{UserId: snow.ID(8).Base36(), Name: "Rev"},
		Body:      "done",
		Edited:    true,
		CreatedAt: timestamppb.New(at),
		UpdatedAt: timestamppb.New(at),
	}}}
	c := newMergeRequestTestClient(t, fs)

	comment, err := c.ReplyMergeRequestThread(context.Background(), "default", "sample", 5, snow.ID(9).Base36(), "done")
	require.NoError(t, err)
	require.Equal(t, int64(5), fs.replyReq.GetNumber())
	require.Equal(t, snow.ID(9).Base36(), fs.replyReq.GetThreadId())
	require.Equal(t, "done", fs.replyReq.GetBody())
	require.Equal(t, snow.ID(10).Base36(), comment.ID)
	require.Equal(t, "done", comment.Body)
	require.True(t, comment.Edited)
	require.Equal(t, "Rev", comment.User.Name)
}

func TestClient_ReplyMergeRequestThread_Error(t *testing.T) {
	fs := &fakeMergeRequestServer{replyErr: status.Error(codes.NotFound, "thread not found")}
	c := newMergeRequestTestClient(t, fs)

	_, err := c.ReplyMergeRequestThread(context.Background(), "default", "sample", 5, "t9", "done")
	require.Error(t, err)

	var domErr *clientDomain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)
}

func TestClient_ResolveMergeRequestThread_Success(t *testing.T) {
	at := time.Unix(700, 0).UTC()
	fs := &fakeMergeRequestServer{resolveResp: &pb.ResolveMergeRequestThreadResponse{Thread: &pb.MergeRequestThreadDetail{
		Id:             snow.ID(9).Base36(),
		MergeRequestId: 5,
		Resolved:       true,
		ResolvedBy:     &pb.ReviewActor{UserId: snow.ID(8).Base36(), Name: "Rev"},
		ResolvedAt:     timestamppb.New(at),
		CreatedAt:      timestamppb.New(at),
	}}}
	c := newMergeRequestTestClient(t, fs)

	thread, err := c.ResolveMergeRequestThread(context.Background(), "default", "sample", 5, snow.ID(9).Base36(), true)
	require.NoError(t, err)
	require.Equal(t, int64(5), fs.resolveReq.GetNumber())
	require.Equal(t, snow.ID(9).Base36(), fs.resolveReq.GetThreadId())
	require.True(t, fs.resolveReq.GetResolved())
	require.True(t, thread.Resolved)
	require.NotNil(t, thread.ResolvedAt)
}

func TestClient_ResolveMergeRequestThread_Error(t *testing.T) {
	fs := &fakeMergeRequestServer{resolveErr: status.Error(codes.PermissionDenied, "no permission")}
	c := newMergeRequestTestClient(t, fs)

	_, err := c.ResolveMergeRequestThread(context.Background(), "default", "sample", 5, "t9", false)
	require.Error(t, err)

	var domErr *clientDomain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 403, domErr.Code)
}

func TestClient_MergeRequestReviewErrorMapping(t *testing.T) {
	fs := &fakeMergeRequestServer{
		submitErr:   status.Error(codes.FailedPrecondition, "cannot review your own request"),
		threadsErr:  status.Error(codes.NotFound, "merge request 5 not found"),
		timelineErr: status.Error(codes.NotFound, "merge request 5 not found"),
		requestsErr: status.Error(codes.NotFound, "merge request 5 not found"),
		requestErr:  status.Error(codes.InvalidArgument, "unknown user"),
		removeErr:   status.Error(codes.PermissionDenied, "no permission"),
	}
	c := newMergeRequestTestClient(t, fs)
	ctx := context.Background()

	_, err := c.SubmitMergeRequestReview(ctx, "default", "sample", 5, "approved", "lgtm")
	require.Equal(t, 409, errorCode(t, err))

	_, err = c.ListMergeRequestThreads(ctx, "default", "sample", 5)
	require.Equal(t, 404, errorCode(t, err))

	_, err = c.ListMergeRequestTimeline(ctx, "default", "sample", 5)
	require.Equal(t, 404, errorCode(t, err))

	_, err = c.ListMergeRequestReviewRequests(ctx, "default", "sample", 5)
	require.Equal(t, 404, errorCode(t, err))

	_, err = c.RequestMergeRequestReview(ctx, "default", "sample", 5, "nobody")
	require.Equal(t, 400, errorCode(t, err))

	err = c.RemoveMergeRequestReviewRequest(ctx, "default", "sample", 5, "nobody")
	require.Equal(t, 403, errorCode(t, err))
}

func TestClient_MergeRequestReviewNotConnected(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	ctx := context.Background()

	_, err := c.ListMergeRequestReviews(ctx, "default", "sample", 5)
	require.EqualError(t, err, "not connected to a nipa server")

	_, err = c.AddMergeRequestComment(ctx, "default", "sample", 5, "a.txt", nil, nil, "note")
	require.EqualError(t, err, "not connected to a nipa server")

	_, err = c.ReplyMergeRequestThread(ctx, "default", "sample", 5, "t1", "done")
	require.EqualError(t, err, "not connected to a nipa server")

	_, err = c.ResolveMergeRequestThread(ctx, "default", "sample", 5, "t1", true)
	require.EqualError(t, err, "not connected to a nipa server")

	_, err = c.SubmitMergeRequestReview(ctx, "default", "sample", 5, "approved", "lgtm")
	require.EqualError(t, err, "not connected to a nipa server")

	_, err = c.ListMergeRequestThreads(ctx, "default", "sample", 5)
	require.EqualError(t, err, "not connected to a nipa server")

	_, err = c.ListMergeRequestTimeline(ctx, "default", "sample", 5)
	require.EqualError(t, err, "not connected to a nipa server")

	_, err = c.ListMergeRequestReviewRequests(ctx, "default", "sample", 5)
	require.EqualError(t, err, "not connected to a nipa server")

	_, err = c.RequestMergeRequestReview(ctx, "default", "sample", 5, "user1")
	require.EqualError(t, err, "not connected to a nipa server")

	err = c.RemoveMergeRequestReviewRequest(ctx, "default", "sample", 5, "user1")
	require.EqualError(t, err, "not connected to a nipa server")

	_, err = c.GetMergeRequestDiff(ctx, "default", "sample", 5)
	require.EqualError(t, err, "not connected to a nipa server")
}

func TestClient_GetMergeRequestDiff_Error(t *testing.T) {
	fs := &fakeMergeRequestServer{diffErr: status.Error(codes.NotFound, "merge request 5 not found")}
	c := newMergeRequestTestClient(t, fs)

	_, err := c.GetMergeRequestDiff(context.Background(), "default", "sample", 5)
	require.Error(t, err)

	var domErr *clientDomain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)
}

func errorCode(t *testing.T, err error) int {
	t.Helper()
	var domErr *clientDomain.Error
	require.ErrorAs(t, err, &domErr)
	return domErr.Code
}

func TestToClientReviewNil(t *testing.T) {
	require.Nil(t, toClientReview(nil))
	require.Nil(t, toClientThread(nil))
	require.Nil(t, toClientComment(nil))
	require.Nil(t, toClientReviewRequest(nil))
	require.Nil(t, toClientTimelineItem(nil))
	require.Nil(t, toClientReviewActorPtr(nil))
	require.Equal(t, clientDomain.ReviewActor{}, toClientReviewActor(nil))
}
