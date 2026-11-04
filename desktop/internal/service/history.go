package service

import (
	"context"
	"errors"
	"io"
	"sort"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/grpc/pb"
)

const (
	defaultHistoryPage = 50
	maxHistoryPage     = 200
	listPage           = 200
)

type Commit struct {
	ID      string   `json:"id"`
	Hash    string   `json:"hash"`
	Parents []string `json:"parents"`
	Author  string   `json:"author"`
	Email   string   `json:"email"`
	Message string   `json:"message"`
	Date    string   `json:"date"` // RFC 3339
}

// HistoryQuery pages through a branch's commits; Path narrows them to the
// commits that changed a file or folder, Start continues from a Next cursor.
type HistoryQuery struct {
	Branch string `json:"branch"`
	Path   string `json:"path"`
	Start  string `json:"start"`
	Limit  int    `json:"limit"`
}

type HistoryPage struct {
	Commits []Commit `json:"commits"`
	Next    string   `json:"next"` // empty on the last page
}

type Branch struct {
	Name      string `json:"name"`
	CommitID  string `json:"commitId"`
	Default   bool   `json:"default"`
	Protected bool   `json:"protected"`
}

type Tag struct {
	Name      string `json:"name"`
	CommitID  string `json:"commitId"`
	Message   string `json:"message"`
	CreatedAt string `json:"createdAt"`
	CreatedBy string `json:"createdBy"`
}

// RevertRequest reverts Target (a commit id) or, with Mode continue, skip or
// abort, resumes the revert waiting on conflicts.
type RevertRequest struct {
	Target string `json:"target"`
	Mode   string `json:"mode"`
}

type RevertOutcome struct {
	Committed bool     `json:"committed"`
	NoChange  bool     `json:"noChange"`
	Skipped   bool     `json:"skipped"`
	Aborted   bool     `json:"aborted"`
	Conflicts []string `json:"conflicts"`
}

// History lists commits newest first.
func (s *Service) History(ctx context.Context, root string, q HistoryQuery) (*HistoryPage, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = defaultHistoryPage
	}
	limit = min(limit, maxHistoryPage)
	req := &pb.GetCommitLogRequest{Branch: q.Branch, Path: strings.Trim(q.Path, "/"), Limit: int32(limit + 1)}
	if q.Start != "" {
		req.StartCommitId = &q.Start
	}
	var res *daemonpb.ProxyCommitLogResponse
	err := s.call(ctx, root, func(c daemonpb.NipaDaemonClient) (err error) {
		res, err = c.ProxyCommitLog(ctx, &daemonpb.ProxyCommitLogRequest{Root: root, Request: req})
		return err
	})
	if err != nil {
		return nil, err
	}
	entries := res.GetResponse().GetCommits()
	page := &HistoryPage{Commits: make([]Commit, 0, min(len(entries), limit))}
	for i, e := range entries {
		if i == limit {
			page.Next = e.GetCommitId()
			break
		}
		page.Commits = append(page.Commits, toCommit(e))
	}
	return page, nil
}

func toCommit(e *pb.CommitLogEntry) Commit {
	c := Commit{ID: e.GetCommitId(), Hash: e.GetCommitHash(), Parents: []string{}, Author: e.GetAuthorName(), Email: e.GetAuthorEmail(), Message: e.GetMessage(), Date: rfc3339(e.GetCreatedAt())}
	for _, p := range []string{e.GetParent_1Id(), e.GetParent_2Id()} {
		if p != "" {
			c.Parents = append(c.Parents, p)
		}
	}
	return c
}

func rfc3339(ts *timestamppb.Timestamp) string {
	if ts == nil {
		return ""
	}
	return ts.AsTime().UTC().Format(time.RFC3339)
}

// CommitFiles lists what a commit changed against its first parent (parent
// is empty for a root commit). Each file carries both revisions so the asset
// viewers load the right versions.
func (s *Service) CommitFiles(ctx context.Context, root, commitID, parent string) ([]FileDiff, error) {
	if commitID == "" {
		return nil, errors.New("choose a commit")
	}
	client, err := s.client(ctx, root)
	if err != nil {
		return nil, err
	}
	stream, err := client.DiffFiles(ctx, &daemonpb.DiffRequest{Root: root, Revisions: []string{commitID}, Show: true})
	if err != nil {
		s.dropIfDisconnected(err)
		return nil, cleanError(err)
	}
	out := []FileDiff{}
	for {
		ev, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			s.dropIfDisconnected(err)
			return nil, cleanError(err)
		}
		if f := ev.GetFailure(); f != nil {
			return nil, errors.New(f.GetMessage())
		}
		if file := ev.GetFile(); file != nil {
			d := toFileDiff(file)
			d.OldRevision, d.NewRevision = parent, commitID
			out = append(out, *d)
		}
	}
}

// Branches lists the project's branches, the default one first.
func (s *Service) Branches(ctx context.Context, root string) ([]Branch, error) {
	out := []Branch{}
	req := &pb.GetListBranchRequest{Limit: listPage}
	for {
		var res *daemonpb.ProxyBranchListResponse
		err := s.call(ctx, root, func(c daemonpb.NipaDaemonClient) (err error) {
			res, err = c.ProxyBranchList(ctx, &daemonpb.ProxyBranchListRequest{Root: root, Request: req})
			return err
		})
		if err != nil {
			return nil, err
		}
		list := res.GetResponse().GetBranches()
		for _, b := range list {
			out = append(out, toBranch(b))
		}
		if len(list) < listPage {
			break
		}
		last := list[len(list)-1]
		req = &pb.GetListBranchRequest{Limit: listPage, LastUpdatedAt: last.GetUpdatedAt(), LastId: ptr(last.GetId())}
	}
	sortBranches(out)
	return out, nil
}

func sortBranches(list []Branch) {
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].Default != list[j].Default {
			return list[i].Default
		}
		return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name)
	})
}

func toBranch(b *pb.Branch) Branch {
	return Branch{Name: b.GetName(), CommitID: b.GetCommitId(), Default: b.GetIsDefault(), Protected: b.GetIsProtected()}
}

func ptr[T any](v T) *T { return &v }

// CreateBranch creates a branch at a commit, else at a branch head, else at
// the default branch head.
func (s *Service) CreateBranch(ctx context.Context, root, name, fromBranch, fromCommit string) (*Branch, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("enter a branch name")
	}
	var res *daemonpb.ProxyBranchCreateResponse
	err := s.call(ctx, root, func(c daemonpb.NipaDaemonClient) (err error) {
		res, err = c.ProxyBranchCreate(ctx, &daemonpb.ProxyBranchCreateRequest{Root: root, Request: &pb.CreateBranchRequest{Name: name, FromBranch: fromBranch, FromCommitId: fromCommit}})
		return err
	})
	if err != nil {
		return nil, err
	}
	b := toBranch(res.GetResponse().GetBranch())
	return &b, nil
}

// DeleteBranch deletes a branch on the server.
func (s *Service) DeleteBranch(ctx context.Context, root, name string) error {
	return s.call(ctx, root, func(c daemonpb.NipaDaemonClient) error {
		_, err := c.ProxyBranchDelete(ctx, &daemonpb.ProxyBranchDeleteRequest{Root: root, Request: &pb.DeleteBranchRequest{Name: name}})
		return err
	})
}

// Tags lists the project's release tags, newest first.
func (s *Service) Tags(ctx context.Context, root string) ([]Tag, error) {
	out := []Tag{}
	req := &pb.ListTagsRequest{Limit: listPage}
	for {
		var res *daemonpb.ProxyTagListResponse
		err := s.call(ctx, root, func(c daemonpb.NipaDaemonClient) (err error) {
			res, err = c.ProxyTagList(ctx, &daemonpb.ProxyTagListRequest{Root: root, Request: req})
			return err
		})
		if err != nil {
			return nil, err
		}
		list := res.GetResponse().GetTags()
		for _, t := range list {
			out = append(out, toTag(t))
		}
		if len(list) < listPage {
			return out, nil
		}
		last := list[len(list)-1]
		req = &pb.ListTagsRequest{Limit: listPage, LastCreatedAt: last.GetCreatedAt(), LastId: ptr(last.GetId())}
	}
}

func toTag(t *pb.Tag) Tag {
	return Tag{Name: t.GetName(), CommitID: t.GetCommitId(), Message: t.GetMessage(), CreatedAt: rfc3339(t.GetCreatedAt()), CreatedBy: t.GetUserId()}
}

// CreateTag tags a commit, else a branch head, else the default branch head.
func (s *Service) CreateTag(ctx context.Context, root, name, fromBranch, fromCommit, message string) (*Tag, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("enter a tag name")
	}
	var res *daemonpb.ProxyTagCreateResponse
	err := s.call(ctx, root, func(c daemonpb.NipaDaemonClient) (err error) {
		res, err = c.ProxyTagCreate(ctx, &daemonpb.ProxyTagCreateRequest{Root: root, Request: &pb.CreateTagRequest{Name: name, FromBranch: fromBranch, FromCommitId: fromCommit, Message: strings.TrimSpace(message)}})
		return err
	})
	if err != nil {
		return nil, err
	}
	t := toTag(res.GetResponse().GetTag())
	return &t, nil
}

// DeleteTag deletes a tag; clones checked out at it keep their files.
func (s *Service) DeleteTag(ctx context.Context, root, name string) error {
	return s.call(ctx, root, func(c daemonpb.NipaDaemonClient) error {
		_, err := c.ProxyTagDelete(ctx, &daemonpb.ProxyTagDeleteRequest{Root: root, Request: &pb.DeleteTagRequest{Name: name}})
		return err
	})
}

// SwitchBranch moves the clone to another branch.
func (s *Service) SwitchBranch(ctx context.Context, root, branch string) (OpOutcome, error) {
	return s.runOp(ctx, root, "switch", func(c daemonpb.NipaDaemonClient) (grpc.ServerStreamingClient[daemonpb.OpEvent], error) {
		return c.Switch(ctx, &daemonpb.SwitchRequest{Root: root, Branch: branch})
	})
}

// CheckoutTag checks the tagged files out read-only (detached) until the
// clone switches back to a branch.
func (s *Service) CheckoutTag(ctx context.Context, root, tag string) (OpOutcome, error) {
	return s.runOp(ctx, root, "switch", func(c daemonpb.NipaDaemonClient) (grpc.ServerStreamingClient[daemonpb.OpEvent], error) {
		return c.Switch(ctx, &daemonpb.SwitchRequest{Root: root, Tag: tag})
	})
}

// Revert undoes a commit with a new commit on the clone's branch, or resumes
// a revert stopped on conflicts.
func (s *Service) Revert(ctx context.Context, root string, req RevertRequest) (OpOutcome, error) {
	msg := &daemonpb.RevertOpRequest{Root: root, Target: req.Target}
	switch req.Mode {
	case "":
		if req.Target == "" {
			return OpOutcome{}, errors.New("choose a commit to revert")
		}
	case "continue":
		msg.Continue = true
	case "skip":
		msg.Skip = true
	case "abort":
		msg.Abort = true
	default:
		return OpOutcome{}, errors.New("unknown revert mode " + req.Mode)
	}
	return s.runOp(ctx, root, "revert", func(c daemonpb.NipaDaemonClient) (grpc.ServerStreamingClient[daemonpb.OpEvent], error) {
		return c.Revert(ctx, msg)
	})
}

func (s *Service) call(ctx context.Context, root string, fn func(daemonpb.NipaDaemonClient) error) error {
	client, err := s.client(ctx, root)
	if err != nil {
		return err
	}
	if err := fn(client); err != nil {
		s.dropIfDisconnected(err)
		return cleanError(err)
	}
	return nil
}
