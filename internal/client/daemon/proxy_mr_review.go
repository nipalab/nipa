package daemon

import (
	"context"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/grpc/pb"
)

// ProxyMergeRequestUpdate edits the title, description or draft state of a merge request.
func (s *Server) ProxyMergeRequestUpdate(ctx context.Context, req *daemonpb.ProxyMergeRequestUpdateRequest) (*daemonpb.ProxyMergeRequestUpdateResponse, error) {
	var res *pb.UpdateMergeRequestResponse
	err := s.withProxy(ctx, req.GetRoot(), func(client pb.NipaServiceClient, project *pb.ProjectContext) error {
		request := req.GetRequest()
		if request == nil {
			request = &pb.UpdateMergeRequestRequest{}
		}
		request.Context = project
		var err error
		res, err = client.UpdateMergeRequest(ctx, request)
		return err
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return &daemonpb.ProxyMergeRequestUpdateResponse{Response: res}, nil
}

// ProxyMergeRequestCommits lists the commits a merge request brings in.
func (s *Server) ProxyMergeRequestCommits(ctx context.Context, req *daemonpb.ProxyMergeRequestCommitsRequest) (*daemonpb.ProxyMergeRequestCommitsResponse, error) {
	var res *pb.ListMergeRequestCommitsResponse
	err := s.withProxy(ctx, req.GetRoot(), func(client pb.NipaServiceClient, project *pb.ProjectContext) error {
		request := req.GetRequest()
		if request == nil {
			request = &pb.ListMergeRequestCommitsRequest{}
		}
		request.Context = project
		var err error
		res, err = client.ListMergeRequestCommits(ctx, request)
		return err
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return &daemonpb.ProxyMergeRequestCommitsResponse{Response: res}, nil
}

// ProxyMergeRequestChecks lists the status checks of the source head.
func (s *Server) ProxyMergeRequestChecks(ctx context.Context, req *daemonpb.ProxyMergeRequestChecksRequest) (*daemonpb.ProxyMergeRequestChecksResponse, error) {
	var res *pb.ListMergeRequestChecksResponse
	err := s.withProxy(ctx, req.GetRoot(), func(client pb.NipaServiceClient, project *pb.ProjectContext) error {
		request := req.GetRequest()
		if request == nil {
			request = &pb.ListMergeRequestChecksRequest{}
		}
		request.Context = project
		var err error
		res, err = client.ListMergeRequestChecks(ctx, request)
		return err
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return &daemonpb.ProxyMergeRequestChecksResponse{Response: res}, nil
}

// ProxyMergeRequestTimeline lists the activity of a merge request.
func (s *Server) ProxyMergeRequestTimeline(ctx context.Context, req *daemonpb.ProxyMergeRequestTimelineRequest) (*daemonpb.ProxyMergeRequestTimelineResponse, error) {
	var res *pb.ListMergeRequestTimelineResponse
	err := s.withProxy(ctx, req.GetRoot(), func(client pb.NipaServiceClient, project *pb.ProjectContext) error {
		request := req.GetRequest()
		if request == nil {
			request = &pb.ListMergeRequestTimelineRequest{}
		}
		request.Context = project
		var err error
		res, err = client.ListMergeRequestTimeline(ctx, request)
		return err
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return &daemonpb.ProxyMergeRequestTimelineResponse{Response: res}, nil
}

// ProxyMergeRequestAddComment starts a comment thread, top-level or on a diff line.
func (s *Server) ProxyMergeRequestAddComment(ctx context.Context, req *daemonpb.ProxyMergeRequestAddCommentRequest) (*daemonpb.ProxyMergeRequestAddCommentResponse, error) {
	var res *pb.AddMergeRequestCommentResponse
	err := s.withProxy(ctx, req.GetRoot(), func(client pb.NipaServiceClient, project *pb.ProjectContext) error {
		request := req.GetRequest()
		if request == nil {
			request = &pb.AddMergeRequestCommentRequest{}
		}
		request.Context = project
		var err error
		res, err = client.AddMergeRequestComment(ctx, request)
		return err
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return &daemonpb.ProxyMergeRequestAddCommentResponse{Response: res}, nil
}

// ProxyMergeRequestReply answers a comment thread.
func (s *Server) ProxyMergeRequestReply(ctx context.Context, req *daemonpb.ProxyMergeRequestReplyRequest) (*daemonpb.ProxyMergeRequestReplyResponse, error) {
	var res *pb.ReplyMergeRequestThreadResponse
	err := s.withProxy(ctx, req.GetRoot(), func(client pb.NipaServiceClient, project *pb.ProjectContext) error {
		request := req.GetRequest()
		if request == nil {
			request = &pb.ReplyMergeRequestThreadRequest{}
		}
		request.Context = project
		var err error
		res, err = client.ReplyMergeRequestThread(ctx, request)
		return err
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return &daemonpb.ProxyMergeRequestReplyResponse{Response: res}, nil
}

// ProxyMergeRequestResolve resolves or reopens a comment thread.
func (s *Server) ProxyMergeRequestResolve(ctx context.Context, req *daemonpb.ProxyMergeRequestResolveRequest) (*daemonpb.ProxyMergeRequestResolveResponse, error) {
	var res *pb.ResolveMergeRequestThreadResponse
	err := s.withProxy(ctx, req.GetRoot(), func(client pb.NipaServiceClient, project *pb.ProjectContext) error {
		request := req.GetRequest()
		if request == nil {
			request = &pb.ResolveMergeRequestThreadRequest{}
		}
		request.Context = project
		var err error
		res, err = client.ResolveMergeRequestThread(ctx, request)
		return err
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return &daemonpb.ProxyMergeRequestResolveResponse{Response: res}, nil
}
