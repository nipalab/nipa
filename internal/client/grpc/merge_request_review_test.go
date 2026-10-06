package grpc

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

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

func TestToClientReviewNil(t *testing.T) {
	require.Nil(t, toClientReview(nil))
	require.Nil(t, toClientThread(nil))
	require.Nil(t, toClientComment(nil))
	require.Nil(t, toClientReviewRequest(nil))
	require.Nil(t, toClientTimelineItem(nil))
}
