package usecase

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"slices"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/treedelta"
	"github.com/nipalab/nipa/internal/treehash"
)

// manifestViewVersion is part of every view token. Bump it whenever the way a
// manifest is filtered changes (a new always-kept root file, a new rule
// kind), so tokens handed out before the change stop matching.
const manifestViewVersion = "nipa-manifest-view-v1"

// ManifestTarget names the commit a manifest is built from: CommitID when
// set, the head of Branch otherwise.
type ManifestTarget struct {
	Branch   string
	CommitID *snow.ID
}

// ManifestBase is the manifest a client holds and wants a delta against.
type ManifestBase struct {
	CommitID snow.ID
	// View is the token the server returned with that manifest.
	View []byte
	// TreeHash is the root hash of that manifest, when the client sent it. It
	// is only used to prove the root hash of the result.
	TreeHash *domain.Hash
}

// ManifestDelta is a delta manifest and the commit it leads to.
type ManifestDelta struct {
	Commit *domain.Commit
	Delta  *treedelta.Delta
}

// ManifestView returns the view token of a manifest request: a fingerprint of
// everything besides the commit that decides what a manifest holds, namely the
// caller's compiled read filter, the sparse set and the recursive flag. Two
// requests with the same token see the same paths of any commit. The token is
// a hash of rule content only, so it is the same on every server instance and
// across restarts, and it changes as soon as a rule or path default that
// affects reading does.
func (b *Branch) ManifestView(ctx context.Context, projectID snow.ID, paths []string, recursive bool) ([]byte, error) {
	sparse, err := domain.NewPrefixSet(paths)
	if err != nil {
		return nil, domain.NewErrorUser(err.Error())
	}
	filter, err := b.permUc.CompileFilter(ctx, projectID, domain.PermissionRead)
	if err != nil {
		return nil, err
	}
	return manifestView(filter, sparse, recursive), nil
}

// manifestView returns nil for a filter that has no fingerprint; a nil token
// never matches, so such a view never gets a delta.
func manifestView(filter *PathFilter, sparse domain.PrefixSet, recursive bool) []byte {
	buf := append([]byte(manifestViewVersion), 0)
	buf, ok := filter.appendFingerprint(buf)
	if !ok {
		return nil
	}
	// The sparse set is taken as sent, minus order and duplicates: two sets
	// that cover the same paths in different words get different tokens,
	// which only costs a full manifest.
	prefixes := slices.Clone([]string(sparse))
	slices.Sort(prefixes)
	prefixes = slices.Compact(prefixes)
	buf = append(buf, 's')
	buf = binary.AppendUvarint(buf, uint64(len(prefixes)))
	for _, prefix := range prefixes {
		buf = binary.AppendUvarint(buf, uint64(len(prefix)))
		buf = append(buf, prefix...)
	}
	if recursive {
		buf = append(buf, 1)
	} else {
		buf = append(buf, 0)
	}
	sum := sha256.Sum256(buf)
	return sum[:]
}

// GetTreeManifestDelta returns the recursive manifest of target as a delta
// against the manifest the caller holds, or nil when it declines and the
// caller must send the full manifest instead. Declining is never an error: it
// happens when the base commit is unknown, belongs to another project or the
// caller may not read the project, when base.View is not the token of the
// caller's current view (a read rule or the sparse set changed since), and for
// every request the full manifest answers with an error or with nothing (an
// unknown branch, an empty branch, a sparse path missing from the target), so
// that those answers stay in one place.
//
// The two trees are walked together level by level and only where they
// differ: a child directory stored with the same hash in both is the same
// subtree (hashes resolve to content, see ListTreeChildrenByTrees) and is
// reported unchanged without being read. Each level costs one file batch and
// one children batch over the changed directories of both sides, and chunk
// lists are read once, for added and modified files only.
func (b *Branch) GetTreeManifestDelta(ctx context.Context, projectID snow.ID, target ManifestTarget, paths []string, base ManifestBase) (*ManifestDelta, error) {
	if len(base.View) == 0 || !b.permUc.HasProjectAccess(ctx, projectID, domain.PermissionRead) {
		return nil, nil
	}
	sparse, err := domain.NewPrefixSet(paths)
	if err != nil {
		return nil, nil
	}
	filter, err := b.permUc.CompileFilter(ctx, projectID, domain.PermissionRead)
	if err != nil {
		return nil, declineOrFail(err)
	}
	if view := manifestView(filter, sparse, true); view == nil || !bytes.Equal(view, base.View) {
		return nil, nil
	}
	targetCommit, err := b.manifestTargetCommit(ctx, projectID, target)
	if err != nil || targetCommit == nil {
		return nil, declineOrFail(err)
	}
	baseCommit, err := b.commitByIDInProject(ctx, projectID, base.CommitID)
	if err != nil {
		return nil, declineOrFail(err)
	}
	targetRoot, err := b.branchRepo.GetTreeNode(ctx, targetCommit.TreeID)
	if err != nil {
		return nil, err
	}
	baseRoot, err := b.branchRepo.GetTreeNode(ctx, baseCommit.TreeID)
	if err != nil {
		return nil, err
	}
	if err := b.verifySparsePaths(ctx, targetRoot, sparse, filter, "the target"); err != nil {
		return nil, declineOrFail(err)
	}
	walk := &deltaWalk{
		repo:   b.branchRepo,
		filter: filter,
		sparse: sparse,
		// See deltaWalk.provable.
		provable: base.TreeHash != nil && *base.TreeHash == baseRoot.Hash,
	}
	delta, err := walk.run(ctx, baseRoot, targetRoot)
	if err != nil {
		return nil, err
	}
	return &ManifestDelta{Commit: targetCommit, Delta: delta}, nil
}

// declineOrFail turns the errors a full manifest reports to the caller (not
// found, no permission, a bad request) into a decline and keeps server-side
// failures.
func declineOrFail(err error) error {
	if err == nil || !domain.IsServerError(err) {
		return nil
	}
	return err
}

// manifestTargetCommit resolves target. A nil commit without an error is a
// branch without commits.
func (b *Branch) manifestTargetCommit(ctx context.Context, projectID snow.ID, target ManifestTarget) (*domain.Commit, error) {
	if target.CommitID != nil {
		return b.commitByIDInProject(ctx, projectID, *target.CommitID)
	}
	branch, err := b.branchRepo.GetBranchByName(ctx, projectID, target.Branch)
	if err != nil || branch.CommitID == nil {
		return nil, err
	}
	return b.branchRepo.GetCommit(ctx, *branch.CommitID)
}

type deltaTreeReader interface {
	ListTreeChildrenByTrees(ctx context.Context, parentIDs []int64) (map[int64][]*domain.TreeNode, error)
	ListFileMetaByTrees(ctx context.Context, treeIDs []int64) (map[int64][]*domain.File, error)
	ListChunksByFiles(ctx context.Context, fileIDs []int64) (map[int64][]domain.Chunk, error)
}

// deltaWalk computes one delta. What a manifest holds for a tree is a
// function of the tree and of the view (filter and sparse) only, so for a
// fixed view:
//
//   - two directories at the same path with the same stored hash give the same
//     manifest subtree, whatever the view hides in them;
//   - two with different stored hashes may still give the same one, when only
//     hidden entries differ. The walk notices that for the directories it
//     reads (deltaPair.same) and reports them unchanged too;
//   - whether an unchanged directory shows anything at all in the view is not
//     known without reading it, so the delta never says: "unchanged" means
//     "keep what you hold, if anything", and treedelta.Apply drops the changed
//     directories that end up empty exactly as loadTreeManifest does.
type deltaWalk struct {
	repo   deltaTreeReader
	filter *PathFilter
	sparse domain.PrefixSet
	// provable says the root hash of the target's full manifest is known to
	// be its stored root hash. A manifest carries rehashed directories
	// (rehashTree), which equal the stored hashes only when the view hides
	// nothing of the tree and the stored hashes are sound all the way down:
	// trees written before file modes were hashed and not yet backfilled
	// (BackfillTreeHashes), or holding empty directory rows, are not. The
	// proof needs no read of an unchanged directory. It starts from the
	// client's own root hash of the base manifest equalling the stored base
	// root, which already rules out a view that hides part of the base, and
	// then requires every directory the walk reads, on both sides, to hash to
	// its stored hash from the entries the manifest lists: on the base side
	// that pins the manifest hash of each child to its stored hash, unchanged
	// children included, and on the target side it rebuilds the manifest hash
	// from those. One directory that does not hash to its stored hash (a
	// hidden entry, an unsound stored hash) ends the proof and the delta
	// carries no root hash.
	provable bool
	// pending are the added and modified files waiting for their chunk lists.
	pending []*domain.File
}

// deltaPair is one changed directory of the walk: target with the directory
// base holds at the same path, nil when the path is new or was a file.
type deltaPair struct {
	path         string
	base, target *domain.TreeNode
	out          *treedelta.Dir
	children     []*deltaPair
	// entriesSame says the visible files and the child directory names are
	// the same on both sides; with every child pair being same, the whole
	// directory is.
	entriesSame bool
	same        bool
}

func (w *deltaWalk) run(ctx context.Context, baseRoot, targetRoot *domain.TreeNode) (*treedelta.Delta, error) {
	root := &deltaPair{base: baseRoot, target: targetRoot, out: &treedelta.Dir{Name: targetRoot.Name}}
	if baseRoot.Hash == targetRoot.Hash {
		root.out.Unchanged = true
		return w.result(root), nil
	}
	levels := [][]*deltaPair{{root}}
	for {
		next, err := w.level(ctx, levels[len(levels)-1])
		if err != nil {
			return nil, err
		}
		if len(next) == 0 {
			break
		}
		levels = append(levels, next)
	}
	// Children are settled before their parents: a directory whose entries
	// all turned out to be the base's is itself unchanged, and a changed one
	// left without entries is not listed.
	for i := len(levels) - 1; i >= 0; i-- {
		for _, pair := range levels[i] {
			pair.settle()
		}
	}
	if err := w.loadChunks(ctx); err != nil {
		return nil, err
	}
	return w.result(root), nil
}

func (w *deltaWalk) result(root *deltaPair) *treedelta.Delta {
	delta := &treedelta.Delta{Root: root.out}
	// A root that differs in the store but not in the view cannot happen in a
	// provable walk; if it does, the stored hash is not to be trusted.
	if w.provable && !root.same {
		hash := root.target.Hash
		delta.RootHash = &hash
	}
	return delta
}

func (p *deltaPair) settle() {
	p.same = p.base != nil && p.entriesSame
	kept := p.out.Dirs[:0]
	next := 0
	for _, dir := range p.out.Dirs {
		// The child pairs are the directories that were not unchanged by
		// their stored hash, in listing order; they are settled already.
		if next == len(p.children) || p.children[next].out != dir {
			kept = append(kept, dir)
			continue
		}
		child := p.children[next]
		next++
		if !child.same {
			p.same = false
			if len(dir.Files) == 0 && len(dir.Dirs) == 0 {
				continue
			}
		}
		kept = append(kept, dir)
	}
	clear(p.out.Dirs[len(kept):])
	p.out.Dirs = kept
	if p.same {
		p.out.Unchanged, p.out.Files, p.out.Dirs = true, nil, nil
	}
}

// level reads the directories of one level on both sides with one batch each
// and returns the changed directories below them.
func (w *deltaWalk) level(ctx context.Context, level []*deltaPair) ([]*deltaPair, error) {
	ids := make([]int64, 0, 2*len(level))
	for _, pair := range level {
		for _, node := range []*domain.TreeNode{pair.base, pair.target} {
			// The empty tree owns no rows, so there is nothing to resolve.
			if node != nil && node.Hash != emptyTreeHash {
				ids = append(ids, node.ID)
			}
		}
	}
	files, err := w.repo.ListFileMetaByTrees(ctx, ids)
	if err != nil {
		return nil, err
	}
	children, err := w.repo.ListTreeChildrenByTrees(ctx, ids)
	if err != nil {
		return nil, err
	}
	var next []*deltaPair
	for _, pair := range level {
		var baseFiles []*domain.File
		var baseDirs []*domain.TreeNode
		if pair.base != nil {
			baseFiles = w.visibleFiles(pair.path, files[pair.base.ID])
			baseDirs = w.visibleDirs(pair.path, children[pair.base.ID])
			w.prove(pair.base, baseFiles, baseDirs)
		}
		targetFiles := w.visibleFiles(pair.path, files[pair.target.ID])
		targetDirs := w.visibleDirs(pair.path, children[pair.target.ID])
		w.prove(pair.target, targetFiles, targetDirs)

		pair.entriesSame = len(baseFiles) == len(targetFiles) && len(baseDirs) == len(targetDirs)
		held := make(map[string]*domain.File, len(baseFiles))
		for _, file := range baseFiles {
			held[file.Name] = file
		}
		pair.out.Files = make([]treedelta.File, 0, len(targetFiles))
		for _, file := range targetFiles {
			entry := treedelta.File{Name: file.Name}
			if old := held[file.Name]; old == nil || !sameManifestFile(old, file) {
				// The same file row can sit at several paths of a level.
				changed := *file
				entry.Changed = &changed
				w.pending = append(w.pending, entry.Changed)
				pair.entriesSame = false
			}
			pair.out.Files = append(pair.out.Files, entry)
		}
		heldDirs := make(map[string]*domain.TreeNode, len(baseDirs))
		for _, dir := range baseDirs {
			heldDirs[dir.Name] = dir
		}
		pair.out.Dirs = make([]*treedelta.Dir, 0, len(targetDirs))
		for _, dir := range targetDirs {
			old := heldDirs[dir.Name]
			if old == nil {
				pair.entriesSame = false
			}
			out := &treedelta.Dir{Name: dir.Name}
			pair.out.Dirs = append(pair.out.Dirs, out)
			if old != nil && old.Hash == dir.Hash {
				out.Unchanged = true
				continue
			}
			child := &deltaPair{path: joinTreePath(pair.path, dir.Name), base: old, target: dir, out: out}
			pair.children = append(pair.children, child)
			next = append(next, child)
		}
	}
	return next, nil
}

// visibleFiles and visibleDirs apply the view exactly as loadManifestLevel
// does.
func (w *deltaWalk) visibleFiles(path string, files []*domain.File) []*domain.File {
	if w.filter.All() && w.sparse.Empty() {
		return files
	}
	kept := make([]*domain.File, 0, len(files))
	for _, file := range files {
		filePath := joinTreePath(path, file.Name)
		if !w.filter.Allow(filePath) {
			continue
		}
		if !w.sparse.Covers(filePath) && !keepRootTeamFile(path, file.Name, w.sparse) {
			continue
		}
		kept = append(kept, file)
	}
	return kept
}

func (w *deltaWalk) visibleDirs(path string, dirs []*domain.TreeNode) []*domain.TreeNode {
	kept := make([]*domain.TreeNode, 0, len(dirs))
	for _, dir := range dirs {
		dirPath := joinTreePath(path, dir.Name)
		if !w.filter.CanDescend(dirPath) || !w.sparse.CanDescend(dirPath) {
			continue
		}
		if dir.Hash == emptyTreeHash {
			continue
		}
		kept = append(kept, dir)
	}
	return kept
}

// prove ends the root hash proof (deltaWalk.provable) unless node hashes to
// its stored hash from the entries a manifest lists for it.
func (w *deltaWalk) prove(node *domain.TreeNode, files []*domain.File, dirs []*domain.TreeNode) {
	if !w.provable {
		return
	}
	fileEntries := make([]treehash.FileEntry, 0, len(files))
	for _, file := range files {
		fileEntries = append(fileEntries, treehash.FileEntry{Name: file.Name, Hash: file.Hash, Mode: file.Mode})
	}
	treeEntries := make([]treehash.TreeEntry, 0, len(dirs))
	for _, dir := range dirs {
		treeEntries = append(treeEntries, treehash.TreeEntry{Name: dir.Name, Hash: dir.Hash})
	}
	if treehash.TreeHash(fileEntries, treeEntries) != node.Hash {
		w.provable = false
	}
}

// sameManifestFile reports whether a manifest carries the same entry for both
// files. A file hash covers the chunk hashes (Push checks it), and a chunk's
// size follows from its hash within a project, so equal hashes mean equal
// chunk lists.
func sameManifestFile(a, b *domain.File) bool {
	if a.ID == b.ID {
		return true
	}
	return a.Hash == b.Hash &&
		domain.ClassifyStoredMode(a.Mode) == domain.ClassifyStoredMode(b.Mode) &&
		a.IsBinary == b.IsBinary &&
		a.Encoding == b.Encoding &&
		a.SizeBytes == b.SizeBytes
}

func (w *deltaWalk) loadChunks(ctx context.Context) error {
	if len(w.pending) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(w.pending))
	for _, file := range w.pending {
		ids = append(ids, file.ID)
	}
	chunks, err := w.repo.ListChunksByFiles(ctx, ids)
	if err != nil {
		return err
	}
	for _, file := range w.pending {
		if list := chunks[file.ID]; len(list) > 0 {
			file.Chunks = slices.Clone(list)
		}
	}
	return nil
}
