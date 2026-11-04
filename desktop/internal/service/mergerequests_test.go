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

type mrDaemon struct {
	*historyDaemon
	failAt   string
	listReq  *pb.ListMergeRequestsRequest
	list     *pb.ListMergeRequestsResponse
	get      *pb.GetMergeRequestResponse
	reviews  []*pb.MergeRequestReviewDetail
	threads  []*pb.MergeRequestThreadDetail
	timeline []*pb.MergeRequestTimelineItem
	checks   []*pb.MergeRequestCheckDetail
	commits  []*pb.CommitLogEntry
	created  *pb.CreateMergeRequestRequest
	updated  *pb.UpdateMergeRequestRequest
	merged   *pb.MergeMergeRequestRequest
	closed   int64
	reopened int64
	review   *pb.SubmitMergeRequestReviewRequest
	comment  *pb.AddMergeRequestCommentRequest
	reply    *pb.ReplyMergeRequestThreadRequest
	resolve  *pb.ResolveMergeRequestThreadRequest
}

func (d *mrDaemon) fail(name string) error {
	if d.failAt == name {
		return status.Error(codes.PermissionDenied, name+" denied")
	}
	return nil
}

func (d *mrDaemon) ProxyMergeRequestList(_ context.Context, req *daemonpb.ProxyMergeRequestListRequest, _ ...grpc.CallOption) (*daemonpb.ProxyMergeRequestListResponse, error) {
	d.listReq = req.GetRequest()
	return &daemonpb.ProxyMergeRequestListResponse{Response: d.list}, d.fail("list")
}

func (d *mrDaemon) ProxyMergeRequestGet(context.Context, *daemonpb.ProxyMergeRequestGetRequest, ...grpc.CallOption) (*daemonpb.ProxyMergeRequestGetResponse, error) {
	return &daemonpb.ProxyMergeRequestGetResponse{Response: d.get}, d.fail("get")
}

func (d *mrDaemon) ProxyMergeRequestReviews(context.Context, *daemonpb.ProxyMergeRequestReviewsRequest, ...grpc.CallOption) (*daemonpb.ProxyMergeRequestReviewsResponse, error) {
	return &daemonpb.ProxyMergeRequestReviewsResponse{Response: &pb.ListMergeRequestReviewsResponse{Reviews: d.reviews}}, d.fail("reviews")
}

func (d *mrDaemon) ProxyMergeRequestThreads(context.Context, *daemonpb.ProxyMergeRequestThreadsRequest, ...grpc.CallOption) (*daemonpb.ProxyMergeRequestThreadsResponse, error) {
	return &daemonpb.ProxyMergeRequestThreadsResponse{Response: &pb.ListMergeRequestThreadsResponse{Threads: d.threads}}, d.fail("threads")
}

func (d *mrDaemon) ProxyMergeRequestTimeline(context.Context, *daemonpb.ProxyMergeRequestTimelineRequest, ...grpc.CallOption) (*daemonpb.ProxyMergeRequestTimelineResponse, error) {
	return &daemonpb.ProxyMergeRequestTimelineResponse{Response: &pb.ListMergeRequestTimelineResponse{Items: d.timeline}}, d.fail("timeline")
}

func (d *mrDaemon) ProxyMergeRequestChecks(context.Context, *daemonpb.ProxyMergeRequestChecksRequest, ...grpc.CallOption) (*daemonpb.ProxyMergeRequestChecksResponse, error) {
	return &daemonpb.ProxyMergeRequestChecksResponse{Response: &pb.ListMergeRequestChecksResponse{Checks: d.checks}}, d.fail("checks")
}

func (d *mrDaemon) ProxyMergeRequestCommits(context.Context, *daemonpb.ProxyMergeRequestCommitsRequest, ...grpc.CallOption) (*daemonpb.ProxyMergeRequestCommitsResponse, error) {
	return &daemonpb.ProxyMergeRequestCommitsResponse{Response: &pb.ListMergeRequestCommitsResponse{Commits: d.commits}}, d.fail("commits")
}

func (d *mrDaemon) ProxyMergeRequestCreate(_ context.Context, req *daemonpb.ProxyMergeRequestCreateRequest, _ ...grpc.CallOption) (*daemonpb.ProxyMergeRequestCreateResponse, error) {
	d.created = req.GetRequest()
	r := req.GetRequest()
	return &daemonpb.ProxyMergeRequestCreateResponse{Response: &pb.CreateMergeRequestResponse{MergeRequest: &pb.MergeRequestDetail{Number: 7, Title: r.GetTitle(), SourceBranch: r.GetSourceBranch(), TargetBranch: r.GetTargetBranch(), Status: "open", Draft: r.GetDraft()}}}, d.fail("create")
}

func (d *mrDaemon) ProxyMergeRequestUpdate(_ context.Context, req *daemonpb.ProxyMergeRequestUpdateRequest, _ ...grpc.CallOption) (*daemonpb.ProxyMergeRequestUpdateResponse, error) {
	d.updated = req.GetRequest()
	return &daemonpb.ProxyMergeRequestUpdateResponse{Response: &pb.UpdateMergeRequestResponse{MergeRequest: &pb.MergeRequestDetail{Number: req.GetRequest().GetNumber(), Title: "edited", Draft: req.GetRequest().GetDraft()}}}, d.fail("update")
}

func (d *mrDaemon) ProxyMergeRequestMerge(_ context.Context, req *daemonpb.ProxyMergeRequestMergeRequest, _ ...grpc.CallOption) (*daemonpb.ProxyMergeRequestMergeResponse, error) {
	d.merged = req.GetRequest()
	return &daemonpb.ProxyMergeRequestMergeResponse{Response: &pb.MergeMergeRequestResponse{MergeRequest: &pb.MergeRequestDetail{Number: 7, Status: "merged"}, Mergeability: &pb.MergeabilityDetail{Status: "merged"}}}, d.fail("merge")
}

func (d *mrDaemon) ProxyMergeRequestClose(_ context.Context, req *daemonpb.ProxyMergeRequestCloseRequest, _ ...grpc.CallOption) (*daemonpb.ProxyMergeRequestCloseResponse, error) {
	d.closed = req.GetRequest().GetNumber()
	return &daemonpb.ProxyMergeRequestCloseResponse{}, d.fail("close")
}

func (d *mrDaemon) ProxyMergeRequestReopen(_ context.Context, req *daemonpb.ProxyMergeRequestReopenRequest, _ ...grpc.CallOption) (*daemonpb.ProxyMergeRequestReopenResponse, error) {
	d.reopened = req.GetRequest().GetNumber()
	return &daemonpb.ProxyMergeRequestReopenResponse{}, d.fail("reopen")
}

func (d *mrDaemon) ProxyMergeRequestSubmitReview(_ context.Context, req *daemonpb.ProxyMergeRequestSubmitReviewRequest, _ ...grpc.CallOption) (*daemonpb.ProxyMergeRequestSubmitReviewResponse, error) {
	d.review = req.GetRequest()
	return &daemonpb.ProxyMergeRequestSubmitReviewResponse{}, d.fail("review")
}

func (d *mrDaemon) ProxyMergeRequestAddComment(_ context.Context, req *daemonpb.ProxyMergeRequestAddCommentRequest, _ ...grpc.CallOption) (*daemonpb.ProxyMergeRequestAddCommentResponse, error) {
	d.comment = req.GetRequest()
	r := req.GetRequest()
	return &daemonpb.ProxyMergeRequestAddCommentResponse{Response: &pb.AddMergeRequestCommentResponse{Thread: &pb.MergeRequestThreadDetail{Id: "t9", FilePath: r.GetFilePath(), NewLine: r.NewLine, OldLine: r.OldLine, Comments: []*pb.ReviewCommentDetail{{Id: "c9", Body: r.GetBody()}}}}}, d.fail("comment")
}

func (d *mrDaemon) ProxyMergeRequestReply(_ context.Context, req *daemonpb.ProxyMergeRequestReplyRequest, _ ...grpc.CallOption) (*daemonpb.ProxyMergeRequestReplyResponse, error) {
	d.reply = req.GetRequest()
	return &daemonpb.ProxyMergeRequestReplyResponse{Response: &pb.ReplyMergeRequestThreadResponse{Comment: &pb.ReviewCommentDetail{Id: "c10", Body: req.GetRequest().GetBody(), User: &pb.ReviewActor{UserId: "u1", Name: "Ana"}}}}, d.fail("reply")
}

func (d *mrDaemon) ProxyMergeRequestResolve(_ context.Context, req *daemonpb.ProxyMergeRequestResolveRequest, _ ...grpc.CallOption) (*daemonpb.ProxyMergeRequestResolveResponse, error) {
	d.resolve = req.GetRequest()
	return &daemonpb.ProxyMergeRequestResolveResponse{}, d.fail("resolve")
}

func newMergeRequests(t *testing.T) (*Service, *mrDaemon) {
	t.Helper()
	d := &mrDaemon{historyDaemon: &historyDaemon{opsDaemon: &opsDaemon{fakeDaemon: &fakeDaemon{watchErr: map[string]error{}, changes: make(chan *daemonpb.RepoChange, 4)}}}}
	svc := New(func(context.Context) (Session, error) { return &opsSession{client: d}, nil }, &memStore{}, nil)
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	return svc, d
}

var mrTime = timestamppb.New(time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC))

func TestService_MergeRequests(t *testing.T) {
	svc, d := newMergeRequests(t)
	next := int64(40)
	d.list = &pb.ListMergeRequestsResponse{NextCursor: &next, MergeRequests: []*pb.MergeRequestDetail{{
		Number: 41, Title: "Hero pass", Status: "open", Draft: true, SourceBranch: "art", TargetBranch: "main", CreatedBy: "u1", CreatedAt: mrTime, MergeBaseCommitId: ptr("b0"),
		Review:    &pb.MergeRequestReviewState{Approvals: 2, ChangesRequested: 1},
		Assignees: []*pb.ReviewActor{{UserId: "u2", Name: "Bo"}},
	}}}

	page, err := svc.MergeRequests(ctx, "/work/game", MergeRequestFilter{Status: "draft", Search: " hero ", Author: "u1", After: 50})
	require.NoError(t, err)
	assert.Equal(t, int64(40), page.Next)
	assert.Equal(t, MergeRequest{
		Number: 41, Title: "Hero pass", Status: "open", Draft: true, Source: "art", Target: "main", AuthorID: "u1", CreatedAt: "2026-11-05T10:00:00Z", MergeBaseID: "b0",
		Approvals: 2, ChangesRequested: 1, Assignees: []Person{{ID: "u2", Name: "Bo"}},
	}, page.Items[0])
	assert.Equal(t, "open", d.listReq.GetStatus())
	assert.True(t, d.listReq.GetDraft())
	assert.Equal(t, "hero", d.listReq.GetSearch())
	assert.Equal(t, int64(50), d.listReq.GetAfterNumber())

	_, err = svc.MergeRequests(ctx, "/work/game", MergeRequestFilter{Status: "merged"})
	require.NoError(t, err)
	assert.Nil(t, d.listReq.Draft)
	assert.Nil(t, d.listReq.AfterNumber)

	d.failAt = "list"
	_, err = svc.MergeRequests(ctx, "/work/game", MergeRequestFilter{})
	require.EqualError(t, err, "list denied")
}

func TestService_MergeRequestDetail(t *testing.T) {
	svc, d := newMergeRequests(t)
	d.get = &pb.GetMergeRequestResponse{
		MergeRequest: &pb.MergeRequestDetail{Number: 41, Title: "Hero pass", Review: &pb.MergeRequestReviewState{OutstandingReviewers: []string{"u3"}}},
		Mergeability: &pb.MergeabilityDetail{Status: "mergeable", BlockedBy: "insufficient_approvals", SourceCommitId: ptr("s1"), TargetCommitId: ptr("t1"), MergeBaseCommitId: ptr("b1")},
	}
	got, err := svc.MergeRequest(ctx, "/work/game", 41)
	require.NoError(t, err)
	assert.Equal(t, Mergeability{Status: "mergeable", BlockedBy: "insufficient_approvals", SourceCommitID: "s1", TargetCommitID: "t1", MergeBaseID: "b1"}, got.Mergeability)
	assert.Equal(t, []string{"u3"}, got.Outstanding)

	d.failAt = "get"
	_, err = svc.MergeRequest(ctx, "/work/game", 41)
	require.Error(t, err)
}

func TestService_MergeRequestActivity(t *testing.T) {
	svc, d := newMergeRequests(t)
	line := int64(12)
	d.reviews = []*pb.MergeRequestReviewDetail{{Id: "r1", Reviewer: &pb.ReviewActor{UserId: "u2", Name: "Bo"}, State: "approved", Body: "ok", Stale: true, DismissedAt: mrTime, CreatedAt: mrTime}}
	d.threads = []*pb.MergeRequestThreadDetail{{Id: "t1", FilePath: "a.go", NewLine: &line, Resolved: true, ResolvedBy: &pb.ReviewActor{Name: "Bo"}, CreatedAt: mrTime, Comments: []*pb.ReviewCommentDetail{{Id: "c1", User: &pb.ReviewActor{UserId: "u1", Name: "Ana"}, Body: "why?", Edited: true, CreatedAt: mrTime}}}}
	d.timeline = []*pb.MergeRequestTimelineItem{{Id: "e1", Kind: "pushed", Actor: &pb.ReviewActor{Name: "Ana"}, CommitId: "s1", CreatedAt: mrTime}}
	d.checks = []*pb.MergeRequestCheckDetail{{Name: "build", State: "success", DetailsUrl: "https://ci", Reporter: &pb.ReviewActor{Name: "CI"}, UpdatedAt: mrTime}}

	act, err := svc.MergeRequestActivity(ctx, "/work/game", 41)
	require.NoError(t, err)
	assert.Equal(t, []Review{{ID: "r1", Reviewer: Person{ID: "u2", Name: "Bo"}, State: "approved", Body: "ok", Stale: true, Dismissed: true, CreatedAt: "2026-11-05T10:00:00Z"}}, act.Reviews)
	assert.Equal(t, Thread{ID: "t1", Path: "a.go", NewLine: 12, Resolved: true, ResolvedBy: "Bo", CreatedAt: "2026-11-05T10:00:00Z", Comments: []Comment{{ID: "c1", Author: Person{ID: "u1", Name: "Ana"}, Body: "why?", Edited: true, CreatedAt: "2026-11-05T10:00:00Z"}}}, act.Threads[0])
	assert.Equal(t, "pushed", act.Timeline[0].Kind)
	assert.Equal(t, Check{Name: "build", State: "success", URL: "https://ci", Reporter: "CI", UpdatedAt: "2026-11-05T10:00:00Z"}, act.Checks[0])

	for _, step := range []string{"reviews", "threads", "timeline", "checks"} {
		d.failAt = step
		_, err := svc.MergeRequestActivity(ctx, "/work/game", 41)
		require.EqualError(t, err, step+" denied")
	}
}

func TestService_MergeRequestCommitsAndFiles(t *testing.T) {
	svc, d := newMergeRequests(t)
	d.commits = []*pb.CommitLogEntry{logEntry("s1", "b1")}
	commits, err := svc.MergeRequestCommits(ctx, "/work/game", 41)
	require.NoError(t, err)
	assert.Equal(t, []string{"b1"}, commits[0].Parents)
	d.failAt = "commits"
	_, err = svc.MergeRequestCommits(ctx, "/work/game", 41)
	require.Error(t, err)

	d.diff = []*daemonpb.DiffFileEvent{{Event: &daemonpb.DiffFileEvent_File{File: &daemonpb.DiffFile{Path: "art/hero.png", Status: "modified", Kind: "image"}}}}
	files, err := svc.RevisionFiles(ctx, "/work/game", "b1", "s1")
	require.NoError(t, err)
	assert.Equal(t, "b1", files[0].OldRevision)
	assert.Equal(t, "s1", files[0].NewRevision)
	assert.Equal(t, []string{"b1", "s1"}, d.diffReq.GetRevisions())
	assert.False(t, d.diffReq.GetShow())

	_, err = svc.RevisionFiles(ctx, "/work/game", "", "s1")
	require.EqualError(t, err, "both revisions are needed")
	d.diff = []*daemonpb.DiffFileEvent{{Event: &daemonpb.DiffFileEvent_Failure{Failure: &daemonpb.OpFailure{Message: "gone"}}}}
	_, err = svc.RevisionFiles(ctx, "/work/game", "b1", "s1")
	require.EqualError(t, err, "gone")
	d.streamErr = status.Error(codes.Internal, "broken")
	_, err = svc.RevisionFiles(ctx, "/work/game", "b1", "s1")
	require.EqualError(t, err, "broken")
	d.diffErr = status.Error(codes.Internal, "down")
	_, err = svc.RevisionFiles(ctx, "/work/game", "b1", "s1")
	require.Error(t, err)
}

func TestService_CreateAndEditMergeRequest(t *testing.T) {
	svc, d := newMergeRequests(t)
	mr, err := svc.CreateMergeRequest(ctx, "/work/game", NewMergeRequest{Title: " Hero pass ", Description: " body ", Source: "art", Target: "main", Draft: true})
	require.NoError(t, err)
	assert.Equal(t, int64(7), mr.Number)
	assert.True(t, mr.Draft)
	assert.Equal(t, "body", d.created.GetDescription())

	for in, msg := range map[NewMergeRequest]string{
		{Source: "a", Target: "b"}:             "enter a title",
		{Title: "x", Source: "a"}:              "choose both branches",
		{Title: "x", Source: "a", Target: "a"}: "choose two different branches",
	} {
		_, err := svc.CreateMergeRequest(ctx, "/work/game", in)
		require.EqualError(t, err, msg)
	}
	d.failAt = "create"
	_, err = svc.CreateMergeRequest(ctx, "/work/game", NewMergeRequest{Title: "x", Source: "a", Target: "b"})
	require.Error(t, err)
	d.failAt = ""

	edited, err := svc.EditMergeRequest(ctx, "/work/game", 7, " New ", "")
	require.NoError(t, err)
	assert.Equal(t, "edited", edited.Title)
	assert.Equal(t, "New", d.updated.GetTitle())
	assert.Nil(t, d.updated.Draft)

	ready, err := svc.SetDraft(ctx, "/work/game", 7, false)
	require.NoError(t, err)
	assert.False(t, ready.Draft)
	require.NotNil(t, d.updated.Draft)
	assert.False(t, d.updated.GetDraft())

	d.failAt = "update"
	_, err = svc.SetDraft(ctx, "/work/game", 7, true)
	require.Error(t, err)
}

func TestService_MergeCloseReopen(t *testing.T) {
	svc, d := newMergeRequests(t)
	res, err := svc.MergeMergeRequest(ctx, "/work/game", 7, "squash", true)
	require.NoError(t, err)
	assert.Equal(t, "merged", res.Request.Status)
	assert.Equal(t, "squash", d.merged.GetStrategy())
	assert.True(t, d.merged.GetDeleteSource())

	require.NoError(t, svc.CloseMergeRequest(ctx, "/work/game", 7))
	require.NoError(t, svc.ReopenMergeRequest(ctx, "/work/game", 8))
	assert.Equal(t, int64(7), d.closed)
	assert.Equal(t, int64(8), d.reopened)

	for _, step := range []string{"merge", "close", "reopen"} {
		d.failAt = step
		var err error
		switch step {
		case "merge":
			_, err = svc.MergeMergeRequest(ctx, "/work/game", 7, "ff", false)
		case "close":
			err = svc.CloseMergeRequest(ctx, "/work/game", 7)
		default:
			err = svc.ReopenMergeRequest(ctx, "/work/game", 7)
		}
		require.EqualError(t, err, step+" denied")
	}
}

func TestService_ReviewsAndComments(t *testing.T) {
	svc, d := newMergeRequests(t)
	require.NoError(t, svc.SubmitReview(ctx, "/work/game", 7, "approved", " looks good "))
	assert.Equal(t, "looks good", d.review.GetBody())
	require.EqualError(t, svc.SubmitReview(ctx, "/work/game", 7, "maybe", ""), "unknown review decision maybe")
	require.EqualError(t, svc.SubmitReview(ctx, "/work/game", 7, "commented", " "), "write a comment")

	thread, err := svc.Comment(ctx, "/work/game", 7, CommentInput{Path: "a.go", NewLine: 12, OldLine: 11, Body: " fix "})
	require.NoError(t, err)
	assert.Equal(t, int64(12), thread.NewLine)
	assert.Nil(t, d.comment.OldLine)
	_, err = svc.Comment(ctx, "/work/game", 7, CommentInput{Path: "a.go", OldLine: 4, Body: "gone"})
	require.NoError(t, err)
	assert.Equal(t, int64(4), d.comment.GetOldLine())
	assert.Nil(t, d.comment.NewLine)
	_, err = svc.Comment(ctx, "/work/game", 7, CommentInput{Body: "overall"})
	require.NoError(t, err)
	assert.Empty(t, d.comment.GetFilePath())
	_, err = svc.Comment(ctx, "/work/game", 7, CommentInput{Path: "a.go", Body: "x"})
	require.EqualError(t, err, "choose a line to comment on")
	_, err = svc.Comment(ctx, "/work/game", 7, CommentInput{Body: " "})
	require.EqualError(t, err, "write a comment")

	c, err := svc.Reply(ctx, "/work/game", 7, "t1", " agreed ")
	require.NoError(t, err)
	assert.Equal(t, Comment{ID: "c10", Author: Person{ID: "u1", Name: "Ana"}, Body: "agreed"}, *c)
	_, err = svc.Reply(ctx, "/work/game", 7, "t1", "")
	require.EqualError(t, err, "write a reply")

	require.NoError(t, svc.ResolveThread(ctx, "/work/game", 7, "t1", true))
	assert.True(t, d.resolve.GetResolved())

	for _, step := range []string{"review", "comment", "reply", "resolve"} {
		d.failAt = step
		var err error
		switch step {
		case "review":
			err = svc.SubmitReview(ctx, "/work/game", 7, "approved", "")
		case "comment":
			_, err = svc.Comment(ctx, "/work/game", 7, CommentInput{Body: "x"})
		case "reply":
			_, err = svc.Reply(ctx, "/work/game", 7, "t1", "x")
		default:
			err = svc.ResolveThread(ctx, "/work/game", 7, "t1", false)
		}
		require.EqualError(t, err, step+" denied")
	}
}

func TestService_MergeRequestsWithoutDaemon(t *testing.T) {
	svc := New(func(context.Context) (Session, error) { return nil, errors.New("daemon down") }, &memStore{}, nil)
	_, err := svc.RevisionFiles(ctx, "/work/game", "a", "b")
	require.Error(t, err)
	_, err = svc.MergeRequestActivity(ctx, "/work/game", 1)
	require.Error(t, err)
}
