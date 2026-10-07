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
	pb "github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/snow"
)

type fakeMergeRequestServer struct {
	pb.UnimplementedNipaServiceServer

	createReq  *pb.CreateMergeRequestRequest
	createResp *pb.CreateMergeRequestResponse
	createErr  error

	updateReq  *pb.UpdateMergeRequestRequest
	updateResp *pb.UpdateMergeRequestResponse
	updateErr  error

	listReq  *pb.ListMergeRequestsRequest
	listResp *pb.ListMergeRequestsResponse
	listErr  error

	mergeReq  *pb.MergeMergeRequestRequest
	mergeResp *pb.MergeMergeRequestResponse
	mergeErr  error

	closeReq  *pb.CloseMergeRequestRequest
	closeResp *pb.CloseMergeRequestResponse
	closeErr  error

	getReq  *pb.GetMergeRequestRequest
	getResp *pb.GetMergeRequestResponse
	getErr  error

	reopenReq  *pb.ReopenMergeRequestRequest
	reopenResp *pb.ReopenMergeRequestResponse
	reopenErr  error

	checkReq  *pb.CheckMergeRequestRequest
	checkResp *pb.CheckMergeRequestResponse
	checkErr  error

	commitsReq  *pb.ListMergeRequestCommitsRequest
	commitsResp *pb.ListMergeRequestCommitsResponse
	commitsErr  error

	diffReq  *pb.GetMergeRequestDiffRequest
	diffResp *pb.GetMergeRequestDiffResponse
	diffErr  error

	submitReq  *pb.SubmitMergeRequestReviewRequest
	submitResp *pb.SubmitMergeRequestReviewResponse
	submitErr  error

	reviewsReq  *pb.ListMergeRequestReviewsRequest
	reviewsResp *pb.ListMergeRequestReviewsResponse
	reviewsErr  error

	addCommentReq  *pb.AddMergeRequestCommentRequest
	addCommentResp *pb.AddMergeRequestCommentResponse
	addCommentErr  error

	replyReq  *pb.ReplyMergeRequestThreadRequest
	replyResp *pb.ReplyMergeRequestThreadResponse
	replyErr  error

	resolveReq  *pb.ResolveMergeRequestThreadRequest
	resolveResp *pb.ResolveMergeRequestThreadResponse
	resolveErr  error

	threadsReq  *pb.ListMergeRequestThreadsRequest
	threadsResp *pb.ListMergeRequestThreadsResponse
	threadsErr  error

	timelineReq  *pb.ListMergeRequestTimelineRequest
	timelineResp *pb.ListMergeRequestTimelineResponse
	timelineErr  error

	requestsReq  *pb.ListMergeRequestReviewRequestsRequest
	requestsResp *pb.ListMergeRequestReviewRequestsResponse
	requestsErr  error

	requestReq  *pb.RequestMergeRequestReviewRequest
	requestResp *pb.RequestMergeRequestReviewResponse
	requestErr  error

	removeReq *pb.RemoveMergeRequestReviewRequestRequest
	removeErr error
}

func (f *fakeMergeRequestServer) CreateMergeRequest(_ context.Context, req *pb.CreateMergeRequestRequest) (*pb.CreateMergeRequestResponse, error) {
	f.createReq = req
	if f.createErr != nil {
		return nil, f.createErr
	}
	return f.createResp, nil
}

func (f *fakeMergeRequestServer) UpdateMergeRequest(_ context.Context, req *pb.UpdateMergeRequestRequest) (*pb.UpdateMergeRequestResponse, error) {
	f.updateReq = req
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	return f.updateResp, nil
}

func (f *fakeMergeRequestServer) ListMergeRequests(_ context.Context, req *pb.ListMergeRequestsRequest) (*pb.ListMergeRequestsResponse, error) {
	f.listReq = req
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.listResp, nil
}

func (f *fakeMergeRequestServer) MergeMergeRequest(_ context.Context, req *pb.MergeMergeRequestRequest) (*pb.MergeMergeRequestResponse, error) {
	f.mergeReq = req
	if f.mergeErr != nil {
		return nil, f.mergeErr
	}
	return f.mergeResp, nil
}

func (f *fakeMergeRequestServer) CloseMergeRequest(_ context.Context, req *pb.CloseMergeRequestRequest) (*pb.CloseMergeRequestResponse, error) {
	f.closeReq = req
	if f.closeErr != nil {
		return nil, f.closeErr
	}
	return f.closeResp, nil
}

func (f *fakeMergeRequestServer) GetMergeRequest(_ context.Context, req *pb.GetMergeRequestRequest) (*pb.GetMergeRequestResponse, error) {
	f.getReq = req
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.getResp, nil
}

func (f *fakeMergeRequestServer) ReopenMergeRequest(_ context.Context, req *pb.ReopenMergeRequestRequest) (*pb.ReopenMergeRequestResponse, error) {
	f.reopenReq = req
	if f.reopenErr != nil {
		return nil, f.reopenErr
	}
	return f.reopenResp, nil
}

func (f *fakeMergeRequestServer) CheckMergeRequest(_ context.Context, req *pb.CheckMergeRequestRequest) (*pb.CheckMergeRequestResponse, error) {
	f.checkReq = req
	if f.checkErr != nil {
		return nil, f.checkErr
	}
	return f.checkResp, nil
}

func (f *fakeMergeRequestServer) ListMergeRequestCommits(_ context.Context, req *pb.ListMergeRequestCommitsRequest) (*pb.ListMergeRequestCommitsResponse, error) {
	f.commitsReq = req
	if f.commitsErr != nil {
		return nil, f.commitsErr
	}
	return f.commitsResp, nil
}

func (f *fakeMergeRequestServer) GetMergeRequestDiff(_ context.Context, req *pb.GetMergeRequestDiffRequest) (*pb.GetMergeRequestDiffResponse, error) {
	f.diffReq = req
	if f.diffErr != nil {
		return nil, f.diffErr
	}
	return f.diffResp, nil
}

func (f *fakeMergeRequestServer) SubmitMergeRequestReview(_ context.Context, req *pb.SubmitMergeRequestReviewRequest) (*pb.SubmitMergeRequestReviewResponse, error) {
	f.submitReq = req
	if f.submitErr != nil {
		return nil, f.submitErr
	}
	return f.submitResp, nil
}

func (f *fakeMergeRequestServer) ListMergeRequestReviews(_ context.Context, req *pb.ListMergeRequestReviewsRequest) (*pb.ListMergeRequestReviewsResponse, error) {
	f.reviewsReq = req
	if f.reviewsErr != nil {
		return nil, f.reviewsErr
	}
	return f.reviewsResp, nil
}

func (f *fakeMergeRequestServer) AddMergeRequestComment(_ context.Context, req *pb.AddMergeRequestCommentRequest) (*pb.AddMergeRequestCommentResponse, error) {
	f.addCommentReq = req
	if f.addCommentErr != nil {
		return nil, f.addCommentErr
	}
	return f.addCommentResp, nil
}

func (f *fakeMergeRequestServer) ReplyMergeRequestThread(_ context.Context, req *pb.ReplyMergeRequestThreadRequest) (*pb.ReplyMergeRequestThreadResponse, error) {
	f.replyReq = req
	if f.replyErr != nil {
		return nil, f.replyErr
	}
	return f.replyResp, nil
}

func (f *fakeMergeRequestServer) ResolveMergeRequestThread(_ context.Context, req *pb.ResolveMergeRequestThreadRequest) (*pb.ResolveMergeRequestThreadResponse, error) {
	f.resolveReq = req
	if f.resolveErr != nil {
		return nil, f.resolveErr
	}
	return f.resolveResp, nil
}

func (f *fakeMergeRequestServer) ListMergeRequestThreads(_ context.Context, req *pb.ListMergeRequestThreadsRequest) (*pb.ListMergeRequestThreadsResponse, error) {
	f.threadsReq = req
	if f.threadsErr != nil {
		return nil, f.threadsErr
	}
	return f.threadsResp, nil
}

func (f *fakeMergeRequestServer) ListMergeRequestTimeline(_ context.Context, req *pb.ListMergeRequestTimelineRequest) (*pb.ListMergeRequestTimelineResponse, error) {
	f.timelineReq = req
	if f.timelineErr != nil {
		return nil, f.timelineErr
	}
	return f.timelineResp, nil
}

func (f *fakeMergeRequestServer) ListMergeRequestReviewRequests(_ context.Context, req *pb.ListMergeRequestReviewRequestsRequest) (*pb.ListMergeRequestReviewRequestsResponse, error) {
	f.requestsReq = req
	if f.requestsErr != nil {
		return nil, f.requestsErr
	}
	return f.requestsResp, nil
}

func (f *fakeMergeRequestServer) RequestMergeRequestReview(_ context.Context, req *pb.RequestMergeRequestReviewRequest) (*pb.RequestMergeRequestReviewResponse, error) {
	f.requestReq = req
	if f.requestErr != nil {
		return nil, f.requestErr
	}
	return f.requestResp, nil
}

func (f *fakeMergeRequestServer) RemoveMergeRequestReviewRequest(_ context.Context, req *pb.RemoveMergeRequestReviewRequestRequest) (*pb.RemoveMergeRequestReviewRequestResponse, error) {
	f.removeReq = req
	if f.removeErr != nil {
		return nil, f.removeErr
	}
	return &pb.RemoveMergeRequestReviewRequestResponse{}, nil
}

func mergeRequestDetail() *pb.MergeRequestDetail {
	now := time.Now().UTC().Truncate(time.Microsecond)
	mergeCommitID := snow.ID(9).Base36()
	mergeBaseID := snow.ID(3).Base36()
	return &pb.MergeRequestDetail{
		Id:                snow.ID(5).Base36(),
		Number:            5,
		ProjectId:         snow.ID(42).Base36(),
		SourceBranch:      "feature",
		TargetBranch:      "main",
		Title:             "Add feature",
		Description:       "body",
		Status:            clientDomain.MergeRequestOpen,
		MergeCommitId:     &mergeCommitID,
		MergeBaseCommitId: &mergeBaseID,
		CreatedBy:         snow.ID(7).Base36(),
		CreatedAt:         timestamppb.New(now),
		UpdatedAt:         timestamppb.New(now.Add(time.Hour)),
	}
}

func newMergeRequestTestClient(t *testing.T, srv pb.NipaServiceServer) *Client {
	t.Helper()
	addr := startTestServer(t, srv)
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))
	return c
}

func TestClient_CreateMergeRequest_Success(t *testing.T) {
	fs := &fakeMergeRequestServer{createResp: &pb.CreateMergeRequestResponse{MergeRequest: mergeRequestDetail()}}
	c := newMergeRequestTestClient(t, fs)

	got, err := c.CreateMergeRequest(context.Background(), "default", "sample", "Add feature", "body", "feature", "main", false)
	require.NoError(t, err)
	require.NotNil(t, fs.createReq)
	require.Equal(t, "default", fs.createReq.GetContext().GetOrg())
	require.Equal(t, "sample", fs.createReq.GetContext().GetProject())
	require.Equal(t, "Add feature", fs.createReq.GetTitle())
	require.Equal(t, "body", fs.createReq.GetDescription())
	require.Equal(t, "feature", fs.createReq.GetSourceBranch())
	require.Equal(t, "main", fs.createReq.GetTargetBranch())

	require.Equal(t, snow.ID(5).Base36(), got.ID)
	require.Equal(t, int64(5), got.Number)
	require.Equal(t, snow.ID(42).Base36(), got.ProjectID)
	require.Equal(t, "feature", got.SourceBranch)
	require.Equal(t, "main", got.TargetBranch)
	require.Equal(t, "Add feature", got.Title)
	require.Equal(t, "body", got.Description)
	require.Equal(t, clientDomain.MergeRequestOpen, got.Status)
	require.Equal(t, snow.ID(9).Base36(), got.MergeCommitID)
	require.Equal(t, snow.ID(3).Base36(), got.MergeBaseCommitID)
	require.Equal(t, snow.ID(7).Base36(), got.CreatedBy)
	require.NotZero(t, got.CreatedAt)
	require.NotZero(t, got.UpdatedAt)
}

func TestClient_CreateMergeRequest_Error(t *testing.T) {
	addr := startTestServer(t, &fakeMergeRequestServer{createErr: status.Error(codes.FailedPrecondition, "branch is protected")})
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	_, err := c.CreateMergeRequest(context.Background(), "default", "sample", "t", "", "feature", "main", false)
	require.Error(t, err)

	var domErr *clientDomain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 409, domErr.Code)
	require.Equal(t, "branch is protected", domErr.Message)
}

func TestClient_CreateMergeRequest_NotConnected(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})

	_, err := c.CreateMergeRequest(context.Background(), "default", "sample", "t", "", "feature", "main", false)
	require.EqualError(t, err, "not connected to a nipa server")
}

func TestClient_UpdateMergeRequest_Success(t *testing.T) {
	fs := &fakeMergeRequestServer{updateResp: &pb.UpdateMergeRequestResponse{MergeRequest: mergeRequestDetail()}}
	c := newMergeRequestTestClient(t, fs)

	got, err := c.UpdateMergeRequest(context.Background(), "default", "sample", 5, "Renamed", "new body", nil)
	require.NoError(t, err)
	require.NotNil(t, fs.updateReq)
	require.Equal(t, "default", fs.updateReq.GetContext().GetOrg())
	require.Equal(t, "sample", fs.updateReq.GetContext().GetProject())
	require.Equal(t, int64(5), fs.updateReq.GetNumber())
	require.Equal(t, "Renamed", fs.updateReq.GetTitle())
	require.Equal(t, "new body", fs.updateReq.GetDescription())
	require.Equal(t, snow.ID(5).Base36(), got.ID)
}

func TestClient_UpdateMergeRequest_Error(t *testing.T) {
	addr := startTestServer(t, &fakeMergeRequestServer{updateErr: status.Error(codes.NotFound, "merge request 5 not found")})
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	_, err := c.UpdateMergeRequest(context.Background(), "default", "sample", 5, "Renamed", "", nil)
	require.Error(t, err)

	var domErr *clientDomain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)
}

func TestClient_UpdateMergeRequest_NotConnected(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})

	_, err := c.UpdateMergeRequest(context.Background(), "default", "sample", 5, "t", "", nil)
	require.EqualError(t, err, "not connected to a nipa server")
}

func TestClient_ListMergeRequests_Success(t *testing.T) {
	next := int64(5)
	fs := &fakeMergeRequestServer{listResp: &pb.ListMergeRequestsResponse{
		MergeRequests: []*pb.MergeRequestDetail{mergeRequestDetail()},
		NextCursor:    &next,
	}}
	c := newMergeRequestTestClient(t, fs)

	got, cursor, err := c.ListMergeRequests(context.Background(), "default", "sample", clientDomain.ListMergeRequestOptions{
		Status:       clientDomain.MergeRequestClosed,
		Author:       snow.ID(7).Base36(),
		SourceBranch: "feature",
		TargetBranch: "main",
		After:        3,
		Limit:        10,
	})
	require.NoError(t, err)
	require.NotNil(t, fs.listReq)
	require.Equal(t, "default", fs.listReq.GetContext().GetOrg())
	require.Equal(t, "sample", fs.listReq.GetContext().GetProject())
	require.Equal(t, clientDomain.MergeRequestClosed, fs.listReq.GetStatus())
	require.Equal(t, snow.ID(7).Base36(), fs.listReq.GetAuthor())
	require.Equal(t, "feature", fs.listReq.GetSourceBranch())
	require.Equal(t, "main", fs.listReq.GetTargetBranch())
	require.Equal(t, int64(3), fs.listReq.GetAfterNumber())
	require.Equal(t, int32(10), fs.listReq.GetLimit())

	require.Len(t, got, 1)
	require.Equal(t, snow.ID(5).Base36(), got[0].ID)
	require.Equal(t, int64(5), got[0].Number)
	require.Equal(t, next, cursor)
}

func TestClient_ListMergeRequests_Error(t *testing.T) {
	addr := startTestServer(t, &fakeMergeRequestServer{listErr: status.Error(codes.InvalidArgument, "invalid merge request status")})
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	_, _, err := c.ListMergeRequests(context.Background(), "default", "sample", clientDomain.ListMergeRequestOptions{Status: "bogus", Limit: 10})
	require.Error(t, err)

	var domErr *clientDomain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 400, domErr.Code)
}

func TestClient_ListMergeRequests_NotConnected(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})

	_, _, err := c.ListMergeRequests(context.Background(), "default", "sample", clientDomain.ListMergeRequestOptions{Limit: 10})
	require.EqualError(t, err, "not connected to a nipa server")
}

func TestClient_MergeMergeRequest_Success(t *testing.T) {
	sourceCommitID := snow.ID(11).Base36()
	targetCommitID := snow.ID(12).Base36()
	mergeBaseID := snow.ID(12).Base36()
	detail := mergeRequestDetail()
	detail.Status = clientDomain.MergeRequestMerged
	fs := &fakeMergeRequestServer{mergeResp: &pb.MergeMergeRequestResponse{
		MergeRequest: detail,
		Mergeability: &pb.MergeabilityDetail{
			Status:            "mergeable",
			SourceCommitId:    &sourceCommitID,
			TargetCommitId:    &targetCommitID,
			MergeBaseCommitId: &mergeBaseID,
		},
	}}
	c := newMergeRequestTestClient(t, fs)

	got, info, err := c.MergeMergeRequest(context.Background(), "default", "sample", 5)
	require.NoError(t, err)
	require.NotNil(t, fs.mergeReq)
	require.Equal(t, "default", fs.mergeReq.GetContext().GetOrg())
	require.Equal(t, "sample", fs.mergeReq.GetContext().GetProject())
	require.Equal(t, int64(5), fs.mergeReq.GetNumber())

	require.Equal(t, clientDomain.MergeRequestMerged, got.Status)
	require.Equal(t, sourceCommitID, info.SourceCommitID)
	require.Equal(t, targetCommitID, info.TargetCommitID)
	require.Equal(t, mergeBaseID, info.MergeBaseCommitID)
}

func TestClient_MergeMergeRequest_Error(t *testing.T) {
	addr := startTestServer(t, &fakeMergeRequestServer{mergeErr: status.Error(codes.FailedPrecondition, "source branch is behind the target")})
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	_, _, err := c.MergeMergeRequest(context.Background(), "default", "sample", 5)
	require.Error(t, err)

	var domErr *clientDomain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 409, domErr.Code)
	require.Equal(t, "source branch is behind the target", domErr.Message)
}

func TestClient_MergeMergeRequest_NotConnected(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})

	_, _, err := c.MergeMergeRequest(context.Background(), "default", "sample", 5)
	require.EqualError(t, err, "not connected to a nipa server")
}

func TestClient_CloseMergeRequest_Success(t *testing.T) {
	detail := mergeRequestDetail()
	detail.Status = clientDomain.MergeRequestClosed
	fs := &fakeMergeRequestServer{closeResp: &pb.CloseMergeRequestResponse{MergeRequest: detail}}
	c := newMergeRequestTestClient(t, fs)

	got, err := c.CloseMergeRequest(context.Background(), "default", "sample", 5)
	require.NoError(t, err)
	require.NotNil(t, fs.closeReq)
	require.Equal(t, "default", fs.closeReq.GetContext().GetOrg())
	require.Equal(t, "sample", fs.closeReq.GetContext().GetProject())
	require.Equal(t, int64(5), fs.closeReq.GetNumber())
	require.Equal(t, clientDomain.MergeRequestClosed, got.Status)
}

func TestClient_CloseMergeRequest_Error(t *testing.T) {
	addr := startTestServer(t, &fakeMergeRequestServer{closeErr: status.Error(codes.PermissionDenied, "no permission")})
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	_, err := c.CloseMergeRequest(context.Background(), "default", "sample", 5)
	require.Error(t, err)

	var domErr *clientDomain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 403, domErr.Code)
}

func TestClient_CloseMergeRequest_NotConnected(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})

	_, err := c.CloseMergeRequest(context.Background(), "default", "sample", 5)
	require.EqualError(t, err, "not connected to a nipa server")
}

func TestClient_GetMergeRequest_Error(t *testing.T) {
	fs := &fakeMergeRequestServer{getErr: status.Error(codes.NotFound, "merge request 5 not found")}
	c := newMergeRequestTestClient(t, fs)

	_, _, err := c.GetMergeRequest(context.Background(), "default", "sample", 5)
	require.Error(t, err)

	var domErr *clientDomain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)
	require.Equal(t, "merge request 5 not found", domErr.Message)
}

func TestClient_GetMergeRequest_NotConnected(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})

	_, _, err := c.GetMergeRequest(context.Background(), "default", "sample", 5)
	require.EqualError(t, err, "not connected to a nipa server")
}

func TestClient_ReopenMergeRequest_Success(t *testing.T) {
	detail := mergeRequestDetail()
	detail.Status = clientDomain.MergeRequestOpen
	fs := &fakeMergeRequestServer{reopenResp: &pb.ReopenMergeRequestResponse{MergeRequest: detail}}
	c := newMergeRequestTestClient(t, fs)

	got, err := c.ReopenMergeRequest(context.Background(), "default", "sample", 5)
	require.NoError(t, err)
	require.NotNil(t, fs.reopenReq)
	require.Equal(t, "default", fs.reopenReq.GetContext().GetOrg())
	require.Equal(t, "sample", fs.reopenReq.GetContext().GetProject())
	require.Equal(t, int64(5), fs.reopenReq.GetNumber())
	require.Equal(t, clientDomain.MergeRequestOpen, got.Status)
}

func TestClient_ReopenMergeRequest_Error(t *testing.T) {
	fs := &fakeMergeRequestServer{reopenErr: status.Error(codes.FailedPrecondition, "merge request is not closed")}
	c := newMergeRequestTestClient(t, fs)

	_, err := c.ReopenMergeRequest(context.Background(), "default", "sample", 5)
	require.Error(t, err)

	var domErr *clientDomain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 409, domErr.Code)
	require.Equal(t, "merge request is not closed", domErr.Message)
}

func TestClient_ReopenMergeRequest_NotConnected(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})

	_, err := c.ReopenMergeRequest(context.Background(), "default", "sample", 5)
	require.EqualError(t, err, "not connected to a nipa server")
}

func TestClient_CheckMergeRequest_Success(t *testing.T) {
	fs := &fakeMergeRequestServer{checkResp: &pb.CheckMergeRequestResponse{
		Mergeability: &pb.MergeabilityDetail{
			Status:            "behind_target",
			SourceCommitId:    strPtr(snow.ID(11).Base36()),
			TargetCommitId:    strPtr(snow.ID(12).Base36()),
			MergeBaseCommitId: strPtr(snow.ID(10).Base36()),
			BlockedBy:         "changes_requested",
		},
	}}
	c := newMergeRequestTestClient(t, fs)

	info, err := c.CheckMergeRequest(context.Background(), "default", "sample", 5)
	require.NoError(t, err)
	require.NotNil(t, fs.checkReq)
	require.Equal(t, "default", fs.checkReq.GetContext().GetOrg())
	require.Equal(t, int64(5), fs.checkReq.GetNumber())
	require.Equal(t, "behind_target", info.Status)
	require.Equal(t, snow.ID(11).Base36(), info.SourceCommitID)
	require.Equal(t, snow.ID(12).Base36(), info.TargetCommitID)
	require.Equal(t, snow.ID(10).Base36(), info.MergeBaseCommitID)
	require.Equal(t, "changes_requested", info.BlockedBy)
}

func TestClient_CheckMergeRequest_Error(t *testing.T) {
	fs := &fakeMergeRequestServer{checkErr: status.Error(codes.NotFound, "merge request 5 not found")}
	c := newMergeRequestTestClient(t, fs)

	_, err := c.CheckMergeRequest(context.Background(), "default", "sample", 5)
	require.Error(t, err)

	var domErr *clientDomain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)
}

func TestClient_CheckMergeRequest_NotConnected(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})

	_, err := c.CheckMergeRequest(context.Background(), "default", "sample", 5)
	require.EqualError(t, err, "not connected to a nipa server")
}

func TestClient_ListMergeRequestCommits_Success(t *testing.T) {
	parent1 := snow.ID(1).Base36()
	parent2 := snow.ID(2).Base36()
	now := time.Now().UTC().Truncate(time.Microsecond)
	fs := &fakeMergeRequestServer{commitsResp: &pb.ListMergeRequestCommitsResponse{Commits: []*pb.CommitLogEntry{
		{
			CommitId:    snow.ID(10).Base36(),
			CommitHash:  "aa",
			Parent_1Id:  &parent1,
			Parent_2Id:  &parent2,
			AuthorName:  "Alice",
			AuthorEmail: "alice@example.com",
			Message:     "land the feature",
			CreatedAt:   timestamppb.New(now),
		},
	}}}
	c := newMergeRequestTestClient(t, fs)

	commits, err := c.ListMergeRequestCommits(context.Background(), "default", "sample", 5)
	require.NoError(t, err)
	require.NotNil(t, fs.commitsReq)
	require.Equal(t, "default", fs.commitsReq.GetContext().GetOrg())
	require.Equal(t, int64(5), fs.commitsReq.GetNumber())
	require.Len(t, commits, 1)
	require.Equal(t, snow.ID(10), commits[0].ID)
	require.NotNil(t, commits[0].Parent1ID)
	require.Equal(t, snow.ID(1), *commits[0].Parent1ID)
	require.NotNil(t, commits[0].Parent2ID)
	require.Equal(t, snow.ID(2), *commits[0].Parent2ID)
	require.Equal(t, "Alice", commits[0].AuthorName)
	require.Equal(t, "alice@example.com", commits[0].AuthorEmail)
	require.Equal(t, "land the feature", commits[0].Message)
	require.Equal(t, now, commits[0].CreatedAt)
}

func TestClient_ListMergeRequestCommits_Error(t *testing.T) {
	fs := &fakeMergeRequestServer{commitsErr: status.Error(codes.NotFound, "merge request 5 not found")}
	c := newMergeRequestTestClient(t, fs)

	_, err := c.ListMergeRequestCommits(context.Background(), "default", "sample", 5)
	require.Error(t, err)

	var domErr *clientDomain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)
}

func TestClient_ListMergeRequestCommits_NotConnected(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})

	_, err := c.ListMergeRequestCommits(context.Background(), "default", "sample", 5)
	require.EqualError(t, err, "not connected to a nipa server")
}

func TestToClientMergeRequest_Nil(t *testing.T) {
	require.Nil(t, toClientMergeRequest(nil))
}

func TestToClientMergeRequest_ReviewState(t *testing.T) {
	detail := mergeRequestDetail()
	detail.Review = &pb.MergeRequestReviewState{
		HeadCommitId:         snow.ID(11).Base36(),
		Approvals:            2,
		ChangesRequested:     1,
		DismissedApprovals:   3,
		OutstandingReviewers: []string{snow.ID(8).Base36(), snow.ID(9).Base36()},
	}

	got := toClientMergeRequest(detail)
	require.NotNil(t, got.Review)
	require.Equal(t, snow.ID(11).Base36(), got.Review.HeadCommitID)
	require.Equal(t, 2, got.Review.Approvals)
	require.Equal(t, 1, got.Review.ChangesRequested)
	require.Equal(t, 3, got.Review.DismissedApprovals)
	require.Equal(t, []string{snow.ID(8).Base36(), snow.ID(9).Base36()}, got.Review.OutstandingReviewers)

	require.Nil(t, toClientMergeRequest(mergeRequestDetail()).Review, "an absent summary stays nil")
	require.Nil(t, toClientReviewState(nil))
}

func TestToClientMergeability_Nil(t *testing.T) {
	require.Nil(t, toClientMergeability(nil))
}

func TestToClientMergeability_BlockedBy(t *testing.T) {
	got := toClientMergeability(&pb.MergeabilityDetail{
		Status:    "mergeable",
		BlockedBy: "insufficient_approvals",
	})
	require.Equal(t, "mergeable", got.Status)
	require.Equal(t, "insufficient_approvals", got.BlockedBy)
}
