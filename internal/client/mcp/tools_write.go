package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/output"
	"github.com/nipalab/nipa/internal/client/usecase"
)

func addWriteTools(srv *mcp.Server, s *server) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "nipa_add",
		Description: "Stage files or directories for the next push. Returns the status after staging.",
	}, s.add)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "nipa_push",
		Description: "Commit the staged changes and upload them to the server, then return the new head commit.",
	}, s.push)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "nipa_branch_create",
		Description: "Create a branch on the server and point the local working copy at it.",
	}, s.branchCreate)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "nipa_lock",
		Description: "Lock a binary file or directory prefix so only the holder can change it. Binary changes must be locked before they can land.",
	}, s.lock)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "nipa_unlock",
		Description: "Release a binary file lock held by the current user.",
	}, s.unlock)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "nipa_mr_create",
		Description: "Open a merge request from the current branch (or an explicit source) to the target branch.",
	}, s.mrCreate)
}

type addInput struct {
	Repo  string   `json:"repo,omitempty" jsonschema:"Path inside a nipa working copy (defaults to the server working directory)"`
	Paths []string `json:"paths" jsonschema:"File or directory paths to stage for the next push"`
}

func (s *server) add(ctx context.Context, _ *mcp.CallToolRequest, in addInput) (*mcp.CallToolResult, output.Status, error) {
	var zero output.Status
	if len(in.Paths) == 0 {
		return nil, zero, toolError(domain.NewUserError("at least one path is required"))
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	wc, cleanup, err := s.workingCopy(in.Repo)
	if err != nil {
		return nil, zero, toolError(err)
	}
	defer cleanup()
	if err := wc.Add(ctx, in.Paths); err != nil {
		return nil, zero, toolError(err)
	}
	st, err := wc.Status(ctx)
	if err != nil {
		return nil, zero, toolError(err)
	}
	return nil, output.NewStatus(st), nil
}

type pushInput struct {
	Repo    string `json:"repo,omitempty" jsonschema:"Path inside a nipa working copy (defaults to the server working directory)"`
	Message string `json:"message" jsonschema:"Commit message for the pushed change set"`
}

type pushOutput struct {
	Branch   string `json:"branch,omitempty"`
	CommitID string `json:"commit_id,omitempty"`
}

func (s *server) push(ctx context.Context, _ *mcp.CallToolRequest, in pushInput) (*mcp.CallToolResult, pushOutput, error) {
	var zero pushOutput
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	root, err := s.repoRoot(in.Repo)
	if err != nil {
		return nil, zero, toolError(err)
	}
	uc, err := s.useCases()
	if err != nil {
		return nil, zero, toolError(err)
	}
	defer uc.close()
	if uc.Push == nil {
		return nil, zero, toolError(domain.NewUserError("push is not configured"))
	}
	if err := uc.Push.Run(ctx, root, in.Message); err != nil {
		return nil, zero, toolError(err)
	}
	out := pushOutput{}
	if cfg, err := loadConfig(root); err == nil {
		out.Branch = cfg.Branch
	}
	if commit, err := loadPinnedCommit(root); err == nil {
		out.CommitID = commit.CommitID
	}
	return nil, out, nil
}

type branchCreateInput struct {
	Repo string `json:"repo,omitempty" jsonschema:"Path inside a nipa working copy (defaults to the server working directory)"`
	Name string `json:"name" jsonschema:"Name of the branch to create"`
}

type branchCreatedOutput struct {
	Name     string `json:"name"`
	CommitID string `json:"commit_id,omitempty"`
}

func (s *server) branchCreate(ctx context.Context, _ *mcp.CallToolRequest, in branchCreateInput) (*mcp.CallToolResult, branchCreatedOutput, error) {
	var zero branchCreatedOutput
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	root, _, url, uc, err := s.connectContext(ctx, in.Repo)
	if err != nil {
		return nil, zero, toolError(err)
	}
	defer uc.close()
	branch, err := uc.Repo.CreateBranch(ctx, root, url.Host, url.Org, url.Project, in.Name)
	if err != nil {
		return nil, zero, toolError(err)
	}
	out := branchCreatedOutput{Name: branch.Name}
	if branch.CommitID != nil {
		out.CommitID = branch.CommitID.Base36()
	}
	return nil, out, nil
}

type lockInput struct {
	Repo   string `json:"repo,omitempty" jsonschema:"Path inside a nipa working copy (defaults to the server working directory)"`
	Path   string `json:"path" jsonschema:"Repo-relative file or directory prefix to lock"`
	Branch string `json:"branch,omitempty" jsonschema:"Branch scope (defaults to the current branch; the default branch is project-global)"`
}

func (s *server) lock(ctx context.Context, _ *mcp.CallToolRequest, in lockInput) (*mcp.CallToolResult, output.Lock, error) {
	var zero output.Lock
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
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
	lock, err := uc.Lock.Lock(ctx, root, in.Path, in.Branch)
	if err != nil {
		return nil, zero, toolError(err)
	}
	return nil, output.NewLock(lock), nil
}

type unlockInput struct {
	Repo   string `json:"repo,omitempty" jsonschema:"Path inside a nipa working copy (defaults to the server working directory)"`
	Path   string `json:"path" jsonschema:"Repo-relative file or directory prefix to unlock"`
	Branch string `json:"branch,omitempty" jsonschema:"Branch scope (defaults to the current branch; the default branch is project-global)"`
}

type unlockedOutput struct {
	Path string `json:"path"`
}

func (s *server) unlock(ctx context.Context, _ *mcp.CallToolRequest, in unlockInput) (*mcp.CallToolResult, unlockedOutput, error) {
	var zero unlockedOutput
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
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
	if err := uc.Lock.Unlock(ctx, root, in.Path, in.Branch); err != nil {
		return nil, zero, toolError(err)
	}
	return nil, unlockedOutput{Path: in.Path}, nil
}

type mrCreateInput struct {
	Repo        string `json:"repo,omitempty" jsonschema:"Path inside a nipa working copy (defaults to the server working directory)"`
	Title       string `json:"title" jsonschema:"Merge request title"`
	Description string `json:"description,omitempty" jsonschema:"Merge request description"`
	Source      string `json:"source,omitempty" jsonschema:"Source branch (defaults to the current branch)"`
	Target      string `json:"target,omitempty" jsonschema:"Target branch (defaults to the project default branch)"`
}

func (s *server) mrCreate(ctx context.Context, _ *mcp.CallToolRequest, in mrCreateInput) (*mcp.CallToolResult, output.MergeRequest, error) {
	var zero output.MergeRequest
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
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
	mr, err := uc.MR.Create(ctx, root, usecase.CreateMergeRequestOptions{
		Title:       in.Title,
		Description: in.Description,
		Source:      in.Source,
		Target:      in.Target,
	})
	if err != nil {
		return nil, zero, toolError(err)
	}
	return nil, output.NewMergeRequest(mr), nil
}
