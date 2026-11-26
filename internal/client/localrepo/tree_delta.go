package localrepo

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/domain"
	sqlcLocalrepo "github.com/nipalab/nipa/internal/repository/sqlc/localrepo"
	"github.com/nipalab/nipa/internal/treedelta"
	"github.com/nipalab/nipa/internal/treehash"
)

// PlanTreeDelta works out what a delta manifest does to the stored tree
// without writing anything: the files it adds or modifies, the rows it
// removes and the root hash it leads to. A caller brings the working copy to
// those changes first and then hands the result to ApplyTreeDelta.
//
// The stored tree must be recorded (synced-tree record) as the manifest of
// in.BaseCommitID fetched with in.Sparse, otherwise the answer is
// clientDomain.ErrTreeDeltaBase; so it is when the delta keeps a file the
// tree does not hold. A delta that carries a root hash must lead to it, or
// the answer is a *clientDomain.TreeDeltaMismatchError. Only the directories
// the delta lists as changed are read.
func (l *LocalRepo) PlanTreeDelta(in clientDomain.TreeDeltaApply) (*clientDomain.TreeDeltaChanges, error) {
	if l.db == nil {
		return nil, errors.New("local repo not initialized")
	}
	ctx := context.Background()
	tx, err := l.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	plan, err := planTreeDelta(ctx, sqlcLocalrepo.New(tx), in, nil)
	if err != nil {
		return nil, err
	}
	return &plan.changes, nil
}

// ApplyTreeDelta turns the stored tree into the manifest of in.CommitID by
// applying the delta to the stored rows, in one transaction that writes only
// the difference: changed directories get their hash recomputed bottom-up the
// way treedelta.Apply does, a changed directory left without entries is
// dropped, what a changed directory holds and the delta does not list is
// deleted, and an unchanged directory the tree does not hold stays absent. On
// success the rows, tree_hash and the synced-tree record (with in.View) equal
// what SaveSyncedTreeView writes for the target's manifest; the fingerprints
// of removed paths are dropped one by one.
//
// planned is the result of PlanTreeDelta for the same input, when the caller
// synced the working copy in between (which drops the synced-tree record):
// the apply then accepts a missing record, and requires instead that the
// stored root hash is still the one the plan saw and that the changes lead to
// the same root. With a nil planned the record alone decides, as in
// PlanTreeDelta.
//
// Nothing is persisted on any error: clientDomain.ErrTreeDeltaBase, a
// *clientDomain.TreeDeltaMismatchError or a failed read or write.
func (l *LocalRepo) ApplyTreeDelta(in clientDomain.TreeDeltaApply, planned *clientDomain.TreeDeltaChanges) (*clientDomain.TreeDeltaChanges, error) {
	if l.db == nil {
		return nil, errors.New("local repo not initialized")
	}
	record, err := json.Marshal(syncedTreeRecord{Commit: in.CommitID, Sparse: canonicalSparse(in.Sparse), View: in.View})
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	tx, err := l.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	q := sqlcLocalrepo.New(tx)

	plan, err := planTreeDelta(ctx, q, in, planned)
	if err != nil {
		return nil, err
	}
	if err := plan.write(ctx, q); err != nil {
		return nil, err
	}
	for _, f := range plan.changes.Removed {
		if err := q.StatCacheDelete(ctx, f.Path); err != nil {
			return nil, err
		}
	}
	if !plan.rootKept {
		if err := q.MetaSet(ctx, sqlcLocalrepo.MetaSetParams{Key: "tree_hash", Value: plan.changes.TreeHash.String()}); err != nil {
			return nil, err
		}
	}
	if err := q.MetaSet(ctx, sqlcLocalrepo.MetaSetParams{Key: syncedTreeKey, Value: string(record)}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &plan.changes, nil
}

// checkDeltaBase accepts the stored tree as the base of a delta and returns
// the stored root tree hash record. See ApplyTreeDelta for the two ways.
func checkDeltaBase(ctx context.Context, q *sqlcLocalrepo.Queries, in clientDomain.TreeDeltaApply, planned *clientDomain.TreeDeltaChanges) (string, error) {
	if in.CommitID == "" || in.BaseCommitID == "" {
		return "", clientDomain.ErrTreeDeltaBase
	}
	treeHash, err := q.MetaGet(ctx, "tree_hash")
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if planned != nil && planned.BaseTreeHash != treeHash {
		return "", clientDomain.ErrTreeDeltaBase
	}
	value, err := q.MetaGet(ctx, syncedTreeKey)
	if errors.Is(err, sql.ErrNoRows) {
		if planned == nil {
			return "", clientDomain.ErrTreeDeltaBase
		}
		return treeHash, nil
	}
	if err != nil {
		return "", err
	}
	var stored syncedTreeRecord
	if json.Unmarshal([]byte(value), &stored) != nil ||
		stored.Commit != in.BaseCommitID ||
		!slices.Equal(canonicalSparse(stored.Sparse), canonicalSparse(in.Sparse)) {
		return "", clientDomain.ErrTreeDeltaBase
	}
	return treeHash, nil
}

// treeDeltaPlan is the set of row changes a delta causes and what they mean
// for the working copy.
type treeDeltaPlan struct {
	rows pushApplyPlan
	// subtrees are stored directories that leave the tree with everything
	// below them.
	subtrees []string
	// written maps the stored path of each file the delta carries in full to
	// its row; tracked says the stored tree holds a row for the path.
	written map[string]sqlcLocalrepo.FileUpsertParams
	files   map[string]*domain.File
	tracked []string
	// rootKept is set when the delta says the root is the base's: no row and
	// no tree_hash changes.
	rootKept bool
	changes  clientDomain.TreeDeltaChanges
}

func planTreeDelta(ctx context.Context, q *sqlcLocalrepo.Queries, in clientDomain.TreeDeltaApply, planned *clientDomain.TreeDeltaChanges) (*treeDeltaPlan, error) {
	if in.Delta == nil || in.Delta.Root == nil {
		return nil, errors.New("tree delta: no root directory")
	}
	baseTreeHash, err := checkDeltaBase(ctx, q, in, planned)
	if err != nil {
		return nil, err
	}
	plan := &treeDeltaPlan{
		written: map[string]sqlcLocalrepo.FileUpsertParams{},
		files:   map[string]*domain.File{},
	}
	plan.changes.BaseTreeHash = baseTreeHash

	if in.Delta.Root.Unchanged {
		plan.rootKept = true
		node, err := q.TreeNodeGet(ctx, "")
		switch {
		case errors.Is(err, sql.ErrNoRows):
			plan.changes.TreeHash = emptyTreeHash
		case err != nil:
			return nil, err
		default:
			plan.changes.TreeHash = hashFromBytes(node.Hash)
		}
	} else {
		hash, _, err := plan.dir(ctx, q, "", in.Delta.Root)
		if err != nil {
			return nil, err
		}
		plan.changes.TreeHash = hash
		if err := plan.resolveFiles(ctx, q); err != nil {
			return nil, err
		}
	}
	if in.Delta.RootHash != nil && plan.changes.TreeHash != *in.Delta.RootHash {
		return nil, &clientDomain.TreeDeltaMismatchError{Expected: *in.Delta.RootHash, Computed: plan.changes.TreeHash}
	}
	if planned != nil && planned.TreeHash != plan.changes.TreeHash {
		return nil, &clientDomain.TreeDeltaMismatchError{Expected: planned.TreeHash, Computed: plan.changes.TreeHash}
	}
	return plan, nil
}

// dir plans one changed directory (stored path, "" is the root) and returns
// its hash, or kept false when it ends up without entries and is dropped.
// Only the directory's own rows and its direct children's hashes are read.
func (p *treeDeltaPlan) dir(ctx context.Context, q *sqlcLocalrepo.Queries, path string, d *treedelta.Dir) (domain.Hash, bool, error) {
	var stored *sqlcLocalrepo.TreeNode
	node, err := q.TreeNodeGet(ctx, path)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return domain.Hash{}, false, err
	default:
		stored = &node
	}
	heldFiles := map[string]sqlcLocalrepo.FileHashesByTreeRow{}
	heldDirs := map[string]domain.Hash{}
	if stored != nil {
		rows, err := q.FileHashesByTree(ctx, path)
		if err != nil {
			return domain.Hash{}, false, err
		}
		for _, row := range rows {
			heldFiles[row.Path] = row
		}
		children, err := q.TreeNodeChildHashes(ctx, path)
		if err != nil {
			return domain.Hash{}, false, err
		}
		for _, child := range children {
			heldDirs[child.Path] = hashFromBytes(child.Hash)
		}
	}

	hashFiles := make([]treehash.FileEntry, 0, len(d.Files))
	for _, f := range d.Files {
		filePath := path + "/" + f.Name
		if f.Name == "" || strings.Contains(f.Name, "/") {
			return domain.Hash{}, false, fmt.Errorf("tree delta: bad file name %q in %q", f.Name, path)
		}
		if _, twice := p.files[filePath]; twice {
			return domain.Hash{}, false, fmt.Errorf("tree delta: file %q listed twice", filePath)
		}
		held, tracked := heldFiles[filePath]
		delete(heldFiles, filePath)
		if f.Changed == nil {
			if !tracked {
				return domain.Hash{}, false, fmt.Errorf("%w: unchanged file %q is not held", clientDomain.ErrTreeDeltaBase, filePath)
			}
			// Marks the path as listed, for the duplicate check.
			p.files[filePath] = nil
			hashFiles = append(hashFiles, treehash.FileEntry{Name: f.Name, Hash: hashFromBytes(held.Hash), Mode: hashModeOfClass(held.Mode)})
			continue
		}
		p.files[filePath] = f.Changed
		p.written[filePath] = deltaFileRow(f.Changed, filePath, path)
		if tracked {
			p.tracked = append(p.tracked, filePath)
		}
		hashFiles = append(hashFiles, treehash.FileEntry{Name: f.Name, Hash: f.Changed.Hash, Mode: treedelta.HashMode(f.Changed.Mode)})
	}
	// What the directory holds and the delta does not list was removed.
	for filePath := range heldFiles {
		p.rows.fileDeletes = append(p.rows.fileDeletes, filePath)
		p.tracked = append(p.tracked, filePath)
	}

	hashTrees := make([]treehash.TreeEntry, 0, len(d.Dirs))
	listed := make(map[string]bool, len(d.Dirs))
	for _, sub := range d.Dirs {
		if sub == nil || sub.Name == "" || strings.Contains(sub.Name, "/") {
			return domain.Hash{}, false, fmt.Errorf("tree delta: bad directory entry in %q", path)
		}
		subPath := path + "/" + sub.Name
		if listed[subPath] {
			return domain.Hash{}, false, fmt.Errorf("tree delta: directory %q listed twice", subPath)
		}
		listed[subPath] = true
		heldHash, held := heldDirs[subPath]
		delete(heldDirs, subPath)
		if sub.Unchanged {
			// "Keep what you hold, if anything": a directory the view shows
			// nothing of is not part of the tree.
			if !held {
				continue
			}
			if heldHash == emptyTreeHash {
				p.subtrees = append(p.subtrees, subPath)
				continue
			}
			hashTrees = append(hashTrees, treehash.TreeEntry{Name: sub.Name, Hash: heldHash})
			continue
		}
		hash, kept, err := p.dir(ctx, q, subPath, sub)
		if err != nil {
			return domain.Hash{}, false, err
		}
		if kept {
			hashTrees = append(hashTrees, treehash.TreeEntry{Name: sub.Name, Hash: hash})
		}
	}
	for subPath := range heldDirs {
		p.subtrees = append(p.subtrees, subPath)
	}

	if path != "" && len(hashFiles) == 0 && len(hashTrees) == 0 {
		if stored != nil {
			p.rows.nodeDeletes = append(p.rows.nodeDeletes, path)
		}
		return domain.Hash{}, false, nil
	}
	hash := treehash.TreeHash(hashFiles, hashTrees)
	// The row a manifest save writes for a directory: flattenTree with the
	// zero mode manifests carry.
	want := sqlcLocalrepo.TreeNodeUpsertParams{Path: path, ParentPath: storedParent(path), Hash: hash.Bytes()}
	if stored == nil || !sameNode(*stored, want) {
		p.rows.nodeUpserts = append(p.rows.nodeUpserts, want)
	}
	return hash, true, nil
}

// deltaFileRow is the row a manifest save writes for a file of the target
// manifest (flattenTree).
func deltaFileRow(file *domain.File, path, treePath string) sqlcLocalrepo.FileUpsertParams {
	hashes := make([]domain.Hash, 0, len(file.Chunks))
	for _, c := range file.Chunks {
		hashes = append(hashes, c.Hash)
	}
	return sqlcLocalrepo.FileUpsertParams{
		Path:      path,
		TreePath:  treePath,
		Hash:      file.Hash.Bytes(),
		SizeBytes: file.SizeBytes,
		Mode:      int64(file.Mode),
		IsBinary:  file.IsBinary,
		Encoding:  file.Encoding,
		Chunks:    encodeChunkHashes(hashes),
	}
}

// resolveFiles reads the stored rows the delta replaces or removes, by path
// and, for directories that leave the tree, by range, and settles what
// changes: a file the delta carries in full but the tree already holds as is
// costs no write and is not reported.
func (p *treeDeltaPlan) resolveFiles(ctx context.Context, q *sqlcLocalrepo.Queries) error {
	removed := map[string]clientDomain.SnapshotFile{}
	base := map[string]clientDomain.SnapshotFile{}
	for start := 0; start < len(p.tracked); start += pathBatch {
		rows, err := q.FileListByPaths(ctx, p.tracked[start:min(start+pathBatch, len(p.tracked))])
		if err != nil {
			return err
		}
		for _, r := range rows {
			f, err := snapshotFile(r.Path, r.Hash, r.SizeBytes, r.Mode, r.IsBinary, r.Encoding, r.Chunks)
			if err != nil {
				return err
			}
			want, rewritten := p.written[r.Path]
			if !rewritten {
				removed[r.Path] = f
				continue
			}
			if want.SizeBytes == r.SizeBytes && want.Mode == r.Mode && want.IsBinary == r.IsBinary &&
				want.Encoding == r.Encoding && bytes.Equal(want.Hash, r.Hash) && bytes.Equal(want.Chunks, r.Chunks) {
				delete(p.written, r.Path)
				continue
			}
			base[r.Path] = f
		}
	}
	for _, dir := range p.subtrees {
		rows, err := q.FileListInRange(ctx, sqlcLocalrepo.FileListInRangeParams{Lower: dir + "/", Upper: dir + "0"})
		if err != nil {
			return err
		}
		for _, r := range rows {
			f, err := snapshotFile(r.Path, r.Hash, r.SizeBytes, r.Mode, r.IsBinary, r.Encoding, r.Chunks)
			if err != nil {
				return err
			}
			removed[r.Path] = f
		}
	}

	paths := make([]string, 0, len(p.written))
	for path := range p.written {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	p.changes.Written = make([]clientDomain.TreeDeltaFile, 0, len(paths))
	for _, path := range paths {
		p.rows.fileUpserts = append(p.rows.fileUpserts, p.written[path])
		entry := clientDomain.TreeDeltaFile{Path: stripRoot(path), File: p.files[path]}
		if f, ok := base[path]; ok {
			entry.Base = &f
		}
		p.changes.Written = append(p.changes.Written, entry)
	}
	paths = paths[:0]
	for path := range removed {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	p.changes.Removed = make([]clientDomain.SnapshotFile, 0, len(paths))
	for _, path := range paths {
		p.changes.Removed = append(p.changes.Removed, removed[path])
	}
	return nil
}

func (p *treeDeltaPlan) write(ctx context.Context, q *sqlcLocalrepo.Queries) error {
	// Rows that leave go first: a path can turn from a file into a directory
	// and back, and the files of a dropped directory must not outlive it.
	for _, dir := range p.subtrees {
		bounds := [2]string{dir + "/", dir + "0"}
		if err := q.FileDeleteInRange(ctx, sqlcLocalrepo.FileDeleteInRangeParams{Lower: bounds[0], Upper: bounds[1]}); err != nil {
			return err
		}
		if err := q.TreeNodeDeleteInRange(ctx, sqlcLocalrepo.TreeNodeDeleteInRangeParams{Lower: bounds[0], Upper: bounds[1]}); err != nil {
			return err
		}
		if err := q.TreeNodeDelete(ctx, dir); err != nil {
			return err
		}
	}
	return p.rows.write(ctx, q)
}
