package difftool

import (
	"os"
	"path/filepath"
)

// File is one changed file with loaded contents for external display.
type File struct {
	Path   string // repository-relative path
	Status string // A, M or D
	Old    []byte // old content; absent for added files
	New    []byte // new content; absent for deleted files
	// Unavailable marks content that could not be loaded; the file is
	// skipped with a warning instead of materialized.
	Unavailable bool
}

// Tree holds one materialized old/new file tree pair.
type Tree struct {
	// Dir is the temp root containing old/ and new/; the caller removes it.
	Dir    string
	OldDir string
	NewDir string
}

// Materialize writes files into <dir>/old and <dir>/new preserving relative
// paths, so tools detect file types by extension. Missing sides of
// added/deleted files become empty files. It returns the tree, the
// per-file pairs, and the paths skipped for unavailable content.
func Materialize(files []File) (tree *Tree, pairs []Pair, skipped []string, err error) {
	dir, err := os.MkdirTemp("", "nipa-diff-")
	if err != nil {
		return nil, nil, nil, err
	}
	tree = &Tree{Dir: dir, OldDir: filepath.Join(dir, "old"), NewDir: filepath.Join(dir, "new")}
	if err := os.MkdirAll(tree.OldDir, 0o755); err != nil {
		os.RemoveAll(dir)
		return nil, nil, nil, err
	}
	if err := os.MkdirAll(tree.NewDir, 0o755); err != nil {
		os.RemoveAll(dir)
		return nil, nil, nil, err
	}
	for _, f := range files {
		if f.Unavailable {
			skipped = append(skipped, f.Path)
			continue
		}
		old, new := f.Old, f.New
		if f.Status == "A" {
			old = nil
		}
		if f.Status == "D" {
			new = nil
		}
		local := filepath.Join(tree.OldDir, filepath.FromSlash(f.Path))
		remote := filepath.Join(tree.NewDir, filepath.FromSlash(f.Path))
		if err := writeVersion(local, old); err != nil {
			os.RemoveAll(dir)
			return nil, nil, nil, err
		}
		if err := writeVersion(remote, new); err != nil {
			os.RemoveAll(dir)
			return nil, nil, nil, err
		}
		pairs = append(pairs, Pair{Path: f.Path, Status: f.Status, Local: local, Remote: remote})
	}
	return tree, pairs, skipped, nil
}

func writeVersion(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
