package usecase

import (
	"context"
	"errors"
	"log/slog"

	"go.opentelemetry.io/otel/attribute"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/obs"
	"github.com/nipalab/nipa/internal/treedelta"
)

// Values of the nipa.sync.tree_sync span attribute: how an update or switch
// brought the stored tree to its target commit.
const (
	treeSyncDelta = "delta"
	treeSyncFull  = "full"
)

// deltaManifestClient is a transport that can answer a manifest request with
// a delta against a manifest the caller holds, and that returns the view
// token with every manifest.
type deltaManifestClient interface {
	GetTreeManifestDelta(ctx context.Context, org, project string, target treedelta.Target, paths []string, base treedelta.Base) (*treedelta.Manifest, error)
}

// treeDeltaLocalRepo is a repository that can apply a delta manifest to its
// stored tree.
type treeDeltaLocalRepo interface {
	SnapshotTreeHash() (string, error)
	PlanTreeDelta(in clientDomain.TreeDeltaApply) (*clientDomain.TreeDeltaChanges, error)
	ApplyTreeDelta(in clientDomain.TreeDeltaApply, planned *clientDomain.TreeDeltaChanges) (*clientDomain.TreeDeltaChanges, error)
}

// viewTreeSaver is a repository that stores the view token with a manifest.
type viewTreeSaver interface {
	SaveSyncedTreeView(root *domain.TreeNode, commitID string, sparse []string, view []byte) error
}

type syncedTreeSaver interface {
	SaveSyncedTree(root *domain.TreeNode, commitID string, sparse []string) error
}

// fetchManifest returns the full recursive manifest of target and the view
// token the server sent with it. A client without the view-carrying call
// answers through legacy and gives no token.
func fetchManifest(ctx context.Context, client any, org, project string, target treedelta.Target, paths []string, legacy func() (*domain.TreeNode, error)) (*domain.TreeNode, []byte, error) {
	dc, ok := client.(deltaManifestClient)
	if !ok {
		tree, err := legacy()
		return tree, nil, err
	}
	manifest, err := dc.GetTreeManifestDelta(ctx, org, project, target, paths, treedelta.Base{})
	if err != nil {
		return nil, nil, err
	}
	if manifest == nil {
		return nil, nil, nil
	}
	if manifest.Delta != nil {
		return nil, nil, errors.New("the server sent a delta manifest nobody asked for")
	}
	return manifest.Full, manifest.View, nil
}

// saveSyncedTree saves a fetched manifest as commitID's tree, with its view
// token when the repository keeps one.
func saveSyncedTree(lr syncedTreeSaver, tree *domain.TreeNode, commitID string, sparse []string, view []byte) error {
	if saver, ok := lr.(viewTreeSaver); ok && len(view) > 0 {
		return saver.SaveSyncedTreeView(tree, commitID, sparse, view)
	}
	return lr.SaveSyncedTree(tree, commitID, sparse)
}

// syncGoal is the commit an update or switch moves the working copy to.
type syncGoal struct {
	// commitID is the target commit, "" for a branch without commits.
	commitID string
	// branch is what a manifest is asked by when there is no commit id, and
	// what a client without the delta call is asked by unless detached is set.
	branch   string
	detached bool
}

// syncTo brings the working copy and the stored tree to goal with the
// configured sparse set.
//
// When the stored tree is recorded as the manifest of a commit in the current
// view (synced-tree record with a view token and the same sparse set), the
// server is asked for a delta against it, by commit id, and the answer is
// applied to the stored rows (localrepo.PlanTreeDelta / ApplyTreeDelta) while
// only the files it touches are checked, written or removed in the working
// copy. Everything else gets the full manifest and the full sync, as before:
// no usable base, a server that declined, and, silently, any failure to plan
// or apply the delta. The sync.tree span names the path taken
// (nipa.sync.tree_sync) and why a full manifest was used
// (nipa.sync.tree_sync_reason).
//
// The returned effect is non-nil when only the delta's rows changed, so the
// caller prunes the object cache for those instead of sweeping it.
func (u *Update) syncTo(ctx context.Context, root string, nu *clientDomain.NipaUrl, sparse []string, goal syncGoal, progress ...DownloadProgress) (effect *pushCacheEffect, err error) {
	ctx, span := obs.StartSpan(ctx, "sync.tree")
	defer func() { obs.FinishSpan(span, err) }()

	scope := clientDomain.ChunkScope{
		Org:       nu.Org,
		Project:   nu.Project,
		CommitIDs: commitIDs(goal.commitID),
		Paths:     sparse,
	}
	target := treedelta.Target{CommitID: goal.commitID}
	if goal.commitID == "" {
		target.Branch = goal.branch
	}
	legacy := func() (*domain.TreeNode, error) {
		if goal.detached {
			return u.client.GetCommitTreeManifest(ctx, nu.Org, nu.Project, goal.commitID, sparse)
		}
		return u.client.GetTreeNodeManifest(ctx, nu.Org, nu.Project, goal.branch, sparse)
	}

	base, reason := u.deltaBase(goal, sparse)
	if base.CommitID != "" {
		manifest, err := u.client.(deltaManifestClient).GetTreeManifestDelta(ctx, nu.Org, nu.Project, target, sparse, base)
		if err != nil {
			return nil, err
		}
		switch {
		case manifest == nil || manifest.Delta == nil:
			// Declined: the answer is the full manifest, used as it is.
			span.SetAttributes(
				attribute.String("nipa.sync.tree_sync", treeSyncFull),
				attribute.String("nipa.sync.tree_sync_reason", "declined"),
			)
			var tree *domain.TreeNode
			var view []byte
			if manifest != nil {
				tree, view = manifest.Full, manifest.View
			}
			return nil, u.syncFull(ctx, root, tree, view, goal.commitID, sparse, scope, progress...)
		default:
			effect, reason, err = u.syncDelta(ctx, root, manifest, base, goal.commitID, sparse, scope, progress...)
			if err != nil {
				return nil, err
			}
			if reason == "" {
				span.SetAttributes(attribute.String("nipa.sync.tree_sync", treeSyncDelta))
				return effect, nil
			}
		}
	}
	span.SetAttributes(
		attribute.String("nipa.sync.tree_sync", treeSyncFull),
		attribute.String("nipa.sync.tree_sync_reason", reason),
	)
	tree, view, err := fetchManifest(ctx, u.client, nu.Org, nu.Project, target, sparse, legacy)
	if err != nil {
		return nil, err
	}
	return nil, u.syncFull(ctx, root, tree, view, goal.commitID, sparse, scope, progress...)
}

// syncFull is the sync every update did before deltas: the whole manifest is
// compared with the snapshot and the working copy, and saved.
func (u *Update) syncFull(ctx context.Context, root string, tree *domain.TreeNode, view []byte, commitID string, sparse []string, scope clientDomain.ChunkScope, progress ...DownloadProgress) error {
	if err := u.sync(ctx, root, tree, scope, progress...); err != nil {
		return err
	}
	return saveSyncedTree(u.localRepo, tree, commitID, sparse, view)
}

// deltaBase returns the manifest the stored tree is recorded as, for naming
// it as the base of a delta request, or an empty base and the reason there is
// none: "client"/"repo" when either side cannot do deltas, "empty" for a
// target without a commit, "record" when the tree is not recorded as any
// commit's, "sparse" when it was fetched with another sparse set and "view"
// when the server sent no view token with it. Nothing here is an error: a
// failed read only costs the full manifest.
func (u *Update) deltaBase(goal syncGoal, sparse []string) (treedelta.Base, string) {
	if _, ok := u.client.(deltaManifestClient); !ok {
		return treedelta.Base{}, "client"
	}
	lr, ok := u.localRepo.(treeDeltaLocalRepo)
	if !ok {
		return treedelta.Base{}, "repo"
	}
	if goal.commitID == "" {
		return treedelta.Base{}, "empty"
	}
	synced, err := u.localRepo.LoadSyncedTree()
	if err != nil || synced == nil || synced.CommitID == "" {
		return treedelta.Base{}, "record"
	}
	if !sameSparseSet(synced.Sparse, sparse) {
		return treedelta.Base{}, "sparse"
	}
	if len(synced.View) == 0 {
		return treedelta.Base{}, "view"
	}
	base := treedelta.Base{CommitID: synced.CommitID, View: synced.View}
	// The root hash is optional: with it the server can vouch for the root
	// hash of the result.
	if stored, err := lr.SnapshotTreeHash(); err == nil {
		if hash, err := domain.ParseHashHex(stored); err == nil {
			base.TreeHash = &hash
		}
	}
	return base, ""
}

// syncDelta applies a delta manifest: the stored rows tell which files it
// adds, modifies and removes, the working copy is synced for those alone, and
// the rows are then written in one transaction. A non-empty reason means the
// delta could not be used and the caller must sync from the full manifest:
// "commit" (the delta leads to another commit than the one asked for),
// "base" (the stored tree is not the delta's base), "mismatch" (the result
// does not hash to the root the delta or the plan names) or "error". Local
// changes in the way, download and write failures are errors, as in the full
// sync.
func (u *Update) syncDelta(ctx context.Context, root string, manifest *treedelta.Manifest, base treedelta.Base, commitID string, sparse []string, scope clientDomain.ChunkScope, progress ...DownloadProgress) (effect *pushCacheEffect, reason string, err error) {
	if manifest.CommitID != commitID {
		slog.Debug("delta manifest leads to another commit; fetching the manifest", "asked", commitID, "got", manifest.CommitID)
		return nil, "commit", nil
	}
	lr := u.localRepo.(treeDeltaLocalRepo)
	in := clientDomain.TreeDeltaApply{
		Delta:        manifest.Delta,
		BaseCommitID: base.CommitID,
		CommitID:     commitID,
		Sparse:       sparse,
		View:         manifest.View,
	}
	changes, err := lr.PlanTreeDelta(in)
	if err != nil {
		return nil, deltaFallbackReason(err), nil
	}
	written, err := syncDeltaWorkingCopy(ctx, u.client, u.localRepo, root, changes, scope, syncOptions{keepLocalChanges: !u.force, restoreMissing: u.force}, progress...)
	if err != nil {
		return nil, "", err
	}
	if _, err := lr.ApplyTreeDelta(in, changes); err != nil {
		// The working copy already holds the target's files; the full sync
		// that follows finds them in place and saves the manifest.
		return nil, deltaFallbackReason(err), nil
	}
	return newDeltaCacheEffect(changes, written), "", nil
}

// deltaFallbackReason names why a delta gave way to the full manifest and
// logs it; the user never sees it.
func deltaFallbackReason(err error) string {
	var mismatch *clientDomain.TreeDeltaMismatchError
	switch {
	case errors.As(err, &mismatch):
		slog.Debug("delta manifest does not lead to the expected tree hash; fetching the manifest",
			"expected", mismatch.Expected.String(), "computed", mismatch.Computed.String())
		return "mismatch"
	case errors.Is(err, clientDomain.ErrTreeDeltaBase):
		slog.Debug("stored tree is not the base of the delta manifest; fetching the manifest", "error", err)
		return "base"
	default:
		slog.Debug("applying the delta manifest failed; fetching the manifest", "error", err)
		return "error"
	}
}

// syncDeltaWorkingCopy is syncWorkingCopyOpts for a delta: it writes the
// files the delta adds or modifies and removes the ones it drops, with the
// same guards (unsafe names, link targets, local changes), downloads and
// fingerprints, and looks at no other file. With opts.restoreMissing (a
// forced sync) it also restores every tracked file that is gone from disk,
// which reads the snapshot and stats each file but still writes no row. It
// returns the files it wrote.
func syncDeltaWorkingCopy(ctx context.Context, client chunkDownloader, lr workingCopyLocalRepo, root string, changes *clientDomain.TreeDeltaChanges, scope clientDomain.ChunkScope, opts syncOptions, progress ...DownloadProgress) (written []materializedFile, err error) {
	ctx, span := obs.StartSpan(ctx, "nipa.sync",
		attribute.String(obs.AttrOrg, scope.Org),
		attribute.String(obs.AttrProject, scope.Project),
		attribute.Bool("nipa.sync.delta", true))
	defer func() { obs.FinishSpan(span, err) }()

	for _, w := range changes.Written {
		if err := checkWorkingPath(w.Path); err != nil {
			return nil, err
		}
	}
	endWrite, err := beginWorkingWrite(lr, root)
	if err != nil {
		return nil, err
	}
	defer endWrite()
	pins, err := loadPins(lr)
	if err != nil {
		return nil, err
	}

	plan := newSyncPlan(pins, opts)
	touched := make(map[string]struct{}, len(changes.Written)+len(changes.Removed))
	for _, w := range changes.Written {
		touched[w.Path] = struct{}{}
		var base clientDomain.SnapshotFile
		if w.Base != nil {
			base = *w.Base
		}
		if w.Base != nil && base.Hash == treeFileHash(w.File) && base.Mode == w.File.Mode && fileExists(root, w.Path) {
			// Only what the row records changed (the binary flag, the
			// encoding label): the working file is already right.
			continue
		}
		plan.write(materializedFromTree(w.Path, w.File), base, w.Base != nil)
	}
	for _, f := range changes.Removed {
		// A path that turned from a file into a directory, or the reverse,
		// is both removed and written below; the removal still applies.
		touched[f.Path] = struct{}{}
	}
	plan.removals = changes.Removed

	if opts.restoreMissing {
		snapshot, err := lr.Snapshot()
		if err != nil {
			return nil, err
		}
		for _, f := range snapshot.Files {
			if _, done := touched[f.Path]; done || fileExists(root, f.Path) {
				continue
			}
			plan.write(materializedFromSnapshot(f), f, true)
		}
	}
	if err := plan.run(ctx, client, lr, root, narrowChunkScope(scope, plan.jobFiles), progress...); err != nil {
		return nil, err
	}
	return plan.jobFiles, nil
}

// maxNarrowedScopePaths bounds the file paths a delta sync names as its chunk
// download scope.
const maxNarrowedScopePaths = 256

// narrowChunkScope restricts a download scope to the files a delta sync
// writes. The server checks every download against the chunks the caller may
// read in the scope, which it finds by walking the commit's tree below the
// scope's paths: with the checkout's own scope (the sparse set, or nothing)
// that walk covers the whole tree, with the written files' paths only the
// directories leading to them. The files are part of the original scope, so
// the narrowed one grants nothing more. Many files, or a path the server
// would not take as a prefix, keep the scope as it is.
func narrowChunkScope(scope clientDomain.ChunkScope, files []materializedFile) clientDomain.ChunkScope {
	if len(files) == 0 || len(files) > maxNarrowedScopePaths {
		return scope
	}
	paths := make([]string, 0, len(files))
	for _, f := range files {
		if normalized, err := domain.NormalizePathPrefix(f.Path); err != nil || normalized != f.Path {
			return scope
		}
		paths = append(paths, f.Path)
	}
	scope.Paths = paths
	return scope
}

// materializedFromSnapshot describes a tracked file for writing it back as
// the snapshot records it. The snapshot keeps no chunk sizes, so such a file
// is always written from the object cache.
func materializedFromSnapshot(f clientDomain.SnapshotFile) materializedFile {
	return materializedFile{
		Path:        f.Path,
		Mode:        f.Mode,
		SizeBytes:   f.SizeBytes,
		Encoding:    f.Encoding,
		FileHash:    f.Hash,
		ChunkHashes: f.Chunks,
	}
}

// newDeltaCacheEffect is what a delta sync can have left unreferenced in the
// object cache: the chunks of the rows it replaced or removed, and the chunks
// it downloaded for the files it wrote.
func newDeltaCacheEffect(changes *clientDomain.TreeDeltaChanges, written []materializedFile) *pushCacheEffect {
	effect := &pushCacheEffect{applied: true}
	seen := map[domain.Hash]bool{}
	add := func(hashes []domain.Hash) {
		for _, h := range hashes {
			if !seen[h] {
				seen[h] = true
				effect.chunks = append(effect.chunks, h)
			}
		}
	}
	for _, w := range changes.Written {
		effect.paths = append(effect.paths, w.Path)
		if w.Base != nil {
			add(w.Base.Chunks)
		}
		for _, c := range w.File.Chunks {
			add([]domain.Hash{c.Hash})
		}
	}
	for _, f := range changes.Removed {
		add(f.Chunks)
	}
	for _, f := range written {
		effect.paths = append(effect.paths, f.Path)
		add(f.ChunkHashes)
	}
	return effect
}
