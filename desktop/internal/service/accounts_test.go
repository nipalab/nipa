package service

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/grpc/pb"
)

type accountDaemon struct {
	*opsDaemon
	signedIn bool
	address  string
	err      error
	login    *daemonpb.LoginRequest
	loginRes *daemonpb.LoginResponse
	ssh      *daemonpb.LoginSSHRequest
	clone    *daemonpb.CloneRequest
	sparse   *daemonpb.SetSparseRequest
	treeReq  *daemonpb.RemoteTreeRequest
	cloneOps []*daemonpb.OpEvent
	cloneErr error
}

func (d *accountDaemon) AccountStatus(_ context.Context, req *daemonpb.AccountStatusRequest, _ ...grpc.CallOption) (*daemonpb.AccountStatusResponse, error) {
	return &daemonpb.AccountStatusResponse{Host: "studio.test", Address: d.address, SignedIn: d.signedIn, Email: "ann@studio.test", UserId: "u1"}, d.err
}

func (d *accountDaemon) Login(_ context.Context, req *daemonpb.LoginRequest, _ ...grpc.CallOption) (*daemonpb.LoginResponse, error) {
	d.login = req
	if d.loginRes != nil {
		return d.loginRes, d.err
	}
	return &daemonpb.LoginResponse{}, d.err
}

func (d *accountDaemon) LoginSSH(_ context.Context, req *daemonpb.LoginSSHRequest, _ ...grpc.CallOption) (*daemonpb.LoginResponse, error) {
	d.ssh = req
	return &daemonpb.LoginResponse{}, d.err
}

func (d *accountDaemon) ListProjects(context.Context, *daemonpb.ListProjectsRequest, ...grpc.CallOption) (*daemonpb.ListProjectsResponse, error) {
	return &daemonpb.ListProjectsResponse{Projects: []*pb.ProjectSummary{{Org: "studio", OrgName: "Studio", OrgRole: "owner", Project: "game", Name: "Game", Description: "d"}}}, d.err
}

func (d *accountDaemon) RemoteBranches(context.Context, *daemonpb.RemoteBranchesRequest, ...grpc.CallOption) (*daemonpb.RemoteBranchesResponse, error) {
	return &daemonpb.RemoteBranchesResponse{Branches: []*pb.Branch{{Name: "main", IsDefault: true, IsProtected: true}, {Name: "feature"}}}, d.err
}

func (d *accountDaemon) RemoteTree(_ context.Context, req *daemonpb.RemoteTreeRequest, _ ...grpc.CallOption) (*daemonpb.RemoteTreeResponse, error) {
	d.treeReq = req
	return &daemonpb.RemoteTreeResponse{Dirs: []*daemonpb.RemoteTreeDir{{Name: "art", Path: "art"}}, Files: 2, FileBytes: 30}, d.err
}

func (d *accountDaemon) Clone(_ context.Context, req *daemonpb.CloneRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[daemonpb.OpEvent], error) {
	d.clone = req
	if d.cloneErr != nil {
		return nil, d.cloneErr
	}
	return &listStream[daemonpb.OpEvent]{items: d.cloneOps}, nil
}

func (d *accountDaemon) SetSparse(_ context.Context, req *daemonpb.SetSparseRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[daemonpb.OpEvent], error) {
	d.sparse = req
	return &listStream[daemonpb.OpEvent]{items: d.cloneOps}, nil
}

func newAccounts(t *testing.T) (*Service, *accountDaemon, *recorder) {
	t.Helper()
	d := &accountDaemon{opsDaemon: &opsDaemon{fakeDaemon: &fakeDaemon{watchErr: map[string]error{}, changes: make(chan *daemonpb.RepoChange, 4)}}, address: "http://studio.test"}
	svc := New(func(context.Context) (Session, error) { return &opsSession{client: d}, nil }, &memStore{}, nil)
	rec := &recorder{}
	svc.Events = rec.emit
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	return svc, d, rec
}

func syncResult(branch string) *daemonpb.OpEvent {
	return &daemonpb.OpEvent{Event: &daemonpb.OpEvent_Result{Result: &daemonpb.OpResult{Outcome: &daemonpb.OpResult_Sync{Sync: &daemonpb.SyncResult{Branch: branch, CommitId: "c1"}}}}}
}

func TestService_Accounts(t *testing.T) {
	svc, d, _ := newAccounts(t)
	acc, err := svc.AccountStatus(ctx, "studio.test")
	require.NoError(t, err)
	assert.Equal(t, Account{Host: "studio.test", Address: "http://studio.test", Email: "ann@studio.test", UserID: "u1"}, *acc)

	res, err := svc.SignIn(ctx, "studio.test", " ann@studio.test ", "pw")
	require.NoError(t, err)
	assert.Equal(t, &SignInResult{}, res)
	assert.Equal(t, "ann@studio.test", d.login.GetUsername())
	assert.Equal(t, "studio.test", d.login.GetHost())
	require.NoError(t, svc.SignInSSH(ctx, "studio.test", ""))
	assert.Equal(t, "studio.test", d.ssh.GetServer())

	projects, err := svc.ListProjects(ctx, "studio.test")
	require.NoError(t, err)
	assert.Equal(t, []RemoteProject{{Org: "studio", OrgName: "Studio", OrgRole: "owner", Project: "game", Name: "Game", Description: "d"}}, projects)
	branches, err := svc.RemoteBranches(ctx, "studio.test", "studio", "game")
	require.NoError(t, err)
	assert.Equal(t, []RemoteBranch{{Name: "main", IsDefault: true, Protected: true}, {Name: "feature"}}, branches)
	tree, err := svc.RemoteTree(ctx, "studio.test", "studio", "game", "feature", "art")
	require.NoError(t, err)
	assert.Equal(t, &RemoteListing{Dirs: []RemoteDir{{Name: "art", Path: "art"}}, Files: 2, FileBytes: 30}, tree)
	assert.Equal(t, "feature", d.treeReq.GetBranch())

	d.err = status.Error(codes.Unauthenticated, "unable to get access token")
	_, err = svc.AccountStatus(ctx, "x")
	assert.EqualError(t, err, "unable to get access token")
	_, err = svc.SignIn(ctx, "x", "a", "b")
	assert.Error(t, err)
	assert.Error(t, svc.SignInMFA(ctx, "x", "a", "ch", "1"))
	assert.Error(t, svc.SignInSSH(ctx, "x", ""))
	_, err = svc.ListProjects(ctx, "x")
	assert.Error(t, err)
	_, err = svc.RemoteBranches(ctx, "x", "o", "p")
	assert.Error(t, err)
	_, err = svc.RemoteTree(ctx, "x", "o", "p", "", "")
	assert.Error(t, err)
}

func TestService_SignInMFA(t *testing.T) {
	svc, d, _ := newAccounts(t)
	d.loginRes = &daemonpb.LoginResponse{MfaRequired: true, MfaChallenge: "ch-1", ExpiresIn: 300}

	res, err := svc.SignIn(ctx, "studio.test", "ann@studio.test", "pw")
	require.NoError(t, err)
	assert.Equal(t, &SignInResult{MFARequired: true, Challenge: "ch-1", ExpiresIn: 300}, res)

	d.loginRes = nil
	require.NoError(t, svc.SignInMFA(ctx, "studio.test", " ann@studio.test ", "ch-1", " 123456 "))
	assert.Equal(t, "studio.test", d.login.GetHost())
	assert.Equal(t, "ann@studio.test", d.login.GetUsername())
	assert.Equal(t, "ch-1", d.login.GetMfaChallenge())
	assert.Equal(t, "123456", d.login.GetOtp())
	assert.Empty(t, d.login.GetPassword())

	d.err = status.Error(codes.InvalidArgument, "invalid code")
	assert.EqualError(t, svc.SignInMFA(ctx, "studio.test", "ann@studio.test", "ch-1", "000000"), "invalid code")
}

func TestService_AccountsNeedTheDaemon(t *testing.T) {
	svc := New(func(context.Context) (Session, error) { return nil, errors.New("no daemon") }, &memStore{}, nil)
	_, err := svc.AccountStatus(ctx, "x")
	assert.Error(t, err)
	_, err = svc.SignIn(ctx, "x", "a", "b")
	assert.Error(t, err)
	assert.Error(t, svc.SignInMFA(ctx, "x", "a", "ch", "1"))
	assert.Error(t, svc.SignInSSH(ctx, "x", ""))
	_, err = svc.ListProjects(ctx, "x")
	assert.Error(t, err)
	_, err = svc.RemoteBranches(ctx, "x", "o", "p")
	assert.Error(t, err)
	_, err = svc.RemoteTree(ctx, "x", "o", "p", "", "")
	assert.Error(t, err)
	_, err = svc.Clone(ctx, CloneRequest{Server: "x", Target: "/tmp/x"})
	assert.Error(t, err)
}

func TestService_Clone(t *testing.T) {
	svc, d, rec := newAccounts(t)
	target := filepath.Join(t.TempDir(), "game")
	d.cloneOps = []*daemonpb.OpEvent{
		{Event: &daemonpb.OpEvent_Started{Started: &daemonpb.OpStarted{Phase: "clone"}}},
		{Event: &daemonpb.OpEvent_Progress{Progress: &daemonpb.OpProgress{Phase: "download", ObjectsDone: 1, ObjectsTotal: 2}}},
		syncResult("main"),
	}
	repo, err := svc.Clone(ctx, CloneRequest{Server: "studio.test", Org: "studio", Project: "game", Branch: "main", Sparse: []string{"art"}, Target: target + "/"})
	require.NoError(t, err)
	assert.Equal(t, target, repo.Root)
	assert.Equal(t, "http://studio.test/studio/game", d.clone.GetUrl())
	assert.Equal(t, []string{"art"}, d.clone.GetSparse())
	assert.Equal(t, target, d.clone.GetTarget())
	events := rec.snapshot()
	require.Len(t, events, 2)
	assert.Equal(t, OpProgress{Root: target, Op: "clone", Phase: "download", ObjectsDone: 1, ObjectsTotal: 2}, events[1])

	d.address = "studio.test:6799"
	_, err = svc.Clone(ctx, CloneRequest{Server: "studio.test:6799", Org: "o", Project: "p", Target: target})
	require.NoError(t, err)
	assert.Equal(t, "http://studio.test:6799/o/p", d.clone.GetUrl())

	_, err = svc.Clone(ctx, CloneRequest{Server: "s", Target: "relative"})
	assert.EqualError(t, err, "choose where the clone goes")
	_, err = svc.Clone(ctx, CloneRequest{Server: "s", Target: " "})
	assert.Error(t, err)

	d.cloneOps = []*daemonpb.OpEvent{{Event: &daemonpb.OpEvent_Failure{Failure: &daemonpb.OpFailure{Code: 400, Message: "target directory is not empty"}}}}
	_, err = svc.Clone(ctx, CloneRequest{Server: "s", Target: target})
	assert.EqualError(t, err, "target directory is not empty")

	d.cloneOps = []*daemonpb.OpEvent{syncResult("main")}
	d.watchErr[target] = status.Error(codes.InvalidArgument, "not a clone")
	_, err = svc.Clone(ctx, CloneRequest{Server: "s", Target: target})
	assert.EqualError(t, err, "not a clone")

	d.err = status.Error(codes.Internal, "boom")
	_, err = svc.Clone(ctx, CloneRequest{Server: "s", Target: target})
	assert.EqualError(t, err, "boom")
}

func TestService_SetSparseAndFolders(t *testing.T) {
	svc, d, _ := newAccounts(t)
	d.cloneOps = []*daemonpb.OpEvent{syncResult("main")}
	out, err := svc.SetSparse(ctx, "/work/game", []string{"art"})
	require.NoError(t, err)
	assert.Equal(t, "main", out.Branch)
	assert.Equal(t, []string{"art"}, d.sparse.GetPaths())

	_, err = svc.ChooseFolder(ctx, "Where?")
	assert.EqualError(t, err, "folder dialogs are not available")
	svc.ChooseFolderFunc = func(title string) (string, error) { return "/picked/" + title, nil }
	dir, err := svc.ChooseFolder(ctx, "x")
	require.NoError(t, err)
	assert.Equal(t, "/picked/x", dir)
}
