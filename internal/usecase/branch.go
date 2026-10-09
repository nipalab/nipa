package usecase

//go:generate go run go.uber.org/mock/mockgen -source=$GOFILE -destination=branch_mock_test.go -package=usecase

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/treehash"
)

type permissionUsecase interface {
	HasProjectAccess(ctx context.Context, projectID snow.ID, permission domain.Permission) bool
	HasPathAccess(ctx context.Context, projectID snow.ID, path string, permission domain.Permission) bool
	CompileFilter(ctx context.Context, projectID snow.ID, permission domain.Permission) (*PathFilter, error)
	AdminHasProject(ctx context.Context, projectID snow.ID) bool
}

type branchRepository interface {
	ListBranches(ctx context.Context, projectID snow.ID, limit int, updatedAfter *time.Time, lastID snow.ID) ([]*domain.Branch, error)
	GetByProjectIDAndID(ctx context.Context, projectID snow.ID, branchID snow.ID) (*domain.Branch, error)
	GetDefaultBranch(ctx context.Context, projectID snow.ID) (*domain.Branch, error)
	GetBranchByName(ctx context.Context, projectID snow.ID, name string) (*domain.Branch, error)
	CreateBranch(ctx context.Context, branch domain.Branch) (*domain.Branch, error)
	RenameBranch(ctx context.Context, projectID, branchID snow.ID, name, key string) error
	DeleteBranch(ctx context.Context, projectID, branchID snow.ID) error
	HasOpenMergeRequests(ctx context.Context, projectID, branchID snow.ID) (bool, error)
	SetBranchProtection(ctx context.Context, projectID, branchID snow.ID, protection domain.BranchProtection) error
	RequiredReviewers(ctx context.Context, branchID snow.ID) ([]domain.ReviewActor, error)
	RequiredChecks(ctx context.Context, branchID snow.ID) ([]string, error)
	SetDefaultBranch(ctx context.Context, projectID, branchID snow.ID) error
	UpdateCommitIf(ctx context.Context, branchID snow.ID, fromCommitID, toCommitID *snow.ID) error
	GetCommit(ctx context.Context, commitID snow.ID) (*domain.Commit, error)
	GetCommitByHash(ctx context.Context, hash domain.Hash) (*domain.Commit, error)
	GetTreeNode(ctx context.Context, id int64) (*domain.TreeNode, error)
	GetTreeChildByName(ctx context.Context, parentID int64, name string) (*domain.TreeNode, error)
	ListTreeChildren(ctx context.Context, parentID int64) ([]*domain.TreeNode, error)
	ListFilesByTree(ctx context.Context, treeID int64) ([]*domain.File, error)
	CommitLog(ctx context.Context, projectID snow.ID, startCommitID snow.ID, limit int) ([]*domain.CommitLogEntry, error)
	CommitLogUntil(ctx context.Context, projectID snow.ID, startCommitID, stopCommitID snow.ID, limit int) ([]*domain.CommitLogEntry, error)
}

type BranchForkPoint struct {
	BranchName string
	CommitID   *snow.ID
	CommitHash *domain.Hash
}

type Branch struct {
	permUc     permissionUsecase
	branchRepo branchRepository
	snowNode   snow.Node
	chunks     chunkReader
	fileLocks  fileLockGate
	hooks      hookBranchGate
	// mergeCommitter and chunkUploads enable the non-fast-forward merge
	// strategies; both are wired by the server mains.
	mergeCommitter mergeCommitter
	chunkUploads   chunkUploader
}

// hookBranchGate is the subset of the webhook emitter used by branch
// management.
type hookBranchGate interface {
	EmitBranch(ctx context.Context, event string, projectID snow.ID, branch *domain.Branch, actor snow.ID) error
}

func NewBranch(permUc permissionUsecase, branchRepo branchRepository, snowNode snow.Node) *Branch {
	return &Branch{
		permUc:     permUc,
		branchRepo: branchRepo,
		snowNode:   snowNode,
	}
}

// WithFileLocks enables the binary lock gate on this usecase.
func (b *Branch) WithFileLocks(locks fileLockGate) *Branch {
	b.fileLocks = locks
	return b
}

// WithHooks enables webhook events on this usecase.
func (b *Branch) WithHooks(hooks hookBranchGate) *Branch {
	b.hooks = hooks
	return b
}

func (b *Branch) ListBranches(ctx context.Context, projectID snow.ID, limit int, updatedAfter *time.Time, lastID snow.ID) ([]*domain.Branch, error) {
	if !b.permUc.HasProjectAccess(ctx, projectID, domain.PermissionRead) {
		return nil, domain.NewErrorNoPermission()
	}
	branches, err := b.branchRepo.ListBranches(ctx, projectID, limit, updatedAfter, lastID)
	if err != nil {
		return nil, err
	}
	for _, branch := range branches {
		if err := b.attachProtection(ctx, branch); err != nil {
			return nil, err
		}
	}
	return branches, nil
}

func (b *Branch) GetByProjectIDAndID(ctx context.Context, projectID snow.ID, branchID snow.ID) (*domain.Branch, error) {
	if !b.permUc.HasProjectAccess(ctx, projectID, domain.PermissionRead) {
		return nil, domain.NewErrorNoPermission()
	}
	return b.branchRepo.GetByProjectIDAndID(ctx, projectID, branchID)
}

func (b *Branch) GetBranchByName(ctx context.Context, projectID snow.ID, name string) (*domain.Branch, error) {
	if !b.permUc.HasProjectAccess(ctx, projectID, domain.PermissionRead) {
		return nil, domain.NewErrorNoPermission()
	}
	branch, err := b.branchRepo.GetBranchByName(ctx, projectID, name)
	if domain.IsErrorNotFound(err) {
		return nil, domain.NewErrorNotFound(fmt.Sprintf("branch %q not found", name))
	}
	return branch, err
}

func (b *Branch) GetDefault(ctx context.Context, projectID snow.ID) (*domain.Branch, error) {
	if !b.permUc.HasProjectAccess(ctx, projectID, domain.PermissionRead) {
		return nil, domain.NewErrorNoPermission()
	}
	return b.branchRepo.GetDefaultBranch(ctx, projectID)
}

func (b *Branch) CreateBranch(ctx context.Context, projectID snow.ID, name string, fork BranchForkPoint) (*domain.Branch, error) {
	if !b.permUc.HasProjectAccess(ctx, projectID, domain.PermissionWrite) {
		return nil, domain.NewErrorNoPermission()
	}
	name = strings.TrimSpace(name)
	if err := validateBranchName(name); err != nil {
		return nil, err
	}

	if _, err := b.branchRepo.GetBranchByName(ctx, projectID, name); err == nil {
		return nil, domain.NewErrorConflict(fmt.Sprintf("branch %q already exists", name))
	} else if !domain.IsErrorNotFound(err) {
		return nil, err
	}

	fromCommitID, err := b.resolveForkPoint(ctx, projectID, fork)
	if err != nil {
		return nil, err
	}

	existing, err := b.branchRepo.ListBranches(ctx, projectID, 1, nil, 0)
	if err != nil {
		return nil, err
	}

	created, err := b.branchRepo.CreateBranch(ctx, domain.Branch{
		ID:        b.snowNode.Generate(),
		ProjectID: projectID,
		Name:      name,
		CommitID:  fromCommitID,
		IsDefault: len(existing) == 0,
	})
	if err != nil {
		return nil, err
	}
	b.emitHook(ctx, domain.WebhookEventBranchCreated, projectID, created)
	return created, nil
}

func (b *Branch) Rename(ctx context.Context, projectID snow.ID, name, newName string) (*domain.Branch, error) {
	branch, err := b.branchByName(ctx, projectID, name)
	if err != nil {
		return nil, err
	}
	if err := b.requireBranchManage(ctx, projectID, branch); err != nil {
		return nil, err
	}
	newName = strings.TrimSpace(newName)
	if err := validateBranchName(newName); err != nil {
		return nil, err
	}
	if newName == branch.Name {
		return branch, nil
	}
	if _, err := b.branchRepo.GetBranchByName(ctx, projectID, newName); err == nil {
		return nil, domain.NewErrorConflict(fmt.Sprintf("branch %q already exists", newName))
	} else if !domain.IsErrorNotFound(err) {
		return nil, err
	}
	if err := b.branchRepo.RenameBranch(ctx, projectID, branch.ID, newName, newName); err != nil {
		return nil, err
	}
	return b.branchRepo.GetByProjectIDAndID(ctx, projectID, branch.ID)
}

func (b *Branch) Delete(ctx context.Context, projectID snow.ID, name string) error {
	branch, err := b.branchByName(ctx, projectID, name)
	if err != nil {
		return err
	}
	if err := b.requireBranchManage(ctx, projectID, branch); err != nil {
		return err
	}
	if branch.IsDefault {
		return domain.NewErrorConflict(fmt.Sprintf("cannot delete the default branch %q", name))
	}
	if branch.IsProtected {
		return domain.NewErrorConflict(fmt.Sprintf("branch %q is protected; unprotect it before deleting", name))
	}
	remaining, err := b.branchRepo.ListBranches(ctx, projectID, 2, nil, 0)
	if err != nil {
		return err
	}
	if len(remaining) <= 1 {
		return domain.NewErrorConflict("cannot delete the last branch")
	}
	hasOpen, err := b.branchRepo.HasOpenMergeRequests(ctx, projectID, branch.ID)
	if err != nil {
		return err
	}
	if hasOpen {
		return domain.NewErrorConflict(fmt.Sprintf("branch %q has open merge requests", name))
	}
	if err := b.branchRepo.DeleteBranch(ctx, projectID, branch.ID); err != nil {
		return err
	}
	if b.fileLocks != nil {
		if err := b.fileLocks.ReleaseBranch(ctx, projectID, branch.ID); err != nil {
			return err
		}
	}
	b.emitHook(ctx, domain.WebhookEventBranchDeleted, projectID, branch)
	return nil
}

func (b *Branch) SetDefault(ctx context.Context, projectID snow.ID, name string) (*domain.Branch, error) {
	if !b.permUc.AdminHasProject(ctx, projectID) {
		return nil, domain.NewErrorNoPermission()
	}
	branch, err := b.branchByName(ctx, projectID, name)
	if err != nil {
		return nil, err
	}
	if branch.IsDefault {
		return branch, nil
	}
	if err := b.branchRepo.SetDefaultBranch(ctx, projectID, branch.ID); err != nil {
		return nil, err
	}
	return b.branchRepo.GetByProjectIDAndID(ctx, projectID, branch.ID)
}

// BranchProtectionOptions patches a branch's protection. Nil fields keep the
// current value, so the protect toggle does not clear settings configured
// earlier.
type BranchProtectionOptions struct {
	Protected             bool
	RequiredApprovals     *int64
	DismissStaleApprovals *bool
	RequiredReviewers     *[]snow.ID
	RequireStatusChecks   *bool
	RequiredChecks        *[]string
}

// SetProtection updates the protection settings of a branch.
func (b *Branch) SetProtection(ctx context.Context, projectID snow.ID, name string, opts BranchProtectionOptions) (*domain.Branch, error) {
	if !b.permUc.AdminHasProject(ctx, projectID) {
		return nil, domain.NewErrorNoPermission()
	}
	branch, err := b.branchByName(ctx, projectID, name)
	if err != nil {
		return nil, err
	}
	approvals := branch.RequiredApprovals
	if opts.RequiredApprovals != nil {
		if *opts.RequiredApprovals < 0 {
			return nil, domain.NewErrorUser("required approvals cannot be negative")
		}
		approvals = *opts.RequiredApprovals
	}
	dismissStale := branch.DismissStaleApprovals
	if opts.DismissStaleApprovals != nil {
		dismissStale = *opts.DismissStaleApprovals
	}
	requireChecks := branch.RequireStatusChecks
	if opts.RequireStatusChecks != nil {
		requireChecks = *opts.RequireStatusChecks
	}
	var reviewers []snow.ID
	if opts.RequiredReviewers != nil {
		reviewers = uniqueIDs(*opts.RequiredReviewers)
	} else if len(branch.RequiredReviewers) > 0 {
		reviewers = make([]snow.ID, 0, len(branch.RequiredReviewers))
		for _, reviewer := range branch.RequiredReviewers {
			reviewers = append(reviewers, reviewer.UserID)
		}
	}
	var checks []string
	if opts.RequiredChecks != nil {
		checks = normalizeCheckNames(*opts.RequiredChecks)
	} else {
		checks = branch.RequiredChecks
	}
	if branch.IsProtected == opts.Protected && branch.RequiredApprovals == approvals &&
		branch.DismissStaleApprovals == dismissStale && branch.RequireStatusChecks == requireChecks &&
		sameIDs(reviewers, branch.RequiredReviewers) && sameStrings(checks, branch.RequiredChecks) {
		return branch, nil
	}
	if err := b.branchRepo.SetBranchProtection(ctx, projectID, branch.ID, domain.BranchProtection{
		Protected:             opts.Protected,
		RequiredApprovals:     approvals,
		DismissStaleApprovals: dismissStale,
		RequireStatusChecks:   requireChecks,
		RequiredReviewers:     reviewers,
		RequiredChecks:        checks,
	}); err != nil {
		return nil, err
	}
	return b.protectedBranch(ctx, projectID, branch.ID)
}

// protectedBranch loads a branch with its protection lists attached.
func (b *Branch) protectedBranch(ctx context.Context, projectID, branchID snow.ID) (*domain.Branch, error) {
	branch, err := b.branchRepo.GetByProjectIDAndID(ctx, projectID, branchID)
	if err != nil {
		return nil, err
	}
	return branch, b.attachProtection(ctx, branch)
}

func (b *Branch) attachProtection(ctx context.Context, branch *domain.Branch) error {
	reviewers, err := b.branchRepo.RequiredReviewers(ctx, branch.ID)
	if err != nil {
		return err
	}
	checks, err := b.branchRepo.RequiredChecks(ctx, branch.ID)
	if err != nil {
		return err
	}
	branch.RequiredReviewers = reviewers
	branch.RequiredChecks = checks
	return nil
}

func uniqueIDs(ids []snow.ID) []snow.ID {
	if len(ids) == 0 {
		return nil
	}
	out := make([]snow.ID, 0, len(ids))
	seen := map[snow.ID]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func normalizeCheckNames(names []string) []string {
	out := make([]string, 0, len(names))
	seen := map[string]bool{}
	for _, name := range names {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		out = append(out, trimmed)
	}
	if len(out) == 0 {
		return nil
	}
	sort.Strings(out)
	return out
}

func sameIDs(ids []snow.ID, actors []domain.ReviewActor) bool {
	if len(ids) != len(actors) {
		return false
	}
	for i, actor := range actors {
		if ids[i] != actor.UserID {
			return false
		}
	}
	return true
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// emitHook publishes a branch event. The change is already stored, so a
// delivery failure must not fail the operation.
func (b *Branch) emitHook(ctx context.Context, event string, projectID snow.ID, branch *domain.Branch) {
	if b.hooks == nil {
		return
	}
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok {
		return
	}
	if err := b.hooks.EmitBranch(ctx, event, projectID, branch, claim.UserID); err != nil {
		slog.Warn("emitting webhook branch event failed", "event", event, "project", projectID, "error", err)
	}
}

func (b *Branch) branchByName(ctx context.Context, projectID snow.ID, name string) (*domain.Branch, error) {
	branch, err := b.branchRepo.GetBranchByName(ctx, projectID, name)
	if domain.IsErrorNotFound(err) {
		return nil, domain.NewErrorNotFound(fmt.Sprintf("branch %q not found", name))
	}
	if err != nil {
		return nil, err
	}
	return branch, nil
}

func (b *Branch) requireBranchManage(ctx context.Context, projectID snow.ID, branch *domain.Branch) error {
	if branch.IsProtected {
		if !b.permUc.AdminHasProject(ctx, projectID) {
			return domain.NewErrorNoPermission()
		}
		return nil
	}
	if !b.permUc.HasProjectAccess(ctx, projectID, domain.PermissionWrite) {
		return domain.NewErrorNoPermission()
	}
	return nil
}

func (b *Branch) resolveForkPoint(ctx context.Context, projectID snow.ID, fork BranchForkPoint) (*snow.ID, error) {
	if fork.CommitID != nil {
		commit, err := b.branchRepo.GetCommit(ctx, *fork.CommitID)
		if err != nil {
			if domain.IsErrorNotFound(err) {
				return nil, domain.NewErrorNotFound(fmt.Sprintf("commit %s not found", fork.CommitID.Base36()))
			}
			return nil, err
		}
		if commit.ProjectID != projectID {
			return nil, domain.NewErrorNotFound(fmt.Sprintf("commit %s not found", fork.CommitID.Base36()))
		}
		id := commit.ID
		return &id, nil
	}

	if fork.CommitHash != nil {
		commit, err := b.branchRepo.GetCommitByHash(ctx, *fork.CommitHash)
		if err != nil {
			if domain.IsErrorNotFound(err) {
				return nil, domain.NewErrorNotFound(fmt.Sprintf("commit %s not found", fork.CommitHash.String()))
			}
			return nil, err
		}
		if commit.ProjectID != projectID {
			return nil, domain.NewErrorNotFound(fmt.Sprintf("commit %s not found", fork.CommitHash.String()))
		}
		id := commit.ID
		return &id, nil
	}

	if fork.BranchName != "" {
		from, err := b.branchRepo.GetBranchByName(ctx, projectID, fork.BranchName)
		if domain.IsErrorNotFound(err) {
			return nil, domain.NewErrorNotFound(fmt.Sprintf("branch %q not found", fork.BranchName))
		}
		if err != nil {
			return nil, err
		}
		return from.CommitID, nil
	}

	def, err := b.branchRepo.GetDefaultBranch(ctx, projectID)
	if domain.IsErrorNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return def.CommitID, nil
}

func validateBranchName(name string) error {
	if name == "" {
		return domain.NewErrorUser("branch name must not be empty")
	}
	if strings.ContainsRune(name, '/') {
		return domain.NewErrorUser(fmt.Sprintf("invalid branch name %q", name))
	}
	if name == "." || name == ".." {
		return domain.NewErrorUser(fmt.Sprintf("invalid branch name %q", name))
	}
	for _, r := range name {
		if r <= 0x20 || r == 0x7f {
			return domain.NewErrorUser(fmt.Sprintf("invalid branch name %q", name))
		}
	}
	return nil
}

func (b *Branch) GetTreeManifest(ctx context.Context, projectID snow.ID, branchName string, paths []string, treeHash string, recursive bool) (*domain.TreeNode, error) {
	if !b.permUc.HasProjectAccess(ctx, projectID, domain.PermissionRead) {
		return nil, domain.NewErrorNoPermission()
	}
	sparse, err := domain.NewPrefixSet(paths)
	if err != nil {
		return nil, domain.NewErrorUser(err.Error())
	}
	branch, err := b.branchRepo.GetBranchByName(ctx, projectID, branchName)
	if domain.IsErrorNotFound(err) {
		return nil, domain.NewErrorNotFound(fmt.Sprintf("branch %q not found", branchName))
	}
	if err != nil {
		return nil, err
	}
	if branch.CommitID == nil {
		if !sparse.Empty() {
			return nil, domain.NewErrorNotFound(fmt.Sprintf("path %q not found in branch %q", sparse[0], branchName))
		}
		return nil, nil
	}
	commit, err := b.branchRepo.GetCommit(ctx, *branch.CommitID)
	if err != nil {
		return nil, err
	}
	return b.manifestForCommit(ctx, projectID, commit, sparse, recursive, treeHash, fmt.Sprintf("branch %q", branchName))
}

// GetCommitTreeManifest builds the manifest of an arbitrary commit's tree,
// scoped to the caller's read filter and the requested sparse paths.
func (b *Branch) GetCommitTreeManifest(ctx context.Context, projectID snow.ID, commitID snow.ID, paths []string, recursive bool) (*domain.TreeNode, error) {
	if !b.permUc.HasProjectAccess(ctx, projectID, domain.PermissionRead) {
		return nil, domain.NewErrorNoPermission()
	}
	sparse, err := domain.NewPrefixSet(paths)
	if err != nil {
		return nil, domain.NewErrorUser(err.Error())
	}
	commit, err := b.branchRepo.GetCommit(ctx, commitID)
	if domain.IsErrorNotFound(err) {
		return nil, domain.NewErrorNotFound(fmt.Sprintf("commit %s not found", commitID.Base36()))
	}
	if err != nil {
		return nil, err
	}
	if commit.ProjectID != projectID {
		return nil, domain.NewErrorNotFound(fmt.Sprintf("commit %s not found", commitID.Base36()))
	}
	return b.manifestForCommit(ctx, projectID, commit, sparse, recursive, "", fmt.Sprintf("commit %s", commitID.Base36()))
}

func (b *Branch) manifestForCommit(ctx context.Context, projectID snow.ID, commit *domain.Commit, sparse domain.PrefixSet, recursive bool, treeHash, source string) (*domain.TreeNode, error) {
	root, err := b.branchRepo.GetTreeNode(ctx, commit.TreeID)
	if err != nil {
		return nil, err
	}
	filter, err := b.permUc.CompileFilter(ctx, projectID, domain.PermissionRead)
	if err != nil {
		return nil, err
	}
	if treeHash != "" && sparse.Empty() && filter.All() && strings.EqualFold(treeHash, root.Hash.String()) {
		return nil, nil
	}
	if err := b.verifySparsePaths(ctx, root, sparse, filter, source); err != nil {
		return nil, err
	}
	if err := b.loadTreeManifest(ctx, root, "", recursive, filter, sparse); err != nil {
		return nil, err
	}
	rehashTree(root)
	if treeHash != "" && strings.EqualFold(treeHash, root.Hash.String()) {
		return nil, nil
	}
	return root, nil
}

func (b *Branch) verifySparsePaths(ctx context.Context, root *domain.TreeNode, sparse domain.PrefixSet, filter *PathFilter, source string) error {
	for _, prefix := range sparse {
		if prefix == "" {
			continue
		}
		if !filter.CanDescend(prefix) {
			return domain.NewErrorNotFound(fmt.Sprintf("path %q not found in %s", prefix, source))
		}
		if _, err := b.findTreeNode(ctx, root, prefix); err != nil {
			if domain.IsErrorNotFound(err) {
				return domain.NewErrorNotFound(fmt.Sprintf("path %q not found in %s", prefix, source))
			}
			return err
		}
	}
	return nil
}

func (b *Branch) findTreeNode(ctx context.Context, root *domain.TreeNode, path string) (*domain.TreeNode, error) {
	node := root
	for _, segment := range strings.Split(strings.Trim(path, "/"), "/") {
		if segment == "" {
			continue
		}
		child, err := b.branchRepo.GetTreeChildByName(ctx, node.ID, segment)
		if err != nil {
			return nil, err
		}
		node = child
	}
	return node, nil
}

func (b *Branch) loadTreeManifest(ctx context.Context, node *domain.TreeNode, path string, recursive bool, filter *PathFilter, sparse domain.PrefixSet) error {
	files, err := b.branchRepo.ListFilesByTree(ctx, node.ID)
	if err != nil {
		return err
	}
	node.FileChildren = nil
	for _, file := range files {
		filePath := joinTreePath(path, file.Name)
		if !filter.Allow(filePath) {
			continue
		}
		if !sparse.Covers(filePath) && !keepRootIgnoreFile(path, file.Name, sparse) {
			continue
		}
		node.FileChildren = append(node.FileChildren, file)
	}

	node.TreeChildren = nil
	if !recursive {
		return nil
	}
	children, err := b.branchRepo.ListTreeChildren(ctx, node.ID)
	if err != nil {
		return err
	}
	for _, child := range children {
		childPath := joinTreePath(path, child.Name)
		if !filter.CanDescend(childPath) || !sparse.CanDescend(childPath) {
			continue
		}
		if err := b.loadTreeManifest(ctx, child, childPath, true, filter, sparse); err != nil {
			return err
		}
		if len(child.FileChildren) == 0 && len(child.TreeChildren) == 0 {
			continue
		}
		node.TreeChildren = append(node.TreeChildren, child)
	}
	return nil
}

func joinTreePath(parent, name string) string {
	name = strings.Trim(name, "/")
	if parent == "" {
		return name
	}
	if name == "" {
		return parent
	}
	return parent + "/" + name
}

// rootIgnoreFile is the versioned ignore rules file. Sparse manifests always
// keep it at the root so partial checkouts still apply the team rules.
const rootIgnoreFile = ".nipaignore"

func keepRootIgnoreFile(path, name string, sparse domain.PrefixSet) bool {
	return path == "" && name == rootIgnoreFile && !sparse.Empty()
}

// EnsureProjectAccess returns a no-permission error unless the caller holds
// permission on any path of the project.
func (b *Branch) EnsureProjectAccess(ctx context.Context, projectID snow.ID, permission domain.Permission) error {
	if !b.permUc.HasProjectAccess(ctx, projectID, permission) {
		return domain.NewErrorNoPermission()
	}
	return nil
}

// VisibleChunks returns the deduplicated chunk hashes of every readable file
// across the given commits, narrowed by paths. The result is sorted so callers
// can binary-search it.
func (b *Branch) VisibleChunks(ctx context.Context, projectID snow.ID, commitIDs []string, paths []string) ([]domain.Hash, error) {
	if !b.permUc.HasProjectAccess(ctx, projectID, domain.PermissionRead) {
		return nil, domain.NewErrorNoPermission()
	}
	if len(commitIDs) == 0 {
		return nil, domain.NewErrorUser("at least one commit id is required")
	}
	sparse, err := domain.NewPrefixSet(paths)
	if err != nil {
		return nil, domain.NewErrorUser(err.Error())
	}
	filter, err := b.permUc.CompileFilter(ctx, projectID, domain.PermissionRead)
	if err != nil {
		return nil, err
	}

	seen := map[domain.Hash]struct{}{}
	for _, raw := range commitIDs {
		commitID, err := snow.ParseBase36(raw)
		if err != nil {
			return nil, domain.NewErrorUser(fmt.Sprintf("invalid commit id %q", raw))
		}
		commit, err := b.commitByIDInProject(ctx, projectID, commitID)
		if err != nil {
			return nil, err
		}
		root, err := b.branchRepo.GetTreeNode(ctx, commit.TreeID)
		if err != nil {
			return nil, err
		}
		if err := b.loadTreeManifest(ctx, root, "", true, filter, sparse); err != nil {
			return nil, err
		}
		collectChunkHashes(root, seen)
	}

	out := make([]domain.Hash, 0, len(seen))
	for hash := range seen {
		out = append(out, hash)
	}
	sort.Slice(out, func(i, j int) bool { return bytes.Compare(out[i][:], out[j][:]) < 0 })
	return out, nil
}

func collectChunkHashes(node *domain.TreeNode, seen map[domain.Hash]struct{}) {
	for _, file := range node.FileChildren {
		for _, chunk := range file.Chunks {
			seen[chunk.Hash] = struct{}{}
		}
	}
	for _, child := range node.TreeChildren {
		collectChunkHashes(child, seen)
	}
}

func rehashTree(node *domain.TreeNode) domain.Hash {
	files := make([]treehash.FileEntry, 0, len(node.FileChildren))
	for _, file := range node.FileChildren {
		files = append(files, treehash.FileEntry{Name: file.Name, Hash: file.Hash, Mode: file.Mode})
	}
	trees := make([]treehash.TreeEntry, 0, len(node.TreeChildren))
	for _, child := range node.TreeChildren {
		trees = append(trees, treehash.TreeEntry{Name: child.Name, Hash: rehashTree(child)})
	}
	node.Hash = treehash.TreeHash(files, trees)
	return node.Hash
}

func (b *Branch) GetCommitLog(ctx context.Context, projectID snow.ID, branchName string, startCommitID *snow.ID, limit int) ([]*domain.CommitLogEntry, error) {
	if !b.permUc.HasProjectAccess(ctx, projectID, domain.PermissionRead) {
		return nil, domain.NewErrorNoPermission()
	}
	if limit <= 0 {
		limit = 50
	}
	var (
		branch *domain.Branch
		err    error
	)
	if branchName == "" {
		branch, err = b.branchRepo.GetDefaultBranch(ctx, projectID)
	} else {
		branch, err = b.branchRepo.GetBranchByName(ctx, projectID, branchName)
	}
	if domain.IsErrorNotFound(err) {
		if branchName == "" {
			return nil, domain.NewErrorNotFound("default branch not found")
		}
		return nil, domain.NewErrorNotFound(fmt.Sprintf("branch %q not found", branchName))
	}
	if err != nil {
		return nil, err
	}
	if branch.CommitID == nil {
		return []*domain.CommitLogEntry{}, nil
	}
	startID := *branch.CommitID
	if startCommitID != nil {
		startID = *startCommitID
	}
	return b.branchRepo.CommitLog(ctx, projectID, startID, limit)
}
