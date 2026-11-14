package daemon

import (
	"context"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/grpc/pb"
)

// ProxyTagList lists the project's release tags, newest first.
func (s *Server) ProxyTagList(ctx context.Context, req *daemonpb.ProxyTagListRequest) (*daemonpb.ProxyTagListResponse, error) {
	var res *pb.ListTagsResponse
	err := s.withProxy(ctx, req.GetRoot(), func(client pb.NipaServiceClient, project *pb.ProjectContext) error {
		request := req.GetRequest()
		if request == nil {
			request = &pb.ListTagsRequest{}
		}
		request.Context = project
		var err error
		res, err = client.ListTags(ctx, request)
		return err
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return &daemonpb.ProxyTagListResponse{Response: res}, nil
}

// ProxyTagCreate tags a commit, a branch head or the default branch head.
func (s *Server) ProxyTagCreate(ctx context.Context, req *daemonpb.ProxyTagCreateRequest) (*daemonpb.ProxyTagCreateResponse, error) {
	var res *pb.CreateTagResponse
	err := s.withProxy(ctx, req.GetRoot(), func(client pb.NipaServiceClient, project *pb.ProjectContext) error {
		request := req.GetRequest()
		if request == nil {
			request = &pb.CreateTagRequest{}
		}
		request.Context = project
		var err error
		res, err = client.CreateTag(ctx, request)
		return err
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return &daemonpb.ProxyTagCreateResponse{Response: res}, nil
}

// ProxyTagDelete deletes a tag; the name can be reused afterwards.
func (s *Server) ProxyTagDelete(ctx context.Context, req *daemonpb.ProxyTagDeleteRequest) (*daemonpb.ProxyTagDeleteResponse, error) {
	var res *pb.DeleteTagResponse
	err := s.withProxy(ctx, req.GetRoot(), func(client pb.NipaServiceClient, project *pb.ProjectContext) error {
		request := req.GetRequest()
		if request == nil {
			request = &pb.DeleteTagRequest{}
		}
		request.Context = project
		var err error
		res, err = client.DeleteTag(ctx, request)
		return err
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return &daemonpb.ProxyTagDeleteResponse{Response: res}, nil
}

// ProxyProjectMembers lists the users who can read the project, for pickers.
func (s *Server) ProxyProjectMembers(ctx context.Context, req *daemonpb.ProxyProjectMembersRequest) (*daemonpb.ProxyProjectMembersResponse, error) {
	var res *pb.ListProjectMembersResponse
	err := s.withProxy(ctx, req.GetRoot(), func(client pb.NipaServiceClient, project *pb.ProjectContext) error {
		request := req.GetRequest()
		if request == nil {
			request = &pb.ListProjectMembersRequest{}
		}
		request.Context = project
		var err error
		res, err = client.ListProjectMembers(ctx, request)
		return err
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return &daemonpb.ProxyProjectMembersResponse{Response: res}, nil
}
