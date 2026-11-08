package mcp

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/output"
	"github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/diff"
)

func addWriteTools(srv *mcp.Server, s *server) {
	if s.allowed(CapStage) {
		mcp.AddTool(srv, &mcp.Tool{
			Name:        "nipa_add",
			Description: "Stage files or directories for the next push. Returns the status after staging.",
			Annotations: nonDestructive(),
		}, s.add)
		mcp.AddTool(srv, &mcp.Tool{
			Name:        "nipa_restore",
			Description: "Discard unpushed changes to files or directories by restoring the last synced version. Paths with staged changes are refused. Returns the status after restoring.",
			Annotations: destructive(),
		}, s.restore)
	}
	if s.allowed(CapPush) {
		mcp.AddTool(srv, &mcp.Tool{
			Name:        "nipa_push",
			Description: "Commit the staged changes and upload them to the server, then return the new head commit.",
			Annotations: destructive(),
		}, s.push)
	}
	if s.allowed(CapSync) {
		mcp.AddTool(srv, &mcp.Tool{
			Name:        "nipa_update",
			Description: "Bring the working copy up to date with the server head of its branch (or re-sync the pinned tag while detached).",
			Annotations: destructive(),
		}, s.update)
		mcp.AddTool(srv, &mcp.Tool{
			Name:        "nipa_switch",
			Description: "Switch the working copy to another branch, or detach it at a release tag.",
			Annotations: destructive(),
		}, s.switchTo)
	}
	if s.allowed(CapBranch) {
		mcp.AddTool(srv, &mcp.Tool{
			Name:        "nipa_branch_create",
			Description: "Create a branch on the server and point the local working copy at it.",
			Annotations: nonDestructive(),
		}, s.branchCreate)
		mcp.AddTool(srv, &mcp.Tool{
			Name:        "nipa_tag_create",
			Description: "Create a release tag at the pinned HEAD commit, a branch head or an explicit commit.",
			Annotations: nonDestructive(),
		}, s.tagCreate)
	}
	if s.allowed(CapLock) {
		mcp.AddTool(srv, &mcp.Tool{
			Name:        "nipa_lock",
			Description: "Lock a binary file or directory prefix so only the holder can change it. Binary changes must be locked before they can land.",
			Annotations: nonDestructive(),
		}, s.lock)
		mcp.AddTool(srv, &mcp.Tool{
			Name:        "nipa_unlock",
			Description: "Release a binary file lock held by the current user.",
			Annotations: destructive(),
		}, s.unlock)
	}
	if s.allowed(CapMerge) {
		mcp.AddTool(srv, &mcp.Tool{
			Name:        "nipa_merge",
			Description: "Merge another branch into the current branch (fast-forward or merge commit), or abort a merge stopped on conflicts. Conflicting files are reported; resolve them with nipa_resolve_conflict and push.",
			Annotations: destructive(),
		}, s.merge)
		mcp.AddTool(srv, &mcp.Tool{
			Name:        "nipa_resolve_conflict",
			Description: "Settle one conflicted file of a pending merge or revert: ours, theirs, content (write the given text), working (keep the file on disk) or delete. The outcome is staged.",
			Annotations: destructive(),
		}, s.resolveConflict)
	}
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

type restoreInput struct {
	Repo  string   `json:"repo,omitempty" jsonschema:"Path inside a nipa working copy (defaults to the server working directory)"`
	Paths []string `json:"paths" jsonschema:"Tracked file or directory paths to restore"`
}

func (s *server) restore(ctx context.Context, _ *mcp.CallToolRequest, in restoreInput) (*mcp.CallToolResult, output.Status, error) {
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
	if s.opts.NewUseCases != nil {
		uc := s.opts.NewUseCases()
		defer uc.close()
		if dl, ok := uc.Connector.(usecase.ContentDownloader); ok {
			wc.WithDownloader(uc.Auth, dl)
		}
	}
	if err := wc.Restore(ctx, in.Paths); err != nil {
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

type headOutput struct {
	Branch   string `json:"branch,omitempty"`
	Tag      string `json:"tag,omitempty"`
	CommitID string `json:"commit_id,omitempty"`
}

func currentHead(root string) headOutput {
	out := headOutput{}
	if cfg, err := loadConfig(root); err == nil {
		out.Branch = cfg.Branch
		if cfg.Head != nil {
			out.Tag = cfg.Head.Name
		}
	}
	if commit, err := loadPinnedCommit(root); err == nil && commit != nil {
		out.CommitID = commit.CommitID
	}
	return out
}

func (s *server) push(ctx context.Context, req *mcp.CallToolRequest, in pushInput) (*mcp.CallToolResult, headOutput, error) {
	var zero headOutput
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	root, uc, err := s.open(in.Repo)
	if err != nil {
		return nil, zero, toolError(err)
	}
	defer uc.close()
	if uc.Push == nil {
		return nil, zero, notConfigured("push is")
	}
	if err := uc.Push.Run(ctx, root, s.withAgentTrailer(in.Message, req)); err != nil {
		return nil, zero, toolError(err)
	}
	return nil, currentHead(root), nil
}

type updateInput struct {
	Repo string `json:"repo,omitempty" jsonschema:"Path inside a nipa working copy (defaults to the server working directory)"`
}

func (s *server) update(ctx context.Context, _ *mcp.CallToolRequest, in updateInput) (*mcp.CallToolResult, headOutput, error) {
	var zero headOutput
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	root, uc, err := s.open(in.Repo)
	if err != nil {
		return nil, zero, toolError(err)
	}
	defer uc.close()
	if uc.Update == nil {
		return nil, zero, notConfigured("update is")
	}
	if err := uc.Update.Run(ctx, root); err != nil {
		return nil, zero, toolError(err)
	}
	return nil, currentHead(root), nil
}

type switchInput struct {
	Repo   string `json:"repo,omitempty" jsonschema:"Path inside a nipa working copy (defaults to the server working directory)"`
	Branch string `json:"branch,omitempty" jsonschema:"Branch to switch to"`
	Tag    string `json:"tag,omitempty" jsonschema:"Release tag to detach at (instead of a branch)"`
}

func (s *server) switchTo(ctx context.Context, _ *mcp.CallToolRequest, in switchInput) (*mcp.CallToolResult, headOutput, error) {
	var zero headOutput
	branch, tag := strings.TrimSpace(in.Branch), strings.TrimSpace(in.Tag)
	if (branch == "") == (tag == "") {
		return nil, zero, toolError(domain.NewUserError("pass exactly one of branch or tag"))
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	root, uc, err := s.open(in.Repo)
	if err != nil {
		return nil, zero, toolError(err)
	}
	defer uc.close()
	if uc.Update == nil {
		return nil, zero, notConfigured("switch is")
	}
	if tag != "" {
		err = uc.Update.SwitchTag(ctx, root, tag)
	} else {
		err = uc.Update.Switch(ctx, root, branch)
	}
	if err != nil {
		return nil, zero, toolError(err)
	}
	return nil, currentHead(root), nil
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

type tagCreateInput struct {
	Repo    string `json:"repo,omitempty" jsonschema:"Path inside a nipa working copy (defaults to the server working directory)"`
	Name    string `json:"name" jsonschema:"Name of the tag to create"`
	Message string `json:"message,omitempty" jsonschema:"Optional annotation message"`
	Branch  string `json:"branch,omitempty" jsonschema:"Tag the head of this branch instead of the pinned HEAD commit"`
	Commit  string `json:"commit,omitempty" jsonschema:"Tag this base36 commit id or hex hash instead of the pinned HEAD commit"`
}

func (s *server) tagCreate(ctx context.Context, _ *mcp.CallToolRequest, in tagCreateInput) (*mcp.CallToolResult, output.Tag, error) {
	var zero output.Tag
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	root, uc, err := s.open(in.Repo)
	if err != nil {
		return nil, zero, toolError(err)
	}
	defer uc.close()
	if uc.Tag == nil {
		return nil, zero, notConfigured("tags are")
	}
	tag, err := uc.Tag.Create(ctx, root, in.Name, in.Message, usecase.TagTarget{Branch: in.Branch, Commit: in.Commit})
	if err != nil {
		return nil, zero, toolError(err)
	}
	return nil, output.NewTag(tag), nil
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
	root, uc, err := s.open(in.Repo)
	if err != nil {
		return nil, zero, toolError(err)
	}
	defer uc.close()
	if uc.Lock == nil {
		return nil, zero, notConfigured("locks are")
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
	root, uc, err := s.open(in.Repo)
	if err != nil {
		return nil, zero, toolError(err)
	}
	defer uc.close()
	if uc.Lock == nil {
		return nil, zero, notConfigured("locks are")
	}
	if err := uc.Lock.Unlock(ctx, root, in.Path, in.Branch); err != nil {
		return nil, zero, toolError(err)
	}
	return nil, unlockedOutput{Path: in.Path}, nil
}

type mergeInput struct {
	Repo    string `json:"repo,omitempty" jsonschema:"Path inside a nipa working copy (defaults to the server working directory)"`
	Source  string `json:"source,omitempty" jsonschema:"Branch to merge into the current branch (required unless abort)"`
	Abort   bool   `json:"abort,omitempty" jsonschema:"Cancel a merge that stopped on conflicts and restore the working copy"`
	FFOnly  bool   `json:"ff_only,omitempty" jsonschema:"Fail instead of creating a merge commit"`
	NoFF    bool   `json:"no_ff,omitempty" jsonschema:"Create a merge commit even when a fast-forward is possible"`
	Message string `json:"message,omitempty" jsonschema:"Message for the merge commit"`
}

type mergeOutput struct {
	UpToDate       bool     `json:"up_to_date,omitempty"`
	FastForwarded  bool     `json:"fast_forwarded,omitempty"`
	MergeCommitted bool     `json:"merge_committed,omitempty"`
	Aborted        bool     `json:"aborted,omitempty"`
	Conflicts      []string `json:"conflicts,omitempty"`
	Next           string   `json:"next,omitempty"`
}

func (s *server) merge(ctx context.Context, _ *mcp.CallToolRequest, in mergeInput) (*mcp.CallToolResult, mergeOutput, error) {
	var zero mergeOutput
	source := strings.TrimSpace(in.Source)
	if !in.Abort && source == "" {
		return nil, zero, toolError(domain.NewUserError("a source branch to merge is required (or set abort)"))
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	root, uc, err := s.open(in.Repo)
	if err != nil {
		return nil, zero, toolError(err)
	}
	defer uc.close()
	if uc.Merge == nil {
		return nil, zero, notConfigured("merge is")
	}
	outcome, err := uc.Merge.Run(ctx, root, source, usecase.MergeOptions{
		Abort:   in.Abort,
		FFOnly:  in.FFOnly,
		NoFF:    in.NoFF,
		Message: in.Message,
	})
	if err != nil {
		return nil, zero, toolError(err)
	}
	out := mergeOutput{Aborted: in.Abort}
	if outcome != nil {
		out.UpToDate = outcome.UpToDate
		out.FastForwarded = outcome.FastForwarded
		out.MergeCommitted = outcome.MergeCommitted
		out.Conflicts = outcome.Conflicts
	}
	if len(out.Conflicts) > 0 {
		out.Next = "resolve each file with nipa_resolve_conflict, then nipa_push (or nipa_merge with abort)"
	}
	return nil, out, nil
}

type resolveConflictInput struct {
	Repo    string `json:"repo,omitempty" jsonschema:"Path inside a nipa working copy (defaults to the server working directory)"`
	Path    string `json:"path" jsonschema:"Repo-relative path of the conflicted file"`
	Choice  string `json:"choice" jsonschema:"ours, theirs, content, working or delete"`
	Content string `json:"content,omitempty" jsonschema:"Resolved file text when choice is content (must not contain conflict markers)"`
}

func (s *server) resolveConflict(ctx context.Context, _ *mcp.CallToolRequest, in resolveConflictInput) (*mcp.CallToolResult, output.Status, error) {
	var zero output.Status
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	wc, lr, root, cleanup, err := s.localWorkingCopy(in.Repo)
	if err != nil {
		return nil, zero, toolError(err)
	}
	defer cleanup()
	c := usecase.NewConflicts(lr, wc, root)
	choice := strings.ToLower(strings.TrimSpace(in.Choice))
	switch choice {
	case "content":
		err = c.Resolve(ctx, in.Path, usecase.Resolution{Content: strings.NewReader(in.Content)})
	case "working":
		err = c.Resolve(ctx, in.Path, usecase.Resolution{})
	case "delete":
		err = c.Resolve(ctx, in.Path, usecase.Resolution{Delete: true})
	case "ours", "theirs":
		err = s.takeVersion(ctx, c, root, in.Path, choice == "theirs")
	default:
		err = domain.NewUserError("choice must be one of ours, theirs, content, working, delete")
	}
	if err != nil {
		return nil, zero, toolError(err)
	}
	st, err := wc.Status(ctx)
	if err != nil {
		return nil, zero, toolError(err)
	}
	return nil, output.NewStatus(st), nil
}

func (s *server) takeVersion(ctx context.Context, c *usecase.Conflicts, root, path string, theirs bool) error {
	set, err := c.List()
	if err != nil {
		return err
	}
	if set == nil {
		return domain.NewUserError("no merge or revert is waiting")
	}
	rev := set.Ours.ID
	if theirs {
		rev = set.Theirs.ID
	}
	if rev == "" {
		return c.Resolve(ctx, path, usecase.Resolution{Delete: true})
	}
	uc, err := s.useCases()
	if err != nil {
		return err
	}
	defer uc.close()
	if uc.Diff == nil {
		return domain.NewUserError("diff is not configured")
	}
	err = uc.Diff.ReadBlob(ctx, root, usecase.BlobRequest{Source: usecase.BlobRevision, Revision: rev, Path: path}, func(entry diff.Entry, r io.Reader) error {
		return c.Resolve(ctx, path, usecase.Resolution{Content: r, AllowMarkers: true, Mode: entry.Mode})
	})
	if errors.Is(err, usecase.ErrFileMissing) {
		return c.Resolve(ctx, path, usecase.Resolution{Delete: true})
	}
	return err
}
