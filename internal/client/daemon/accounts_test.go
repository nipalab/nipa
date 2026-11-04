package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/grpc/pb"
)

type remoteService struct {
	pb.NipaServiceClient
	projects  []*pb.ProjectSummary
	branches  []*pb.Branch
	entries   []*pb.DirectoryEntry
	err       error
	dirReq    *pb.ListDirectoryRequest
	branchReq *pb.GetListBranchRequest
}

func (r *remoteService) ListMyProjects(context.Context, *pb.ListMyProjectsRequest, ...grpc.CallOption) (*pb.ListMyProjectsResponse, error) {
	return &pb.ListMyProjectsResponse{Projects: r.projects}, r.err
}

func (r *remoteService) GetListBranch(_ context.Context, req *pb.GetListBranchRequest, _ ...grpc.CallOption) (*pb.GetListBranchResponse, error) {
	r.branchReq = req
	return &pb.GetListBranchResponse{Branches: r.branches}, r.err
}

func (r *remoteService) ListDirectory(_ context.Context, req *pb.ListDirectoryRequest, _ ...grpc.CallOption) (*pb.ListDirectoryResponse, error) {
	r.dirReq = req
	if r.err != nil {
		return nil, r.err
	}
	return &pb.ListDirectoryResponse{Entries: r.entries}, nil
}

type remoteConn struct {
	svc    pb.NipaServiceClient
	err    error
	closed int
}

func (c *remoteConn) ServiceClient() (pb.NipaServiceClient, error) { return c.svc, c.err }
func (c *remoteConn) Close() error                                 { c.closed++; return nil }

func accountServer(t *testing.T, accounts Accounts, ops RepoOps) *Server {
	t.Helper()
	srv, err := NewServer(Options{
		EndpointPath: filepath.Join(t.TempDir(), "daemon.json"),
		Runners:      Runners{New: func(string) RepoOps { return ops }},
		Accounts:     accounts,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		srv.Stop()
		srv.repos.closeAll()
		_ = srv.listener.Close()
	})
	return srv
}

func remoteAccounts(svc pb.NipaServiceClient, dialed *[]string) (Accounts, *remoteConn) {
	conn := &remoteConn{svc: svc}
	return Accounts{Connect: func(_ context.Context, address string) (RemoteConn, error) {
		*dialed = append(*dialed, address)
		return conn, nil
	}}, conn
}

func TestServer_AccountStatus(t *testing.T) {
	ctx := context.Background()
	srv := accountServer(t, Accounts{Session: func(host string) (Session, bool) {
		return Session{Email: "ann@studio.test", UserID: "u1"}, host == "nipa.studio.test"
	}}, RepoOps{})

	res, err := srv.AccountStatus(ctx, &daemonpb.AccountStatusRequest{Server: "https://nipa.studio.test/org/game"})
	require.NoError(t, err)
	require.Equal(t, "nipa.studio.test", res.GetHost())
	require.Equal(t, "https://nipa.studio.test", res.GetAddress())
	require.True(t, res.GetSignedIn())
	require.Equal(t, "ann@studio.test", res.GetEmail())
	require.Equal(t, "u1", res.GetUserId())

	res, err = srv.AccountStatus(ctx, &daemonpb.AccountStatusRequest{Server: "127.0.0.1:6799"})
	require.NoError(t, err)
	require.False(t, res.GetSignedIn())
	require.Equal(t, "127.0.0.1:6799", res.GetAddress())

	_, err = srv.AccountStatus(ctx, &daemonpb.AccountStatusRequest{Server: " "})
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	bare := accountServer(t, Accounts{}, RepoOps{})
	res, err = bare.AccountStatus(ctx, &daemonpb.AccountStatusRequest{Server: "host"})
	require.NoError(t, err)
	require.False(t, res.GetSignedIn())
}

func TestServer_LoginSSH(t *testing.T) {
	ctx := context.Background()
	_, err := accountServer(t, Accounts{}, RepoOps{}).LoginSSH(ctx, &daemonpb.LoginSSHRequest{Server: "host"})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))

	var got [2]string
	srv := accountServer(t, Accounts{LoginSSH: func(_ context.Context, address, email string) error {
		got = [2]string{address, email}
		if email == "bad@studio.test" {
			return clientDomain.NewUserError("invalid email or SSH key")
		}
		return nil
	}}, RepoOps{})
	_, err = srv.LoginSSH(ctx, &daemonpb.LoginSSHRequest{Server: "http://127.0.0.1:6799", Email: " ann@studio.test "})
	require.NoError(t, err)
	require.Equal(t, [2]string{"http://127.0.0.1:6799", "ann@studio.test"}, got)

	_, err = srv.LoginSSH(ctx, &daemonpb.LoginSSHRequest{Server: "h", Email: "bad@studio.test"})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = srv.LoginSSH(ctx, &daemonpb.LoginSSHRequest{Server: "ftp://h"})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestServer_RemoteBrowsing(t *testing.T) {
	ctx := context.Background()
	svc := &remoteService{
		projects: []*pb.ProjectSummary{{Org: "studio", Project: "game"}},
		branches: []*pb.Branch{{Name: "main", IsDefault: true}, {Name: "feature"}},
		entries: []*pb.DirectoryEntry{
			{Name: "textures", IsDir: true}, {Name: "readme.md", SizeBytes: 10}, {Name: "Audio", IsDir: true},
			{Name: "models", IsDir: true}, {Name: "logo.png", SizeBytes: 90},
		},
	}
	var dialed []string
	accounts, conn := remoteAccounts(svc, &dialed)
	srv := accountServer(t, accounts, RepoOps{})

	projects, err := srv.ListProjects(ctx, &daemonpb.ListProjectsRequest{Server: "https://nipa.studio.test"})
	require.NoError(t, err)
	require.Equal(t, "game", projects.GetProjects()[0].GetProject())
	require.Equal(t, []string{"https://nipa.studio.test"}, dialed)

	branches, err := srv.RemoteBranches(ctx, &daemonpb.RemoteBranchesRequest{Server: "h", Org: "studio", Project: "game"})
	require.NoError(t, err)
	require.Len(t, branches.GetBranches(), 2)
	require.Equal(t, "game", svc.branchReq.GetContext().GetProject())

	tree, err := srv.RemoteTree(ctx, &daemonpb.RemoteTreeRequest{Server: "h", Org: "studio", Project: "game", Path: "/art/"})
	require.NoError(t, err)
	require.Equal(t, "", svc.dirReq.GetRevision())
	require.Equal(t, "art", svc.dirReq.GetPath())
	require.Equal(t, "studio", svc.dirReq.GetContext().GetOrg())
	names := []string{}
	for _, d := range tree.GetDirs() {
		names = append(names, d.GetPath())
	}
	require.Equal(t, []string{"art/Audio", "art/models", "art/textures"}, names)
	require.EqualValues(t, 2, tree.GetFiles())
	require.EqualValues(t, 100, tree.GetFileBytes())

	_, err = srv.RemoteTree(ctx, &daemonpb.RemoteTreeRequest{Server: "h", Org: "studio", Project: "game", Branch: "feature"})
	require.NoError(t, err)
	require.Equal(t, "feature", svc.dirReq.GetRevision())
	require.Equal(t, "", svc.dirReq.GetPath())
	require.Equal(t, 4, conn.closed)

	svc.err = status.Error(codes.Unauthenticated, "unable to get access token")
	_, err = srv.ListProjects(ctx, &daemonpb.ListProjectsRequest{Server: "h"})
	require.Equal(t, codes.Unauthenticated, status.Code(err))
	_, err = srv.RemoteBranches(ctx, &daemonpb.RemoteBranchesRequest{Server: "h"})
	require.Equal(t, codes.Unauthenticated, status.Code(err))
	_, err = srv.RemoteTree(ctx, &daemonpb.RemoteTreeRequest{Server: "h", Branch: "x"})
	require.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestServer_RemoteConnectionFailures(t *testing.T) {
	ctx := context.Background()
	_, err := accountServer(t, Accounts{}, RepoOps{}).ListProjects(ctx, &daemonpb.ListProjectsRequest{Server: "h"})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))

	srv := accountServer(t, Accounts{Connect: func(context.Context, string) (RemoteConn, error) {
		return nil, status.Error(codes.Unavailable, "connection refused")
	}}, RepoOps{})
	_, err = srv.ListProjects(ctx, &daemonpb.ListProjectsRequest{Server: "h"})
	require.Equal(t, codes.Unavailable, status.Code(err))
	_, err = srv.ListProjects(ctx, &daemonpb.ListProjectsRequest{Server: ""})
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	conn := &remoteConn{err: errors.New("not connected")}
	srv = accountServer(t, Accounts{Connect: func(context.Context, string) (RemoteConn, error) { return conn, nil }}, RepoOps{})
	_, err = srv.RemoteBranches(ctx, &daemonpb.RemoteBranchesRequest{Server: "h"})
	require.Error(t, err)
	require.Equal(t, 1, conn.closed)
}

func fakeClone(t *testing.T) func(context.Context, string, string, []string, string, usecase.DownloadProgress) error {
	return func(_ context.Context, url, branch string, sparse []string, target string, progress usecase.DownloadProgress) error {
		if url == "https://nipa.studio.test/studio/broken" {
			return clientDomain.NewUserError("target directory is not empty")
		}
		progress.DownloadStart(1, 10)
		progress.DownloadEnd()
		lr := localrepo.NewLocalRepoWithTarget(target)
		defer func() { _ = lr.Close() }()
		require.NoError(t, lr.Init(target))
		require.NoError(t, lr.SaveConfig(clientDomain.Config{Url: url, Branch: branch, Sparse: sparse}))
		if branch != "nocommit" {
			require.NoError(t, lr.SaveCommit("c42", "h42"))
		}
		return nil
	}
}

func TestServer_Clone(t *testing.T) {
	srv := accountServer(t, Accounts{Clone: fakeClone(t)}, RepoOps{})
	target := filepath.Join(t.TempDir(), "game")

	stream := &opTestStream{ctx: context.Background()}
	require.NoError(t, srv.Clone(&daemonpb.CloneRequest{Url: "https://nipa.studio.test/studio/game", Branch: "main", Sparse: []string{"art"}, Target: target + "/"}, stream))
	events := stream.events()
	require.Equal(t, "clone", events[0].GetStarted().GetPhase())
	require.NotEmpty(t, progressEvents(events))
	sync := terminalEvent(t, events).GetResult().GetSync()
	require.Equal(t, "main", sync.GetBranch())
	require.Equal(t, "c42", sync.GetCommitId())

	stream = &opTestStream{ctx: context.Background()}
	require.NoError(t, srv.Clone(&daemonpb.CloneRequest{Url: "u", Branch: "nocommit", Target: filepath.Join(t.TempDir(), "x")}, stream))
	require.Equal(t, "", terminalEvent(t, stream.events()).GetResult().GetSync().GetCommitId())

	for _, req := range []*daemonpb.CloneRequest{
		{Url: "https://nipa.studio.test/studio/broken", Target: filepath.Join(t.TempDir(), "b")},
		{Url: "u", Target: "relative/dir"},
		{Url: "u", Target: " "},
	} {
		stream = &opTestStream{ctx: context.Background()}
		require.NoError(t, srv.Clone(req, stream))
		last := stream.events()[len(stream.events())-1]
		require.NotNil(t, last.GetFailure(), req.GetTarget())
	}

	empty := filepath.Join(t.TempDir(), "noconfig")
	require.NoError(t, os.MkdirAll(empty, 0o755))
	srv = accountServer(t, Accounts{Clone: func(context.Context, string, string, []string, string, usecase.DownloadProgress) error { return nil }}, RepoOps{})
	stream = &opTestStream{ctx: context.Background()}
	require.NoError(t, srv.Clone(&daemonpb.CloneRequest{Url: "u", Target: empty}, stream))
	require.NotNil(t, stream.events()[len(stream.events())-1].GetFailure())

	srv = accountServer(t, Accounts{}, RepoOps{})
	stream = &opTestStream{ctx: context.Background()}
	require.NoError(t, srv.Clone(&daemonpb.CloneRequest{Url: "u", Target: target}, stream))
	require.Contains(t, stream.events()[len(stream.events())-1].GetFailure().GetMessage(), "not configured")
}

func TestServer_SetSparse(t *testing.T) {
	root := newTestClone(t)
	var sparse []string
	updated := 0
	runner := &stubUpdateRunner{
		sparseFn: func(r string, paths []string) error {
			sparse = paths
			lr := localrepo.NewLocalRepoWithTarget(r)
			defer func() { _ = lr.Close() }()
			cfg, err := lr.LoadConfig()
			require.NoError(t, err)
			cfg.Sparse = paths
			return lr.SaveConfig(*cfg)
		},
		run: func(context.Context, string, ...usecase.DownloadProgress) error { updated++; return nil },
	}
	srv := newOpServer(t, RepoOps{Update: runner})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)

	stream := &opTestStream{ctx: context.Background()}
	require.NoError(t, srv.SetSparse(&daemonpb.SetSparseRequest{Root: root, Paths: []string{"art", "audio"}}, stream))
	require.Equal(t, "sparse", stream.events()[0].GetStarted().GetPhase())
	require.Equal(t, "main", terminalEvent(t, stream.events()).GetResult().GetSync().GetBranch())
	require.Equal(t, []string{"art", "audio"}, sparse)
	require.Equal(t, 1, updated)
	info, err := srv.WatchRepo(context.Background(), &daemonpb.WatchRepoRequest{Root: root})
	require.NoError(t, err)
	require.Equal(t, []string{"art", "audio"}, info.GetRepo().GetSparse())

	runner.sparseFn = func(string, []string) error { return clientDomain.NewUserError("invalid path") }
	stream = &opTestStream{ctx: context.Background()}
	require.NoError(t, srv.SetSparse(&daemonpb.SetSparseRequest{Root: root, Paths: []string{"../x"}}, stream))
	require.Contains(t, stream.events()[len(stream.events())-1].GetFailure().GetMessage(), "invalid path")
	require.Equal(t, 1, updated)

	runner.sparseFn = nil
	runner.run = func(context.Context, string, ...usecase.DownloadProgress) error { return errors.New("offline") }
	stream = &opTestStream{ctx: context.Background()}
	require.NoError(t, srv.SetSparse(&daemonpb.SetSparseRequest{Root: root}, stream))
	require.Contains(t, stream.events()[len(stream.events())-1].GetFailure().GetMessage(), "offline")

	bare := newOpServer(t, RepoOps{})
	_, err = bare.repos.watch(root)
	require.NoError(t, err)
	stream = &opTestStream{ctx: context.Background()}
	require.NoError(t, bare.SetSparse(&daemonpb.SetSparseRequest{Root: root}, stream))
	require.Contains(t, stream.events()[len(stream.events())-1].GetFailure().GetMessage(), "not configured")
}
