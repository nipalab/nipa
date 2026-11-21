package usecase

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
)

// dirGuard refuses a working-copy path when a folder between the root and
// the path is a symbolic link or a junction, wherever it points: following
// one would write or delete outside the clone or inside .nipa. The root
// itself may be reached through a link; only the components below it are
// checked. A folder found safe, or missing (the operation creates it, and
// everything below it, as real folders), is remembered, so a guard costs at
// most one Lstat per distinct folder and none below a missing one.
type dirGuard struct {
	root  string
	lstat func(string) (fs.FileInfo, error)

	mu sync.RWMutex
	// safe maps a verified folder to whether it was missing.
	safe map[string]bool
}

func newDirGuard(root string) *dirGuard {
	return &dirGuard{root: root, lstat: os.Lstat, safe: map[string]bool{}}
}

// check verifies the folders above the slash-separated working path p. verb
// names the refused action in the error.
func (g *dirGuard) check(verb, p string) error {
	dir := parentDir(p)
	if dir == "" {
		return nil
	}
	var pending []string
	for d := dir; d != ""; d = parentDir(d) {
		if missing, ok := g.known(d); ok {
			if missing {
				g.remember(true, pending...)
				return nil
			}
			break
		}
		pending = append(pending, d)
	}
	for i := len(pending) - 1; i >= 0; i-- {
		d := pending[i]
		info, err := g.lstat(filepath.Join(g.root, filepath.FromSlash(d)))
		if errors.Is(err, fs.ErrNotExist) {
			g.remember(true, pending[:i+1]...)
			return nil
		}
		if err != nil {
			return err
		}
		if link := clientDomain.LinkKind(info.Mode()); link != "" {
			return refusedLinkError(verb, p, d, link)
		}
		if !info.IsDir() {
			// Not a folder: the write itself fails with the system's error.
			return nil
		}
		g.remember(false, d)
	}
	return nil
}

func (g *dirGuard) known(dir string) (missing, ok bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	missing, ok = g.safe[dir]
	return missing, ok
}

func (g *dirGuard) remember(missing bool, dirs ...string) {
	if len(dirs) == 0 {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, d := range dirs {
		g.safe[d] = missing
	}
}

func refusedLinkError(verb, p, link, kind string) error {
	return clientDomain.NewUserError(fmt.Sprintf("refusing to %s %q: %q is a %s", verb, p, link, kind)).
		WithHint("Nipa never writes or deletes through a symbolic link or junction inside the working copy", fmt.Sprintf("replace %q with a real folder (or move it out of the working copy), then run the command again", link))
}

// writeGuards shares one dirGuard and one workTree among the writes to a root
// while an operation that opened a guardWrites scope runs, so its folders are
// checked once per operation, never trusted from an earlier one, and the root
// is opened once per operation.
var writeGuards = struct {
	sync.Mutex
	scopes map[string]*writeGuardScope
}{scopes: map[string]*writeGuardScope{}}

type writeGuardScope struct {
	guard *dirGuard
	tree  *workTree
	refs  int
}

// guardWrites opens a guard scope for root and returns the function that
// closes it. Scopes nest; the scope's root handle is closed with the last one
// (or with the last write still holding it).
func guardWrites(root string) func() {
	key := filepath.Clean(root)
	writeGuards.Lock()
	scope, ok := writeGuards.scopes[key]
	if !ok {
		scope = &writeGuardScope{guard: newDirGuard(root), tree: &workTree{path: root, refs: 1}}
		writeGuards.scopes[key] = scope
	}
	scope.refs++
	writeGuards.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			writeGuards.Lock()
			defer writeGuards.Unlock()
			if scope.refs--; scope.refs == 0 {
				delete(writeGuards.scopes, key)
				scope.tree.release()
			}
		})
	}
}

// guardFor returns the guard of root's open scope, or a fresh one that only
// serves the caller when no scope is open.
func guardFor(root string) *dirGuard {
	writeGuards.Lock()
	defer writeGuards.Unlock()
	if scope, ok := writeGuards.scopes[filepath.Clean(root)]; ok {
		return scope.guard
	}
	return newDirGuard(root)
}

// checkWriteInsideRoot guards every write of a tracked path into the working
// copy: the path must be a safe relative path and no folder below root on the
// way to it may be a link. The file itself may be a link: writes replace it
// by renaming over it, never by following it.
func checkWriteInsideRoot(root, p string) error {
	if err := checkWorkingPath(p); err != nil {
		return err
	}
	return guardFor(root).check("write", p)
}

// checkRemoveInsideRoot is checkWriteInsideRoot for deleting a working file.
func checkRemoveInsideRoot(root, p string) error {
	if err := checkWorkingPath(p); err != nil {
		return err
	}
	return guardFor(root).check("remove", p)
}

// checkTargets checks every path an operation is about to write or remove,
// before it downloads or changes anything, through one guard.
func checkTargets(root, verb string, paths []string) error {
	g := guardFor(root)
	for _, p := range paths {
		if err := checkWorkingPath(p); err != nil {
			return err
		}
		if err := g.check(verb, p); err != nil {
			return err
		}
	}
	return nil
}
