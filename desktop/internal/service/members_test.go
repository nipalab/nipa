package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/grpc/pb"
)

type memberDaemon struct {
	*fakeDaemon
	members  *pb.ListProjectMembersRequest
	assigned *pb.SetMergeRequestAssigneesRequest
	asked    *pb.RequestMergeRequestReviewRequest
	err      error
}

func (d *memberDaemon) ProxyProjectMembers(_ context.Context, req *daemonpb.ProxyProjectMembersRequest, _ ...grpc.CallOption) (*daemonpb.ProxyProjectMembersResponse, error) {
	d.members = req.GetRequest()
	return &daemonpb.ProxyProjectMembersResponse{Response: &pb.ListProjectMembersResponse{Members: []*pb.ProjectMember{{Id: "u1", Name: "Ana"}, {Id: "u2", Name: "Bo"}}}}, d.err
}

func (d *memberDaemon) ProxyMergeRequestAssign(_ context.Context, req *daemonpb.ProxyMergeRequestAssignRequest, _ ...grpc.CallOption) (*daemonpb.ProxyMergeRequestAssignResponse, error) {
	d.assigned = req.GetRequest()
	return &daemonpb.ProxyMergeRequestAssignResponse{Response: &pb.SetMergeRequestAssigneesResponse{MergeRequest: &pb.MergeRequestDetail{Number: req.GetRequest().GetNumber(), Assignees: []*pb.ReviewActor{{UserId: "u1", Name: "Ana"}}}}}, d.err
}

func (d *memberDaemon) ProxyMergeRequestRequestReview(_ context.Context, req *daemonpb.ProxyMergeRequestRequestReviewRequest, _ ...grpc.CallOption) (*daemonpb.ProxyMergeRequestRequestReviewResponse, error) {
	d.asked = req.GetRequest()
	return &daemonpb.ProxyMergeRequestRequestReviewResponse{}, d.err
}

func newMembers(t *testing.T) (*Service, *memberDaemon) {
	t.Helper()
	d := &memberDaemon{fakeDaemon: &fakeDaemon{watchErr: map[string]error{}, changes: make(chan *daemonpb.RepoChange, 4)}}
	svc := New(func(context.Context) (Session, error) { return &opsSession{client: d}, nil }, &memStore{}, nil)
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	return svc, d
}

func TestService_Members(t *testing.T) {
	svc, d := newMembers(t)

	people, err := svc.Members(context.Background(), "/r", "  an ")

	require.NoError(t, err)
	require.Equal(t, []Person{{ID: "u1", Name: "Ana"}, {ID: "u2", Name: "Bo"}}, people)
	require.Equal(t, "an", d.members.GetQuery())
	require.Equal(t, int32(memberLimit), d.members.GetLimit())

	d.err = errors.New("down")
	_, err = svc.Members(context.Background(), "/r", "")
	require.Error(t, err)
}

func TestService_AssignMergeRequest(t *testing.T) {
	svc, d := newMembers(t)

	mr, err := svc.AssignMergeRequest(context.Background(), "/r", 7, []string{"u1"})

	require.NoError(t, err)
	require.Equal(t, int64(7), mr.Number)
	require.Equal(t, []Person{{ID: "u1", Name: "Ana"}}, mr.Assignees)
	require.Equal(t, []string{"u1"}, d.assigned.GetUserIds())

	d.err = errors.New("down")
	_, err = svc.AssignMergeRequest(context.Background(), "/r", 7, nil)
	require.Error(t, err)
}

func TestService_RequestReview(t *testing.T) {
	svc, d := newMembers(t)

	require.Error(t, svc.RequestReview(context.Background(), "/r", 7, ""))
	require.NoError(t, svc.RequestReview(context.Background(), "/r", 7, "u2"))
	require.Equal(t, "u2", d.asked.GetUserId())
	require.Equal(t, int64(7), d.asked.GetNumber())

	d.err = errors.New("down")
	require.Error(t, svc.RequestReview(context.Background(), "/r", 7, "u2"))
}
