package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/obs"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/treehash"
)

//go:generate go run go.uber.org/mock/mockgen -source=$GOFILE -destination=push_mock_test.go -package=usecase
type pushRepository interface {
	ApplyPush(ctx context.Context, req ApplyPushRequest) error
	ApplyImport(ctx context.Context, req ApplyPushRequest) (*domain.Commit, error)
}

type ApplyPushRequest struct {
	ProjectID  snow.ID
	BranchID   snow.ID
	CommitID   snow.ID
	CommitHash domain.Hash
	ParentID   *snow.ID
	ParentID2  *snow.ID
	UserID     snow.ID
	Message    string
	Nodes      []PushNodeRow
	Files      []PushFileRow

	Origin      string
	AuthorName  string
	AuthorEmail string
	CreatedAt   *time.Time

	// PathChanges are the commit's path change index rows (what it changes
	// against its first parent; none for a root commit), written with the
	// commit when IndexPaths is set. A request without IndexPaths leaves the
	// commit unindexed, and the first per-path history read over it indexes
	// it from the stored trees.
	PathChanges []PathChange
	IndexPaths  bool
}

// PushNodeRow is a new tree node (directory). IDs are logical and parent-first;
// the repository translates them to real row IDs.
type PushNodeRow struct {
	ID       int64
	Hash     domain.Hash
	Name     string
	Mode     int
	ParentID *int64
}

// PushFileRow is a new file row attached to the node with the given logical ID.
type PushFileRow struct {
	Name        string
	Mode        int
	SizeBytes   int64
	IsBinary    bool
	Encoding    string
	Hash        domain.Hash
	TreeID      int64
	ChunkHashes []domain.Hash
}

type Push struct {
	permUc     permissionUsecase
	branchRepo branchRepository
	pushRepo   pushRepository
	snowNode   snow.Node
	fileLocks  fileLockGate
	reviews    reviewPushGate
	hooks      hookPushGate
	metrics    *obs.Metrics
	maxFiles   int
	storage    storageWriteGate
	legacy     legacyTreeHashLookup
}

// legacyTreeHashLookup returns the hash a root tree had before file modes
// became part of the tree hash, or nil (TreeHashRepository.LegacyTreeHash).
type legacyTreeHashLookup interface {
	LegacyTreeHash(ctx context.Context, treeID int64) (*domain.Hash, error)
}

// WithLegacyTreeHashes accepts a push whose base tree hash is the head tree's
// pre-backfill hash, so a client that has not updated since the tree hashes
// were recomputed is not refused as if the branch had moved.
func (p *Push) WithLegacyTreeHashes(lookup legacyTreeHashLookup) *Push {
	p.legacy = lookup
	return p
}

// WithStorageGate refuses pushes to projects whose store is read-only.
func (p *Push) WithStorageGate(gate storageWriteGate) *Push {
	p.storage = gate
	return p
}

func (p *Push) requireWritableStorage(ctx context.Context, projectID snow.ID) error {
	if p.storage == nil {
		return nil
	}
	return p.storage.RequireWritable(ctx, projectID)
}

// DefaultPushMaxFiles bounds the files plus removed paths one push may carry.
const DefaultPushMaxFiles = 2_000_000

// reviewPushGate is the subset of the review usecase used by the push flow to
// mark reviews stale and log the push on the merge request timeline.
type reviewPushGate interface {
	NoteBranchPush(ctx context.Context, projectID, branchID, newHead, actor snow.ID, commitHash string) error
}

// hookPushGate is the subset of the webhook emitter used by the push flow.
type hookPushGate interface {
	EmitPush(ctx context.Context, event PushEvent) error
}

func NewPush(permUc permissionUsecase, branchRepo branchRepository, pushRepo pushRepository, snowNode snow.Node) *Push {
	return &Push{
		permUc:     permUc,
		branchRepo: branchRepo,
		pushRepo:   pushRepo,
		snowNode:   snowNode,
	}
}

// WithFileLocks enables the mandatory binary lock gate on this usecase.
func (p *Push) WithFileLocks(locks fileLockGate) *Push {
	p.fileLocks = locks
	return p
}

// WithMaxFiles overrides DefaultPushMaxFiles; zero or less keeps the default.
func (p *Push) WithMaxFiles(n int) *Push {
	p.maxFiles = n
	return p
}

func (p *Push) MaxFiles() int {
	if p.maxFiles > 0 {
		return p.maxFiles
	}
	return DefaultPushMaxFiles
}

// CheckTarget runs the checks that only need the push header, so a streamed
// push is refused before its file list is received. Push repeats them.
func (p *Push) CheckTarget(ctx context.Context, projectID snow.ID, branchName, message, baseCommitID string) error {
	if !p.permUc.HasProjectAccess(ctx, projectID, domain.PermissionWrite) {
		return domain.NewErrorNoPermission()
	}
	if err := p.requireWritableStorage(ctx, projectID); err != nil {
		return err
	}
	if strings.TrimSpace(message) == "" {
		return domain.NewErrorUser("commit message is required")
	}
	branch, err := p.pushBranch(ctx, projectID, branchName)
	if err != nil {
		return err
	}
	if baseCommitID != "" {
		return checkBaseCommit(branch, baseCommitID)
	}
	return nil
}

func (p *Push) pushBranch(ctx context.Context, projectID snow.ID, branchName string) (*domain.Branch, error) {
	branch, err := p.branchRepo.GetBranchByName(ctx, projectID, branchName)
	if domain.IsErrorNotFound(err) {
		return nil, domain.NewErrorNotFound(fmt.Sprintf("branch %q not found", branchName))
	}
	if err != nil {
		return nil, err
	}
	if branch.IsProtected {
		return nil, domain.NewErrorForbidden(fmt.Sprintf("branch %q is protected; push through a merge request", branchName))
	}
	return branch, nil
}

func checkBaseCommit(branch *domain.Branch, baseCommitID string) error {
	baseID, err := snow.ParseBase36(baseCommitID)
	if err != nil {
		return domain.NewErrorUser("invalid base commit id")
	}
	if branch.CommitID == nil || *branch.CommitID != baseID {
		current := ""
		if branch.CommitID != nil {
			current = branch.CommitID.Base36()
		}
		return domain.NewErrorConflict(fmt.Sprintf("branch %q has moved: expected base commit %s, current head is %s", branch.Name, baseCommitID, current))
	}
	return nil
}

// WithReviews enables the review staleness gate on this usecase.
func (p *Push) WithReviews(reviews reviewPushGate) *Push {
	p.reviews = reviews
	return p
}

// WithHooks enables webhook events on this usecase.
func (p *Push) WithHooks(hooks hookPushGate) *Push {
	p.hooks = hooks
	return p
}

// WithMetrics attaches observability metrics. Without one (the default), push
// results are not counted.
func (p *Push) WithMetrics(metrics *obs.Metrics) *Push {
	p.metrics = metrics
	return p
}

func (p *Push) Push(ctx context.Context, projectID snow.ID, branchName, baseTreeHash, message string, files []*domain.PushFile, removed []string, parent2CommitHash, baseCommitID string) (result *domain.PushResult, err error) {
	markLockableBinaries(files)
	ctx, span := obs.StartSpan(ctx, "push.apply",
		attribute.String(obs.AttrProjectID, projectID.Base36()),
		attribute.String(obs.AttrBranch, branchName),
		attribute.Int(obs.AttrPushFiles, len(files)),
		attribute.Int(obs.AttrPushRemoved, len(removed)),
		attribute.String("nipa.push.base_tree", baseTreeHash))
	defer func() {
		if result != nil {
			span.SetAttributes(
				attribute.String("nipa.push.commit", result.CommitID.Base36()),
				attribute.String("nipa.push.commit_hash", result.CommitHash.String()),
			)
		}
		obs.FinishSpan(span, err)
	}()
	defer func() {
		if err != nil {
			p.metrics.ObservePush("error")
			return
		}
		p.metrics.ObservePush("ok")
	}()
	if !p.permUc.HasProjectAccess(ctx, projectID, domain.PermissionWrite) {
		return nil, domain.NewErrorNoPermission()
	}
	if err := p.requireWritableStorage(ctx, projectID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(message) == "" {
		return nil, domain.NewErrorUser("commit message is required")
	}
	if err := checkPushContent(ctx, p.permUc, projectID, files, removed, p.MaxFiles()); err != nil {
		return nil, err
	}

	branch, err := p.pushBranch(ctx, projectID, branchName)
	if err != nil {
		return nil, err
	}

	claim, ok := domain.ClaimFromContext(ctx)
	if !ok {
		return nil, domain.NewErrorNoPermission()
	}

	var headCommit *domain.Commit
	if branch.CommitID != nil {
		if headCommit, err = p.branchRepo.GetCommit(ctx, *branch.CommitID); err != nil {
			return nil, err
		}
	}

	var headFlags map[string]bool
	if headCommit != nil && (p.fileLocks != nil || p.hooks != nil) {
		flags, err := p.headBinaryPaths(ctx, headCommit, touchedPushPaths(files, removed))
		if err != nil {
			if p.fileLocks != nil {
				return nil, err
			}
			slog.Warn("scanning the head tree for the webhook payload failed", "project", projectID, "error", err)
		} else {
			headFlags = flags
		}
	}

	var lockPlan pushLockPlan
	if p.fileLocks != nil {
		lockPlan = planPushLocks(files, removed, headFlags)
		if err := p.fileLocks.EnsureLocks(ctx, projectID, branch, lockPlan.required, lockPlan.checked, claim.UserID); err != nil {
			return nil, err
		}
	}

	var parent2Commit *domain.Commit
	if parent2CommitHash != "" {
		hash, err := domain.ParseHashHex(parent2CommitHash)
		if err != nil {
			return nil, domain.NewErrorUser(fmt.Sprintf("invalid parent_2 commit hash %q", parent2CommitHash))
		}
		if parent2Commit, err = p.branchRepo.GetCommitByHash(ctx, projectID, hash); err != nil {
			if domain.IsErrorNotFound(err) {
				return nil, domain.NewErrorNotFound(fmt.Sprintf("parent_2 commit %s not found", parent2CommitHash))
			}
			return nil, err
		}
	}

	if baseCommitID != "" {
		if err := checkBaseCommit(branch, baseCommitID); err != nil {
			return nil, err
		}
	} else {
		headTreeHash, err := p.headTreeHash(ctx, headCommit)
		if err != nil {
			return nil, err
		}
		if baseTreeHash == "" {
			baseTreeHash = treehash.TreeHash(nil, nil).String()
		}
		matches, err := p.baseTreeMatches(ctx, headCommit, headTreeHash, baseTreeHash)
		if err != nil {
			return nil, err
		}
		if !matches {
			return nil, domain.NewErrorConflict(fmt.Sprintf("branch %q has moved: expected base tree hash %s, current head is %s", branchName, baseTreeHash, headTreeHash))
		}
	}

	view, err := p.pushView(ctx, projectID, files)
	if err != nil {
		return nil, err
	}
	root, err := buildNewTree(ctx, p.branchRepo, headCommit, files, removed)
	if err != nil {
		return nil, err
	}
	req := p.toApplyRequest(ctx, projectID, branch, headCommit, parent2Commit, message, root)
	req.UserID = claim.UserID

	if err := p.pushRepo.ApplyPush(ctx, req); err != nil {
		return nil, err
	}
	if p.fileLocks != nil {
		if err := p.fileLocks.ReleaseLanded(ctx, projectID, branch, lockPlan.landed(), claim.UserID); err != nil {
			return nil, err
		}
	}
	if p.reviews != nil {
		// The commit is already stored, so a bookkeeping failure must not be
		// reported to the client as a failed push.
		if err := p.reviews.NoteBranchPush(ctx, projectID, branch.ID, req.CommitID, claim.UserID, req.CommitHash.String()); err != nil {
			slog.Warn("recording merge request push failed", "project", projectID, "branch", branchName, "error", err)
		}
	}
	if p.hooks != nil {
		// Delivery is post-commit and best effort, same as the review gate.
		if err := p.hooks.EmitPush(ctx, PushEvent{
			ProjectID:  projectID,
			Actor:      claim.UserID,
			Branch:     branch,
			Before:     headCommit,
			AfterID:    req.CommitID,
			Message:    message,
			Files:      files,
			Removed:    removed,
			HeadBinary: headFlags,
		}); err != nil {
			slog.Warn("emitting webhook push event failed", "project", projectID, "branch", branchName, "error", err)
		}
	}
	return &domain.PushResult{
		CommitID:   req.CommitID,
		CommitHash: req.CommitHash,
		TreeHash:   root.Hash,
		View:       view,
	}, nil
}

// pushView describes the pusher's full, recursive, unsparse manifest view:
// its token, computed exactly as ManifestView computes it for a request
// without paths, whether it hides anything at all, and whether it hides a
// pushed path. A client that applies the push to its stored tree records the
// token with the result when that tree is provably this view of the new
// commit (localrepo.ApplyPush), so its next update or switch can ask for a
// delta without having fetched a manifest first.
//
// The read filter comes from the rule set the write check just loaded (the
// permission usecase caches it per project and user), so this costs the
// fingerprint, one SHA-256 and, for a path-restricted reader, one Allow per
// pushed file: nothing proportional to the repository.
func (p *Push) pushView(ctx context.Context, projectID snow.ID, files []*domain.PushFile) (domain.PushView, error) {
	filter, err := p.permUc.CompileFilter(ctx, projectID, domain.PermissionRead)
	if err != nil {
		return domain.PushView{}, err
	}
	view := domain.PushView{
		Token:        manifestView(filter, nil, true),
		Unrestricted: filter.Unrestricted(),
	}
	if !view.Unrestricted {
		for _, file := range files {
			if !filter.Allow(file.Path) {
				view.HidesPushed = true
				break
			}
		}
	}
	return view, nil
}

func touchedPushPaths(files []*domain.PushFile, removed []string) []string {
	seen := map[string]struct{}{}
	paths := make([]string, 0, len(files)+len(removed))
	for _, file := range files {
		if _, ok := seen[file.Path]; ok {
			continue
		}
		seen[file.Path] = struct{}{}
		paths = append(paths, file.Path)
	}
	for _, path := range removed {
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	return paths
}

// markLockableBinaries forces the binary flag on known binary asset
// extensions: the client's flag may only add, never clear, so a hostile client
// cannot dodge the lock requirement by declaring such a file as text.
func markLockableBinaries(files []*domain.PushFile) {
	for _, file := range files {
		if file != nil && chunker.IsLockablePath(file.Path) {
			file.IsBinary = true
		}
	}
}

// pushLockPlan splits the touched binary paths into paths that must be covered
// by the pusher's lock (tracked binaries being changed) and paths that only
// conflict with someone else's lock (new binary files).
type pushLockPlan struct {
	required []string
	checked  []string
}

func (p pushLockPlan) landed() []string {
	paths := make([]string, 0, len(p.required)+len(p.checked))
	paths = append(paths, p.required...)
	paths = append(paths, p.checked...)
	return paths
}

func planPushLocks(files []*domain.PushFile, removed []string, headBinary map[string]bool) pushLockPlan {
	var plan pushLockPlan
	seenRequired := map[string]struct{}{}
	seenChecked := map[string]struct{}{}
	addRequired := func(path string) {
		if _, ok := seenRequired[path]; ok {
			return
		}
		seenRequired[path] = struct{}{}
		plan.required = append(plan.required, path)
	}
	addChecked := func(path string) {
		if _, ok := seenChecked[path]; ok {
			return
		}
		seenChecked[path] = struct{}{}
		plan.checked = append(plan.checked, path)
	}
	for _, file := range files {
		oldBinary, tracked := headBinary[file.Path]
		switch {
		case !tracked:
			if file.IsBinary {
				addChecked(file.Path)
			}
		case oldBinary || file.IsBinary:
			addRequired(file.Path)
		}
	}
	for _, path := range removed {
		if oldBinary, tracked := headBinary[path]; tracked && oldBinary {
			addRequired(path)
		}
	}
	return plan
}

// headBinaryPaths reports the head-tree binary flag of the touched paths that
// are tracked there, so a tracked binary overwritten with text content (or a
// removed binary with an unknown extension) still requires a lock.
func (p *Push) headBinaryPaths(ctx context.Context, headCommit *domain.Commit, paths []string) (map[string]bool, error) {
	result := map[string]bool{}
	if headCommit == nil || len(paths) == 0 {
		return result, nil
	}
	root, err := p.branchRepo.GetTreeNode(ctx, headCommit.TreeID)
	if err != nil {
		return nil, err
	}
	pending := map[string]map[string]struct{}{}
	dirs := map[string]bool{"": true}
	for _, path := range paths {
		dir := dirOf(path)
		if pending[dir] == nil {
			pending[dir] = map[string]struct{}{}
		}
		pending[dir][baseOf(path)] = struct{}{}
		for d := dir; ; {
			dirs[d] = true
			i := strings.LastIndex(d, "/")
			if i < 0 {
				break
			}
			d = d[:i]
		}
	}
	subdirs := subdirIndex(dirs)
	type treeNodeRef struct {
		dir  string
		tree *domain.TreeNode
	}
	queue := []treeNodeRef{{dir: "", tree: root}}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		files, err := p.branchRepo.ListFileMetaByTree(ctx, current.tree.ID)
		if err != nil {
			return nil, err
		}
		names := pending[current.dir]
		for _, file := range files {
			if _, ok := names[file.Name]; ok {
				result[joinPath(current.dir, file.Name)] = file.IsBinary
			}
		}
		for _, sub := range subdirs[current.dir] {
			child, err := p.branchRepo.GetTreeChildByName(ctx, current.tree.ID, sub)
			if domain.IsErrorNotFound(err) {
				continue
			}
			if err != nil {
				return nil, err
			}
			queue = append(queue, treeNodeRef{dir: joinPath(current.dir, sub), tree: child})
		}
	}
	return result, nil
}

func (p *Push) headTreeHash(ctx context.Context, headCommit *domain.Commit) (domain.Hash, error) {
	if headCommit == nil {
		return treehash.TreeHash(nil, nil), nil
	}
	root, err := p.branchRepo.GetTreeNode(ctx, headCommit.TreeID)
	if err != nil {
		return domain.Hash{}, err
	}
	return root.Hash, nil
}

// baseTreeMatches reports whether the client's base tree hash names the head
// tree: its current hash, or the hash it had before the tree hash backfill.
func (p *Push) baseTreeMatches(ctx context.Context, headCommit *domain.Commit, headTreeHash domain.Hash, baseTreeHash string) (bool, error) {
	if strings.EqualFold(baseTreeHash, headTreeHash.String()) {
		return true, nil
	}
	if p.legacy == nil || headCommit == nil {
		return false, nil
	}
	legacy, err := p.legacy.LegacyTreeHash(ctx, headCommit.TreeID)
	if err != nil {
		return false, err
	}
	return legacy != nil && strings.EqualFold(baseTreeHash, legacy.String()), nil
}

func (p *Push) toApplyRequest(ctx context.Context, projectID snow.ID, branch *domain.Branch, headCommit, parent2Commit *domain.Commit, message string, root *pushTreeNode) ApplyPushRequest {
	var parentHashes []domain.Hash
	var parentID *snow.ID
	var parentID2 *snow.ID
	if headCommit != nil {
		parentHashes = []domain.Hash{headCommit.Hash}
		pid := branch.CommitID
		parentID = pid
		if parent2Commit != nil {
			parentHashes = append(parentHashes, parent2Commit.Hash)
			id := parent2Commit.ID
			parentID2 = &id
		}
	}
	return buildApplyRequest(p.snowNode, projectID, branch.ID, parentID, parentID2, parentHashes, message, "", root)
}

// buildApplyRequest turns a built tree into the row-level request the push
// repository writes. Parent hashes and the origin feed the commit hash; parent
// IDs feed the commit rows.
func buildApplyRequest(snowNode snow.Node, projectID, branchID snow.ID, parentID, parent2ID *snow.ID, parentHashes []domain.Hash, message, origin string, root *pushTreeNode) ApplyPushRequest {
	req := ApplyPushRequest{
		ProjectID:  projectID,
		BranchID:   branchID,
		CommitID:   snowNode.Generate(),
		CommitHash: treehash.CommitHashWithOrigin(root.Hash, parentHashes, message, origin),
		ParentID:   parentID,
		ParentID2:  parent2ID,
		Message:    message,
		Origin:     origin,
		IndexPaths: parentID == nil || root.changes != nil,
	}
	if parentID != nil && root.changes != nil {
		// The tree was built on the first parent's tree (push, import, a
		// merge or squash commit, a rebase's first commit), so what the build
		// changed is what the commit changes. A rebase replaces it for its
		// later commits (MergeForMergeRequest).
		req.PathChanges = root.changes.since(nil)
	}

	queue := []*pushTreeNode{root}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		var parentLogicalID *int64
		if n.Parent != nil {
			id := n.Parent.ID
			parentLogicalID = &id
		}
		req.Nodes = append(req.Nodes, PushNodeRow{
			ID:       n.ID,
			Hash:     n.Hash,
			Name:     n.Name,
			Mode:     n.Mode,
			ParentID: parentLogicalID,
		})
		for _, f := range n.Files {
			req.Files = append(req.Files, PushFileRow{
				Name:        f.Name,
				Mode:        f.Mode,
				SizeBytes:   f.SizeBytes,
				IsBinary:    f.IsBinary,
				Encoding:    f.Encoding,
				Hash:        f.Hash,
				TreeID:      n.ID,
				ChunkHashes: f.ChunkHashes,
			})
		}
		for _, kept := range n.KeepDirs {
			id := n.ID
			req.Nodes = append(req.Nodes, PushNodeRow{
				ID:       kept.ID,
				Hash:     kept.Hash,
				Name:     kept.Name,
				Mode:     kept.Mode,
				ParentID: &id,
			})
		}
		queue = append(queue, n.Children...)
	}
	return req
}

// checkPushContent runs the file list checks a push and an import share.
func checkPushContent(ctx context.Context, perm permissionUsecase, projectID snow.ID, files []*domain.PushFile, removed []string, maxFiles int) error {
	if n := len(files) + len(removed); n > maxFiles {
		return tooManyPushFiles(n, maxFiles)
	}
	if err := validatePushFiles(files); err != nil {
		return err
	}
	if err := validateRemovedFiles(removed, files); err != nil {
		return err
	}
	for _, f := range files {
		if got := treehash.FileHash(f.ChunkHashes); got != f.FileHash {
			return domain.NewErrorUser(fmt.Sprintf("file hash mismatch for %q", f.Path))
		}
	}
	return ensurePathWrites(ctx, perm, projectID, files, removed)
}

func ensurePathWrites(ctx context.Context, perm permissionUsecase, projectID snow.ID, files []*domain.PushFile, removed []string) error {
	for _, file := range files {
		if !perm.HasPathAccess(ctx, projectID, file.Path, domain.PermissionWrite) {
			return domain.NewErrorNoPermission()
		}
	}
	for _, path := range removed {
		if !perm.HasPathAccess(ctx, projectID, path, domain.PermissionWrite) {
			return domain.NewErrorNoPermission()
		}
	}
	return nil
}

func tooManyPushFiles(n, limit int) error {
	return domain.NewErrorUser(fmt.Sprintf("push has %d files and removed paths, more than the server limit of %d; push in smaller parts", n, limit))
}

// TooManyFiles is the error a push over the limit gets, for callers that count
// while receiving.
func (p *Push) TooManyFiles(n int) error {
	return tooManyPushFiles(n, p.MaxFiles())
}

func validatePushFiles(files []*domain.PushFile) error {
	seen := map[string]struct{}{}
	for _, f := range files {
		if err := domain.ValidateRepoPath(f.Path); err != nil {
			return err
		}
		if _, ok := seen[f.Path]; ok {
			return domain.NewErrorUser(fmt.Sprintf("duplicate file path %q", f.Path))
		}
		seen[f.Path] = struct{}{}
		if f.SizeBytes < 0 {
			return domain.NewErrorUser(fmt.Sprintf("invalid size %d for %q", f.SizeBytes, f.Path))
		}
		if f.SizeBytes == 0 && len(f.ChunkHashes) > 0 {
			return domain.NewErrorUser(fmt.Sprintf("file %q has chunks but zero size", f.Path))
		}
		if f.SizeBytes > 0 && len(f.ChunkHashes) == 0 {
			return domain.NewErrorUser(fmt.Sprintf("file %q has no chunks", f.Path))
		}
		encoding, ok := chunker.NormalizeEncoding(f.Encoding)
		if !ok {
			return domain.NewErrorUser(fmt.Sprintf("invalid encoding %q for %q", f.Encoding, f.Path))
		}
		f.Encoding = encoding
	}
	return nil
}

func validateRemovedFiles(removed []string, files []*domain.PushFile) error {
	changed := map[string]struct{}{}
	for _, f := range files {
		changed[f.Path] = struct{}{}
	}
	seen := map[string]bool{}
	for _, r := range removed {
		if err := domain.ValidateRemovedRepoPath(r); err != nil {
			return err
		}
		if _, ok := changed[r]; ok {
			return domain.NewErrorUser(fmt.Sprintf("path %q is both pushed and removed", r))
		}
		if seen[r] {
			return domain.NewErrorUser(fmt.Sprintf("duplicate removed path %q", r))
		}
		seen[r] = true
	}
	return nil
}

type pushTreeNode struct {
	ID       int64
	Parent   *pushTreeNode
	Hash     domain.Hash
	Name     string
	Mode     int
	Files    []*pushFile
	Children []*pushTreeNode
	KeepDirs []*pushTreeNode
	// changes is what the build changed against its base tree; only the root
	// carries it.
	changes *treeChanges
}

type pushFile struct {
	Name        string
	Mode        int
	SizeBytes   int64
	IsBinary    bool
	Encoding    string
	Hash        domain.Hash
	ChunkHashes []domain.Hash
}

func buildNewTree(ctx context.Context, branchRepo branchRepository, headCommit *domain.Commit, files []*domain.PushFile, removed []string) (*pushTreeNode, error) {
	b := &treeBuilder{
		branchRepo:  branchRepo,
		changed:     map[string][]*domain.PushFile{},
		removed:     map[string]map[string]bool{},
		rebuildDirs: map[string]bool{},
	}
	if headCommit != nil {
		// A tree without a base is a root commit's, which has no path change
		// rows: nothing is recorded for its files.
		b.changes = newTreeChanges()
		root, err := branchRepo.GetTreeNode(ctx, headCommit.TreeID)
		if err != nil {
			return nil, err
		}
		b.baseRoot = root
	}

	addAncestors := func(dirPath string) {
		for d := dirPath; ; {
			b.rebuildDirs[d] = true
			i := strings.LastIndex(d, "/")
			if i < 0 {
				break
			}
			d = d[:i]
		}
	}
	for _, f := range files {
		d := dirOf(f.Path)
		b.changed[d] = append(b.changed[d], f)
		addAncestors(d)
	}
	for _, r := range removed {
		d := dirOf(r)
		if b.removed[d] == nil {
			b.removed[d] = map[string]bool{}
		}
		b.removed[d][baseOf(r)] = true
		addAncestors(d)
	}
	b.rebuildDirs[""] = true
	b.subdirs = subdirIndex(b.rebuildDirs)

	root, err := b.build(ctx, "", b.baseRoot)
	if err != nil {
		return nil, err
	}
	if len(b.clashes) > 0 {
		return nil, newTreeClashError(b.clashes)
	}
	b.finalize(ctx, root)
	root.changes = b.changes
	return root, nil
}

// treeClash is a name a built directory would hold twice: as a file and as a
// subdirectory. No working copy can hold such a tree, so a clone or update of
// the commit fails for everyone.
type treeClash struct {
	// path is the clashing name with its directory.
	path string
	// filePushed is set when the file comes from the change being applied,
	// not from the base tree.
	filePushed bool
	// dirInBase is set when the directory exists in the base tree and the
	// change leaves content in it.
	dirInBase bool
}

func (c treeClash) describe() string {
	switch {
	case c.filePushed && c.dirInBase:
		return fmt.Sprintf("%q is written as a file, but the folder %q still holds files that are not deleted", c.path, c.path)
	case c.filePushed:
		return fmt.Sprintf("%q is written as a file and files are written below it", c.path)
	case c.dirInBase:
		return fmt.Sprintf("%q already is a file and a folder in the base commit", c.path)
	default:
		return fmt.Sprintf("files are written below %q, but %q is a file that is not deleted", c.path, c.path)
	}
}

// treeClashLimit caps the clashes an error message spells out.
const treeClashLimit = 20

// treeClashError is the cause of the user error buildNewTree returns for a
// change that would store a name as a file and as a directory; callers that
// did not get the change from a user's push (a merge) reword it.
type treeClashError struct {
	clashes []treeClash
}

func (e *treeClashError) Error() string {
	shown := e.clashes
	suffix := ""
	if len(shown) > treeClashLimit {
		shown = shown[:treeClashLimit]
		suffix = fmt.Sprintf(" (+%d more)", len(e.clashes)-treeClashLimit)
	}
	parts := make([]string, 0, len(shown))
	for _, c := range shown {
		parts = append(parts, c.describe())
	}
	return strings.Join(parts, "; ") + suffix
}

func (e *treeClashError) paths() []string {
	paths := make([]string, 0, len(e.clashes))
	for _, c := range e.clashes {
		paths = append(paths, c.path)
	}
	return paths
}

// newTreeClashError is the refusal of a change that would make a path both a
// file and a folder. The change is refused as a whole rather than repaired:
// dropping the folder's content would delete files nobody asked to delete,
// and dropping the file would lose what was pushed.
func newTreeClashError(clashes []treeClash) error {
	sort.Slice(clashes, func(i, j int) bool { return clashes[i].path < clashes[j].path })
	cause := &treeClashError{clashes: clashes}
	err := domain.NewErrorUser("a path cannot be both a file and a folder: " + cause.Error() +
		". Delete what the path was in the same push: stage the deletions too (nipa add <path>, or nipa add . for everything) and push again")
	err.Cause = cause
	return err
}

// mergeTreeClash rewords a tree clash for a server-side merge, where the two
// branches disagree on what a path is: a conflict the user resolves in a
// working copy. Other errors pass through.
func mergeTreeClash(err error, prefix string) error {
	var clash *treeClashError
	if !errors.As(err, &clash) {
		return err
	}
	if prefix != "" {
		prefix += ": "
	}
	paths := clash.paths()
	suffix := ""
	if len(paths) > mergeConflictLimit {
		suffix = fmt.Sprintf(" (+%d more)", len(paths)-mergeConflictLimit)
		paths = paths[:mergeConflictLimit]
	}
	return domain.NewErrorConflict(fmt.Sprintf("%smerge conflicts in %s%s: one branch has the path as a file, the other as a folder; merge the target into the source branch in a working copy, keep one of the two and push", prefix, strings.Join(paths, ", "), suffix))
}

type treeBuilder struct {
	branchRepo  branchRepository
	baseRoot    *domain.TreeNode
	changed     map[string][]*domain.PushFile
	removed     map[string]map[string]bool
	rebuildDirs map[string]bool
	subdirs     map[string][]string
	nextID      int64
	// clashes collects the names that would be a file and a directory at
	// once; a tree with any is never returned.
	clashes []treeClash
	// changes records the base and built state of every file the change
	// touches and every directory it rebuilds: the path change index rows
	// come from it without another tree read. It is nil without a base tree.
	changes *treeChanges
}

func (b *treeBuilder) build(ctx context.Context, dirPath string, baseNode *domain.TreeNode) (*pushTreeNode, error) {
	node := b.alloc(nodeName(dirPath))

	var baseFiles []*domain.File
	var baseChildren []*domain.TreeNode
	if baseNode != nil {
		var err error
		if baseFiles, err = b.branchRepo.ListFilesByTree(ctx, baseNode.ID); err != nil {
			return nil, err
		}
		if baseChildren, err = b.branchRepo.ListTreeChildren(ctx, baseNode.ID); err != nil {
			return nil, err
		}
	}

	byName := map[string]*pushFile{}
	for _, pf := range b.changed[dirPath] {
		name := baseOf(pf.Path)
		byName[name] = &pushFile{
			Name:        name,
			Mode:        pf.Mode,
			SizeBytes:   pf.SizeBytes,
			IsBinary:    pf.IsBinary,
			Encoding:    pf.Encoding,
			Hash:        pf.FileHash,
			ChunkHashes: pf.ChunkHashes,
		}
	}
	removedSet := b.removed[dirPath]
	b.recordFileChanges(dirPath, baseFiles, byName, removedSet)
	for _, bf := range baseFiles {
		if removedSet[bf.Name] {
			continue
		}
		if _, overridden := byName[bf.Name]; overridden {
			continue
		}
		chunks := make([]domain.Hash, 0, len(bf.Chunks))
		for _, c := range bf.Chunks {
			chunks = append(chunks, c.Hash)
		}
		byName[bf.Name] = &pushFile{
			Name:        bf.Name,
			Mode:        bf.Mode,
			SizeBytes:   bf.SizeBytes,
			IsBinary:    bf.IsBinary,
			Encoding:    bf.Encoding,
			Hash:        bf.Hash,
			ChunkHashes: chunks,
		}
	}
	for _, f := range byName {
		node.Files = append(node.Files, f)
	}
	sort.Slice(node.Files, func(i, j int) bool { return node.Files[i].Name < node.Files[j].Name })

	baseChildByName := map[string]*domain.TreeNode{}
	for _, c := range baseChildren {
		baseChildByName[c.Name] = c
	}

	for _, sub := range b.subdirs[dirPath] {
		childBase := baseChildByName[sub]
		child, err := b.build(ctx, joinPath(dirPath, sub), childBase)
		if err != nil {
			return nil, err
		}
		kept := len(child.Files) > 0 || len(child.Children) > 0 || len(child.KeepDirs) > 0
		if b.changes != nil {
			b.changes.dirs[joinPath(dirPath, sub)] = [2]bool{childBase != nil && childBase.Hash != emptyTreeHash, kept}
		}
		if !kept {
			continue
		}
		child.Parent = node
		node.Children = append(node.Children, child)
	}
	sort.Slice(node.Children, func(i, j int) bool { return node.Children[i].Name < node.Children[j].Name })

	for _, c := range baseChildren {
		sub := joinPath(dirPath, c.Name)
		if b.rebuildDirs[sub] || c.Hash == emptyTreeHash {
			continue
		}
		kept := b.alloc(c.Name)
		kept.Hash = c.Hash
		kept.Parent = node
		node.KeepDirs = append(node.KeepDirs, kept)
	}

	// The one place a directory's files and subdirectories meet: a name in
	// both is refused here, whichever write path asked for the tree. It costs
	// a map lookup per subdirectory of a directory that is rebuilt anyway.
	for i, child := range node.Children {
		// Children are sorted. Validated paths never name a directory twice;
		// a caller that skips validation must not get such a tree either.
		if i > 0 && node.Children[i-1].Name == child.Name {
			return nil, domain.NewErrorInternalServer(fmt.Sprintf("directory %q is built twice", joinPath(dirPath, child.Name)))
		}
		if _, clash := byName[child.Name]; clash {
			b.noteClash(dirPath, child.Name, baseChildByName[child.Name] != nil)
		}
	}
	if len(byName) > 0 {
		for _, kept := range node.KeepDirs {
			if _, clash := byName[kept.Name]; clash {
				b.noteClash(dirPath, kept.Name, true)
			}
		}
	}
	return node, nil
}

// recordFileChanges records the base and built state of the files of one
// rebuilt directory that the change pushes or removes. byName holds only the
// pushed files when it is called.
func (b *treeBuilder) recordFileChanges(dirPath string, baseFiles []*domain.File, pushed map[string]*pushFile, removed map[string]bool) {
	if b.changes == nil || (len(pushed) == 0 && len(removed) == 0) {
		return
	}
	base := make(map[string]*domain.File, len(baseFiles))
	for _, bf := range baseFiles {
		base[bf.Name] = bf
	}
	for name, pf := range pushed {
		built := fileState{present: true, hash: pf.Hash, class: domain.ClassifyStoredMode(pf.Mode)}
		b.changes.files[joinPath(dirPath, name)] = [2]fileState{storedFileState(base[name]), built}
	}
	for name := range removed {
		if _, ok := pushed[name]; ok {
			continue
		}
		b.changes.files[joinPath(dirPath, name)] = [2]fileState{storedFileState(base[name]), {}}
	}
}

func (b *treeBuilder) noteClash(dirPath, name string, dirInBase bool) {
	clash := treeClash{path: joinPath(dirPath, name), dirInBase: dirInBase}
	for _, pf := range b.changed[dirPath] {
		if baseOf(pf.Path) == name {
			clash.filePushed = true
			break
		}
	}
	b.clashes = append(b.clashes, clash)
}

// emptyTreeHash is the hash of a directory with no files and no
// subdirectories. Pushes do not store such directories.
var emptyTreeHash = treehash.TreeHash(nil, nil)

func (b *treeBuilder) alloc(name string) *pushTreeNode {
	b.nextID++
	return &pushTreeNode{
		ID:   b.nextID,
		Name: name,
		Mode: 0o444,
	}
}

func (b *treeBuilder) finalize(ctx context.Context, node *pushTreeNode) domain.Hash {
	files := make([]treehash.FileEntry, 0, len(node.Files))
	for _, f := range node.Files {
		files = append(files, treehash.FileEntry{Name: f.Name, Mode: f.Mode, Hash: f.Hash})
	}
	trees := make([]treehash.TreeEntry, 0, len(node.KeepDirs)+len(node.Children))
	for _, k := range node.KeepDirs {
		trees = append(trees, treehash.TreeEntry{Name: k.Name, Hash: k.Hash})
	}
	for _, c := range node.Children {
		trees = append(trees, treehash.TreeEntry{Name: c.Name, Hash: b.finalize(ctx, c)})
	}
	node.Hash = treehash.TreeHash(files, trees)
	return node.Hash
}

func dirOf(p string) string {
	i := strings.LastIndex(p, "/")
	if i < 0 {
		return ""
	}
	return p[:i]
}

func baseOf(p string) string {
	i := strings.LastIndex(p, "/")
	if i < 0 {
		return p
	}
	return p[i+1:]
}

func nodeName(dirPath string) string {
	if dirPath == "" {
		return "root"
	}
	return baseOf(dirPath)
}

func joinPath(dirPath, name string) string {
	if dirPath == "" {
		return name
	}
	return dirPath + "/" + name
}

// subdirIndex maps every directory to the sorted names of its immediate
// subdirectories in dirs.
func subdirIndex(dirs map[string]bool) map[string][]string {
	index := map[string][]string{}
	for d := range dirs {
		if d == "" {
			continue
		}
		parent := ""
		if i := strings.LastIndex(d, "/"); i >= 0 {
			parent = d[:i]
		}
		index[parent] = append(index[parent], baseOf(d))
	}
	for _, names := range index {
		sort.Strings(names)
	}
	return index
}
