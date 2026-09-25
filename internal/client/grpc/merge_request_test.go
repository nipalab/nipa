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

	got, err := c.CreateMergeRequest(context.Background(), "default", "sample", "Add feature", "body", "feature", "main")
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

	_, err := c.CreateMergeRequest(context.Background(), "default", "sample", "t", "", "feature", "main")
	require.Error(t, err)

	var domErr *clientDomain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 409, domErr.Code)
	require.Equal(t, "branch is protected", domErr.Message)
}

func TestClient_CreateMergeRequest_NotConnected(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})

	_, err := c.CreateMergeRequest(context.Background(), "default", "sample", "t", "", "feature", "main")
	require.EqualError(t, err, "not connected to a nipa server")
}

func TestClient_UpdateMergeRequest_Success(t *testing.T) {
	fs := &fakeMergeRequestServer{updateResp: &pb.UpdateMergeRequestResponse{MergeRequest: mergeRequestDetail()}}
	c := newMergeRequestTestClient(t, fs)

	got, err := c.UpdateMergeRequest(context.Background(), "default", "sample", 5, "Renamed", "new body")
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

	_, err := c.UpdateMergeRequest(context.Background(), "default", "sample", 5, "Renamed", "")
	require.Error(t, err)

	var domErr *clientDomain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)
}

func TestClient_UpdateMergeRequest_NotConnected(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})

	_, err := c.UpdateMergeRequest(context.Background(), "default", "sample", 5, "t", "")
	require.EqualError(t, err, "not connected to a nipa server")
}

func TestClient_ListMergeRequests_Success(t *testing.T) {
	fs := &fakeMergeRequestServer{listResp: &pb.ListMergeRequestsResponse{
		MergeRequests: []*pb.MergeRequestDetail{mergeRequestDetail()},
	}}
	c := newMergeRequestTestClient(t, fs)

	got, err := c.ListMergeRequests(context.Background(), "default", "sample", clientDomain.MergeRequestClosed, 10)
	require.NoError(t, err)
	require.NotNil(t, fs.listReq)
	require.Equal(t, "default", fs.listReq.GetContext().GetOrg())
	require.Equal(t, "sample", fs.listReq.GetContext().GetProject())
	require.Equal(t, clientDomain.MergeRequestClosed, fs.listReq.GetStatus())
	require.Equal(t, int32(10), fs.listReq.GetLimit())

	require.Len(t, got, 1)
	require.Equal(t, snow.ID(5).Base36(), got[0].ID)
	require.Equal(t, int64(5), got[0].Number)
}

func TestClient_ListMergeRequests_Error(t *testing.T) {
	addr := startTestServer(t, &fakeMergeRequestServer{listErr: status.Error(codes.InvalidArgument, "invalid merge request status")})
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	_, err := c.ListMergeRequests(context.Background(), "default", "sample", "bogus", 10)
	require.Error(t, err)

	var domErr *clientDomain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 400, domErr.Code)
}

func TestClient_ListMergeRequests_NotConnected(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})

	_, err := c.ListMergeRequests(context.Background(), "default", "sample", "", 10)
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

func TestToClientMergeRequest_Nil(t *testing.T) {
	require.Nil(t, toClientMergeRequest(nil))
}

func TestToClientMergeability_Nil(t *testing.T) {
	require.Nil(t, toClientMergeability(nil))
}
