package daemon

import (
	"context"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/grpc/pb"
)

// ProxyMergeRequestList lists merge requests of the repo's project.
func (s *Server) ProxyMergeRequestList(ctx context.Context, req *daemonpb.ProxyMergeRequestListRequest) (*daemonpb.ProxyMergeRequestListResponse, error) {
	var res *pb.ListMergeRequestsResponse
	err := s.withProxy(ctx, req.GetRoot(), func(client pb.NipaServiceClient, project *pb.ProjectContext) error {
		request := req.GetRequest()
		if request == nil {
			request = &pb.ListMergeRequestsRequest{}
		}
		request.Context = project
		var err error
		res, err = client.ListMergeRequests(ctx, request)
		return err
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return &daemonpb.ProxyMergeRequestListResponse{Response: res}, nil
}

// ProxyMergeRequestCreate opens a merge request.
func (s *Server) ProxyMergeRequestCreate(ctx context.Context, req *daemonpb.ProxyMergeRequestCreateRequest) (*daemonpb.ProxyMergeRequestCreateResponse, error) {
	var res *pb.CreateMergeRequestResponse
	err := s.withProxy(ctx, req.GetRoot(), func(client pb.NipaServiceClient, project *pb.ProjectContext) error {
		request := req.GetRequest()
		if request == nil {
			request = &pb.CreateMergeRequestRequest{}
		}
		request.Context = project
		var err error
		res, err = client.CreateMergeRequest(ctx, request)
		return err
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return &daemonpb.ProxyMergeRequestCreateResponse{Response: res}, nil
}

// ProxyMergeRequestMerge merges a merge request.
func (s *Server) ProxyMergeRequestMerge(ctx context.Context, req *daemonpb.ProxyMergeRequestMergeRequest) (*daemonpb.ProxyMergeRequestMergeResponse, error) {
	var res *pb.MergeMergeRequestResponse
	err := s.withProxy(ctx, req.GetRoot(), func(client pb.NipaServiceClient, project *pb.ProjectContext) error {
		request := req.GetRequest()
		if request == nil {
			request = &pb.MergeMergeRequestRequest{}
		}
		request.Context = project
		var err error
		res, err = client.MergeMergeRequest(ctx, request)
		return err
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return &daemonpb.ProxyMergeRequestMergeResponse{Response: res}, nil
}

// ProxyMergeRequestClose closes a merge request.
func (s *Server) ProxyMergeRequestClose(ctx context.Context, req *daemonpb.ProxyMergeRequestCloseRequest) (*daemonpb.ProxyMergeRequestCloseResponse, error) {
	var res *pb.CloseMergeRequestResponse
	err := s.withProxy(ctx, req.GetRoot(), func(client pb.NipaServiceClient, project *pb.ProjectContext) error {
		request := req.GetRequest()
		if request == nil {
			request = &pb.CloseMergeRequestRequest{}
		}
		request.Context = project
		var err error
		res, err = client.CloseMergeRequest(ctx, request)
		return err
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return &daemonpb.ProxyMergeRequestCloseResponse{Response: res}, nil
}

// ProxyMergeRequestReviews lists the reviews of a merge request.
func (s *Server) ProxyMergeRequestReviews(ctx context.Context, req *daemonpb.ProxyMergeRequestReviewsRequest) (*daemonpb.ProxyMergeRequestReviewsResponse, error) {
	var res *pb.ListMergeRequestReviewsResponse
	err := s.withProxy(ctx, req.GetRoot(), func(client pb.NipaServiceClient, project *pb.ProjectContext) error {
		request := req.GetRequest()
		if request == nil {
			request = &pb.ListMergeRequestReviewsRequest{}
		}
		request.Context = project
		var err error
		res, err = client.ListMergeRequestReviews(ctx, request)
		return err
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return &daemonpb.ProxyMergeRequestReviewsResponse{Response: res}, nil
}

// ProxyMergeRequestReviewState returns the live review summary of a request.
func (s *Server) ProxyMergeRequestReviewState(ctx context.Context, req *daemonpb.ProxyMergeRequestReviewStateRequest) (*daemonpb.ProxyMergeRequestReviewStateResponse, error) {
	var res *pb.GetMergeRequestReviewStateResponse
	err := s.withProxy(ctx, req.GetRoot(), func(client pb.NipaServiceClient, project *pb.ProjectContext) error {
		request := req.GetRequest()
		if request == nil {
			request = &pb.GetMergeRequestReviewStateRequest{}
		}
		request.Context = project
		var err error
		res, err = client.GetMergeRequestReviewState(ctx, request)
		return err
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return &daemonpb.ProxyMergeRequestReviewStateResponse{Response: res}, nil
}

// ProxyMergeRequestThreads lists the conversation threads of a request.
func (s *Server) ProxyMergeRequestThreads(ctx context.Context, req *daemonpb.ProxyMergeRequestThreadsRequest) (*daemonpb.ProxyMergeRequestThreadsResponse, error) {
	var res *pb.ListMergeRequestThreadsResponse
	err := s.withProxy(ctx, req.GetRoot(), func(client pb.NipaServiceClient, project *pb.ProjectContext) error {
		request := req.GetRequest()
		if request == nil {
			request = &pb.ListMergeRequestThreadsRequest{}
		}
		request.Context = project
		var err error
		res, err = client.ListMergeRequestThreads(ctx, request)
		return err
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return &daemonpb.ProxyMergeRequestThreadsResponse{Response: res}, nil
}

// ProxyMergeRequestGet returns one merge request with its mergeability.
func (s *Server) ProxyMergeRequestGet(ctx context.Context, req *daemonpb.ProxyMergeRequestGetRequest) (*daemonpb.ProxyMergeRequestGetResponse, error) {
	var res *pb.GetMergeRequestResponse
	err := s.withProxy(ctx, req.GetRoot(), func(client pb.NipaServiceClient, project *pb.ProjectContext) error {
		request := req.GetRequest()
		if request == nil {
			request = &pb.GetMergeRequestRequest{}
		}
		request.Context = project
		var err error
		res, err = client.GetMergeRequest(ctx, request)
		return err
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return &daemonpb.ProxyMergeRequestGetResponse{Response: res}, nil
}

// ProxyMergeRequestCheck recomputes the mergeability of a request.
func (s *Server) ProxyMergeRequestCheck(ctx context.Context, req *daemonpb.ProxyMergeRequestCheckRequest) (*daemonpb.ProxyMergeRequestCheckResponse, error) {
	var res *pb.CheckMergeRequestResponse
	err := s.withProxy(ctx, req.GetRoot(), func(client pb.NipaServiceClient, project *pb.ProjectContext) error {
		request := req.GetRequest()
		if request == nil {
			request = &pb.CheckMergeRequestRequest{}
		}
		request.Context = project
		var err error
		res, err = client.CheckMergeRequest(ctx, request)
		return err
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return &daemonpb.ProxyMergeRequestCheckResponse{Response: res}, nil
}

// ProxyMergeRequestReopen reopens a closed merge request.
func (s *Server) ProxyMergeRequestReopen(ctx context.Context, req *daemonpb.ProxyMergeRequestReopenRequest) (*daemonpb.ProxyMergeRequestReopenResponse, error) {
	var res *pb.ReopenMergeRequestResponse
	err := s.withProxy(ctx, req.GetRoot(), func(client pb.NipaServiceClient, project *pb.ProjectContext) error {
		request := req.GetRequest()
		if request == nil {
			request = &pb.ReopenMergeRequestRequest{}
		}
		request.Context = project
		var err error
		res, err = client.ReopenMergeRequest(ctx, request)
		return err
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return &daemonpb.ProxyMergeRequestReopenResponse{Response: res}, nil
}

// ProxyMergeRequestSubmitReview records a review decision.
func (s *Server) ProxyMergeRequestSubmitReview(ctx context.Context, req *daemonpb.ProxyMergeRequestSubmitReviewRequest) (*daemonpb.ProxyMergeRequestSubmitReviewResponse, error) {
	var res *pb.SubmitMergeRequestReviewResponse
	err := s.withProxy(ctx, req.GetRoot(), func(client pb.NipaServiceClient, project *pb.ProjectContext) error {
		request := req.GetRequest()
		if request == nil {
			request = &pb.SubmitMergeRequestReviewRequest{}
		}
		request.Context = project
		var err error
		res, err = client.SubmitMergeRequestReview(ctx, request)
		return err
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return &daemonpb.ProxyMergeRequestSubmitReviewResponse{Response: res}, nil
}
