package mcp

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/output"
	"github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/diff"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

func addReadTools(srv *mcp.Server, s *server) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "nipa_status",
		Description: "Show the working copy status: staged (A), deleted (D), modified (M), untracked (?), missing (!) and conflicting (C) paths. Runs offline.",
		Annotations: readOnly(),
	}, s.status)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "nipa_diff",
		Description: "Show working-copy or revision changes with structured hunks. Runs offline without revisions; renames, binary and content-unavailable markers included. Output is capped by max_bytes: truncated files are flagged and dropped files counted in omitted_files.",
		Annotations: readOnly(),
	}, s.diff)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "nipa_log",
		Description: "Show the commit history of the current branch, newest first (default 50 commits, at most 500).",
		Annotations: readOnly(),
	}, s.log)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "nipa_branch_list",
		Description: "List all branches of the project with their head commit and protection state.",
		Annotations: readOnly(),
	}, s.branchList)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "nipa_mr_list",
		Description: "List merge requests of the project, optionally filtered by status (open, merged, closed). At most 200 per call.",
		Annotations: readOnly(),
	}, s.mrList)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "nipa_lock_list",
		Description: "List active binary file locks: path, scope (mainline or branch), holder and acquisition time.",
		Annotations: readOnly(),
	}, s.lockList)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "nipa_lock_query",
		Description: "Tell whether a file or directory is covered by a binary lock (exact path or a locking directory prefix) in the lock scope of the working copy branch (or the given branch), and who holds it. The default branch is guarded by mainline locks, other branches by their own locks; covering locks of other scopes are listed in other_scopes.",
		Annotations: readOnly(),
	}, s.lockQuery)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "nipa_tag_list",
		Description: "List the release tags of the project with the commit each one points at.",
		Annotations: readOnly(),
	}, s.tagList)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "nipa_conflicts",
		Description: "Describe the merge or revert waiting in the working copy: the base/ours/theirs revisions and each conflicted file (kind, binary, resolved). Runs offline; empty when nothing is pending.",
		Annotations: readOnly(),
	}, s.conflicts)
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
	MaxBytes          int      `json:"max_bytes,omitempty" jsonschema:"Approximate output budget in bytes (default 262144, at most 2097152)"`
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
	budget, err := diffBudget(in.MaxBytes)
	if err != nil {
		return nil, zero, err
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
	defer uc.close()
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
	return nil, capDiff(output.NewDiff(files, renderOpts), budget), nil
}

type logInput struct {
	Repo  string `json:"repo,omitempty" jsonschema:"Path inside a nipa working copy (defaults to the server working directory)"`
	Limit int    `json:"limit,omitempty" jsonschema:"Maximum number of commits (default 50, at most 500)"`
}

func (s *server) log(ctx context.Context, _ *mcp.CallToolRequest, in logInput) (*mcp.CallToolResult, output.Log, error) {
	var zero output.Log
	_, cfg, url, uc, err := s.connectContext(ctx, in.Repo)
	if err != nil {
		return nil, zero, toolError(err)
	}
	defer uc.close()
	limit := clampLimit(in.Limit, defaultLogLimit, maxLogLimit)
	entries, err := uc.Repo.Log(ctx, url.Host, url.Org, url.Project, cfg.Branch, usecase.WithCommitLogMax(limit))
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
	defer uc.close()
	branches, err := uc.Repo.ListBranches(ctx, url.Host, url.Org, url.Project)
	if err != nil {
		return nil, zero, toolError(err)
	}
	return nil, output.NewBranches(cfg.Branch, branches), nil
}

type mrListInput struct {
	Repo   string `json:"repo,omitempty" jsonschema:"Path inside a nipa working copy (defaults to the server working directory)"`
	Status string `json:"status,omitempty" jsonschema:"Filter by status: open, merged, closed"`
	Limit  int    `json:"limit,omitempty" jsonschema:"Maximum number of merge requests (default 50, at most 200)"`
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
	defer uc.close()
	if uc.MR == nil {
		return nil, zero, toolError(domain.NewUserError("merge requests are not configured"))
	}
	requests, _, err := uc.MR.List(ctx, root, usecase.ListMergeRequestOptions{
		Status: in.Status,
		Limit:  clampLimit(in.Limit, 50, maxMRListLimit),
	})
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
	defer uc.close()
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
		uc.close()
		return "", nil, nil, UseCases{}, domain.NewUserError("server connection is not configured")
	}
	if err := uc.Connector.Connect(ctx, url.Origin); err != nil {
		uc.close()
		return "", nil, nil, UseCases{}, err
	}
	return root, cfg, url, uc, nil
}

type lockQueryInput struct {
	Repo   string `json:"repo,omitempty" jsonschema:"Path inside a nipa working copy (defaults to the server working directory)"`
	Path   string `json:"path" jsonschema:"Repo-relative file or directory path to check"`
	Branch string `json:"branch,omitempty" jsonschema:"Branch whose lock scope to check (defaults to the working copy branch)"`
}

type lockQueryOutput struct {
	Path        string        `json:"path"`
	Branch      string        `json:"branch"`
	Scope       string        `json:"scope"`
	Locked      bool          `json:"locked"`
	Locks       []output.Lock `json:"locks"`
	OtherScopes []output.Lock `json:"other_scopes,omitempty"`
}

func (s *server) lockQuery(ctx context.Context, _ *mcp.CallToolRequest, in lockQueryInput) (*mcp.CallToolResult, lockQueryOutput, error) {
	var zero lockQueryOutput
	path := strings.Trim(strings.TrimSpace(in.Path), "/")
	if path == "" {
		return nil, zero, toolError(domain.NewUserError("a path is required"))
	}
	root, cfg, url, uc, err := s.connectContext(ctx, in.Repo)
	if err != nil {
		return nil, zero, toolError(err)
	}
	defer uc.close()
	if uc.Lock == nil {
		return nil, zero, notConfigured("locks are")
	}
	branch := strings.TrimSpace(in.Branch)
	if branch == "" {
		branch = cfg.Branch
	}
	branches, err := uc.Repo.ListBranches(ctx, url.Host, url.Org, url.Project)
	if err != nil {
		return nil, zero, toolError(err)
	}
	mainline := false
	for _, b := range branches {
		if b != nil && b.Name == branch {
			mainline = b.IsDefault
		}
	}
	locks, err := uc.Lock.List(ctx, root)
	if err != nil {
		return nil, zero, toolError(err)
	}
	out := lockQueryOutput{Path: path, Branch: branch, Scope: "branch", Locks: []output.Lock{}}
	if mainline {
		out.Scope = "mainline"
	}
	for _, lock := range locks {
		if lock == nil || !serverDomain.PrefixCovers(strings.Trim(lock.Path, "/"), path) {
			continue
		}
		if lockInScope(lock, branch, mainline) {
			out.Locks = append(out.Locks, output.NewLock(lock))
		} else {
			out.OtherScopes = append(out.OtherScopes, output.NewLock(lock))
		}
	}
	out.Locked = len(out.Locks) > 0
	return nil, out, nil
}

// lockInScope mirrors the server rule: the default branch is guarded by the
// project-global (mainline) locks, any other branch only by its own locks.
func lockInScope(lock *domain.FileLock, branch string, mainline bool) bool {
	if mainline {
		return lock.Global
	}
	return !lock.Global && lock.Branch == branch
}

type tagListInput struct {
	Repo string `json:"repo,omitempty" jsonschema:"Path inside a nipa working copy (defaults to the server working directory)"`
}

func (s *server) tagList(ctx context.Context, _ *mcp.CallToolRequest, in tagListInput) (*mcp.CallToolResult, output.Tags, error) {
	var zero output.Tags
	root, uc, err := s.open(in.Repo)
	if err != nil {
		return nil, zero, toolError(err)
	}
	defer uc.close()
	if uc.Tag == nil {
		return nil, zero, notConfigured("tags are")
	}
	tags, err := uc.Tag.List(ctx, root)
	if err != nil {
		return nil, zero, toolError(err)
	}
	return nil, output.NewTags(tags), nil
}

type conflictsInput struct {
	Repo string `json:"repo,omitempty" jsonschema:"Path inside a nipa working copy (defaults to the server working directory)"`
}

type conflictRevision struct {
	ID    string `json:"id,omitempty"`
	Label string `json:"label,omitempty"`
}

type conflictFile struct {
	Path     string `json:"path"`
	Kind     string `json:"kind,omitempty"`
	Binary   bool   `json:"binary,omitempty"`
	Resolved bool   `json:"resolved,omitempty"`
}

type conflictsOutput struct {
	Op     string           `json:"op,omitempty"`
	Base   conflictRevision `json:"base"`
	Ours   conflictRevision `json:"ours"`
	Theirs conflictRevision `json:"theirs"`
	Files  []conflictFile   `json:"files"`
}

func newConflictsOutput(set *domain.ConflictSet) conflictsOutput {
	out := conflictsOutput{Files: []conflictFile{}}
	if set == nil {
		return out
	}
	rev := func(r domain.ConflictRevision) conflictRevision { return conflictRevision{ID: r.ID, Label: r.Label} }
	out.Op, out.Base, out.Ours, out.Theirs = set.Op, rev(set.Base), rev(set.Ours), rev(set.Theirs)
	for _, f := range set.Files {
		out.Files = append(out.Files, conflictFile{Path: f.Path, Kind: f.Kind, Binary: f.Binary, Resolved: f.Resolved})
	}
	return out
}

func (s *server) conflicts(_ context.Context, _ *mcp.CallToolRequest, in conflictsInput) (*mcp.CallToolResult, conflictsOutput, error) {
	var zero conflictsOutput
	wc, lr, root, cleanup, err := s.localWorkingCopy(in.Repo)
	if err != nil {
		return nil, zero, toolError(err)
	}
	defer cleanup()
	set, err := usecase.NewConflicts(lr, wc, root).List()
	if err != nil {
		return nil, zero, toolError(err)
	}
	return nil, newConflictsOutput(set), nil
}
