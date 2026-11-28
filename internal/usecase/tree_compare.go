package usecase

import (
	"context"
	"slices"

	"github.com/nipalab/nipa/internal/domain"
)

// treeBatchReader is the batched read side of the tree walks: one call per
// level for files (without chunks) and one for child directories, and chunk
// lists only for the files a walk keeps. Every requested node id is resolved
// to its content node by hash, so two nodes stored with the same hash read the
// same rows.
type treeBatchReader interface {
	ListTreeChildrenByTrees(ctx context.Context, parentIDs []int64) (map[int64][]*domain.TreeNode, error)
	ListFileMetaByTrees(ctx context.Context, treeIDs []int64) (map[int64][]*domain.File, error)
	ListChunksByFiles(ctx context.Context, fileIDs []int64) (map[int64][]domain.Chunk, error)
}

// readTreeLevel reads the files (metadata only) and child directories of the
// given directories with one batch each. Nil nodes and nodes holding the empty
// tree own no rows and are not queried; an id listed twice is queried once.
func readTreeLevel(ctx context.Context, repo treeBatchReader, nodes []*domain.TreeNode) (map[int64][]*domain.File, map[int64][]*domain.TreeNode, error) {
	ids := make([]int64, 0, len(nodes))
	seen := make(map[int64]struct{}, len(nodes))
	for _, node := range nodes {
		if node == nil || node.Hash == emptyTreeHash {
			continue
		}
		if _, ok := seen[node.ID]; ok {
			continue
		}
		seen[node.ID] = struct{}{}
		ids = append(ids, node.ID)
	}
	if len(ids) == 0 {
		return nil, nil, nil
	}
	files, err := repo.ListFileMetaByTrees(ctx, ids)
	if err != nil {
		return nil, nil, err
	}
	children, err := repo.ListTreeChildrenByTrees(ctx, ids)
	if err != nil {
		return nil, nil, err
	}
	return files, children, nil
}

// treeView is what a caller sees of a tree: its read filter and sparse set,
// applied exactly as loadManifestLevel applies them.
type treeView struct {
	filter *PathFilter
	sparse domain.PrefixSet
}

func (v treeView) files(path string, files []*domain.File) []*domain.File {
	if v.filter.All() && v.sparse.Empty() {
		return files
	}
	kept := make([]*domain.File, 0, len(files))
	for _, file := range files {
		filePath := joinTreePath(path, file.Name)
		if !v.filter.Allow(filePath) {
			continue
		}
		if !v.sparse.Covers(filePath) && !keepRootTeamFile(path, file.Name, v.sparse) {
			continue
		}
		kept = append(kept, file)
	}
	return kept
}

// dirs drops directories the view cannot descend into and directories holding
// the empty tree, which list nothing.
func (v treeView) dirs(path string, dirs []*domain.TreeNode) []*domain.TreeNode {
	kept := make([]*domain.TreeNode, 0, len(dirs))
	for _, dir := range dirs {
		dirPath := joinTreePath(path, dir.Name)
		if !v.filter.CanDescend(dirPath) || !v.sparse.CanDescend(dirPath) {
			continue
		}
		if dir.Hash == emptyTreeHash {
			continue
		}
		kept = append(kept, dir)
	}
	return kept
}

// comparedFile is one file path where the compared trees differ: the
// directory it sits in, its name and, per tree, the file stored there (nil
// when that tree has no visible file at the path) with its chunk list loaded.
type comparedFile struct {
	dir, name string
	files     []*domain.File
}

func (f comparedFile) path() string { return joinTreePath(f.dir, f.name) }

// compareTrees returns the files that differ between trees, reading only the
// directories where they differ.
//
// roots are the root nodes of the compared trees (nil stands for the empty
// tree, e.g. no merge base). decide lists the trees whose differences matter:
// a path is reported when those trees do not all hold a file there with the
// same hash and mode (the equality diff.Compare and merge.ThreeWay use), and a
// directory is skipped without being read when they all store it with the
// same non-zero hash. The other trees ride along: their file at each reported
// path is returned too, read from the directories the walk reads anyway. A
// nil decide means every tree.
//
// Skipping by stored hash is exact, not an approximation, even though a tree
// hash covers file names, hashes and mode classes only: reads resolve every
// node to the content node of its hash (see treeBatchReader), so two
// directories with the same hash list the very same rows, binary flag,
// encoding, size and raw mode included, and no full comparison can see a
// difference between them either.
//
// The walk goes level by level: each level costs one file batch and one
// children batch over the differing directories of every tree, and one
// chunk batch at the end loads chunk lists for the reported files only. The
// view filters every tree like a manifest load does, so a hidden path is
// never reported and never descended into.
func compareTrees(ctx context.Context, repo treeBatchReader, view treeView, roots []*domain.TreeNode, decide []int) ([]comparedFile, error) {
	if decide == nil {
		decide = make([]int, len(roots))
		for i := range decide {
			decide[i] = i
		}
	}
	w := &compareWalk{repo: repo, view: view, sides: len(roots), decide: decide}
	if w.sameDirs(roots) {
		return nil, nil
	}
	level := []compareDir{{nodes: roots}}
	for len(level) > 0 {
		next, err := w.level(ctx, level)
		if err != nil {
			return nil, err
		}
		level = next
	}
	if err := w.loadChunks(ctx); err != nil {
		return nil, err
	}
	return w.out, nil
}

type compareWalk struct {
	repo   treeBatchReader
	view   treeView
	sides  int
	decide []int
	out    []comparedFile
}

// compareDir is one directory path the walk reads, with the node every tree
// holds there (nil when it holds none).
type compareDir struct {
	path  string
	nodes []*domain.TreeNode
}

// sameDirs reports whether the deciding trees all store the directory with
// the same known hash. A zero hash (a row never hashed) never matches.
func (w *compareWalk) sameDirs(nodes []*domain.TreeNode) bool {
	first := nodes[w.decide[0]]
	if first == nil || first.Hash == (domain.Hash{}) {
		return false
	}
	for _, side := range w.decide[1:] {
		if node := nodes[side]; node == nil || node.Hash != first.Hash {
			return false
		}
	}
	return true
}

func sameComparedFile(a, b *domain.File) bool {
	return a.Hash == b.Hash && a.Mode == b.Mode
}

// differs reports whether the deciding trees hold different files (or a file
// on some sides only) at one name.
func (w *compareWalk) differs(files []*domain.File) bool {
	first := files[w.decide[0]]
	for _, side := range w.decide[1:] {
		other := files[side]
		if (first == nil) != (other == nil) {
			return true
		}
		if first != nil && !sameComparedFile(first, other) {
			return true
		}
	}
	return false
}

func (w *compareWalk) level(ctx context.Context, level []compareDir) ([]compareDir, error) {
	nodes := make([]*domain.TreeNode, 0, w.sides*len(level))
	for _, dir := range level {
		nodes = append(nodes, dir.nodes...)
	}
	files, children, err := readTreeLevel(ctx, w.repo, nodes)
	if err != nil {
		return nil, err
	}
	var next []compareDir
	for _, dir := range level {
		sideFiles := make([]map[string]*domain.File, w.sides)
		sideDirs := make([]map[string]*domain.TreeNode, w.sides)
		var fileNames, dirNames []string
		deciding := make([]bool, w.sides)
		for _, side := range w.decide {
			deciding[side] = true
		}
		for side, node := range dir.nodes {
			if node == nil {
				continue
			}
			visibleFiles := w.view.files(dir.path, files[node.ID])
			sideFiles[side] = make(map[string]*domain.File, len(visibleFiles))
			for _, file := range visibleFiles {
				sideFiles[side][file.Name] = file
				if deciding[side] {
					fileNames = append(fileNames, file.Name)
				}
			}
			visibleDirs := w.view.dirs(dir.path, children[node.ID])
			sideDirs[side] = make(map[string]*domain.TreeNode, len(visibleDirs))
			for _, child := range visibleDirs {
				sideDirs[side][child.Name] = child
				if deciding[side] {
					dirNames = append(dirNames, child.Name)
				}
			}
		}
		slices.Sort(fileNames)
		for _, name := range slices.Compact(fileNames) {
			at := make([]*domain.File, w.sides)
			for side := range at {
				at[side] = sideFiles[side][name]
			}
			if w.differs(at) {
				w.out = append(w.out, comparedFile{dir: dir.path, name: name, files: at})
			}
		}
		slices.Sort(dirNames)
		for _, name := range slices.Compact(dirNames) {
			at := make([]*domain.TreeNode, w.sides)
			for side := range at {
				at[side] = sideDirs[side][name]
			}
			if w.sameDirs(at) {
				continue
			}
			next = append(next, compareDir{path: joinTreePath(dir.path, name), nodes: at})
		}
	}
	return next, nil
}

// loadChunks reads the chunk lists of every reported file in one batch. The
// same file row can be reported at several paths; it gets its own copy at
// each, so no caller shares a File between paths.
func (w *compareWalk) loadChunks(ctx context.Context) error {
	var ids []int64
	seen := map[int64]struct{}{}
	for _, entry := range w.out {
		for _, file := range entry.files {
			if file == nil {
				continue
			}
			if _, ok := seen[file.ID]; ok {
				continue
			}
			seen[file.ID] = struct{}{}
			ids = append(ids, file.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	chunks, err := w.repo.ListChunksByFiles(ctx, ids)
	if err != nil {
		return err
	}
	for _, entry := range w.out {
		for side, file := range entry.files {
			if file == nil {
				continue
			}
			loaded := *file
			loaded.Chunks = slices.Clone(chunks[file.ID])
			entry.files[side] = &loaded
		}
	}
	return nil
}
