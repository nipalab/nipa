package daemon

import (
	"context"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ProxyConn is the per-repo server connection the proxy surface forwards to.
// It is the daemon's own client transport with the repo's host binding.
type ProxyConn interface {
	Connect(ctx context.Context, host string) error
	ServiceClient() (pb.NipaServiceClient, error)
	Close() error
}

// withProxy resolves a watched repo, binds the repo's proxy connection to its
// server host and hands the caller the raw typed client plus the project
// context derived from .nipa/config. Proxy requests carry the wrapped server
// message; any context the caller set is overwritten with the clone's.
func (s *Server) withProxy(ctx context.Context, root string, fn func(pb.NipaServiceClient, *pb.ProjectContext) error) error {
	rp, unref, err := s.repos.ref(root)
	if err != nil {
		return err
	}
	defer unref()
	if rp.proxy == nil {
		return status.Error(codes.FailedPrecondition, "proxy is not configured")
	}
	nu, err := clientDomain.ParseNipaUrl(rp.config().Url)
	if err != nil {
		return err
	}
	if err := rp.proxy.Connect(ctx, nu.Host); err != nil {
		return err
	}
	client, err := rp.proxy.ServiceClient()
	if err != nil {
		return err
	}
	return fn(client, &pb.ProjectContext{Org: nu.Org, Project: nu.Project})
}

// ProxyBranchList lists the branches of the repo's project.
func (s *Server) ProxyBranchList(ctx context.Context, req *daemonpb.ProxyBranchListRequest) (*daemonpb.ProxyBranchListResponse, error) {
	var res *pb.GetListBranchResponse
	err := s.withProxy(ctx, req.GetRoot(), func(client pb.NipaServiceClient, project *pb.ProjectContext) error {
		request := req.GetRequest()
		if request == nil {
			request = &pb.GetListBranchRequest{}
		}
		request.Context = project
		var err error
		res, err = client.GetListBranch(ctx, request)
		return err
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return &daemonpb.ProxyBranchListResponse{Response: res}, nil
}

// ProxyBranchCreate creates a branch, optionally forked from a commit.
func (s *Server) ProxyBranchCreate(ctx context.Context, req *daemonpb.ProxyBranchCreateRequest) (*daemonpb.ProxyBranchCreateResponse, error) {
	var res *pb.CreateBranchResponse
	err := s.withProxy(ctx, req.GetRoot(), func(client pb.NipaServiceClient, project *pb.ProjectContext) error {
		request := req.GetRequest()
		if request == nil {
			request = &pb.CreateBranchRequest{}
		}
		request.Context = project
		var err error
		res, err = client.CreateBranch(ctx, request)
		return err
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return &daemonpb.ProxyBranchCreateResponse{Response: res}, nil
}

// ProxyBranchDelete deletes a branch.
func (s *Server) ProxyBranchDelete(ctx context.Context, req *daemonpb.ProxyBranchDeleteRequest) (*daemonpb.ProxyBranchDeleteResponse, error) {
	var res *pb.DeleteBranchResponse
	err := s.withProxy(ctx, req.GetRoot(), func(client pb.NipaServiceClient, project *pb.ProjectContext) error {
		request := req.GetRequest()
		if request == nil {
			request = &pb.DeleteBranchRequest{}
		}
		request.Context = project
		var err error
		res, err = client.DeleteBranch(ctx, request)
		return err
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return &daemonpb.ProxyBranchDeleteResponse{Response: res}, nil
}

// ProxyTreeManifest returns a branch tree manifest.
func (s *Server) ProxyTreeManifest(ctx context.Context, req *daemonpb.ProxyTreeManifestRequest) (*daemonpb.ProxyTreeManifestResponse, error) {
	var res *pb.GetTreeManifestResponse
	err := s.withProxy(ctx, req.GetRoot(), func(client pb.NipaServiceClient, project *pb.ProjectContext) error {
		request := req.GetRequest()
		if request == nil {
			request = &pb.GetTreeManifestRequest{}
		}
		request.Context = project
		var err error
		res, err = client.GetTreeManifest(ctx, request)
		return err
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return &daemonpb.ProxyTreeManifestResponse{Response: res}, nil
}

// ProxyCommitLog returns the commit log of a branch.
func (s *Server) ProxyCommitLog(ctx context.Context, req *daemonpb.ProxyCommitLogRequest) (*daemonpb.ProxyCommitLogResponse, error) {
	var res *pb.GetCommitLogResponse
	err := s.withProxy(ctx, req.GetRoot(), func(client pb.NipaServiceClient, project *pb.ProjectContext) error {
		request := req.GetRequest()
		if request == nil {
			request = &pb.GetCommitLogRequest{}
		}
		request.Context = project
		var err error
		res, err = client.GetCommitLog(ctx, request)
		return err
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return &daemonpb.ProxyCommitLogResponse{Response: res}, nil
}

// ProxyCommitGet returns one commit plus its recursive tree manifest.
func (s *Server) ProxyCommitGet(ctx context.Context, req *daemonpb.ProxyCommitGetRequest) (*daemonpb.ProxyCommitGetResponse, error) {
	var res *pb.GetCommitResponse
	err := s.withProxy(ctx, req.GetRoot(), func(client pb.NipaServiceClient, project *pb.ProjectContext) error {
		request := req.GetRequest()
		if request == nil {
			request = &pb.GetCommitRequest{}
		}
		request.Context = project
		var err error
		res, err = client.GetCommit(ctx, request)
		return err
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return &daemonpb.ProxyCommitGetResponse{Response: res}, nil
}

// ProxyCommitWalk walks commits newest-first, both parents, stop-exclusive.
func (s *Server) ProxyCommitWalk(ctx context.Context, req *daemonpb.ProxyCommitWalkRequest) (*daemonpb.ProxyCommitWalkResponse, error) {
	var res *pb.WalkCommitsResponse
	err := s.withProxy(ctx, req.GetRoot(), func(client pb.NipaServiceClient, project *pb.ProjectContext) error {
		request := req.GetRequest()
		if request == nil {
			request = &pb.WalkCommitsRequest{}
		}
		request.Context = project
		var err error
		res, err = client.WalkCommits(ctx, request)
		return err
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return &daemonpb.ProxyCommitWalkResponse{Response: res}, nil
}

// ProxyMergeBase returns the merge base of two branch and/or commit refs.
func (s *Server) ProxyMergeBase(ctx context.Context, req *daemonpb.ProxyMergeBaseRequest) (*daemonpb.ProxyMergeBaseResponse, error) {
	var res *pb.GetMergeBaseResponse
	err := s.withProxy(ctx, req.GetRoot(), func(client pb.NipaServiceClient, project *pb.ProjectContext) error {
		request := req.GetRequest()
		if request == nil {
			request = &pb.GetMergeBaseRequest{}
		}
		request.Context = project
		var err error
		res, err = client.GetMergeBase(ctx, request)
		return err
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return &daemonpb.ProxyMergeBaseResponse{Response: res}, nil
}

// ProxyLockFile acquires a binary file lock (branch empty = mainline lock).
func (s *Server) ProxyLockFile(ctx context.Context, req *daemonpb.ProxyLockFileRequest) (*daemonpb.ProxyLockFileResponse, error) {
	var res *pb.LockFileResponse
	err := s.withProxy(ctx, req.GetRoot(), func(client pb.NipaServiceClient, project *pb.ProjectContext) error {
		request := req.GetRequest()
		if request == nil {
			request = &pb.LockFileRequest{}
		}
		request.Context = project
		var err error
		res, err = client.LockFile(ctx, request)
		return err
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return &daemonpb.ProxyLockFileResponse{Response: res}, nil
}

// ProxyUnlockFile releases a file lock.
func (s *Server) ProxyUnlockFile(ctx context.Context, req *daemonpb.ProxyUnlockFileRequest) (*daemonpb.ProxyUnlockFileResponse, error) {
	var res *pb.UnlockFileResponse
	err := s.withProxy(ctx, req.GetRoot(), func(client pb.NipaServiceClient, project *pb.ProjectContext) error {
		request := req.GetRequest()
		if request == nil {
			request = &pb.UnlockFileRequest{}
		}
		request.Context = project
		var err error
		res, err = client.UnlockFile(ctx, request)
		return err
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return &daemonpb.ProxyUnlockFileResponse{Response: res}, nil
}

// ProxyListFileLocks lists the locks visible in the project.
func (s *Server) ProxyListFileLocks(ctx context.Context, req *daemonpb.ProxyListFileLocksRequest) (*daemonpb.ProxyListFileLocksResponse, error) {
	var res *pb.ListFileLocksResponse
	err := s.withProxy(ctx, req.GetRoot(), func(client pb.NipaServiceClient, project *pb.ProjectContext) error {
		request := req.GetRequest()
		if request == nil {
			request = &pb.ListFileLocksRequest{}
		}
		request.Context = project
		var err error
		res, err = client.ListFileLocks(ctx, request)
		return err
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return &daemonpb.ProxyListFileLocksResponse{Response: res}, nil
}
