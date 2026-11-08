package mcp

import (
	"context"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/output"
	"github.com/nipalab/nipa/internal/client/usecase"
)

func addMRReadTools(srv *mcp.Server, s *server) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "nipa_mr_view",
		Description: "Show one merge request with its live mergeability (blocked_by), the reviews and the status checks of the current source head.",
		Annotations: readOnly(),
	}, s.mrView)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "nipa_mr_diff",
		Description: "Show the changes a merge request brings (merge base vs source head) as unified patch lines per file. Output is capped by max_bytes: truncated files are flagged and dropped files counted in omitted_files.",
		Annotations: readOnly(),
	}, s.mrDiff)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "nipa_mr_threads",
		Description: "List the conversation threads of a merge request: top-level and inline (file/line) comments with replies, resolved and outdated state.",
		Annotations: readOnly(),
	}, s.mrThreads)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "nipa_mr_commits",
		Description: "List the commits a merge request adds on top of its target, newest first.",
		Annotations: readOnly(),
	}, s.mrCommits)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "nipa_mr_timeline",
		Description: "List the activity timeline of a merge request (opened, pushed, reviews, review requests, merged, closed...).",
		Annotations: readOnly(),
	}, s.mrTimeline)
}

func addMRWriteTools(srv *mcp.Server, s *server) {
	if s.allowed(CapMR) {
		mcp.AddTool(srv, &mcp.Tool{
			Name:        "nipa_mr_create",
			Description: "Open a merge request from the current branch (or an explicit source) to the target branch.",
			Annotations: nonDestructive(),
		}, s.mrCreate)
		mcp.AddTool(srv, &mcp.Tool{
			Name:        "nipa_mr_update",
			Description: "Change the title or description of a merge request, or mark it draft (draft=true) or ready for review (draft=false).",
			Annotations: nonDestructive(),
		}, s.mrUpdate)
		mcp.AddTool(srv, &mcp.Tool{
			Name:        "nipa_mr_comment",
			Description: "Start a conversation thread on a merge request: top-level, or anchored to a diff line with file plus new_line (added/context line) or old_line (deleted line).",
			Annotations: nonDestructive(),
		}, s.mrComment)
		mcp.AddTool(srv, &mcp.Tool{
			Name:        "nipa_mr_reply",
			Description: "Reply to a merge request conversation thread.",
			Annotations: nonDestructive(),
		}, s.mrReply)
		mcp.AddTool(srv, &mcp.Tool{
			Name:        "nipa_mr_resolve",
			Description: "Mark a merge request thread resolved (or unresolved again).",
			Annotations: nonDestructive(),
		}, s.mrResolve)
		mcp.AddTool(srv, &mcp.Tool{
			Name:        "nipa_mr_review",
			Description: "Submit a review decision on a merge request: approved, changes_requested or commented, with a message.",
			Annotations: nonDestructive(),
		}, s.mrReview)
		mcp.AddTool(srv, &mcp.Tool{
			Name:        "nipa_mr_request_review",
			Description: "Ask a user (by id) to review a merge request.",
			Annotations: nonDestructive(),
		}, s.mrRequestReview)
		mcp.AddTool(srv, &mcp.Tool{
			Name:        "nipa_mr_check",
			Description: "Report a status check (pending, success, failed) for the commit the working copy is pinned to, or an explicit source commit.",
			Annotations: nonDestructive(),
		}, s.mrCheck)
	}
	if s.allowed(CapMerge) {
		mcp.AddTool(srv, &mcp.Tool{
			Name:        "nipa_mr_merge",
			Description: "Land a merge request with a strategy (ff, merge, squash, rebase; default ff), optionally deleting the source branch.",
			Annotations: destructive(),
		}, s.mrMerge)
	}
}

func mrRef(number int64) string {
	return strconv.FormatInt(number, 10)
}

func (s *server) openMR(repo string) (string, UseCases, error) {
	root, uc, err := s.open(repo)
	if err != nil {
		return "", UseCases{}, toolError(err)
	}
	if uc.MR == nil {
		uc.close()
		return "", UseCases{}, notConfigured("merge requests are")
	}
	return root, uc, nil
}

type mrInput struct {
	Repo   string `json:"repo,omitempty" jsonschema:"Path inside a nipa working copy (defaults to the server working directory)"`
	Number int64  `json:"number" jsonschema:"Merge request number"`
}

type mrViewOutput struct {
	MergeRequest output.MergeRequest          `json:"merge_request"`
	Mergeability *domain.Mergeability         `json:"mergeability,omitempty"`
	Reviews      []*domain.MergeRequestReview `json:"reviews"`
	Checks       []output.MergeRequestCheck   `json:"checks"`
}

func (s *server) mrView(ctx context.Context, _ *mcp.CallToolRequest, in mrInput) (*mcp.CallToolResult, mrViewOutput, error) {
	var zero mrViewOutput
	root, uc, err := s.openMR(in.Repo)
	if err != nil {
		return nil, zero, err
	}
	defer uc.close()
	id := mrRef(in.Number)
	mr, info, err := uc.MR.View(ctx, root, id)
	if err != nil {
		return nil, zero, toolError(err)
	}
	reviews, err := uc.MR.Reviews(ctx, root, id)
	if err != nil {
		return nil, zero, toolError(err)
	}
	checks, err := uc.MR.Checks(ctx, root, id)
	if err != nil {
		return nil, zero, toolError(err)
	}
	if reviews == nil {
		reviews = []*domain.MergeRequestReview{}
	}
	return nil, mrViewOutput{
		MergeRequest: output.NewMergeRequest(mr),
		Mergeability: info,
		Reviews:      reviews,
		Checks:       output.NewMergeRequestChecks(checks).Checks,
	}, nil
}

type mrDiffInput struct {
	Repo     string `json:"repo,omitempty" jsonschema:"Path inside a nipa working copy (defaults to the server working directory)"`
	Number   int64  `json:"number" jsonschema:"Merge request number"`
	MaxBytes int    `json:"max_bytes,omitempty" jsonschema:"Approximate output budget in bytes (default 262144, at most 2097152)"`
}

func (s *server) mrDiff(ctx context.Context, _ *mcp.CallToolRequest, in mrDiffInput) (*mcp.CallToolResult, mrDiffOutput, error) {
	var zero mrDiffOutput
	budget, err := diffBudget(in.MaxBytes)
	if err != nil {
		return nil, zero, err
	}
	root, uc, err := s.openMR(in.Repo)
	if err != nil {
		return nil, zero, err
	}
	defer uc.close()
	files, err := uc.MR.Diff(ctx, root, mrRef(in.Number))
	if err != nil {
		return nil, zero, toolError(err)
	}
	return nil, capMRDiff(files, budget), nil
}

func (s *server) mrThreads(ctx context.Context, _ *mcp.CallToolRequest, in mrInput) (*mcp.CallToolResult, output.MergeRequestThreads, error) {
	var zero output.MergeRequestThreads
	root, uc, err := s.openMR(in.Repo)
	if err != nil {
		return nil, zero, err
	}
	defer uc.close()
	threads, err := uc.MR.Threads(ctx, root, mrRef(in.Number))
	if err != nil {
		return nil, zero, toolError(err)
	}
	if threads == nil {
		threads = []*domain.MergeRequestThread{}
	}
	return nil, output.MergeRequestThreads{Threads: threads}, nil
}

func (s *server) mrCommits(ctx context.Context, _ *mcp.CallToolRequest, in mrInput) (*mcp.CallToolResult, output.Log, error) {
	var zero output.Log
	root, uc, err := s.openMR(in.Repo)
	if err != nil {
		return nil, zero, err
	}
	defer uc.close()
	commits, err := uc.MR.Commits(ctx, root, mrRef(in.Number))
	if err != nil {
		return nil, zero, toolError(err)
	}
	return nil, output.NewLog(commits), nil
}

func (s *server) mrTimeline(ctx context.Context, _ *mcp.CallToolRequest, in mrInput) (*mcp.CallToolResult, output.MergeRequestTimeline, error) {
	var zero output.MergeRequestTimeline
	root, uc, err := s.openMR(in.Repo)
	if err != nil {
		return nil, zero, err
	}
	defer uc.close()
	items, err := uc.MR.Timeline(ctx, root, mrRef(in.Number))
	if err != nil {
		return nil, zero, toolError(err)
	}
	if items == nil {
		items = []*domain.MergeRequestTimelineItem{}
	}
	return nil, output.MergeRequestTimeline{Timeline: items}, nil
}

type mrCreateInput struct {
	Repo        string   `json:"repo,omitempty" jsonschema:"Path inside a nipa working copy (defaults to the server working directory)"`
	Title       string   `json:"title" jsonschema:"Merge request title"`
	Description string   `json:"description,omitempty" jsonschema:"Merge request description"`
	Source      string   `json:"source,omitempty" jsonschema:"Source branch (defaults to the current branch)"`
	Target      string   `json:"target,omitempty" jsonschema:"Target branch (defaults to the project default branch)"`
	Draft       bool     `json:"draft,omitempty" jsonschema:"Open the request as a draft"`
	Reviewers   []string `json:"reviewers,omitempty" jsonschema:"User ids asked to review the merge request"`
	Assignees   []string `json:"assignees,omitempty" jsonschema:"User ids assigned to the merge request"`
}

func (s *server) mrCreate(ctx context.Context, req *mcp.CallToolRequest, in mrCreateInput) (*mcp.CallToolResult, output.MergeRequest, error) {
	var zero output.MergeRequest
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	root, uc, err := s.openMR(in.Repo)
	if err != nil {
		return nil, zero, err
	}
	defer uc.close()
	mr, err := uc.MR.Create(ctx, root, usecase.CreateMergeRequestOptions{
		Title:       in.Title,
		Description: s.withAgentTrailer(in.Description, req),
		Source:      in.Source,
		Target:      in.Target,
		Draft:       in.Draft,
		Reviewers:   in.Reviewers,
		Assignees:   in.Assignees,
	})
	if err != nil {
		return nil, zero, toolError(err)
	}
	return nil, output.NewMergeRequest(mr), nil
}

type mrUpdateInput struct {
	Repo        string `json:"repo,omitempty" jsonschema:"Path inside a nipa working copy (defaults to the server working directory)"`
	Number      int64  `json:"number" jsonschema:"Merge request number"`
	Title       string `json:"title,omitempty" jsonschema:"New title"`
	Description string `json:"description,omitempty" jsonschema:"New description"`
	Draft       *bool  `json:"draft,omitempty" jsonschema:"true marks the request draft, false marks it ready for review"`
}

func (s *server) mrUpdate(ctx context.Context, req *mcp.CallToolRequest, in mrUpdateInput) (*mcp.CallToolResult, output.MergeRequest, error) {
	var zero output.MergeRequest
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	root, uc, err := s.openMR(in.Repo)
	if err != nil {
		return nil, zero, err
	}
	defer uc.close()
	mr, err := uc.MR.Update(ctx, root, mrRef(in.Number), in.Title, s.withAgentTrailer(in.Description, req), in.Draft)
	if err != nil {
		return nil, zero, toolError(err)
	}
	return nil, output.NewMergeRequest(mr), nil
}

type mrCommentInput struct {
	Repo    string `json:"repo,omitempty" jsonschema:"Path inside a nipa working copy (defaults to the server working directory)"`
	Number  int64  `json:"number" jsonschema:"Merge request number"`
	Body    string `json:"body" jsonschema:"Comment text"`
	File    string `json:"file,omitempty" jsonschema:"Repo-relative file to anchor the comment to (omit for a top-level comment)"`
	NewLine *int64 `json:"new_line,omitempty" jsonschema:"Line number on the source side (added or context line)"`
	OldLine *int64 `json:"old_line,omitempty" jsonschema:"Line number on the target side (deleted line)"`
}

func (s *server) mrComment(ctx context.Context, req *mcp.CallToolRequest, in mrCommentInput) (*mcp.CallToolResult, domain.MergeRequestThread, error) {
	var zero domain.MergeRequestThread
	if in.File == "" && (in.NewLine != nil || in.OldLine != nil) {
		return nil, zero, toolError(domain.NewUserError("new_line and old_line need a file"))
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	root, uc, err := s.openMR(in.Repo)
	if err != nil {
		return nil, zero, err
	}
	defer uc.close()
	thread, err := uc.MR.Comment(ctx, root, mrRef(in.Number), in.File, in.OldLine, in.NewLine, s.withAgentTrailer(in.Body, req))
	if err != nil {
		return nil, zero, toolError(err)
	}
	return nil, deref(thread), nil
}

type mrReplyInput struct {
	Repo     string `json:"repo,omitempty" jsonschema:"Path inside a nipa working copy (defaults to the server working directory)"`
	Number   int64  `json:"number" jsonschema:"Merge request number"`
	ThreadID string `json:"thread_id" jsonschema:"Thread id from nipa_mr_threads"`
	Body     string `json:"body" jsonschema:"Reply text"`
}

func (s *server) mrReply(ctx context.Context, req *mcp.CallToolRequest, in mrReplyInput) (*mcp.CallToolResult, domain.MergeRequestComment, error) {
	var zero domain.MergeRequestComment
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	root, uc, err := s.openMR(in.Repo)
	if err != nil {
		return nil, zero, err
	}
	defer uc.close()
	comment, err := uc.MR.Reply(ctx, root, mrRef(in.Number), in.ThreadID, s.withAgentTrailer(in.Body, req))
	if err != nil {
		return nil, zero, toolError(err)
	}
	return nil, deref(comment), nil
}

type mrResolveInput struct {
	Repo      string `json:"repo,omitempty" jsonschema:"Path inside a nipa working copy (defaults to the server working directory)"`
	Number    int64  `json:"number" jsonschema:"Merge request number"`
	ThreadID  string `json:"thread_id" jsonschema:"Thread id from nipa_mr_threads"`
	Unresolve bool   `json:"unresolve,omitempty" jsonschema:"Reopen a resolved thread instead"`
}

func (s *server) mrResolve(ctx context.Context, _ *mcp.CallToolRequest, in mrResolveInput) (*mcp.CallToolResult, domain.MergeRequestThread, error) {
	var zero domain.MergeRequestThread
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	root, uc, err := s.openMR(in.Repo)
	if err != nil {
		return nil, zero, err
	}
	defer uc.close()
	thread, err := uc.MR.Resolve(ctx, root, mrRef(in.Number), in.ThreadID, !in.Unresolve)
	if err != nil {
		return nil, zero, toolError(err)
	}
	return nil, deref(thread), nil
}

type mrReviewInput struct {
	Repo   string `json:"repo,omitempty" jsonschema:"Path inside a nipa working copy (defaults to the server working directory)"`
	Number int64  `json:"number" jsonschema:"Merge request number"`
	State  string `json:"state" jsonschema:"approved, changes_requested or commented"`
	Body   string `json:"body" jsonschema:"Review message"`
}

func (s *server) mrReview(ctx context.Context, req *mcp.CallToolRequest, in mrReviewInput) (*mcp.CallToolResult, domain.MergeRequestReview, error) {
	var zero domain.MergeRequestReview
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	root, uc, err := s.openMR(in.Repo)
	if err != nil {
		return nil, zero, err
	}
	defer uc.close()
	review, err := uc.MR.Review(ctx, root, mrRef(in.Number), in.State, s.withAgentTrailer(in.Body, req))
	if err != nil {
		return nil, zero, toolError(err)
	}
	return nil, deref(review), nil
}

type mrRequestReviewInput struct {
	Repo   string `json:"repo,omitempty" jsonschema:"Path inside a nipa working copy (defaults to the server working directory)"`
	Number int64  `json:"number" jsonschema:"Merge request number"`
	UserID string `json:"user_id" jsonschema:"Id of the user asked to review"`
}

func (s *server) mrRequestReview(ctx context.Context, _ *mcp.CallToolRequest, in mrRequestReviewInput) (*mcp.CallToolResult, domain.MergeRequestReviewRequest, error) {
	var zero domain.MergeRequestReviewRequest
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	root, uc, err := s.openMR(in.Repo)
	if err != nil {
		return nil, zero, err
	}
	defer uc.close()
	request, err := uc.MR.RequestReview(ctx, root, mrRef(in.Number), in.UserID)
	if err != nil {
		return nil, zero, toolError(err)
	}
	return nil, deref(request), nil
}

type mrCheckInput struct {
	Repo       string `json:"repo,omitempty" jsonschema:"Path inside a nipa working copy (defaults to the server working directory)"`
	Number     int64  `json:"number" jsonschema:"Merge request number"`
	Name       string `json:"name" jsonschema:"Check name"`
	State      string `json:"state" jsonschema:"pending, success or failed"`
	DetailsURL string `json:"details_url,omitempty" jsonschema:"Link to the check details"`
	Commit     string `json:"commit,omitempty" jsonschema:"Source commit the check ran against (defaults to the pinned commit)"`
}

func (s *server) mrCheck(ctx context.Context, _ *mcp.CallToolRequest, in mrCheckInput) (*mcp.CallToolResult, output.MergeRequestCheck, error) {
	var zero output.MergeRequestCheck
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	root, uc, err := s.openMR(in.Repo)
	if err != nil {
		return nil, zero, err
	}
	defer uc.close()
	check, err := uc.MR.ReportCheck(ctx, root, mrRef(in.Number), in.Name, in.State, in.DetailsURL, in.Commit)
	if err != nil {
		return nil, zero, toolError(err)
	}
	checks := output.NewMergeRequestChecks([]*domain.MergeRequestCheck{check}).Checks
	if len(checks) == 0 {
		return nil, zero, nil
	}
	return nil, checks[0], nil
}

type mrMergeInput struct {
	Repo         string `json:"repo,omitempty" jsonschema:"Path inside a nipa working copy (defaults to the server working directory)"`
	Number       int64  `json:"number" jsonschema:"Merge request number"`
	Strategy     string `json:"strategy,omitempty" jsonschema:"ff (default), merge, squash or rebase"`
	DeleteSource bool   `json:"delete_source,omitempty" jsonschema:"Delete the source branch after merging"`
}

type mrMergeOutput struct {
	MergeRequest output.MergeRequest  `json:"merge_request"`
	Mergeability *domain.Mergeability `json:"mergeability,omitempty"`
}

func (s *server) mrMerge(ctx context.Context, _ *mcp.CallToolRequest, in mrMergeInput) (*mcp.CallToolResult, mrMergeOutput, error) {
	var zero mrMergeOutput
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	root, uc, err := s.openMR(in.Repo)
	if err != nil {
		return nil, zero, err
	}
	defer uc.close()
	mr, info, err := uc.MR.Merge(ctx, root, mrRef(in.Number), in.Strategy, in.DeleteSource)
	if err != nil {
		return nil, zero, toolError(err)
	}
	return nil, mrMergeOutput{MergeRequest: output.NewMergeRequest(mr), Mergeability: info}, nil
}

func deref[T any](v *T) T {
	var zero T
	if v == nil {
		return zero
	}
	return *v
}
