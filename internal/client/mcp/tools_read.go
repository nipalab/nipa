package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/output"
	"github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/diff"
)

func addReadTools(srv *mcp.Server, s *server) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "nipa_status",
		Description: "Show the working copy status: staged (A), deleted (D), modified (M), untracked (?), missing (!) and conflicting (C) paths. Runs offline.",
		Annotations: readOnly(),
	}, s.status)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "nipa_diff",
		Description: "Show working-copy or revision changes with structured hunks. Runs offline without revisions; renames, binary and content-unavailable markers included.",
		Annotations: readOnly(),
	}, s.diff)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "nipa_log",
		Description: "Show the commit history of the current branch, newest first.",
		Annotations: readOnly(),
	}, s.log)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "nipa_branch_list",
		Description: "List all branches of the project with their head commit and protection state.",
		Annotations: readOnly(),
	}, s.branchList)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "nipa_mr_list",
		Description: "List merge requests of the project, optionally filtered by status (open, merged, closed).",
		Annotations: readOnly(),
	}, s.mrList)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "nipa_lock_list",
		Description: "List active binary file locks: path, scope (mainline or branch), holder and acquisition time.",
		Annotations: readOnly(),
	}, s.lockList)
}

type statusInput struct {
	Repo    string `json:"repo,omitempty" jsonschema:"Path inside a nipa working copy (defaults to the server working directory)"`
	NoCache bool   `json:"no_cache,omitempty" jsonschema:"Rehash every tracked file instead of trusting the stat cache"`
}

func (s *server) status(ctx context.Context, _ *mcp.CallToolRequest, in statusInput) (*mcp.CallToolResult, output.Status, error) {
	var zero output.Status
	wc, cleanup, err := s.workingCopy(in.Repo)
	if err != nil {
		return nil, zero, toolError(err)
	}
	defer cleanup()
	st, err := wc.Status(ctx, usecase.StatusOptions{NoCache: in.NoCache})
	if err != nil {
		return nil, zero, toolError(err)
	}
	return nil, output.NewStatus(st), nil
}

type diffInput struct {
	Repo              string   `json:"repo,omitempty" jsonschema:"Path inside a nipa working copy (defaults to the server working directory)"`
	Revisions         []string `json:"revisions,omitempty" jsonschema:"Up to two revisions: branch name, base36 commit id or HEAD/@; omit to compare the working tree against the last synced snapshot"`
	Paths             []string `json:"paths,omitempty" jsonschema:"Limit the diff to these repo-relative file or directory paths"`
	Staged            bool     `json:"staged,omitempty" jsonschema:"Only show changes staged for the next push"`
	MergeBase         bool     `json:"merge_base,omitempty" jsonschema:"Compare the merge base of two revisions against the second (three-dot diff)"`
	Unified           *int     `json:"unified,omitempty" jsonschema:"Lines of context around each hunk (default 3)"`
	IgnoreAllSpace    bool     `json:"ignore_all_space,omitempty" jsonschema:"Ignore all whitespace when comparing lines"`
	IgnoreSpaceChange bool     `json:"ignore_space_change,omitempty" jsonschema:"Ignore changes in the amount of whitespace"`
	NoCache           bool     `json:"no_cache,omitempty" jsonschema:"Read every working file instead of trusting the stat cache"`
}

func (s *server) diff(ctx context.Context, _ *mcp.CallToolRequest, in diffInput) (*mcp.CallToolResult, output.Diff, error) {
	var zero output.Diff
	if len(in.Revisions) > 2 {
		return nil, zero, toolError(domain.NewUserError("too many revisions (expected at most two)"))
	}
	if len(in.Revisions) > 0 && in.Staged {
		return nil, zero, toolError(domain.NewUserError("staged cannot be combined with revisions"))
	}
	if in.MergeBase && len(in.Revisions) != 2 {
		return nil, zero, toolError(domain.NewUserError("merge_base requires two revisions"))
	}
	contextLines := diff.DefaultContext
	if in.Unified != nil {
		if *in.Unified < 0 {
			return nil, zero, toolError(domain.NewUserError("unified must not be negative"))
		}
		contextLines = *in.Unified
	}
	root, err := s.repoRoot(in.Repo)
	if err != nil {
		return nil, zero, toolError(err)
	}
	uc, err := s.useCases()
	if err != nil {
		return nil, zero, toolError(err)
	}
	if uc.Diff == nil {
		return nil, zero, toolError(domain.NewUserError("diff is not configured"))
	}
	files, err := uc.Diff.Run(ctx, root, in.Revisions, usecase.DiffOptions{
		Staged:    in.Staged,
		Paths:     in.Paths,
		MergeBase: in.MergeBase,
		NoCache:   in.NoCache,
	})
	if err != nil {
		return nil, zero, toolError(err)
	}
	renderOpts := diff.Options{
		Context:           contextLines,
		IgnoreAllSpace:    in.IgnoreAllSpace,
		IgnoreSpaceChange: in.IgnoreSpaceChange,
	}
	files = diff.FilterIgnored(files, renderOpts)
	return nil, output.NewDiff(files, renderOpts), nil
}

type logInput struct {
	Repo  string `json:"repo,omitempty" jsonschema:"Path inside a nipa working copy (defaults to the server working directory)"`
	Limit int    `json:"limit,omitempty" jsonschema:"Maximum number of commits (0 = full history)"`
}

func (s *server) log(ctx context.Context, _ *mcp.CallToolRequest, in logInput) (*mcp.CallToolResult, output.Log, error) {
	var zero output.Log
	_, cfg, url, uc, err := s.connectContext(ctx, in.Repo)
	if err != nil {
		return nil, zero, toolError(err)
	}
	opts := []usecase.CommitLogOption{}
	if in.Limit > 0 {
		opts = append(opts, usecase.WithCommitLogMax(in.Limit))
	}
	entries, err := uc.Repo.Log(ctx, url.Host, url.Org, url.Project, cfg.Branch, opts...)
	if err != nil {
		return nil, zero, toolError(err)
	}
	return nil, output.NewLog(entries), nil
}

type branchListInput struct {
	Repo string `json:"repo,omitempty" jsonschema:"Path inside a nipa working copy (defaults to the server working directory)"`
}

func (s *server) branchList(ctx context.Context, _ *mcp.CallToolRequest, in branchListInput) (*mcp.CallToolResult, output.Branches, error) {
	var zero output.Branches
	_, cfg, url, uc, err := s.connectContext(ctx, in.Repo)
	if err != nil {
		return nil, zero, toolError(err)
	}
	branches, err := uc.Repo.ListBranches(ctx, url.Host, url.Org, url.Project)
	if err != nil {
		return nil, zero, toolError(err)
	}
	return nil, output.NewBranches(cfg.Branch, branches), nil
}

type mrListInput struct {
	Repo   string `json:"repo,omitempty" jsonschema:"Path inside a nipa working copy (defaults to the server working directory)"`
	Status string `json:"status,omitempty" jsonschema:"Filter by status: open, merged, closed"`
	Limit  int    `json:"limit,omitempty" jsonschema:"Maximum number of merge requests (default 50)"`
}

func (s *server) mrList(ctx context.Context, _ *mcp.CallToolRequest, in mrListInput) (*mcp.CallToolResult, output.MergeRequests, error) {
	var zero output.MergeRequests
	root, err := s.repoRoot(in.Repo)
	if err != nil {
		return nil, zero, toolError(err)
	}
	uc, err := s.useCases()
	if err != nil {
		return nil, zero, toolError(err)
	}
	if uc.MR == nil {
		return nil, zero, toolError(domain.NewUserError("merge requests are not configured"))
	}
	requests, err := uc.MR.List(ctx, root, in.Status, in.Limit)
	if err != nil {
		return nil, zero, toolError(err)
	}
	return nil, output.NewMergeRequests(requests), nil
}

type lockListInput struct {
	Repo string `json:"repo,omitempty" jsonschema:"Path inside a nipa working copy (defaults to the server working directory)"`
}

func (s *server) lockList(ctx context.Context, _ *mcp.CallToolRequest, in lockListInput) (*mcp.CallToolResult, output.Locks, error) {
	var zero output.Locks
	root, err := s.repoRoot(in.Repo)
	if err != nil {
		return nil, zero, toolError(err)
	}
	uc, err := s.useCases()
	if err != nil {
		return nil, zero, toolError(err)
	}
	if uc.Lock == nil {
		return nil, zero, toolError(domain.NewUserError("locks are not configured"))
	}
	locks, err := uc.Lock.List(ctx, root)
	if err != nil {
		return nil, zero, toolError(err)
	}
	return nil, output.NewLocks(locks), nil
}

// connectContext loads the working-copy config, connects the transport to the
// project host and returns the per-call usecase graph for the online tools.
func (s *server) connectContext(ctx context.Context, repo string) (string, *domain.Config, *domain.NipaUrl, UseCases, error) {
	root, err := s.repoRoot(repo)
	if err != nil {
		return "", nil, nil, UseCases{}, err
	}
	cfg, err := loadConfig(root)
	if err != nil {
		return "", nil, nil, UseCases{}, err
	}
	url, err := domain.ParseNipaUrl(cfg.Url)
	if err != nil {
		return "", nil, nil, UseCases{}, err
	}
	uc, err := s.useCases()
	if err != nil {
		return "", nil, nil, UseCases{}, err
	}
	if uc.Connector == nil || uc.Repo == nil {
		return "", nil, nil, UseCases{}, domain.NewUserError("server connection is not configured")
	}
	if err := uc.Connector.Connect(ctx, url.Host); err != nil {
		return "", nil, nil, UseCases{}, err
	}
	return root, cfg, url, uc, nil
}
