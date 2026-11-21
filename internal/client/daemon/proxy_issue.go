package daemon

import (
	"context"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/grpc/pb"
)

// ProxyResolveIssues looks up issue keys through the project's issue tracker.
func (s *Server) ProxyResolveIssues(ctx context.Context, req *daemonpb.ProxyResolveIssuesRequest) (*daemonpb.ProxyResolveIssuesResponse, error) {
	var res *pb.ResolveIssuesResponse
	err := s.withProxy(ctx, req.GetRoot(), func(client pb.NipaServiceClient, project *pb.ProjectContext) error {
		request := req.GetRequest()
		if request == nil {
			request = &pb.ResolveIssuesRequest{}
		}
		request.Context = project
		var err error
		res, err = client.ResolveIssues(ctx, request)
		return err
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return &daemonpb.ProxyResolveIssuesResponse{Response: res}, nil
}

// ProxySearchIssues searches the project's issue tracker for issue pickers.
func (s *Server) ProxySearchIssues(ctx context.Context, req *daemonpb.ProxySearchIssuesRequest) (*daemonpb.ProxySearchIssuesResponse, error) {
	var res *pb.SearchIssuesResponse
	err := s.withProxy(ctx, req.GetRoot(), func(client pb.NipaServiceClient, project *pb.ProjectContext) error {
		request := req.GetRequest()
		if request == nil {
			request = &pb.SearchIssuesRequest{}
		}
		request.Context = project
		var err error
		res, err = client.SearchIssues(ctx, request)
		return err
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return &daemonpb.ProxySearchIssuesResponse{Response: res}, nil
}
