package usecase

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/merge"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

const revertMaxTargets = 16

type revertClient interface {
	Connect(ctx context.Context, host string) error
	GetCommit(ctx context.Context, org, project, commitID string) (*domain.CommitDetail, error)
	WalkCommits(ctx context.Context, org, project, startCommitID, stopCommitID string, limit int) ([]*domain.CommitWalkEntry, error)
	GetBranchByName(ctx context.Context, org, project, name string) (*serverDomain.Branch, error)
	GetTreeNodeManifest(ctx context.Context, org, project, branch string, paths []string) (*serverDomain.TreeNode, error)
	DownloadChunks(ctx context.Context, scope domain.ChunkScope, hashes []serverDomain.Hash, onChunk func(h serverDomain.Hash, data []byte) error) error
}

type revertLocalRepo interface {
	workingCopyLocalRepo
	Init(target string) error
	LoadConfig() (*domain.Config, error)
	ListStaged() ([]string, error)
	LoadCommit() (*domain.LocalCommit, error)
	LoadMergeState() (*domain.MergeState, error)
	LoadRevertState() (*domain.RevertState, error)
	SaveRevertState(state *domain.RevertState) error
	ClearRevertState() error
	ClearStaged() error
	SaveTree(root *serverDomain.TreeNode) error
	SaveCommit(commitID, commitHash string) error
	StageAdd(path string) error
}

type RevertOptions struct {
	Abort    bool
	Continue bool
	Skip     bool
	NoCommit bool
	Mainline int
	Message  string
}

type RevertOutcome struct {
	Committed bool
	NoChange  bool
	Conflicts []string
}

type Revert struct {
	auth      *Auth
	client    revertClient
	localRepo revertLocalRepo
	push      *Push
}

func NewRevert(auth *Auth, client revertClient, localRepo revertLocalRepo, push *Push) *Revert {
	return &Revert{
		auth:      auth,
		client:    client,
		localRepo: localRepo,
		push:      push,
	}
}

func (r *Revert) Run(ctx context.Context, root, target string, opts RevertOptions, progress ...UploadProgress) (*RevertOutcome, error) {
	if err := r.localRepo.Init(root); err != nil {
		return nil, err
	}
	cfg, err := r.localRepo.LoadConfig()
	if err != nil {
		return nil, err
	}
	nipaUrl, err := domain.ParseNipaUrl(cfg.Url)
	if err != nil {
		return nil, err
	}
	if nipaUrl.Path != "" || len(cfg.Sparse) > 0 {
		return nil, domain.NewUserError("reverting in a sparse or subdirectory clone is not supported yet")
	}
	if opts.Mainline != 0 && opts.Mainline != 1 && opts.Mainline != 2 {
		return nil, domain.NewUserError("--mainline must be 1 or 2")
	}

	switch {
	case opts.Abort:
		return r.abort(ctx, root, nipaUrl, cfg.Branch)
	case opts.Continue:
		return r.resume(ctx, root, nipaUrl, cfg.Branch, progress...)
	case opts.Skip:
		return r.skip(ctx, root, nipaUrl, cfg.Branch, progress...)
	}

	if strings.TrimSpace(target) == "" {
		return nil, domain.NewUserError("a commit to revert is required")
	}
	if err := r.client.Connect(ctx, nipaUrl.Host); err != nil {
		return nil, err
	}
	if err := r.auth.MakeSureLoggedIn(ctx, nipaUrl.Host); err != nil {
		return nil, err
	}
	if err := r.checkNoPendingOperations(); err != nil {
		return nil, err
	}

	refs, err := r.resolveTargets(ctx, nipaUrl, target, opts.Mainline)
	if err != nil {
		return nil, err
	}
	message := strings.TrimSpace(opts.Message)
	if message != "" && len(refs) > 1 {
		return nil, domain.NewUserError("--message can only be used when reverting a single commit")
	}

	head, err := r.client.GetBranchByName(ctx, nipaUrl.Org, nipaUrl.Project, cfg.Branch)
	if err != nil {
		return nil, err
	}
	headTree, err := r.client.GetTreeNodeManifest(ctx, nipaUrl.Org, nipaUrl.Project, cfg.Branch, nil)
	if err != nil {
		return nil, err
	}
	if headTree == nil {
		return nil, domain.NewUserError(fmt.Sprintf("branch %q has no commits to revert onto", cfg.Branch))
	}
	snapshot, err := r.localRepo.Snapshot()
	if err != nil {
		return nil, err
	}
	headHash := headTree.Hash.String()
	if snapshot.TreeHash != headHash {
		return nil, domain.NewUserError("the working copy is not at the branch head; run nipa update first")
	}

	state := &domain.RevertState{
		Targets:          refs,
		CurrentTreeHash:  headHash,
		CurrentCommitID:  commitIDString(head),
		OriginalTreeHash: headHash,
		Mainline:         opts.Mainline,
		NoCommit:         opts.NoCommit,
		Message:          message,
	}
	if pin, err := r.localRepo.LoadCommit(); err != nil {
		return nil, err
	} else if pin != nil {
		state.OriginalCommitID = pin.CommitID
		state.OriginalCommitHash = pin.CommitHash
	}
	if err := r.localRepo.SaveRevertState(state); err != nil {
		return nil, err
	}
	return r.process(ctx, root, nipaUrl, cfg.Branch, state, merge.Flatten(headTree), progress...)
}

func (r *Revert) checkNoPendingOperations() error {
	mergeState, err := r.localRepo.LoadMergeState()
	if err != nil {
		return err
	}
	if mergeState != nil {
		return domain.NewUserError("a merge is already in progress; resolve the conflicts and push, or run nipa merge --abort")
	}
	revertState, err := r.localRepo.LoadRevertState()
	if err != nil {
		return err
	}
	if revertState != nil {
		return domain.NewUserError("a revert is already in progress; resolve the conflicts and run nipa revert --continue, or run nipa revert --abort")
	}
	staged, err := r.localRepo.ListStaged()
	if err != nil {
		return err
	}
	if len(staged) > 0 {
		return domain.NewUserError("cannot revert with staged changes; push or reset them first")
	}
	return nil
}

func (r *Revert) resolveTargets(ctx context.Context, url *domain.NipaUrl, target string, mainline int) ([]domain.CommitRef, error) {
	if !strings.Contains(target, "..") {
		detail, err := r.client.GetCommit(ctx, url.Org, url.Project, strings.TrimSpace(target))
		if err != nil {
			return nil, err
		}
		if detail == nil {
			return nil, domain.NewUserError(fmt.Sprintf("commit %s not found", target))
		}
		if detail.Parent2ID != "" && mainline == 0 {
			return nil, domain.NewUserError(fmt.Sprintf("commit %s is a merge commit; specify --mainline 1 or 2", detail.ID))
		}
		return []domain.CommitRef{commitRefOf(detail)}, nil
	}

	parts := strings.SplitN(target, "..", 2)
	from, to := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	if from == "" || to == "" {
		return nil, domain.NewUserError("a revert range must be <from>..<to>")
	}
	entries, err := r.client.WalkCommits(ctx, url.Org, url.Project, to, from, revertMaxTargets+1)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, domain.NewUserError("no commits in the revert range")
	}
	if len(entries) > revertMaxTargets {
		return nil, domain.NewUserError(fmt.Sprintf("revert range is too large (max %d commits)", revertMaxTargets))
	}
	refs := make([]domain.CommitRef, 0, len(entries))
	for _, e := range entries {
		if e == nil {
			continue
		}
		if e.Parent2ID != "" && mainline == 0 {
			return nil, domain.NewUserError(fmt.Sprintf("commit %s is a merge commit; specify --mainline 1 or 2", e.ID))
		}
		refs = append(refs, domain.CommitRef{ID: e.ID, Hash: e.Hash, Subject: firstLine(e.Message)})
	}
	return refs, nil
}

func (r *Revert) process(ctx context.Context, root string, url *domain.NipaUrl, branch string, state *domain.RevertState, ours map[string]merge.File, progress ...UploadProgress) (*RevertOutcome, error) {
	head, err := r.client.GetBranchByName(ctx, url.Org, url.Project, branch)
	if err != nil {
		return nil, err
	}
	headID := commitIDString(head)

	committed := false
	changed := false
	for len(state.Targets) > 0 {
		target := state.Targets[0]
		detail, err := r.client.GetCommit(ctx, url.Org, url.Project, target.ID)
		if err != nil {
			return nil, err
		}
		theirs, err := r.parentTree(ctx, url, detail, state.Mainline)
		if err != nil {
			return nil, err
		}
		res := merge.ThreeWay(merge.Flatten(detail.Tree), ours, theirs)

		baseByPath, err := r.snapshotByPath()
		if err != nil {
			return nil, err
		}
		scope := domain.ChunkScope{
			Org:       url.Org,
			Project:   url.Project,
			CommitIDs: commitIDs(detail.ID, parentIDOf(detail, state.Mainline), headID),
		}
		applied, err := applyThreeWay(ctx, r.client, r.localRepo, root, ours, baseByPath, res, scope)
		if err != nil {
			return nil, err
		}

		if len(applied.Conflicted) > 0 {
			state.Conflicts = append([]string(nil), applied.Conflicted...)
			if err := r.localRepo.SaveTree(treeFromFiles(applied.Files)); err != nil {
				return nil, err
			}
			if err := r.localRepo.SaveRevertState(state); err != nil {
				return nil, err
			}
			return &RevertOutcome{Committed: committed, Conflicts: applied.Conflicted}, nil
		}

		ours = applied.Files
		state.Targets = state.Targets[1:]
		state.Conflicts = nil

		if len(applied.Staged) == 0 {
			if err := r.localRepo.SaveRevertState(state); err != nil {
				return nil, err
			}
			continue
		}
		changed = true

		if state.NoCommit {
			if err := r.localRepo.SaveTree(treeFromFiles(ours)); err != nil {
				return nil, err
			}
			if err := r.localRepo.SaveRevertState(state); err != nil {
				return nil, err
			}
			continue
		}

		msg := state.Message
		if msg == "" {
			msg = revertMessage(target)
		}
		result, err := r.push.pushStaged(ctx, root, url, branch, msg, state.CurrentTreeHash, state.CurrentCommitID, "", progress...)
		if err != nil {
			return nil, err
		}
		state.CurrentTreeHash = result.TreeHash.String()
		state.CurrentCommitID = result.CommitID.Base36()
		committed = true
		if err := r.localRepo.SaveRevertState(state); err != nil {
			return nil, err
		}
	}

	if err := r.localRepo.ClearRevertState(); err != nil {
		return nil, err
	}
	return &RevertOutcome{Committed: committed, NoChange: !changed}, nil
}

func (r *Revert) resume(ctx context.Context, root string, url *domain.NipaUrl, branch string, progress ...UploadProgress) (*RevertOutcome, error) {
	state, err := r.localRepo.LoadRevertState()
	if err != nil {
		return nil, err
	}
	if state == nil || len(state.Targets) == 0 {
		return nil, domain.NewUserError("no revert in progress to continue")
	}
	if err := r.client.Connect(ctx, url.Host); err != nil {
		return nil, err
	}
	if err := r.auth.MakeSureLoggedIn(ctx, url.Host); err != nil {
		return nil, err
	}

	current := state.Targets[0]
	committed := false
	var ours map[string]merge.File

	if state.NoCommit {
		ours, err = r.workingTreeFiles(root)
		if err != nil {
			return nil, err
		}
	} else {
		headTree, err := r.client.GetTreeNodeManifest(ctx, url.Org, url.Project, branch, nil)
		if err != nil {
			return nil, err
		}
		if headTree == nil {
			return nil, domain.NewUserError(fmt.Sprintf("branch %q has no commits", branch))
		}
		ours = merge.Flatten(headTree)

		staged, err := r.localRepo.ListStaged()
		if err != nil {
			return nil, err
		}
		if len(staged) > 0 {
			msg := state.Message
			if msg == "" {
				msg = revertMessage(current)
			}
			result, err := r.push.pushStaged(ctx, root, url, branch, msg, state.CurrentTreeHash, state.CurrentCommitID, "", progress...)
			if err != nil {
				return nil, err
			}
			state.CurrentTreeHash = result.TreeHash.String()
			state.CurrentCommitID = result.CommitID.Base36()
			committed = true
			headTree, err = r.client.GetTreeNodeManifest(ctx, url.Org, url.Project, branch, nil)
			if err != nil {
				return nil, err
			}
			ours = merge.Flatten(headTree)
		}
	}

	state.Targets = state.Targets[1:]
	state.Conflicts = nil
	if err := r.localRepo.SaveRevertState(state); err != nil {
		return nil, err
	}
	if len(state.Targets) == 0 {
		if err := r.localRepo.ClearRevertState(); err != nil {
			return nil, err
		}
		return &RevertOutcome{Committed: committed}, nil
	}
	outcome, err := r.process(ctx, root, url, branch, state, ours, progress...)
	if err != nil {
		return nil, err
	}
	outcome.Committed = outcome.Committed || committed
	return outcome, nil
}

func (r *Revert) abort(ctx context.Context, root string, url *domain.NipaUrl, branch string) (*RevertOutcome, error) {
	state, err := r.localRepo.LoadRevertState()
	if err != nil {
		return nil, err
	}
	if state == nil {
		return nil, domain.NewUserError("no revert in progress to abort")
	}
	if err := r.client.Connect(ctx, url.Host); err != nil {
		return nil, err
	}
	if err := r.auth.MakeSureLoggedIn(ctx, url.Host); err != nil {
		return nil, err
	}
	head, err := r.client.GetBranchByName(ctx, url.Org, url.Project, branch)
	if err != nil {
		return nil, err
	}
	scope := domain.ChunkScope{
		Org:       url.Org,
		Project:   url.Project,
		CommitIDs: commitIDs(commitIDString(head)),
	}
	tree, err := r.client.GetTreeNodeManifest(ctx, url.Org, url.Project, branch, nil)
	if err != nil {
		return nil, err
	}
	if err := syncWorkingCopy(ctx, r.client, r.localRepo, root, tree, scope); err != nil {
		return nil, err
	}
	if err := r.localRepo.SaveTree(tree); err != nil {
		return nil, err
	}
	if err := r.localRepo.SaveCommit(state.OriginalCommitID, state.OriginalCommitHash); err != nil {
		return nil, err
	}
	if err := r.localRepo.ClearStaged(); err != nil {
		return nil, err
	}
	if err := r.localRepo.ClearRevertState(); err != nil {
		return nil, err
	}
	return &RevertOutcome{}, nil
}

func (r *Revert) skip(ctx context.Context, root string, url *domain.NipaUrl, branch string, progress ...UploadProgress) (*RevertOutcome, error) {
	state, err := r.localRepo.LoadRevertState()
	if err != nil {
		return nil, err
	}
	if state == nil || len(state.Targets) == 0 {
		return nil, domain.NewUserError("no revert in progress to skip")
	}
	if state.NoCommit {
		return nil, domain.NewUserError("cannot skip a commit during a --no-commit revert")
	}
	if err := r.client.Connect(ctx, url.Host); err != nil {
		return nil, err
	}
	if err := r.auth.MakeSureLoggedIn(ctx, url.Host); err != nil {
		return nil, err
	}
	head, err := r.client.GetBranchByName(ctx, url.Org, url.Project, branch)
	if err != nil {
		return nil, err
	}
	scope := domain.ChunkScope{
		Org:       url.Org,
		Project:   url.Project,
		CommitIDs: commitIDs(commitIDString(head)),
	}
	headTree, err := r.client.GetTreeNodeManifest(ctx, url.Org, url.Project, branch, nil)
	if err != nil {
		return nil, err
	}
	if headTree == nil {
		return nil, domain.NewUserError(fmt.Sprintf("branch %q has no commits", branch))
	}
	if err := syncWorkingCopy(ctx, r.client, r.localRepo, root, headTree, scope); err != nil {
		return nil, err
	}
	if err := r.localRepo.SaveTree(headTree); err != nil {
		return nil, err
	}
	if err := r.localRepo.ClearStaged(); err != nil {
		return nil, err
	}

	state.Targets = state.Targets[1:]
	state.Conflicts = nil
	state.CurrentTreeHash = headTree.Hash.String()
	if err := r.localRepo.SaveRevertState(state); err != nil {
		return nil, err
	}
	if len(state.Targets) == 0 {
		if err := r.localRepo.ClearRevertState(); err != nil {
			return nil, err
		}
		return &RevertOutcome{}, nil
	}
	return r.process(ctx, root, url, branch, state, merge.Flatten(headTree), progress...)
}

func (r *Revert) parentTree(ctx context.Context, url *domain.NipaUrl, detail *domain.CommitDetail, mainline int) (map[string]merge.File, error) {
	parentID := parentIDOf(detail, mainline)
	if parentID == "" {
		return map[string]merge.File{}, nil
	}
	parent, err := r.client.GetCommit(ctx, url.Org, url.Project, parentID)
	if err != nil {
		return nil, err
	}
	return merge.Flatten(parent.Tree), nil
}

func parentIDOf(detail *domain.CommitDetail, mainline int) string {
	switch {
	case detail.Parent1ID == "" && detail.Parent2ID == "":
		return ""
	case detail.Parent2ID == "":
		return detail.Parent1ID
	case mainline == 2:
		return detail.Parent2ID
	default:
		return detail.Parent1ID
	}
}

func (r *Revert) snapshotByPath() (map[string]domain.SnapshotFile, error) {
	snapshot, err := r.localRepo.Snapshot()
	if err != nil {
		return nil, err
	}
	byPath := make(map[string]domain.SnapshotFile, len(snapshot.Files))
	for _, f := range snapshot.Files {
		byPath[f.Path] = f
	}
	return byPath, nil
}

func (r *Revert) workingTreeFiles(root string) (map[string]merge.File, error) {
	snapshot, err := r.localRepo.Snapshot()
	if err != nil {
		return nil, err
	}
	out := make(map[string]merge.File, len(snapshot.Files))
	for _, f := range snapshot.Files {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f.Path)))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		hash, chunks, err := chunkFile(f.Path, data)
		if err != nil {
			return nil, err
		}
		hashes := make([]serverDomain.Hash, len(chunks))
		sizes := make([]int64, len(chunks))
		for i, c := range chunks {
			hashes[i] = c.Hash
			sizes[i] = c.SizeBytes
		}
		out[f.Path] = merge.File{
			Path:        f.Path,
			Mode:        f.Mode,
			SizeBytes:   int64(len(data)),
			IsBinary:    chunker.IsBinary(data),
			Hash:        hash,
			ChunkHashes: hashes,
			ChunkSizes:  sizes,
		}
	}
	return out, nil
}

func commitRefOf(detail *domain.CommitDetail) domain.CommitRef {
	return domain.CommitRef{
		ID:      detail.ID,
		Hash:    detail.Hash,
		Subject: firstLine(detail.Message),
	}
}

func revertMessage(ref domain.CommitRef) string {
	return fmt.Sprintf("Revert %q\n\nThis reverts commit %s (%s).", ref.Subject, ref.ID, ref.Hash)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
