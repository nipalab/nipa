package service

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/grpc/pb"
)

const mergeRequestPage = 50

type Person struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type MergeRequest struct {
	Number           int64    `json:"number"`
	Title            string   `json:"title"`
	Description      string   `json:"description"`
	Status           string   `json:"status"` // open, merged, closed
	Draft            bool     `json:"draft"`
	Source           string   `json:"source"`
	Target           string   `json:"target"`
	AuthorID         string   `json:"authorId"`
	CreatedAt        string   `json:"createdAt"`
	UpdatedAt        string   `json:"updatedAt"`
	MergeCommitID    string   `json:"mergeCommitId"`
	MergeBaseID      string   `json:"mergeBaseId"`
	MergedSourceID   string   `json:"mergedSourceId"`
	Approvals        int      `json:"approvals"`
	ChangesRequested int      `json:"changesRequested"`
	Assignees        []Person `json:"assignees"`
}

// MergeRequestFilter narrows the list; Status draft means open drafts.
type MergeRequestFilter struct {
	Status string `json:"status"`
	Search string `json:"search"`
	Author string `json:"author"`
	After  int64  `json:"after"`
}

type MergeRequestPage struct {
	Items []MergeRequest `json:"items"`
	Next  int64          `json:"next"` // 0 on the last page
}

type Mergeability struct {
	Status         string `json:"status"` // mergeable, behind_target, up_to_date, invalid, merged, closed
	BlockedBy      string `json:"blockedBy"`
	SourceCommitID string `json:"sourceCommitId"`
	TargetCommitID string `json:"targetCommitId"`
	MergeBaseID    string `json:"mergeBaseId"`
}

type MergeRequestDetail struct {
	Request      MergeRequest `json:"request"`
	Mergeability Mergeability `json:"mergeability"`
	Outstanding  []string     `json:"outstanding"` // reviewer ids still asked to review
}

type Comment struct {
	ID        string `json:"id"`
	Author    Person `json:"author"`
	Body      string `json:"body"`
	CreatedAt string `json:"createdAt"`
	Edited    bool   `json:"edited"`
}

type Thread struct {
	ID         string    `json:"id"`
	Path       string    `json:"path"` // empty for the conversation
	OldLine    int64     `json:"oldLine"`
	NewLine    int64     `json:"newLine"`
	Outdated   bool      `json:"outdated"`
	Resolved   bool      `json:"resolved"`
	ResolvedBy string    `json:"resolvedBy"`
	CreatedAt  string    `json:"createdAt"`
	Comments   []Comment `json:"comments"`
}

type Review struct {
	ID        string `json:"id"`
	Reviewer  Person `json:"reviewer"`
	State     string `json:"state"` // approved, changes_requested, commented
	Body      string `json:"body"`
	Stale     bool   `json:"stale"`
	Dismissed bool   `json:"dismissed"`
	CreatedAt string `json:"createdAt"`
}

type TimelineEvent struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Actor     Person `json:"actor"`
	Subject   Person `json:"subject"`
	Body      string `json:"body"`
	CommitID  string `json:"commitId"`
	CreatedAt string `json:"createdAt"`
}

type Check struct {
	Name      string `json:"name"`
	State     string `json:"state"` // pending, success, failed
	URL       string `json:"url"`
	Reporter  string `json:"reporter"`
	UpdatedAt string `json:"updatedAt"`
}

// Activity is everything the conversation shows besides the request itself.
type Activity struct {
	Reviews  []Review        `json:"reviews"`
	Threads  []Thread        `json:"threads"`
	Timeline []TimelineEvent `json:"timeline"`
	Checks   []Check         `json:"checks"`
}

type NewMergeRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Source      string `json:"source"`
	Target      string `json:"target"`
	Draft       bool   `json:"draft"`
}

// CommentInput starts a thread; Path empty is a conversation comment,
// otherwise OldLine or NewLine anchors it to a line the diff shows.
type CommentInput struct {
	Path    string `json:"path"`
	OldLine int64  `json:"oldLine"`
	NewLine int64  `json:"newLine"`
	Body    string `json:"body"`
}

// MergeRequests lists the project's merge requests, newest first.
func (s *Service) MergeRequests(ctx context.Context, root string, f MergeRequestFilter) (*MergeRequestPage, error) {
	req := &pb.ListMergeRequestsRequest{Status: f.Status, Search: strings.TrimSpace(f.Search), Author: f.Author, Limit: mergeRequestPage}
	if f.Status == "draft" {
		req.Status, req.Draft = "open", ptr(true)
	}
	if f.After > 0 {
		req.AfterNumber = ptr(f.After)
	}
	var res *daemonpb.ProxyMergeRequestListResponse
	err := s.call(ctx, root, func(c daemonpb.NipaDaemonClient) (err error) {
		res, err = c.ProxyMergeRequestList(ctx, &daemonpb.ProxyMergeRequestListRequest{Root: root, Request: req})
		return err
	})
	if err != nil {
		return nil, err
	}
	list := res.GetResponse()
	page := &MergeRequestPage{Items: make([]MergeRequest, 0, len(list.GetMergeRequests())), Next: list.GetNextCursor()}
	for _, mr := range list.GetMergeRequests() {
		page.Items = append(page.Items, toMergeRequest(mr))
	}
	return page, nil
}

func toMergeRequest(mr *pb.MergeRequestDetail) MergeRequest {
	out := MergeRequest{
		Number: mr.GetNumber(), Title: mr.GetTitle(), Description: mr.GetDescription(), Status: mr.GetStatus(), Draft: mr.GetDraft(),
		Source: mr.GetSourceBranch(), Target: mr.GetTargetBranch(), AuthorID: mr.GetCreatedBy(),
		CreatedAt: rfc3339(mr.GetCreatedAt()), UpdatedAt: rfc3339(mr.GetUpdatedAt()), MergeCommitID: mr.GetMergeCommitId(), MergeBaseID: mr.GetMergeBaseCommitId(), MergedSourceID: mr.GetMergedSourceCommitId(),
		Approvals: int(mr.GetReview().GetApprovals()), ChangesRequested: int(mr.GetReview().GetChangesRequested()), Assignees: []Person{},
	}
	for _, a := range mr.GetAssignees() {
		out.Assignees = append(out.Assignees, toPerson(a))
	}
	return out
}

func toPerson(a *pb.ReviewActor) Person {
	return Person{ID: a.GetUserId(), Name: a.GetName()}
}

func toMergeability(m *pb.MergeabilityDetail) Mergeability {
	return Mergeability{Status: m.GetStatus(), BlockedBy: m.GetBlockedBy(), SourceCommitID: m.GetSourceCommitId(), TargetCommitID: m.GetTargetCommitId(), MergeBaseID: m.GetMergeBaseCommitId()}
}

// MergeRequest returns one request with its mergeability.
func (s *Service) MergeRequest(ctx context.Context, root string, number int64) (*MergeRequestDetail, error) {
	var res *daemonpb.ProxyMergeRequestGetResponse
	err := s.call(ctx, root, func(c daemonpb.NipaDaemonClient) (err error) {
		res, err = c.ProxyMergeRequestGet(ctx, &daemonpb.ProxyMergeRequestGetRequest{Root: root, Request: &pb.GetMergeRequestRequest{Number: number}})
		return err
	})
	if err != nil {
		return nil, err
	}
	r := res.GetResponse()
	return &MergeRequestDetail{
		Request:      toMergeRequest(r.GetMergeRequest()),
		Mergeability: toMergeability(r.GetMergeability()),
		Outstanding:  append([]string{}, r.GetMergeRequest().GetReview().GetOutstandingReviewers()...),
	}, nil
}

// MergeRequestActivity loads reviews, comment threads, the timeline and the
// status checks of a request.
func (s *Service) MergeRequestActivity(ctx context.Context, root string, number int64) (*Activity, error) {
	out := &Activity{Reviews: []Review{}, Threads: []Thread{}, Timeline: []TimelineEvent{}, Checks: []Check{}}
	err := s.call(ctx, root, func(c daemonpb.NipaDaemonClient) error {
		reviews, err := c.ProxyMergeRequestReviews(ctx, &daemonpb.ProxyMergeRequestReviewsRequest{Root: root, Request: &pb.ListMergeRequestReviewsRequest{Number: number}})
		if err != nil {
			return err
		}
		for _, r := range reviews.GetResponse().GetReviews() {
			out.Reviews = append(out.Reviews, Review{ID: r.GetId(), Reviewer: toPerson(r.GetReviewer()), State: r.GetState(), Body: r.GetBody(), Stale: r.GetStale(), Dismissed: r.GetDismissedAt() != nil, CreatedAt: rfc3339(r.GetCreatedAt())})
		}
		threads, err := c.ProxyMergeRequestThreads(ctx, &daemonpb.ProxyMergeRequestThreadsRequest{Root: root, Request: &pb.ListMergeRequestThreadsRequest{Number: number}})
		if err != nil {
			return err
		}
		for _, t := range threads.GetResponse().GetThreads() {
			out.Threads = append(out.Threads, toThread(t))
		}
		timeline, err := c.ProxyMergeRequestTimeline(ctx, &daemonpb.ProxyMergeRequestTimelineRequest{Root: root, Request: &pb.ListMergeRequestTimelineRequest{Number: number}})
		if err != nil {
			return err
		}
		for _, e := range timeline.GetResponse().GetItems() {
			out.Timeline = append(out.Timeline, TimelineEvent{ID: e.GetId(), Kind: e.GetKind(), Actor: toPerson(e.GetActor()), Subject: toPerson(e.GetSubject()), Body: e.GetBody(), CommitID: e.GetCommitId(), CreatedAt: rfc3339(e.GetCreatedAt())})
		}
		checks, err := c.ProxyMergeRequestChecks(ctx, &daemonpb.ProxyMergeRequestChecksRequest{Root: root, Request: &pb.ListMergeRequestChecksRequest{Number: number}})
		if err != nil {
			return err
		}
		for _, ch := range checks.GetResponse().GetChecks() {
			out.Checks = append(out.Checks, Check{Name: ch.GetName(), State: ch.GetState(), URL: ch.GetDetailsUrl(), Reporter: ch.GetReporter().GetName(), UpdatedAt: rfc3339(ch.GetUpdatedAt())})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func toThread(t *pb.MergeRequestThreadDetail) Thread {
	out := Thread{
		ID: t.GetId(), Path: t.GetFilePath(), OldLine: t.GetOldLine(), NewLine: t.GetNewLine(), Outdated: t.GetOutdated(),
		Resolved: t.GetResolved(), ResolvedBy: t.GetResolvedBy().GetName(), CreatedAt: rfc3339(t.GetCreatedAt()), Comments: []Comment{},
	}
	for _, c := range t.GetComments() {
		out.Comments = append(out.Comments, toComment(c))
	}
	return out
}

func toComment(c *pb.ReviewCommentDetail) Comment {
	return Comment{ID: c.GetId(), Author: toPerson(c.GetUser()), Body: c.GetBody(), CreatedAt: rfc3339(c.GetCreatedAt()), Edited: c.GetEdited()}
}

// MergeRequestCommits lists the commits the request brings in.
func (s *Service) MergeRequestCommits(ctx context.Context, root string, number int64) ([]Commit, error) {
	var res *daemonpb.ProxyMergeRequestCommitsResponse
	err := s.call(ctx, root, func(c daemonpb.NipaDaemonClient) (err error) {
		res, err = c.ProxyMergeRequestCommits(ctx, &daemonpb.ProxyMergeRequestCommitsRequest{Root: root, Request: &pb.ListMergeRequestCommitsRequest{Number: number}})
		return err
	})
	if err != nil {
		return nil, err
	}
	out := make([]Commit, 0, len(res.GetResponse().GetCommits()))
	for _, e := range res.GetResponse().GetCommits() {
		out = append(out, toCommit(e))
	}
	return out, nil
}

// RevisionFiles lists what changed between two commits, with both revisions
// on every file so the asset viewers load the right versions. A merge
// request's changes are its merge base against the source head.
func (s *Service) RevisionFiles(ctx context.Context, root, oldRev, newRev string) ([]FileDiff, error) {
	if oldRev == "" || newRev == "" {
		return nil, errors.New("both revisions are needed")
	}
	client, err := s.client(ctx, root)
	if err != nil {
		return nil, err
	}
	stream, err := client.DiffFiles(ctx, &daemonpb.DiffRequest{Root: root, Revisions: []string{oldRev, newRev}})
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
			d.OldRevision, d.NewRevision = oldRev, newRev
			out = append(out, *d)
		}
	}
}

// CreateMergeRequest opens a request from one branch into another.
func (s *Service) CreateMergeRequest(ctx context.Context, root string, in NewMergeRequest) (*MergeRequest, error) {
	in.Title = strings.TrimSpace(in.Title)
	switch {
	case in.Title == "":
		return nil, errors.New("enter a title")
	case in.Source == "" || in.Target == "":
		return nil, errors.New("choose both branches")
	case in.Source == in.Target:
		return nil, errors.New("choose two different branches")
	}
	var res *daemonpb.ProxyMergeRequestCreateResponse
	err := s.call(ctx, root, func(c daemonpb.NipaDaemonClient) (err error) {
		res, err = c.ProxyMergeRequestCreate(ctx, &daemonpb.ProxyMergeRequestCreateRequest{Root: root, Request: &pb.CreateMergeRequestRequest{
			Title: in.Title, Description: strings.TrimSpace(in.Description), SourceBranch: in.Source, TargetBranch: in.Target, Draft: in.Draft,
		}})
		return err
	})
	if err != nil {
		return nil, err
	}
	mr := toMergeRequest(res.GetResponse().GetMergeRequest())
	return &mr, nil
}

// EditMergeRequest changes the title and description; empty keeps a field.
func (s *Service) EditMergeRequest(ctx context.Context, root string, number int64, title, description string) (*MergeRequest, error) {
	return s.updateMergeRequest(ctx, root, &pb.UpdateMergeRequestRequest{Number: number, Title: strings.TrimSpace(title), Description: strings.TrimSpace(description)})
}

// SetDraft marks a request as a draft or ready for review.
func (s *Service) SetDraft(ctx context.Context, root string, number int64, draft bool) (*MergeRequest, error) {
	return s.updateMergeRequest(ctx, root, &pb.UpdateMergeRequestRequest{Number: number, Draft: &draft})
}

func (s *Service) updateMergeRequest(ctx context.Context, root string, req *pb.UpdateMergeRequestRequest) (*MergeRequest, error) {
	var res *daemonpb.ProxyMergeRequestUpdateResponse
	err := s.call(ctx, root, func(c daemonpb.NipaDaemonClient) (err error) {
		res, err = c.ProxyMergeRequestUpdate(ctx, &daemonpb.ProxyMergeRequestUpdateRequest{Root: root, Request: req})
		return err
	})
	if err != nil {
		return nil, err
	}
	mr := toMergeRequest(res.GetResponse().GetMergeRequest())
	return &mr, nil
}

// MergeMergeRequest lands a request with a strategy: ff, merge, squash or
// rebase.
func (s *Service) MergeMergeRequest(ctx context.Context, root string, number int64, strategy string, deleteSource bool) (*MergeRequestDetail, error) {
	var res *daemonpb.ProxyMergeRequestMergeResponse
	err := s.call(ctx, root, func(c daemonpb.NipaDaemonClient) (err error) {
		res, err = c.ProxyMergeRequestMerge(ctx, &daemonpb.ProxyMergeRequestMergeRequest{Root: root, Request: &pb.MergeMergeRequestRequest{Number: number, Strategy: strategy, DeleteSource: deleteSource}})
		return err
	})
	if err != nil {
		return nil, err
	}
	r := res.GetResponse()
	return &MergeRequestDetail{Request: toMergeRequest(r.GetMergeRequest()), Mergeability: toMergeability(r.GetMergeability()), Outstanding: []string{}}, nil
}

// CloseMergeRequest closes a request without merging; Reopen undoes it.
func (s *Service) CloseMergeRequest(ctx context.Context, root string, number int64) error {
	return s.call(ctx, root, func(c daemonpb.NipaDaemonClient) error {
		_, err := c.ProxyMergeRequestClose(ctx, &daemonpb.ProxyMergeRequestCloseRequest{Root: root, Request: &pb.CloseMergeRequestRequest{Number: number}})
		return err
	})
}

// ReopenMergeRequest reopens a closed request.
func (s *Service) ReopenMergeRequest(ctx context.Context, root string, number int64) error {
	return s.call(ctx, root, func(c daemonpb.NipaDaemonClient) error {
		_, err := c.ProxyMergeRequestReopen(ctx, &daemonpb.ProxyMergeRequestReopenRequest{Root: root, Request: &pb.ReopenMergeRequestRequest{Number: number}})
		return err
	})
}

// SubmitReview approves, requests changes or comments on a request.
func (s *Service) SubmitReview(ctx context.Context, root string, number int64, state, body string) error {
	switch state {
	case "approved", "changes_requested", "commented":
	default:
		return errors.New("unknown review decision " + state)
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return errors.New("write a comment")
	}
	return s.call(ctx, root, func(c daemonpb.NipaDaemonClient) error {
		_, err := c.ProxyMergeRequestSubmitReview(ctx, &daemonpb.ProxyMergeRequestSubmitReviewRequest{Root: root, Request: &pb.SubmitMergeRequestReviewRequest{Number: number, State: state, Body: body}})
		return err
	})
}

// Comment starts a thread in the conversation or on a diff line.
func (s *Service) Comment(ctx context.Context, root string, number int64, in CommentInput) (*Thread, error) {
	in.Body = strings.TrimSpace(in.Body)
	if in.Body == "" {
		return nil, errors.New("write a comment")
	}
	req := &pb.AddMergeRequestCommentRequest{Number: number, FilePath: in.Path, Body: in.Body}
	if in.Path != "" {
		switch {
		case in.NewLine > 0:
			req.NewLine = ptr(in.NewLine)
		case in.OldLine > 0:
			req.OldLine = ptr(in.OldLine)
		default:
			return nil, errors.New("choose a line to comment on")
		}
	}
	var res *daemonpb.ProxyMergeRequestAddCommentResponse
	err := s.call(ctx, root, func(c daemonpb.NipaDaemonClient) (err error) {
		res, err = c.ProxyMergeRequestAddComment(ctx, &daemonpb.ProxyMergeRequestAddCommentRequest{Root: root, Request: req})
		return err
	})
	if err != nil {
		return nil, err
	}
	t := toThread(res.GetResponse().GetThread())
	return &t, nil
}

// Reply answers a thread.
func (s *Service) Reply(ctx context.Context, root string, number int64, threadID, body string) (*Comment, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, errors.New("write a reply")
	}
	var res *daemonpb.ProxyMergeRequestReplyResponse
	err := s.call(ctx, root, func(c daemonpb.NipaDaemonClient) (err error) {
		res, err = c.ProxyMergeRequestReply(ctx, &daemonpb.ProxyMergeRequestReplyRequest{Root: root, Request: &pb.ReplyMergeRequestThreadRequest{Number: number, ThreadId: threadID, Body: body}})
		return err
	})
	if err != nil {
		return nil, err
	}
	c := toComment(res.GetResponse().GetComment())
	return &c, nil
}

// ResolveThread resolves a thread, or reopens it with resolved false.
func (s *Service) ResolveThread(ctx context.Context, root string, number int64, threadID string, resolved bool) error {
	return s.call(ctx, root, func(c daemonpb.NipaDaemonClient) error {
		_, err := c.ProxyMergeRequestResolve(ctx, &daemonpb.ProxyMergeRequestResolveRequest{Root: root, Request: &pb.ResolveMergeRequestThreadRequest{Number: number, ThreadId: threadID, Resolved: resolved}})
		return err
	})
}
