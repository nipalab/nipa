// Package treedelta describes a tree manifest relative to another one and
// applies such a description. A client that holds the manifest of a base
// commit asks the server for a delta to a target commit instead of the whole
// target manifest; the server sends only the directories that differ.
//
// Apply works on manifests as the client transport decodes them: a file's
// Mode is its mode class (domain.FileModeClass*), its Hash covers its chunk
// hashes and a directory's Hash is the tree hash of what the manifest lists.
package treedelta

import (
	"errors"
	"fmt"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/treehash"
)

// Delta is a target manifest described relative to a base manifest of the
// same view (same read rules, same sparse set).
type Delta struct {
	// Root describes the root directory. It is never nil.
	Root *Dir
	// RootHash is the root hash the full target manifest carries, when the
	// server could prove it without loading unchanged directories. Nil means
	// there is nothing to verify the result against: the hashes Apply computes
	// are then the only ones.
	RootHash *domain.Hash
}

// Dir is one directory of a delta.
type Dir struct {
	Name string
	// Unchanged says the whole subtree is the base's: the holder keeps
	// whatever it has under this path, which may be nothing (a directory
	// without visible files is not part of a manifest). Files and Dirs are
	// empty then.
	Unchanged bool
	// Files and Dirs list every entry of a changed directory in manifest
	// order. Anything the base holds in this directory that is not listed was
	// removed.
	Files []File
	Dirs  []*Dir
}

// File is one file of a changed directory.
type File struct {
	Name string
	// Changed is the file as the target manifest carries it when it was added
	// or modified. Nil means the file is the base's.
	Changed *domain.File
}

// Unchanged reports whether the file is the base's.
func (f File) Unchanged() bool { return f.Changed == nil }

// Base names the manifest a client holds when it asks for a delta.
type Base struct {
	// CommitID is the base36 id of the commit the manifest belongs to.
	CommitID string
	// View is the token the server returned with that manifest. A delta is
	// only sound against a manifest of the same view, and the server declines
	// when the token is not the current one.
	View []byte
	// TreeHash is the root hash of that manifest. It is optional; with it the
	// server can vouch for the root hash of the result (Delta.RootHash).
	TreeHash *domain.Hash
}

// Manifest is the answer to a manifest request that may be served as a delta:
// exactly one of Full and Delta describes the target, except that both are
// nil when there is no tree at all (a branch without commits).
type Manifest struct {
	// View is the token of the view the answer was built for; the holder
	// stores it with the manifest and sends it as Base.View next time. Empty
	// when the server predates view tokens.
	View []byte
	// Full is the whole target manifest: no base was named, or the server
	// declined the delta.
	Full *domain.TreeNode
	// Delta is the target relative to the base that was named.
	Delta *Delta
	// CommitID is the base36 id of the commit a delta leads to. It is not set
	// with a full manifest.
	CommitID string
}

// ErrRootHashMismatch is returned by Apply when the result does not hash to
// the root hash the delta carries: the base was not the manifest the delta
// was computed against.
var ErrRootHashMismatch = errors.New("tree delta: the result does not match the root hash of the delta")

// Apply returns the target manifest for a delta computed against base. base
// is not modified; the result shares its unchanged directories and files, so
// neither tree may be changed in place afterwards. A nil base is an empty
// root. Changed directories get their tree hash recomputed, directories that
// end up empty are dropped (the root is kept), and the result is checked
// against Delta.RootHash when the delta carries one.
func Apply(base *domain.TreeNode, delta *Delta) (*domain.TreeNode, error) {
	if delta == nil || delta.Root == nil {
		return nil, errors.New("tree delta: no root directory")
	}
	root, err := applyDir(base, delta.Root, delta.Root.Name)
	if err != nil {
		return nil, err
	}
	if root == nil {
		root = &domain.TreeNode{
			Name:         delta.Root.Name,
			Hash:         treehash.TreeHash(nil, nil),
			TreeChildren: []*domain.TreeNode{},
			FileChildren: []*domain.File{},
		}
	}
	if delta.RootHash != nil && root.Hash != *delta.RootHash {
		return nil, ErrRootHashMismatch
	}
	return root, nil
}

// applyDir returns the directory d describes, or nil when the base holds
// nothing for an unchanged directory. path only labels errors.
func applyDir(base *domain.TreeNode, d *Dir, path string) (*domain.TreeNode, error) {
	if d.Unchanged {
		return base, nil
	}
	node := &domain.TreeNode{
		Name:         d.Name,
		TreeChildren: make([]*domain.TreeNode, 0, len(d.Dirs)),
		FileChildren: make([]*domain.File, 0, len(d.Files)),
	}
	var baseFiles map[string]*domain.File
	var baseDirs map[string]*domain.TreeNode
	if base != nil {
		baseFiles = make(map[string]*domain.File, len(base.FileChildren))
		for _, f := range base.FileChildren {
			baseFiles[f.Name] = f
		}
		baseDirs = make(map[string]*domain.TreeNode, len(base.TreeChildren))
		for _, child := range base.TreeChildren {
			baseDirs[child.Name] = child
		}
	}
	files := make([]treehash.FileEntry, 0, len(d.Files))
	for _, f := range d.Files {
		file := f.Changed
		if file == nil {
			if file = baseFiles[f.Name]; file == nil {
				return nil, fmt.Errorf("tree delta: unchanged file %q is not in the base", path+"/"+f.Name)
			}
		}
		node.FileChildren = append(node.FileChildren, file)
		files = append(files, treehash.FileEntry{Name: file.Name, Hash: file.Hash, Mode: HashMode(file.Mode)})
	}
	trees := make([]treehash.TreeEntry, 0, len(d.Dirs))
	for _, sub := range d.Dirs {
		if sub == nil {
			return nil, fmt.Errorf("tree delta: empty directory entry in %q", path)
		}
		child, err := applyDir(baseDirs[sub.Name], sub, path+"/"+sub.Name)
		if err != nil {
			return nil, err
		}
		if child == nil || (len(child.FileChildren) == 0 && len(child.TreeChildren) == 0) {
			continue
		}
		node.TreeChildren = append(node.TreeChildren, child)
		trees = append(trees, treehash.TreeEntry{Name: child.Name, Hash: child.Hash})
	}
	node.Hash = treehash.TreeHash(files, trees)
	return node, nil
}

// HashMode turns the mode class a manifest file carries into the mode the
// server stores for it, which is what the tree hash classifies. Classes must
// not reach treehash as they are: class 1 (read-only) would read as the
// permission 0o001, an executable.
func HashMode(class int) int {
	switch class {
	case domain.FileModeClassReadOnly:
		return 0o444
	case domain.FileModeClassExecutable:
		return 0o755
	default:
		return 0o644
	}
}
