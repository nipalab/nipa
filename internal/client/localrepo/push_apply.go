package localrepo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"slices"
	"strings"

	"github.com/nipalab/nipa/internal/chunker"
	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/domain"
	sqlcLocalrepo "github.com/nipalab/nipa/internal/repository/sqlc/localrepo"
	"github.com/nipalab/nipa/internal/treehash"
)

// emptyTreeHash is the hash of a directory without files or subdirectories.
// The server does not keep such directories in a tree it rebuilds.
var emptyTreeHash = treehash.TreeHash(nil, nil)

// ApplyPush turns the stored tree into the tree of the commit a push just
// created, from what the push sent instead of the new head's manifest. The
// server only accepts a push onto the branch head, so the new tree is the
// base tree plus the pushed files minus the removed paths; directory hashes
// are derived the way the server's push does (usecase.buildNewTree) and the
// resulting root must equal in.TreeHash.
//
// It reads and writes only the directories on the way from a touched path to
// the root. Everything happens in one transaction: on
// clientDomain.ErrPushApplyBase (the stored tree is not known to be the full
// tree of in.BaseCommitID) or a *clientDomain.PushApplyMismatchError nothing
// is persisted, and the caller saves the fetched manifest instead. On success
// the rows, tree_hash and the synced-tree record equal what SaveSyncedTree
// writes for the new head's manifest.
func (l *LocalRepo) ApplyPush(in clientDomain.PushApply) error {
	if l.db == nil {
		return errors.New("local repo not initialized")
	}
	sparse := canonicalSparse(in.Sparse)
	if in.CommitID == "" || len(sparse) > 0 {
		return clientDomain.ErrPushApplyBase
	}

	ctx := context.Background()
	tx, err := l.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	q := sqlcLocalrepo.New(tx)

	view, err := checkPushBase(ctx, q, in.BaseCommitID)
	if err != nil {
		return err
	}
	// The view token is kept: it names the read rules, the sparse set and the
	// recursive flag, none of which a push changes, and the tree this apply
	// leaves is the new head's manifest in that same view (the root hash check
	// below). Should the rules have changed since, the server compares the
	// token with the current view and declines the delta.
	record, err := json.Marshal(syncedTreeRecord{Commit: in.CommitID, Sparse: sparse, View: view})
	if err != nil {
		return err
	}
	plan, err := planPushApply(ctx, q, in.Files, in.Removed)
	if err != nil {
		return err
	}
	if plan.root != in.TreeHash {
		return &clientDomain.PushApplyMismatchError{Expected: in.TreeHash, Computed: plan.root}
	}
	if err := plan.write(ctx, q); err != nil {
		return err
	}
	// Only removed paths can leave the tracked set here, so their fingerprints
	// are dropped one by one instead of sweeping the whole stat cache.
	for _, path := range in.Removed {
		if err := q.StatCacheDelete(ctx, path); err != nil {
			return err
		}
	}
	if err := q.MetaSet(ctx, sqlcLocalrepo.MetaSetParams{Key: "tree_hash", Value: plan.root.String()}); err != nil {
		return err
	}
	if err := q.MetaSet(ctx, sqlcLocalrepo.MetaSetParams{Key: syncedTreeKey, Value: string(record)}); err != nil {
		return err
	}
	return tx.Commit()
}

// checkPushBase accepts the stored tree as the push base when the synced-tree
// record says it is the full tree of baseCommitID, or, for a push onto an
// empty branch, when nothing is stored at all. Trees saved without a record
// (merge and revert build theirs locally, without directory hashes) and
// sparse trees are refused. It returns the view token of the accepted record.
func checkPushBase(ctx context.Context, q *sqlcLocalrepo.Queries, baseCommitID string) ([]byte, error) {
	var record *syncedTreeRecord
	value, err := q.MetaGet(ctx, syncedTreeKey)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return nil, err
	default:
		var stored syncedTreeRecord
		if json.Unmarshal([]byte(value), &stored) != nil || stored.Commit == "" {
			return nil, clientDomain.ErrPushApplyBase
		}
		record = &stored
	}

	if baseCommitID == "" {
		if record != nil {
			return nil, clientDomain.ErrPushApplyBase
		}
		_, err := q.TreeNodeGet(ctx, "")
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return nil, clientDomain.ErrPushApplyBase
	}
	if record == nil || record.Commit != baseCommitID || len(canonicalSparse(record.Sparse)) > 0 {
		return nil, clientDomain.ErrPushApplyBase
	}
	return record.View, nil
}

// pushApplyPlan is the set of row changes a push causes, and the root hash
// they lead to.
type pushApplyPlan struct {
	root        domain.Hash
	nodeUpserts []sqlcLocalrepo.TreeNodeUpsertParams
	fileUpserts []sqlcLocalrepo.FileUpsertParams
	fileDeletes []string
	nodeDeletes []string
}

func (p *pushApplyPlan) write(ctx context.Context, q *sqlcLocalrepo.Queries) error {
	// Parents first, so a new directory exists before its files reference it.
	slices.SortFunc(p.nodeUpserts, func(a, b sqlcLocalrepo.TreeNodeUpsertParams) int {
		return strings.Count(a.Path, "/") - strings.Count(b.Path, "/")
	})
	for _, node := range p.nodeUpserts {
		if err := q.TreeNodeUpsert(ctx, node); err != nil {
			return err
		}
	}
	for _, file := range p.fileUpserts {
		if err := q.FileUpsert(ctx, file); err != nil {
			return err
		}
	}
	for _, path := range p.fileDeletes {
		if err := q.FileDelete(ctx, path); err != nil {
			return err
		}
	}
	for _, path := range p.nodeDeletes {
		if err := q.TreeNodeDelete(ctx, path); err != nil {
			return err
		}
	}
	return nil
}

// pushDir is a directory the push rebuilds: one that holds a touched path, or
// an ancestor of one. Paths are in stored form ("" is the root, "/a/b").
type pushDir struct {
	path    string
	pushed  []*domain.PushFile
	removed []string
	// subdirs are the rebuilt directories directly below this one.
	subdirs []*pushDir
	hash    domain.Hash
	// dropped is set when the directory ends up empty: the tree does not
	// hold it, like on the server.
	dropped bool
}

// planPushApply works out the rows the push changes. It mirrors the server's
// buildNewTree: every directory from a touched path up to the root is
// rebuilt from its stored files and subdirectories, an untouched
// subdirectory keeps its stored hash (and its rows), a rebuilt directory
// that ends up empty is dropped, and so is an untouched empty one below a
// rebuilt directory. Only the rebuilt directories' direct children are read.
func planPushApply(ctx context.Context, q *sqlcLocalrepo.Queries, files []*domain.PushFile, removed []string) (*pushApplyPlan, error) {
	dirs := map[string]*pushDir{}
	var touch func(path string) *pushDir
	touch = func(path string) *pushDir {
		if dir, ok := dirs[path]; ok {
			return dir
		}
		dir := &pushDir{path: path}
		dirs[path] = dir
		if path != "" {
			parent := touch(storedParent(path))
			parent.subdirs = append(parent.subdirs, dir)
		}
		return dir
	}
	touch("")
	for _, file := range files {
		if file == nil || file.Path == "" {
			return nil, errors.New("pushed file without a path")
		}
		dir := touch(storedParent(storedDir(file.Path)))
		dir.pushed = append(dir.pushed, file)
	}
	for _, path := range removed {
		if path == "" {
			return nil, errors.New("removed path is empty")
		}
		dir := touch(storedParent(storedDir(path)))
		dir.removed = append(dir.removed, storedDir(path))
	}

	// Deepest first: a directory's hash needs its rebuilt subdirectories'.
	order := make([]*pushDir, 0, len(dirs))
	for _, dir := range dirs {
		order = append(order, dir)
	}
	slices.SortFunc(order, func(a, b *pushDir) int {
		return strings.Count(b.path, "/") - strings.Count(a.path, "/")
	})

	plan := &pushApplyPlan{}
	for _, dir := range order {
		if err := plan.rebuild(ctx, q, dir, dirs); err != nil {
			return nil, err
		}
	}
	plan.root = dirs[""].hash
	return plan, nil
}

func (p *pushApplyPlan) rebuild(ctx context.Context, q *sqlcLocalrepo.Queries, dir *pushDir, rebuilt map[string]*pushDir) error {
	var stored *sqlcLocalrepo.TreeNode
	node, err := q.TreeNodeGet(ctx, dir.path)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return err
	default:
		stored = &node
	}

	fileEntries := map[string]treehash.FileEntry{}
	treeEntries := map[string]domain.Hash{}
	if stored != nil {
		rows, err := q.FileHashesByTree(ctx, dir.path)
		if err != nil {
			return err
		}
		for _, row := range rows {
			fileEntries[row.Path] = treehash.FileEntry{
				Name: storedName(row.Path),
				Hash: hashFromBytes(row.Hash),
				Mode: hashModeOfClass(row.Mode),
			}
		}
		children, err := q.TreeNodeChildHashes(ctx, dir.path)
		if err != nil {
			return err
		}
		for _, child := range children {
			if _, ok := rebuilt[child.Path]; ok {
				continue
			}
			hash := hashFromBytes(child.Hash)
			if hash == emptyTreeHash {
				p.nodeDeletes = append(p.nodeDeletes, child.Path)
				continue
			}
			treeEntries[child.Path] = hash
		}
	}

	for _, path := range dir.removed {
		if _, ok := fileEntries[path]; ok {
			delete(fileEntries, path)
			p.fileDeletes = append(p.fileDeletes, path)
		}
	}
	for _, file := range dir.pushed {
		row := pushedFileRow(file, dir.path)
		fileEntries[row.Path] = treehash.FileEntry{
			Name: storedName(row.Path),
			Hash: file.FileHash,
			Mode: hashModeOfClass(row.Mode),
		}
		p.fileUpserts = append(p.fileUpserts, row)
	}
	for _, sub := range dir.subdirs {
		if !sub.dropped {
			treeEntries[sub.path] = sub.hash
		}
	}

	// Files and subdirectories are keyed by stored path, so a name that is
	// both shows as one key in both maps. The server refuses such a tree
	// (usecase.buildNewTree); neither does this apply write one.
	for path := range treeEntries {
		if _, clash := fileEntries[path]; clash {
			return &clientDomain.PushApplyClashError{Path: strings.TrimPrefix(path, "/")}
		}
	}

	if dir.path != "" && len(fileEntries) == 0 && len(treeEntries) == 0 {
		dir.dropped = true
		if stored != nil {
			p.nodeDeletes = append(p.nodeDeletes, dir.path)
		}
		return nil
	}

	hashFiles := make([]treehash.FileEntry, 0, len(fileEntries))
	for _, entry := range fileEntries {
		hashFiles = append(hashFiles, entry)
	}
	hashTrees := make([]treehash.TreeEntry, 0, len(treeEntries))
	for path, hash := range treeEntries {
		hashTrees = append(hashTrees, treehash.TreeEntry{Name: storedName(path), Hash: hash})
	}
	dir.hash = treehash.TreeHash(hashFiles, hashTrees)

	// The row a manifest save writes for a directory: flattenTree with the
	// zero mode manifests carry.
	want := sqlcLocalrepo.TreeNodeUpsertParams{
		Path:       dir.path,
		ParentPath: storedParent(dir.path),
		Hash:       dir.hash.Bytes(),
	}
	if stored == nil || !sameNode(*stored, want) {
		p.nodeUpserts = append(p.nodeUpserts, want)
	}
	return nil
}

// pushedFileRow is the row a manifest save writes for a pushed file once the
// server stored it: the mode travels as its class (pb.FileMode), a lockable
// asset is always binary (usecase.markLockableBinaries) and the encoding is
// normalized (usecase.validatePushFiles).
func pushedFileRow(file *domain.PushFile, treePath string) sqlcLocalrepo.FileUpsertParams {
	encoding, ok := chunker.NormalizeEncoding(file.Encoding)
	if !ok {
		encoding = file.Encoding
	}
	return sqlcLocalrepo.FileUpsertParams{
		Path:      storedDir(file.Path),
		TreePath:  treePath,
		Hash:      file.FileHash.Bytes(),
		SizeBytes: file.SizeBytes,
		Mode:      int64(domain.ClassifyPerm(fs.FileMode(uint32(file.Mode)))),
		IsBinary:  file.IsBinary || chunker.IsLockablePath(file.Path),
		Encoding:  encoding,
		Chunks:    encodeChunkHashes(file.ChunkHashes),
	}
}

// hashModeOfClass turns the mode class the snapshot tracks (pb.FileMode) into
// the mode the server stores for it, which is what the tree hash classifies.
// Classes must not reach treehash as they are: class 1 (read-only) would read
// as the permission 0o001, an executable.
func hashModeOfClass(class int64) int {
	switch class {
	case domain.FileModeClassReadOnly:
		return 0o444
	case domain.FileModeClassExecutable:
		return 0o755
	default:
		return 0o644
	}
}

// storedParent returns the stored path of the directory holding a stored
// path: "/a/b" gives "/a", "/a" gives the root "".
func storedParent(path string) string {
	i := strings.LastIndex(path, "/")
	if i < 0 {
		return ""
	}
	return path[:i]
}

func storedName(path string) string {
	return path[strings.LastIndex(path, "/")+1:]
}
