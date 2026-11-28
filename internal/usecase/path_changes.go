package usecase

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

// The path change index records, per commit, the paths whose content
// differs from the commit's first parent. The per-path history
// (PathCommitLog: blame, the web UI file and folder history) reads it
// instead of comparing the path between every pair of commits it walks.
//
// "Changed" is exactly what the per-path history has always reported: the
// path's content hash differs between the commit and its first parent, where
// a file's content is its hash and a folder's is the tree hash of what is
// readable below it (file names, hashes and mode classes). A file row
// (PathChangeFile) is a file that appeared, disappeared or changed hash; a
// mode row (PathChangeMode) is a file that kept its hash and changed its mode
// class, which changes the folders above it but not the file's own history; a
// directory row (PathChangeDir) is a folder that appeared or disappeared,
// which a reader whose rules hide everything inside it still sees.
//
// A root commit has no rows: the per-path history lists the root commit of
// the walk whenever it reaches it, so its 100,000-file tree is never written
// out. Every other commit gets its rows in the transaction that stores it
// (ApplyPushRequest.PathChanges) together with a marker; a commit without a
// marker (stored before the index, or by a writer that did not compute its
// changes) is indexed by the first read whose walk crosses it.
//
// That read indexes incrementally (indexedPathCommitLog): the unmarked
// commits of its walk, newest first, in batches of growing size, one write
// transaction per batch. Before each batch it answers from the part of the
// walk that is already complete (every commit newer than the next unmarked
// one) and stops as soon as that part yields the page; only a query that
// needs older commits indexes deeper. A walk with nothing unmarked costs the
// one lookup.

// PathChangeKind says what one path change index row records.
type PathChangeKind int

const (
	// PathChangeFile is a file that appeared, disappeared or changed hash.
	PathChangeFile PathChangeKind = 1
	// PathChangeMode is a file that kept its hash and changed its mode class.
	PathChangeMode PathChangeKind = 2
	// PathChangeDir is a directory that appeared or disappeared.
	PathChangeDir PathChangeKind = 3
)

// PathChange is one row of a commit's path changes.
type PathChange struct {
	Path string
	Kind PathChangeKind
}

// PathChangeRow is a stored path change of one commit.
type PathChangeRow struct {
	CommitID snow.ID
	Path     string
	Kind     PathChangeKind
}

// UnindexedCommit is a commit with a first parent that has no path change
// rows yet, with the root tree nodes of both and its depth in the walk that
// found it (0 is the walk's start).
type UnindexedCommit struct {
	ID           snow.ID
	TreeID       int64
	ParentTreeID int64
	Depth        int
}

// CommitPathChanges is the complete set of path change rows of one commit,
// stored together with its marker (RecordPathChanges).
type CommitPathChanges struct {
	CommitID snow.ID
	Changes  []PathChange
}

// PathChangeQuery selects, from the first-parent walk of at most ScanLimit
// commits from StartID, the commits that changed Path (a file or a folder;
// "" is the whole tree) and the walk's root commit, newest first, at most
// Limit of them. The root is a commit without a first parent: the last
// commit of a walk cut short by ScanLimit is listed only when it changed
// Path, so a walk cut at depth d lists exactly the first d commits' share of
// the full walk's answer.
type PathChangeQuery struct {
	ProjectID snow.ID
	StartID   snow.ID
	ScanLimit int
	Path      string
	Limit     int
}

// AnyPath reports whether every change matches (the whole tree).
func (q PathChangeQuery) AnyPath() bool { return q.Path == "" }

// PrefixRange returns the half-open byte range [from, to) of the paths below
// Path: "a/b/" up to "a/b0" ('0' follows '/'), empty for the whole tree.
func (q PathChangeQuery) PrefixRange() (from, to string) {
	if q.Path == "" {
		return "", ""
	}
	return q.Path + "/", q.Path + "0"
}

// maxPathHistoryScan caps the first-parent commits one per-path history walks.
// The walk costs one index probe per commit inside a single statement, so the
// cap is far above the 500 commits the tree-comparing scan could afford.
const maxPathHistoryScan = 10000

// pathIndexFirstBatch and pathIndexMaxBatch bound the unindexed commits one
// read indexes per write transaction: the first batch is small so a page
// near the head answers quickly, and each next one doubles up to the cap so a
// path that changes rarely needs few rounds to reach deep into the walk.
// They are variables only so tests can cross batch boundaries with a short
// history (SetPathIndexBatches); nothing else assigns them.
var (
	pathIndexFirstBatch = 100
	pathIndexMaxBatch   = 1600
)

// pathChangeRowBatch bounds the commits whose rows a path-restricted reader
// loads at once.
const pathChangeRowBatch = 500

// PathCommitLog returns the commits that touched path, newest first, from the
// first-parent history of rev (or of startCommitID). path may be a file or a
// directory; the walk is capped at maxPathHistoryScan commits, and the root
// commit of the walk is always listed when the walk reaches it.
func (b *Branch) PathCommitLog(ctx context.Context, projectID snow.ID, rev, path string, startCommitID *snow.ID, limit int) ([]*domain.CommitLogEntry, error) {
	if !b.permUc.HasProjectAccess(ctx, projectID, domain.PermissionRead) {
		return nil, domain.NewErrorNoPermission()
	}
	path = strings.Trim(path, "/")
	if limit <= 0 {
		limit = 50
	}
	commitID, err := b.resolveRevision(ctx, projectID, rev)
	if err != nil {
		return nil, err
	}
	if commitID == nil {
		return []*domain.CommitLogEntry{}, nil
	}
	startID := *commitID
	if startCommitID != nil {
		startID = *startCommitID
	}
	filter, err := b.permUc.CompileFilter(ctx, projectID, domain.PermissionRead)
	if err != nil {
		return nil, err
	}
	if path != "" && !filter.CanDescend(path) {
		return nil, domain.NewErrorNotFound(fmt.Sprintf("path %q not found", path))
	}
	q := PathChangeQuery{ProjectID: projectID, StartID: startID, ScanLimit: maxPathHistoryScan, Path: path, Limit: limit}
	return b.indexedPathCommitLog(ctx, q, filter)
}

// indexedPathCommitLog answers q after indexing as much of its walk as the
// answer needs. The unmarked commits come newest first; before indexing the
// batch that starts at depth d, the walk cut at d (all complete) is asked for
// the page, and a full page there is the full walk's page too, since entries
// come in walk order and the cut never lists its last commit as a root. A
// short page means older commits may belong to it, so the batch is indexed
// and the next cut tried; once nothing is left unmarked the whole walk is
// asked.
func (b *Branch) indexedPathCommitLog(ctx context.Context, q PathChangeQuery, filter *PathFilter) ([]*domain.CommitLogEntry, error) {
	pending, err := b.branchRepo.UnindexedPathChangeCommits(ctx, q.ProjectID, q.StartID, q.ScanLimit)
	if err != nil {
		return nil, err
	}
	size := pathIndexFirstBatch
	for start := 0; start < len(pending); start += size {
		if start > 0 {
			size = min(2*size, pathIndexMaxBatch)
		}
		if depth := pending[start].Depth; depth > 0 {
			cut := q
			cut.ScanLimit = depth
			entries, err := b.pathChangePage(ctx, cut, filter)
			if err != nil {
				return nil, err
			}
			if len(entries) >= q.Limit {
				return entries, nil
			}
		}
		if err := b.indexPathChanges(ctx, pending[start:min(start+size, len(pending))]); err != nil {
			return nil, err
		}
	}
	return b.pathChangePage(ctx, q, filter)
}

// pathChangePage answers q from the index as the reader's filter sees it.
func (b *Branch) pathChangePage(ctx context.Context, q PathChangeQuery, filter *PathFilter) ([]*domain.CommitLogEntry, error) {
	if !filter.Unrestricted() {
		return b.restrictedPathCommitLog(ctx, q, filter)
	}
	entries, err := b.branchRepo.PathChangeLog(ctx, q)
	if err != nil {
		return nil, err
	}
	if entries == nil {
		entries = []*domain.CommitLogEntry{}
	}
	return entries, nil
}

// restrictedPathCommitLog serves a reader whose rules hide paths: the
// candidates are the commits with any change at or below the path, and each
// one is kept only when a change the reader can see is among its rows.
// Candidates are checked in batches, newest first, until the limit is met.
func (b *Branch) restrictedPathCommitLog(ctx context.Context, q PathChangeQuery, filter *PathFilter) ([]*domain.CommitLogEntry, error) {
	limit := q.Limit
	q.Limit = q.ScanLimit
	candidates, err := b.branchRepo.PathChangeLog(ctx, q)
	if err != nil {
		return nil, err
	}
	batch := min(max(2*limit, 50), pathChangeRowBatch)
	out := make([]*domain.CommitLogEntry, 0, min(limit, len(candidates)))
	for start := 0; start < len(candidates) && len(out) < limit; start += batch {
		group := candidates[start:min(start+batch, len(candidates))]
		ids := make([]snow.ID, 0, len(group))
		for _, entry := range group {
			if entry.Parent1ID != nil {
				ids = append(ids, entry.ID)
			}
		}
		byCommit := map[snow.ID][]PathChangeRow{}
		if len(ids) > 0 {
			rows, err := b.branchRepo.PathChangeRows(ctx, q, ids)
			if err != nil {
				return nil, err
			}
			for _, row := range rows {
				byCommit[row.CommitID] = append(byCommit[row.CommitID], row)
			}
		}
		for _, entry := range group {
			if entry.Parent1ID == nil || visiblePathChange(q.Path, byCommit[entry.ID], filter) {
				out = append(out, entry)
				if len(out) >= limit {
					break
				}
			}
		}
	}
	return out, nil
}

// visiblePathChange reports whether rows (a commit's changes at or below
// path) change path as filter sees it. At the path itself a file counts when
// the reader may read it and a directory that appeared or disappeared always
// counts (the reader was allowed to look into it); below it, a file change
// counts when the file is readable and every folder between path and the file
// can be descended into, exactly what a filtered manifest load keeps. A
// mode-only change of the file at path itself never counts: a file's history
// compares its hash only.
func visiblePathChange(path string, rows []PathChangeRow, filter *PathFilter) bool {
	for _, row := range rows {
		if row.Path == path {
			switch row.Kind {
			case PathChangeDir:
				return true
			case PathChangeFile:
				if filter.Allow(path) {
					return true
				}
			}
			continue
		}
		if row.Kind == PathChangeDir {
			continue
		}
		if visibleBelow(path, row.Path, filter) {
			return true
		}
	}
	return false
}

// visibleBelow reports whether the file at filePath, below the folder base
// ("" for the root), is part of base's readable content.
func visibleBelow(base, filePath string, filter *PathFilter) bool {
	if !filter.Allow(filePath) {
		return false
	}
	rel := filePath
	if base != "" {
		rel = strings.TrimPrefix(filePath, base+"/")
	}
	for i := 0; i < len(rel); i++ {
		if rel[i] == '/' && !filter.CanDescend(joinTreePath(base, rel[:i])) {
			return false
		}
	}
	return true
}

// indexPathChanges indexes a batch of commits that predate the index (no
// marker): each one's changes against its first parent come from comparing
// the two trees, the same result its writer would have recorded, and the
// whole batch is stored in one transaction. Two reads indexing the same
// commits at once store the same rows (idempotent inserts), in the same walk
// order.
func (b *Branch) indexPathChanges(ctx context.Context, pending []UnindexedCommit) error {
	batch := make([]CommitPathChanges, 0, len(pending))
	for _, commit := range pending {
		changes, err := b.comparePathChanges(ctx, commit.ParentTreeID, commit.TreeID)
		if err != nil {
			return err
		}
		batch = append(batch, CommitPathChanges{CommitID: commit.ID, Changes: changes})
	}
	return b.branchRepo.RecordPathChanges(ctx, batch)
}

// comparePathChanges computes the path change rows between two stored trees.
func (b *Branch) comparePathChanges(ctx context.Context, parentTreeID, treeID int64) ([]PathChange, error) {
	parent, err := b.branchRepo.GetTreeNode(ctx, parentTreeID)
	if err != nil {
		return nil, err
	}
	tree, err := b.branchRepo.GetTreeNode(ctx, treeID)
	if err != nil {
		return nil, err
	}
	return treePathChanges(ctx, b.branchRepo, parent, tree)
}

// treePathChanges compares two stored trees in full (no read filter) and
// returns their path change rows, reading only the directories whose stored
// hashes differ (compareTrees).
func treePathChanges(ctx context.Context, repo treeBatchReader, older, newer *domain.TreeNode) ([]PathChange, error) {
	walk, err := walkCompare(ctx, repo, treeView{filter: AllowAllFilter()}, []*domain.TreeNode{older, newer}, nil, false)
	if err != nil {
		return nil, err
	}
	var out []PathChange
	for _, file := range walk.out {
		if kind, ok := fileChangeKind(storedFileState(file.files[0]), storedFileState(file.files[1])); ok {
			out = append(out, PathChange{Path: file.path(), Kind: kind})
		}
	}
	for _, dir := range walk.dirsToggled {
		out = append(out, PathChange{Path: dir, Kind: PathChangeDir})
	}
	sortPathChanges(out)
	return out, nil
}

// fileState is what the path change index compares of a file: whether it
// exists, its hash and its mode class.
type fileState struct {
	present bool
	hash    domain.Hash
	class   int
}

func storedFileState(file *domain.File) fileState {
	if file == nil {
		return fileState{}
	}
	return fileState{present: true, hash: file.Hash, class: domain.ClassifyStoredMode(file.Mode)}
}

// fileChangeKind returns the row a file's change from older to newer gets,
// if any.
func fileChangeKind(older, newer fileState) (PathChangeKind, bool) {
	switch {
	case older.present != newer.present:
		return PathChangeFile, true
	case !older.present:
		return 0, false
	case older.hash != newer.hash:
		return PathChangeFile, true
	case older.class != newer.class:
		return PathChangeMode, true
	}
	return 0, false
}

func sortPathChanges(changes []PathChange) {
	slices.SortFunc(changes, func(a, b PathChange) int {
		if c := strings.Compare(a.Path, b.Path); c != 0 {
			return c
		}
		return int(a.Kind) - int(b.Kind)
	})
}

// treeChanges is what a tree build (buildNewTree) changed against its base
// tree: for every file path the change touched, its state in the base and in
// the built tree, and for every directory it rebuilt, whether the directory
// exists in each. Paths a change touches without changing them are recorded
// too, so two builds on the same base can be compared (since).
type treeChanges struct {
	files map[string][2]fileState
	dirs  map[string][2]bool
}

func newTreeChanges() *treeChanges {
	return &treeChanges{files: map[string][2]fileState{}, dirs: map[string][2]bool{}}
}

// since returns the path change rows from prev's built tree to c's built
// tree, both built on the same base tree; a nil prev is the base itself. A
// path only one of the builds touched has its base state in the other.
func (c *treeChanges) since(prev *treeChanges) []PathChange {
	if prev == nil {
		prev = newTreeChanges()
	}
	var out []PathChange
	for path, cur := range c.files {
		older := cur[0]
		if p, ok := prev.files[path]; ok {
			older = p[1]
		}
		if kind, ok := fileChangeKind(older, cur[1]); ok {
			out = append(out, PathChange{Path: path, Kind: kind})
		}
	}
	for path, p := range prev.files {
		if _, ok := c.files[path]; ok {
			continue
		}
		if kind, ok := fileChangeKind(p[1], p[0]); ok {
			out = append(out, PathChange{Path: path, Kind: kind})
		}
	}
	for path, cur := range c.dirs {
		older := cur[0]
		if p, ok := prev.dirs[path]; ok {
			older = p[1]
		}
		if older != cur[1] {
			out = append(out, PathChange{Path: path, Kind: PathChangeDir})
		}
	}
	for path, p := range prev.dirs {
		if _, ok := c.dirs[path]; ok {
			continue
		}
		if p[1] != p[0] {
			out = append(out, PathChange{Path: path, Kind: PathChangeDir})
		}
	}
	sortPathChanges(out)
	return out
}
