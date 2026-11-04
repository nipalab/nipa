package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/grpc/pb"
)

type historyDaemon struct {
	*opsDaemon
	err        error
	logReqs    []*pb.GetCommitLogRequest
	log        []*pb.CommitLogEntry
	diffReq    *daemonpb.DiffRequest
	streamErr  error
	branches   [][]*pb.Branch
	branchReqs []*pb.GetListBranchRequest
	created    *pb.CreateBranchRequest
	deleted    string
	tags       [][]*pb.Tag
	tagReqs    []*pb.ListTagsRequest
	tagCreated *pb.CreateTagRequest
	tagDeleted string
	switched   *daemonpb.SwitchRequest
	reverted   *daemonpb.RevertOpRequest
}

func (d *historyDaemon) ProxyCommitLog(_ context.Context, req *daemonpb.ProxyCommitLogRequest, _ ...grpc.CallOption) (*daemonpb.ProxyCommitLogResponse, error) {
	d.logReqs = append(d.logReqs, req.GetRequest())
	return &daemonpb.ProxyCommitLogResponse{Response: &pb.GetCommitLogResponse{Commits: d.log}}, d.err
}

func (d *historyDaemon) DiffFiles(ctx context.Context, req *daemonpb.DiffRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[daemonpb.DiffFileEvent], error) {
	d.diffReq = req
	if d.streamErr != nil {
		return &listStream[daemonpb.DiffFileEvent]{err: d.streamErr}, nil
	}
	return d.opsDaemon.DiffFiles(ctx, req, opts...)
}

func (d *historyDaemon) ProxyBranchList(_ context.Context, req *daemonpb.ProxyBranchListRequest, _ ...grpc.CallOption) (*daemonpb.ProxyBranchListResponse, error) {
	d.branchReqs = append(d.branchReqs, req.GetRequest())
	page := []*pb.Branch{}
	if len(d.branches) > 0 {
		page, d.branches = d.branches[0], d.branches[1:]
	}
	return &daemonpb.ProxyBranchListResponse{Response: &pb.GetListBranchResponse{Branches: page}}, d.err
}

func (d *historyDaemon) ProxyBranchCreate(_ context.Context, req *daemonpb.ProxyBranchCreateRequest, _ ...grpc.CallOption) (*daemonpb.ProxyBranchCreateResponse, error) {
	d.created = req.GetRequest()
	return &daemonpb.ProxyBranchCreateResponse{Response: &pb.CreateBranchResponse{Branch: &pb.Branch{Name: req.GetRequest().GetName(), CommitId: ptr("c9")}}}, d.err
}

func (d *historyDaemon) ProxyBranchDelete(_ context.Context, req *daemonpb.ProxyBranchDeleteRequest, _ ...grpc.CallOption) (*daemonpb.ProxyBranchDeleteResponse, error) {
	d.deleted = req.GetRequest().GetName()
	return &daemonpb.ProxyBranchDeleteResponse{}, d.err
}

func (d *historyDaemon) ProxyTagList(_ context.Context, req *daemonpb.ProxyTagListRequest, _ ...grpc.CallOption) (*daemonpb.ProxyTagListResponse, error) {
	d.tagReqs = append(d.tagReqs, req.GetRequest())
	page := []*pb.Tag{}
	if len(d.tags) > 0 {
		page, d.tags = d.tags[0], d.tags[1:]
	}
	return &daemonpb.ProxyTagListResponse{Response: &pb.ListTagsResponse{Tags: page}}, d.err
}

func (d *historyDaemon) ProxyTagCreate(_ context.Context, req *daemonpb.ProxyTagCreateRequest, _ ...grpc.CallOption) (*daemonpb.ProxyTagCreateResponse, error) {
	d.tagCreated = req.GetRequest()
	r := req.GetRequest()
	return &daemonpb.ProxyTagCreateResponse{Response: &pb.CreateTagResponse{Tag: &pb.Tag{Name: r.GetName(), CommitId: r.GetFromCommitId(), Message: r.GetMessage()}}}, d.err
}

func (d *historyDaemon) ProxyTagDelete(_ context.Context, req *daemonpb.ProxyTagDeleteRequest, _ ...grpc.CallOption) (*daemonpb.ProxyTagDeleteResponse, error) {
	d.tagDeleted = req.GetRequest().GetName()
	return &daemonpb.ProxyTagDeleteResponse{}, d.err
}

func (d *historyDaemon) Switch(_ context.Context, req *daemonpb.SwitchRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[daemonpb.OpEvent], error) {
	d.switched = req
	return d.opStream()
}

func (d *historyDaemon) Revert(_ context.Context, req *daemonpb.RevertOpRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[daemonpb.OpEvent], error) {
	d.reverted = req
	return d.opStream()
}

func newHistory(t *testing.T) (*Service, *historyDaemon) {
	t.Helper()
	d := &historyDaemon{opsDaemon: &opsDaemon{fakeDaemon: &fakeDaemon{watchErr: map[string]error{}, changes: make(chan *daemonpb.RepoChange, 4)}}}
	svc := New(func(context.Context) (Session, error) { return &opsSession{client: d}, nil }, &memStore{}, nil)
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	return svc, d
}

func logEntry(id string, parents ...string) *pb.CommitLogEntry {
	e := &pb.CommitLogEntry{CommitId: id, CommitHash: "h" + id, AuthorName: "Ana", AuthorEmail: "ana@example.com", Message: "msg " + id, CreatedAt: timestamppb.New(time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC))}
	if len(parents) > 0 {
		e.Parent_1Id = &parents[0]
	}
	if len(parents) > 1 {
		e.Parent_2Id = &parents[1]
	}
	return e
}

func TestService_HistoryPages(t *testing.T) {
	svc, d := newHistory(t)
	d.log = []*pb.CommitLogEntry{logEntry("c3", "c2", "f1"), logEntry("c2", "c1"), logEntry("c1")}

	page, err := svc.History(ctx, "/work/game", HistoryQuery{Branch: "main", Path: "/art/hero.png/", Limit: 2})
	require.NoError(t, err)
	assert.Equal(t, "c1", page.Next)
	require.Len(t, page.Commits, 2)
	assert.Equal(t, Commit{ID: "c3", Hash: "hc3", Parents: []string{"c2", "f1"}, Author: "Ana", Email: "ana@example.com", Message: "msg c3", Date: "2026-11-04T09:00:00Z"}, page.Commits[0])
	assert.Equal(t, int32(3), d.logReqs[0].GetLimit())
	assert.Equal(t, "art/hero.png", d.logReqs[0].GetPath())
	assert.Nil(t, d.logReqs[0].StartCommitId)

	page, err = svc.History(ctx, "/work/game", HistoryQuery{Start: "c1", Limit: 500})
	require.NoError(t, err)
	assert.Empty(t, page.Next)
	assert.Len(t, page.Commits, 3)
	assert.Equal(t, "c1", d.logReqs[1].GetStartCommitId())
	assert.Equal(t, int32(maxHistoryPage+1), d.logReqs[1].GetLimit())

	_, err = svc.History(ctx, "/work/game", HistoryQuery{})
	require.NoError(t, err)
	assert.Equal(t, int32(defaultHistoryPage+1), d.logReqs[2].GetLimit())

	d.err = status.Error(codes.NotFound, "branch not found")
	_, err = svc.History(ctx, "/work/game", HistoryQuery{})
	require.EqualError(t, err, "branch not found")
}

func TestService_CommitFiles(t *testing.T) {
	svc, d := newHistory(t)
	d.diff = []*daemonpb.DiffFileEvent{{Event: &daemonpb.DiffFileEvent_File{File: &daemonpb.DiffFile{Path: "art/hero.png", Status: "modified", Kind: "image", Old: &daemonpb.DiffFileSide{Path: "art/hero.png"}, New: &daemonpb.DiffFileSide{Path: "art/hero.png"}}}}}

	files, err := svc.CommitFiles(ctx, "/work/game", "c2", "c1")
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.Equal(t, "c1", files[0].OldRevision)
	assert.Equal(t, "c2", files[0].NewRevision)
	assert.Equal(t, "image", files[0].Kind)
	assert.True(t, d.diffReq.GetShow())
	assert.Equal(t, []string{"c2"}, d.diffReq.GetRevisions())

	_, err = svc.CommitFiles(ctx, "/work/game", "", "")
	require.EqualError(t, err, "choose a commit")

	d.diff = []*daemonpb.DiffFileEvent{{Event: &daemonpb.DiffFileEvent_Failure{Failure: &daemonpb.OpFailure{Message: "revision not found"}}}}
	_, err = svc.CommitFiles(ctx, "/work/game", "c2", "c1")
	require.EqualError(t, err, "revision not found")

	d.diffErr = status.Error(codes.Unavailable, "gone")
	_, err = svc.CommitFiles(ctx, "/work/game", "c2", "c1")
	require.Error(t, err)
}

func TestService_CommitFilesStreamError(t *testing.T) {
	svc, d := newHistory(t)
	d.streamErr = status.Error(codes.Internal, "broken")
	_, err := svc.CommitFiles(ctx, "/work/game", "c2", "c1")
	require.EqualError(t, err, "broken")
}

func TestService_Branches(t *testing.T) {
	svc, d := newHistory(t)
	full := make([]*pb.Branch, listPage)
	for i := range full {
		full[i] = &pb.Branch{Id: "b", Name: "feature-" + string(rune('a'+i%26)), UpdatedAt: timestamppb.Now()}
	}
	full[listPage-1] = &pb.Branch{Id: "last", Name: "Zeta", UpdatedAt: timestamppb.Now()}
	d.branches = [][]*pb.Branch{full, {{Name: "main", IsDefault: true, IsProtected: true, CommitId: ptr("c3")}}}

	list, err := svc.Branches(ctx, "/work/game")
	require.NoError(t, err)
	require.Len(t, list, listPage+1)
	assert.Equal(t, Branch{Name: "main", CommitID: "c3", Default: true, Protected: true}, list[0])
	assert.Equal(t, "Zeta", list[len(list)-1].Name)
	require.Len(t, d.branchReqs, 2)
	assert.Equal(t, "last", d.branchReqs[1].GetLastId())

	d.err = errors.New("boom")
	_, err = svc.Branches(ctx, "/work/game")
	require.Error(t, err)
}

func TestService_CreateAndDeleteBranch(t *testing.T) {
	svc, d := newHistory(t)
	b, err := svc.CreateBranch(ctx, "/work/game", " fix-hero ", "", "c2")
	require.NoError(t, err)
	assert.Equal(t, &Branch{Name: "fix-hero", CommitID: "c9"}, b)
	assert.Equal(t, "c2", d.created.GetFromCommitId())

	_, err = svc.CreateBranch(ctx, "/work/game", "  ", "", "")
	require.EqualError(t, err, "enter a branch name")

	require.NoError(t, svc.DeleteBranch(ctx, "/work/game", "fix-hero"))
	assert.Equal(t, "fix-hero", d.deleted)

	_, _ = svc.CreateBranch(ctx, "/work/game", "from-main", "main", "")
	assert.Equal(t, "main", d.created.GetFromBranch())

	d.err = status.Error(codes.AlreadyExists, "branch exists")
	_, err = svc.CreateBranch(ctx, "/work/game", "fix-hero", "main", "")
	require.EqualError(t, err, "branch exists")
}

func TestService_Tags(t *testing.T) {
	svc, d := newHistory(t)
	at := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	full := make([]*pb.Tag, listPage)
	for i := range full {
		full[i] = &pb.Tag{Id: "t", Name: "v0", CreatedAt: timestamppb.New(at)}
	}
	full[listPage-1] = &pb.Tag{Id: "last", Name: "v0.1", CreatedAt: timestamppb.New(at)}
	d.tags = [][]*pb.Tag{full, {{Name: "v1.0", CommitId: "c3", Message: "ship", UserId: "u1", CreatedAt: timestamppb.New(at)}}}

	list, err := svc.Tags(ctx, "/work/game")
	require.NoError(t, err)
	require.Len(t, list, listPage+1)
	assert.Equal(t, Tag{Name: "v1.0", CommitID: "c3", Message: "ship", CreatedAt: "2026-11-01T12:00:00Z", CreatedBy: "u1"}, list[listPage])
	assert.Equal(t, "last", d.tagReqs[1].GetLastId())

	d.err = errors.New("boom")
	_, err = svc.Tags(ctx, "/work/game")
	require.Error(t, err)
}

func TestService_CreateAndDeleteTag(t *testing.T) {
	svc, d := newHistory(t)
	tag, err := svc.CreateTag(ctx, "/work/game", " v1.1 ", "", "c3", " milestone ")
	require.NoError(t, err)
	assert.Equal(t, &Tag{Name: "v1.1", CommitID: "c3", Message: "milestone"}, tag)
	assert.Equal(t, "c3", d.tagCreated.GetFromCommitId())

	_, err = svc.CreateTag(ctx, "/work/game", "", "", "c3", "")
	require.EqualError(t, err, "enter a tag name")

	require.NoError(t, svc.DeleteTag(ctx, "/work/game", "v1.1"))
	assert.Equal(t, "v1.1", d.tagDeleted)

	_, _ = svc.CreateTag(ctx, "/work/game", "v1.2", "feature", "", "")
	assert.Equal(t, "feature", d.tagCreated.GetFromBranch())

	d.err = status.Error(codes.AlreadyExists, "tag exists")
	_, err = svc.CreateTag(ctx, "/work/game", "v1.1", "", "", "")
	require.EqualError(t, err, "tag exists")
}

func switchResult(branch, commit string) []*daemonpb.OpEvent {
	return []*daemonpb.OpEvent{{Event: &daemonpb.OpEvent_Result{Result: &daemonpb.OpResult{Outcome: &daemonpb.OpResult_Sync{Sync: &daemonpb.SyncResult{Branch: branch, CommitId: commit}}}}}}
}

func TestService_SwitchAndCheckoutTag(t *testing.T) {
	svc, d := newHistory(t)
	d.op = switchResult("feature", "c5")
	out, err := svc.SwitchBranch(ctx, "/work/game", "feature")
	require.NoError(t, err)
	assert.Equal(t, OpOutcome{Branch: "feature", CommitID: "c5"}, out)
	assert.Equal(t, "feature", d.switched.GetBranch())

	d.op = switchResult("main", "c3")
	_, err = svc.CheckoutTag(ctx, "/work/game", "v1.0")
	require.NoError(t, err)
	assert.Equal(t, "v1.0", d.switched.GetTag())
	assert.Empty(t, d.switched.GetBranch())
}

func TestService_Revert(t *testing.T) {
	svc, d := newHistory(t)
	d.op = []*daemonpb.OpEvent{{Event: &daemonpb.OpEvent_Result{Result: &daemonpb.OpResult{Outcome: &daemonpb.OpResult_Revert{Revert: &daemonpb.RevertResult{Conflicts: []string{"a.txt"}}}}}}}
	out, err := svc.Revert(ctx, "/work/game", RevertRequest{Target: "c2"})
	require.NoError(t, err)
	assert.Equal(t, &RevertOutcome{Conflicts: []string{"a.txt"}}, out.Revert)
	assert.Equal(t, "c2", d.reverted.GetTarget())

	for mode, check := range map[string]func(*daemonpb.RevertOpRequest) bool{
		"continue": (*daemonpb.RevertOpRequest).GetContinue,
		"skip":     (*daemonpb.RevertOpRequest).GetSkip,
		"abort":    (*daemonpb.RevertOpRequest).GetAbort,
	} {
		d.op = []*daemonpb.OpEvent{{Event: &daemonpb.OpEvent_Result{Result: &daemonpb.OpResult{Outcome: &daemonpb.OpResult_Revert{Revert: &daemonpb.RevertResult{Committed: true}}}}}}
		out, err := svc.Revert(ctx, "/work/game", RevertRequest{Mode: mode})
		require.NoError(t, err, mode)
		assert.True(t, check(d.reverted), mode)
		assert.True(t, out.Revert.Committed, mode)
	}

	_, err = svc.Revert(ctx, "/work/game", RevertRequest{})
	require.EqualError(t, err, "choose a commit to revert")
	_, err = svc.Revert(ctx, "/work/game", RevertRequest{Mode: "redo"})
	require.EqualError(t, err, "unknown revert mode redo")
}

func TestService_HistoryWithoutDaemon(t *testing.T) {
	svc := New(func(context.Context) (Session, error) { return nil, errors.New("daemon down") }, &memStore{}, nil)
	_, err := svc.History(ctx, "/work/game", HistoryQuery{})
	require.Error(t, err)
	_, err = svc.CommitFiles(ctx, "/work/game", "c1", "")
	require.Error(t, err)
}
