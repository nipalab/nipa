package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/grpc/pb"
)

type feedDaemon struct {
	*fakeDaemon
	branches []*pb.Branch
	commits  map[string][]*pb.CommitLogEntry
	open     []*pb.MergeRequestDetail
	merged   []*pb.MergeRequestDetail
	locks    []*pb.FileLockDetail
	members  []*pb.ProjectMember
	failLog  map[string]bool
	failMRs  bool
	failLock bool
	failMem  bool
}

func (d *feedDaemon) ProxyBranchList(context.Context, *daemonpb.ProxyBranchListRequest, ...grpc.CallOption) (*daemonpb.ProxyBranchListResponse, error) {
	return &daemonpb.ProxyBranchListResponse{Response: &pb.GetListBranchResponse{Branches: d.branches}}, nil
}

func (d *feedDaemon) ProxyCommitLog(_ context.Context, req *daemonpb.ProxyCommitLogRequest, _ ...grpc.CallOption) (*daemonpb.ProxyCommitLogResponse, error) {
	branch := req.GetRequest().GetBranch()
	if d.failLog[branch] {
		return nil, errors.New("gone")
	}
	return &daemonpb.ProxyCommitLogResponse{Response: &pb.GetCommitLogResponse{Commits: d.commits[branch]}}, nil
}

func (d *feedDaemon) ProxyMergeRequestList(_ context.Context, req *daemonpb.ProxyMergeRequestListRequest, _ ...grpc.CallOption) (*daemonpb.ProxyMergeRequestListResponse, error) {
	if d.failMRs {
		return nil, errors.New("down")
	}
	list := d.open
	if req.GetRequest().GetStatus() == "merged" {
		list = d.merged
	}
	return &daemonpb.ProxyMergeRequestListResponse{Response: &pb.ListMergeRequestsResponse{MergeRequests: list}}, nil
}

func (d *feedDaemon) LockStatus(context.Context, *daemonpb.LockStatusRequest, ...grpc.CallOption) (*daemonpb.LockStatusResponse, error) {
	if d.failLock {
		return nil, errors.New("down")
	}
	return &daemonpb.LockStatusResponse{Locks: d.locks}, nil
}

func (d *feedDaemon) ProxyProjectMembers(context.Context, *daemonpb.ProxyProjectMembersRequest, ...grpc.CallOption) (*daemonpb.ProxyProjectMembersResponse, error) {
	if d.failMem {
		return nil, errors.New("down")
	}
	return &daemonpb.ProxyProjectMembersResponse{Response: &pb.ListProjectMembersResponse{Members: d.members}}, nil
}

func newFeed(t *testing.T, d *feedDaemon) *Service {
	t.Helper()
	d.fakeDaemon = &fakeDaemon{watchErr: map[string]error{}, changes: make(chan *daemonpb.RepoChange, 4)}
	svc := New(func(context.Context) (Session, error) { return &opsSession{client: d}, nil }, &memStore{}, nil)
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	return svc
}

func at(day, hour int) *timestamppb.Timestamp {
	return timestamppb.New(time.Date(2026, 11, day, hour, 0, 0, 0, time.UTC))
}

func commit(id, author, message string, day, hour int) *pb.CommitLogEntry {
	return &pb.CommitLogEntry{CommitId: id, AuthorName: author, Message: message, CreatedAt: at(day, hour)}
}

func newFeedDaemon() *feedDaemon {
	return &feedDaemon{
		branches: []*pb.Branch{
			{Name: "feature", UpdatedAt: at(12, 9)},
			{Name: "main", IsDefault: true, UpdatedAt: at(11, 9)},
			{Name: "old", UpdatedAt: at(1, 9)},
		},
		commits: map[string][]*pb.CommitLogEntry{
			"main":    {commit("c3", "Ana", "Fix door\n\nlong body", 11, 9), commit("c1", "Bo", "Old work", 2, 9)},
			"feature": {commit("c4", "Bo", "Paint walls", 12, 9), commit("c3", "Ana", "Fix door", 11, 9)},
		},
		open:    []*pb.MergeRequestDetail{{Number: 5, Title: "Walls", SourceBranch: "feature", CreatedBy: "u2", CreatedAt: at(12, 10), UpdatedAt: at(12, 10)}},
		merged:  []*pb.MergeRequestDetail{{Number: 4, Title: "Doors", SourceBranch: "doors", CreatedBy: "u1", CreatedAt: at(3, 8), UpdatedAt: at(11, 12)}, {Number: 2, Title: "Ancient", CreatedBy: "u9", CreatedAt: at(1, 1), UpdatedAt: at(1, 2)}},
		locks:   []*pb.FileLockDetail{{Id: "l1", Path: "art/door.psd", Branch: "main", HeldByName: "Ana", AcquiredAt: at(12, 8)}, {Id: "l2", Path: "art/old.psd", HeldByName: "Bo", AcquiredAt: at(2, 8)}},
		members: []*pb.ProjectMember{{Id: "u1", Name: "Ana"}, {Id: "u2", Name: "Bo"}},
	}
}

var feedFrom = time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)

func TestService_ProjectActivity(t *testing.T) {
	svc := newFeed(t, newFeedDaemon())

	feed, err := svc.ProjectActivity(context.Background(), "/r", feedFrom)

	require.NoError(t, err)
	require.False(t, feed.Partial)
	require.False(t, feed.Truncated)
	var got []string
	for _, e := range feed.Events {
		got = append(got, e.ID)
	}
	require.Equal(t, []string{"mr:5:mr_opened", "commit:c4", "lock:l1", "mr:4:mr_merged", "commit:c3"}, got)

	byID := map[string]FeedEvent{}
	for _, e := range feed.Events {
		byID[e.ID] = e
	}
	require.Equal(t, FeedEvent{ID: "commit:c3", Kind: FeedCommit, Time: "2026-11-11T09:00:00Z", Actor: "Ana", Title: "Fix door", Branch: "main", CommitID: "c3"}, byID["commit:c3"])
	require.Equal(t, "Bo", byID["mr:5:mr_opened"].Actor)
	require.Equal(t, int64(5), byID["mr:5:mr_opened"].Number)
	require.Equal(t, "Ana", byID["mr:4:mr_merged"].Actor)
	require.Equal(t, "art/door.psd", byID["lock:l1"].Path)
}

func TestService_ProjectActivity_DefaultsToAWeekAndSurvivesFailures(t *testing.T) {
	d := newFeedDaemon()
	d.failLog = map[string]bool{"feature": true}
	d.failMRs, d.failLock, d.failMem = true, true, true
	svc := newFeed(t, d)

	feed, err := svc.ProjectActivity(context.Background(), "/r", "")

	require.NoError(t, err)
	require.True(t, feed.Partial)
	for _, e := range feed.Events {
		require.Equal(t, FeedCommit, e.Kind)
		require.NotEqual(t, "feature", e.Branch)
	}
}

func TestService_ProjectActivity_UnknownAuthorsAndTruncation(t *testing.T) {
	d := newFeedDaemon()
	d.members = nil
	d.branches = d.branches[:1]
	var many []*pb.CommitLogEntry
	for i := 0; i < feedCommits; i++ {
		many = append(many, commit("x"+string(rune('a'+i)), "Bo", "c", 12, 9))
	}
	d.commits["feature"] = many
	svc := newFeed(t, d)

	feed, err := svc.ProjectActivity(context.Background(), "/r", feedFrom)

	require.NoError(t, err)
	require.True(t, feed.Truncated)
	var opened FeedEvent
	for _, e := range feed.Events {
		if e.Kind == FeedOpened {
			opened = e
		}
	}
	require.Equal(t, feedNoOneYet, opened.Actor)
}

func TestService_ProjectActivity_TooManyBranches(t *testing.T) {
	d := newFeedDaemon()
	d.branches = nil
	for i := 0; i <= feedBranches; i++ {
		d.branches = append(d.branches, &pb.Branch{Name: "b", UpdatedAt: at(12, 9)})
	}
	svc := newFeed(t, d)

	feed, err := svc.ProjectActivity(context.Background(), "/r", feedFrom)

	require.NoError(t, err)
	require.True(t, feed.Truncated)
}

func TestMergeFeed_OrdersNewestFirstAndDedupes(t *testing.T) {
	events := mergeFeed(
		[]FeedEvent{{ID: "b", Time: "2026-11-12T09:00:00Z"}, {ID: "a", Time: "2026-11-12T09:00:00Z"}},
		[]FeedEvent{{ID: "b", Time: "2026-11-01T09:00:00Z"}, {ID: "z", Time: "2026-11-13T09:00:00Z"}},
	)

	var ids []string
	for _, e := range events {
		ids = append(ids, e.ID)
	}
	require.Equal(t, []string{"z", "a", "b"}, ids)
	require.Empty(t, mergeFeed())
}

func TestFeedHelpers(t *testing.T) {
	require.Equal(t, "one", firstLine("one\ntwo"))
	require.Equal(t, "single", firstLine("single"))
	require.Equal(t, feedNoOneYet, orSomeone(""))
	require.False(t, within("", time.Time{}))
	require.False(t, within("garbage", time.Time{}))
}
