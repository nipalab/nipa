package daemon

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/grpc/pb"
)

type fakeProxyConn struct {
	host     string
	connects int
	client   pb.NipaServiceClient
	err      error
	closed   bool
}

func (f *fakeProxyConn) Connect(_ context.Context, host string) error {
	f.connects++
	f.host = host
	return f.err
}

func (f *fakeProxyConn) ServiceClient() (pb.NipaServiceClient, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.client, nil
}

func (f *fakeProxyConn) Close() error {
	f.closed = true
	return nil
}

// fakeService embeds the full server client and implements only the proxied
// methods the tests exercise; any other call would panic, which is fine.
type fakeService struct {
	pb.NipaServiceClient

	listBranches func(ctx context.Context, req *pb.GetListBranchRequest) (*pb.GetListBranchResponse, error)
	treeManifest func(ctx context.Context, req *pb.GetTreeManifestRequest) (*pb.GetTreeManifestResponse, error)
	createBranch func(ctx context.Context, req *pb.CreateBranchRequest) (*pb.CreateBranchResponse, error)
	mergeBase    func(ctx context.Context, req *pb.GetMergeBaseRequest) (*pb.GetMergeBaseResponse, error)
	createMR     func(ctx context.Context, req *pb.CreateMergeRequestRequest) (*pb.CreateMergeRequestResponse, error)
	lockFile     func(ctx context.Context, req *pb.LockFileRequest) (*pb.LockFileResponse, error)
}

func (f *fakeService) GetListBranch(ctx context.Context, req *pb.GetListBranchRequest, _ ...grpc.CallOption) (*pb.GetListBranchResponse, error) {
	if f.listBranches != nil {
		return f.listBranches(ctx, req)
	}
	return &pb.GetListBranchResponse{}, nil
}

func (f *fakeService) GetTreeManifest(ctx context.Context, req *pb.GetTreeManifestRequest, _ ...grpc.CallOption) (*pb.GetTreeManifestResponse, error) {
	if f.treeManifest != nil {
		return f.treeManifest(ctx, req)
	}
	return &pb.GetTreeManifestResponse{}, nil
}

func (f *fakeService) CreateBranch(ctx context.Context, req *pb.CreateBranchRequest, _ ...grpc.CallOption) (*pb.CreateBranchResponse, error) {
	if f.createBranch != nil {
		return f.createBranch(ctx, req)
	}
	return &pb.CreateBranchResponse{}, nil
}

func (f *fakeService) GetMergeBase(ctx context.Context, req *pb.GetMergeBaseRequest, _ ...grpc.CallOption) (*pb.GetMergeBaseResponse, error) {
	if f.mergeBase != nil {
		return f.mergeBase(ctx, req)
	}
	return &pb.GetMergeBaseResponse{}, nil
}

func (f *fakeService) CreateMergeRequest(ctx context.Context, req *pb.CreateMergeRequestRequest, _ ...grpc.CallOption) (*pb.CreateMergeRequestResponse, error) {
	if f.createMR != nil {
		return f.createMR(ctx, req)
	}
	return &pb.CreateMergeRequestResponse{}, nil
}

func (f *fakeService) LockFile(ctx context.Context, req *pb.LockFileRequest, _ ...grpc.CallOption) (*pb.LockFileResponse, error) {
	if f.lockFile != nil {
		return f.lockFile(ctx, req)
	}
	return &pb.LockFileResponse{}, nil
}

// emptyService implements every proxied server method with an empty response,
// for exercising handler plumbing.
type emptyService struct {
	pb.NipaServiceClient
}

func (emptyService) GetListBranch(context.Context, *pb.GetListBranchRequest, ...grpc.CallOption) (*pb.GetListBranchResponse, error) {
	return &pb.GetListBranchResponse{}, nil
}

func (emptyService) CreateBranch(context.Context, *pb.CreateBranchRequest, ...grpc.CallOption) (*pb.CreateBranchResponse, error) {
	return &pb.CreateBranchResponse{}, nil
}

func (emptyService) DeleteBranch(context.Context, *pb.DeleteBranchRequest, ...grpc.CallOption) (*pb.DeleteBranchResponse, error) {
	return &pb.DeleteBranchResponse{}, nil
}

func (emptyService) GetTreeManifest(context.Context, *pb.GetTreeManifestRequest, ...grpc.CallOption) (*pb.GetTreeManifestResponse, error) {
	return &pb.GetTreeManifestResponse{}, nil
}

func (emptyService) GetCommitLog(context.Context, *pb.GetCommitLogRequest, ...grpc.CallOption) (*pb.GetCommitLogResponse, error) {
	return &pb.GetCommitLogResponse{}, nil
}

func (emptyService) GetCommit(context.Context, *pb.GetCommitRequest, ...grpc.CallOption) (*pb.GetCommitResponse, error) {
	return &pb.GetCommitResponse{}, nil
}

func (emptyService) WalkCommits(context.Context, *pb.WalkCommitsRequest, ...grpc.CallOption) (*pb.WalkCommitsResponse, error) {
	return &pb.WalkCommitsResponse{}, nil
}

func (emptyService) GetMergeBase(context.Context, *pb.GetMergeBaseRequest, ...grpc.CallOption) (*pb.GetMergeBaseResponse, error) {
	return &pb.GetMergeBaseResponse{}, nil
}

func (emptyService) ListMergeRequests(context.Context, *pb.ListMergeRequestsRequest, ...grpc.CallOption) (*pb.ListMergeRequestsResponse, error) {
	return &pb.ListMergeRequestsResponse{}, nil
}

func (emptyService) CreateMergeRequest(context.Context, *pb.CreateMergeRequestRequest, ...grpc.CallOption) (*pb.CreateMergeRequestResponse, error) {
	return &pb.CreateMergeRequestResponse{}, nil
}

func (emptyService) MergeMergeRequest(context.Context, *pb.MergeMergeRequestRequest, ...grpc.CallOption) (*pb.MergeMergeRequestResponse, error) {
	return &pb.MergeMergeRequestResponse{}, nil
}

func (emptyService) CloseMergeRequest(context.Context, *pb.CloseMergeRequestRequest, ...grpc.CallOption) (*pb.CloseMergeRequestResponse, error) {
	return &pb.CloseMergeRequestResponse{}, nil
}

func (emptyService) ListMergeRequestReviews(context.Context, *pb.ListMergeRequestReviewsRequest, ...grpc.CallOption) (*pb.ListMergeRequestReviewsResponse, error) {
	return &pb.ListMergeRequestReviewsResponse{}, nil
}

func (emptyService) GetMergeRequestReviewState(context.Context, *pb.GetMergeRequestReviewStateRequest, ...grpc.CallOption) (*pb.GetMergeRequestReviewStateResponse, error) {
	return &pb.GetMergeRequestReviewStateResponse{}, nil
}

func (emptyService) ListMergeRequestThreads(context.Context, *pb.ListMergeRequestThreadsRequest, ...grpc.CallOption) (*pb.ListMergeRequestThreadsResponse, error) {
	return &pb.ListMergeRequestThreadsResponse{}, nil
}

func (emptyService) LockFile(context.Context, *pb.LockFileRequest, ...grpc.CallOption) (*pb.LockFileResponse, error) {
	return &pb.LockFileResponse{}, nil
}

func (emptyService) UnlockFile(context.Context, *pb.UnlockFileRequest, ...grpc.CallOption) (*pb.UnlockFileResponse, error) {
	return &pb.UnlockFileResponse{}, nil
}

func (emptyService) ListFileLocks(context.Context, *pb.ListFileLocksRequest, ...grpc.CallOption) (*pb.ListFileLocksResponse, error) {
	return &pb.ListFileLocksResponse{}, nil
}

// proxyCalls exercises every proxy handler with a nil wrapped request.
func proxyCalls(srv *Server, ctx context.Context, root string) []struct {
	name string
	call func() error
} {
	return []struct {
		name string
		call func() error
	}{
		{"BranchList", func() error {
			_, err := srv.ProxyBranchList(ctx, &daemonpb.ProxyBranchListRequest{Root: root})
			return err
		}},
		{"BranchCreate", func() error {
			_, err := srv.ProxyBranchCreate(ctx, &daemonpb.ProxyBranchCreateRequest{Root: root})
			return err
		}},
		{"BranchDelete", func() error {
			_, err := srv.ProxyBranchDelete(ctx, &daemonpb.ProxyBranchDeleteRequest{Root: root})
			return err
		}},
		{"TreeManifest", func() error {
			_, err := srv.ProxyTreeManifest(ctx, &daemonpb.ProxyTreeManifestRequest{Root: root})
			return err
		}},
		{"CommitLog", func() error {
			_, err := srv.ProxyCommitLog(ctx, &daemonpb.ProxyCommitLogRequest{Root: root})
			return err
		}},
		{"CommitGet", func() error {
			_, err := srv.ProxyCommitGet(ctx, &daemonpb.ProxyCommitGetRequest{Root: root})
			return err
		}},
		{"CommitWalk", func() error {
			_, err := srv.ProxyCommitWalk(ctx, &daemonpb.ProxyCommitWalkRequest{Root: root})
			return err
		}},
		{"MergeBase", func() error {
			_, err := srv.ProxyMergeBase(ctx, &daemonpb.ProxyMergeBaseRequest{Root: root})
			return err
		}},
		{"MergeRequestList", func() error {
			_, err := srv.ProxyMergeRequestList(ctx, &daemonpb.ProxyMergeRequestListRequest{Root: root})
			return err
		}},
		{"MergeRequestCreate", func() error {
			_, err := srv.ProxyMergeRequestCreate(ctx, &daemonpb.ProxyMergeRequestCreateRequest{Root: root})
			return err
		}},
		{"MergeRequestMerge", func() error {
			_, err := srv.ProxyMergeRequestMerge(ctx, &daemonpb.ProxyMergeRequestMergeRequest{Root: root})
			return err
		}},
		{"MergeRequestClose", func() error {
			_, err := srv.ProxyMergeRequestClose(ctx, &daemonpb.ProxyMergeRequestCloseRequest{Root: root})
			return err
		}},
		{"MergeRequestReviews", func() error {
			_, err := srv.ProxyMergeRequestReviews(ctx, &daemonpb.ProxyMergeRequestReviewsRequest{Root: root})
			return err
		}},
		{"MergeRequestReviewState", func() error {
			_, err := srv.ProxyMergeRequestReviewState(ctx, &daemonpb.ProxyMergeRequestReviewStateRequest{Root: root})
			return err
		}},
		{"MergeRequestThreads", func() error {
			_, err := srv.ProxyMergeRequestThreads(ctx, &daemonpb.ProxyMergeRequestThreadsRequest{Root: root})
			return err
		}},
		{"LockFile", func() error {
			_, err := srv.ProxyLockFile(ctx, &daemonpb.ProxyLockFileRequest{Root: root})
			return err
		}},
		{"UnlockFile", func() error {
			_, err := srv.ProxyUnlockFile(ctx, &daemonpb.ProxyUnlockFileRequest{Root: root})
			return err
		}},
		{"ListFileLocks", func() error {
			_, err := srv.ProxyListFileLocks(ctx, &daemonpb.ProxyListFileLocksRequest{Root: root})
			return err
		}},
	}
}

func TestServer_ProxyNilRequestsForEveryHandler(t *testing.T) {
	srv, root := watchedProxyServer(t, &fakeProxyConn{client: emptyService{}})

	for _, tt := range proxyCalls(srv, context.Background(), root) {
		t.Run(tt.name, func(t *testing.T) {
			require.NoError(t, tt.call(), "a nil wrapped request must be defaulted")
		})
	}
}

func TestServer_ProxyEveryHandlerMapsErrors(t *testing.T) {
	srv, root := watchedProxyServer(t, &fakeProxyConn{err: errors.New("no route to host")})

	for _, tt := range proxyCalls(srv, context.Background(), root) {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call()
			require.Equal(t, codes.Unknown, status.Code(err))
			require.Contains(t, status.Convert(err).Message(), "no route to host")
		})
	}
}

func watchedProxyServer(t *testing.T, conn ProxyConn) (*Server, string) {
	t.Helper()
	root := newTestClone(t)
	srv := newOpServer(t, RepoOps{Proxy: conn})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)
	return srv, root
}

func TestServer_ProxyFillsProjectContext(t *testing.T) {
	var got *pb.GetListBranchRequest
	conn := &fakeProxyConn{client: &fakeService{
		listBranches: func(_ context.Context, req *pb.GetListBranchRequest) (*pb.GetListBranchResponse, error) {
			got = req
			return &pb.GetListBranchResponse{Branches: []*pb.Branch{{Name: "main", IsDefault: true}}}, nil
		},
	}}
	srv, root := watchedProxyServer(t, conn)

	res, err := srv.ProxyBranchList(context.Background(), &daemonpb.ProxyBranchListRequest{Root: root})
	require.NoError(t, err)
	require.Equal(t, "default", got.GetContext().GetOrg())
	require.Equal(t, "default", got.GetContext().GetProject())
	require.Equal(t, "nipa.example.com", conn.host, "the proxy connects to the clone's server host")
	require.Equal(t, 1, conn.connects)
	require.Equal(t, "main", res.GetResponse().GetBranches()[0].GetName())
}

func TestServer_ProxyOverwritesCallerContext(t *testing.T) {
	var got *pb.GetTreeManifestRequest
	conn := &fakeProxyConn{client: &fakeService{
		treeManifest: func(_ context.Context, req *pb.GetTreeManifestRequest) (*pb.GetTreeManifestResponse, error) {
			got = req
			return &pb.GetTreeManifestResponse{}, nil
		},
	}}
	srv, root := watchedProxyServer(t, conn)

	_, err := srv.ProxyTreeManifest(context.Background(), &daemonpb.ProxyTreeManifestRequest{
		Root: root,
		Request: &pb.GetTreeManifestRequest{
			Context:   &pb.ProjectContext{Org: "evil", Project: "evil"},
			Branch:    "main",
			Recursive: true,
		},
	})
	require.NoError(t, err)
	require.Equal(t, "default", got.GetContext().GetOrg(), "the clone config wins over caller-supplied context")
	require.Equal(t, "main", got.GetBranch())
	require.True(t, got.GetRecursive())
}

func TestServer_ProxyNilRequestIsFilled(t *testing.T) {
	var got *pb.LockFileRequest
	conn := &fakeProxyConn{client: &fakeService{
		lockFile: func(_ context.Context, req *pb.LockFileRequest) (*pb.LockFileResponse, error) {
			got = req
			return &pb.LockFileResponse{Lock: &pb.FileLockDetail{Path: req.GetPath()}}, nil
		},
	}}
	srv, root := watchedProxyServer(t, conn)

	res, err := srv.ProxyLockFile(context.Background(), &daemonpb.ProxyLockFileRequest{Root: root})
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, "default", got.GetContext().GetOrg())
	require.Empty(t, res.GetResponse().GetLock().GetPath())
}

func TestServer_ProxyPassesMRAndLockFields(t *testing.T) {
	var gotMR *pb.CreateMergeRequestRequest
	var gotLock *pb.LockFileRequest
	conn := &fakeProxyConn{client: &fakeService{
		createMR: func(_ context.Context, req *pb.CreateMergeRequestRequest) (*pb.CreateMergeRequestResponse, error) {
			gotMR = req
			return &pb.CreateMergeRequestResponse{MergeRequest: &pb.MergeRequestDetail{Number: 7}}, nil
		},
		lockFile: func(_ context.Context, req *pb.LockFileRequest) (*pb.LockFileResponse, error) {
			gotLock = req
			return &pb.LockFileResponse{Lock: &pb.FileLockDetail{Path: req.GetPath(), Global: true}}, nil
		},
	}}
	srv, root := watchedProxyServer(t, conn)

	mrRes, err := srv.ProxyMergeRequestCreate(context.Background(), &daemonpb.ProxyMergeRequestCreateRequest{
		Root: root,
		Request: &pb.CreateMergeRequestRequest{
			Title:        "Feature",
			SourceBranch: "feature",
			TargetBranch: "main",
		},
	})
	require.NoError(t, err)
	require.Equal(t, "feature", gotMR.GetSourceBranch())
	require.Equal(t, "main", gotMR.GetTargetBranch())
	require.EqualValues(t, 7, mrRes.GetResponse().GetMergeRequest().GetNumber())

	lockRes, err := srv.ProxyLockFile(context.Background(), &daemonpb.ProxyLockFileRequest{
		Root:    root,
		Request: &pb.LockFileRequest{Path: "assets/logo.png"},
	})
	require.NoError(t, err)
	require.Equal(t, "assets/logo.png", gotLock.GetPath())
	require.True(t, lockRes.GetResponse().GetLock().GetGlobal())
}

func TestServer_ProxyServerErrorPassesThrough(t *testing.T) {
	conn := &fakeProxyConn{client: &fakeService{
		listBranches: func(context.Context, *pb.GetListBranchRequest) (*pb.GetListBranchResponse, error) {
			return nil, status.Error(codes.NotFound, "branch not found")
		},
	}}
	srv, root := watchedProxyServer(t, conn)

	_, err := srv.ProxyBranchList(context.Background(), &daemonpb.ProxyBranchListRequest{Root: root})
	require.Equal(t, codes.NotFound, status.Code(err))
	require.Contains(t, status.Convert(err).Message(), "branch not found")
}

func TestServer_ProxyConnectError(t *testing.T) {
	srv, root := watchedProxyServer(t, &fakeProxyConn{err: errors.New("no route to host")})

	_, err := srv.ProxyBranchList(context.Background(), &daemonpb.ProxyBranchListRequest{Root: root})
	require.Equal(t, codes.Unknown, status.Code(err))
	require.Contains(t, status.Convert(err).Message(), "no route to host")
}

func TestServer_ProxyUnwatchedRoot(t *testing.T) {
	srv := newOpServer(t, RepoOps{Proxy: &fakeProxyConn{client: &fakeService{}}})

	_, err := srv.ProxyBranchList(context.Background(), &daemonpb.ProxyBranchListRequest{Root: newTestClone(t)})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Contains(t, status.Convert(err).Message(), "not watched")
}

func TestServer_ProxyWithoutConn(t *testing.T) {
	root := newTestClone(t)
	srv := newOpServer(t, RepoOps{})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)

	_, err = srv.ProxyBranchList(context.Background(), &daemonpb.ProxyBranchListRequest{Root: root})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	require.Contains(t, status.Convert(err).Message(), "not configured")
}

func TestRepo_CloseClosesProxy(t *testing.T) {
	conn := &fakeProxyConn{client: &fakeService{}}
	rp, err := openRepo(newTestClone(t), Runners{New: func(string) RepoOps {
		return RepoOps{Proxy: conn}
	}})
	require.NoError(t, err)
	require.NoError(t, rp.close())
	require.True(t, conn.closed)
}
