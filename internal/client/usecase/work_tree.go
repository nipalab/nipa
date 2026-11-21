package usecase

import (
	"errors"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
)

// workTree performs an operation's writes, renames, chmods and removals of
// working files through one os.Root opened on the clone root, so the runtime
// refuses any step out of the clone even when a folder the guard verified is
// swapped for a link before the write happens. The root is opened on first
// use and closed when the last holder releases it; a write scope
// (guardWrites) is one holder for its whole operation, so an operation opens
// the root at most once. Names are relative to the root, OS separated.
type workTree struct {
	path string

	mu     sync.Mutex
	root   *os.Root
	refs   int
	closed bool
}

// acquireWorkTree returns the work tree of root's open write scope, or a
// fresh one that only serves the caller when no scope is open. Every acquire
// is paired with a release.
func acquireWorkTree(root string) *workTree {
	writeGuards.Lock()
	defer writeGuards.Unlock()
	if scope, ok := writeGuards.scopes[filepath.Clean(root)]; ok {
		scope.tree.mu.Lock()
		scope.tree.refs++
		scope.tree.mu.Unlock()
		return scope.tree
	}
	return &workTree{path: root, refs: 1}
}

// release drops one hold and closes the root with the last one.
func (w *workTree) release() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.refs--; w.refs > 0 {
		return
	}
	w.closed = true
	if w.root != nil {
		_ = w.root.Close()
		w.root = nil
	}
}

// open returns the root, opening it on first use. create makes a missing
// clone root first (the root itself may be reached through a link).
func (w *workTree) open(create bool) (*os.Root, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil, os.ErrClosed
	}
	if w.root != nil {
		return w.root, nil
	}
	r, err := os.OpenRoot(w.path)
	if create && errors.Is(err, fs.ErrNotExist) {
		if err = os.MkdirAll(w.path, 0o755); err == nil {
			r, err = os.OpenRoot(w.path)
		}
	}
	if err != nil {
		return nil, err
	}
	w.root = r
	return r, nil
}

// dirOf opens the folder of the file name as a root of its own, so the steps
// on one file resolve its folder once and then name only the file; with
// create a missing folder is made first. done closes it. verb names the
// refused action when the folder leads out of the clone.
func (w *workTree) dirOf(verb, name string, create bool) (d *os.Root, done func(), err error) {
	r, err := w.open(create)
	if err != nil {
		return nil, nil, err
	}
	dir := filepath.Dir(name)
	if dir == "." {
		return r, func() {}, nil
	}
	d, err = r.OpenRoot(dir)
	if create && errors.Is(err, fs.ErrNotExist) {
		if err = r.MkdirAll(dir, 0o755); err == nil {
			d, err = r.OpenRoot(dir)
		}
	}
	if err != nil {
		return nil, nil, refuseEscape(verb, name, err)
	}
	return d, func() { _ = d.Close() }, nil
}

// createTempIn creates a new file in d named prefix plus a random decimal
// suffix, like os.CreateTemp (os.Root has none), and returns it open for
// reading and writing with its name.
func createTempIn(d *os.Root, prefix string) (*os.File, string, error) {
	for try := 0; ; try++ {
		name := prefix + strconv.FormatUint(uint64(rand.Uint32()), 10)
		f, err := d.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) && try < 10000 {
			continue
		}
		if err != nil {
			return nil, "", err
		}
		return f, name, nil
	}
}

func (w *workTree) openFile(name string, flag int, perm fs.FileMode) (*os.File, error) {
	r, err := w.open(false)
	if err != nil {
		return nil, err
	}
	return r.OpenFile(name, flag, perm)
}

func (w *workTree) chmod(name string, mode fs.FileMode) error {
	r, err := w.open(false)
	if err != nil {
		return err
	}
	return refuseEscape("write", name, r.Chmod(name, mode))
}

func (w *workTree) remove(name string) error {
	r, err := w.open(false)
	if err != nil {
		return err
	}
	return refuseEscape("remove", name, r.Remove(name))
}

// replace renames src over dst (replaceIn); src is a file in dst's folder.
func (w *workTree) replace(src, dst string) error {
	d, done, err := w.dirOf("write", dst, false)
	if err != nil {
		return err
	}
	defer done()
	return replaceIn(d, filepath.Base(src), filepath.Base(dst))
}

// isReadOnly is isReadOnlyIn for a name inside the root.
func (w *workTree) isReadOnly(name string) bool {
	r, err := w.open(false)
	return err == nil && isReadOnlyIn(r, name)
}

// rootEscapeMessage is the message of the error os.Root returns for a name
// that would leave the root; the error value itself is not exported.
const rootEscapeMessage = "path escapes from parent"

// refuseEscape turns os.Root's refusal to leave the root into the same kind
// of user error the write guard gives.
func refuseEscape(verb, name string, err error) error {
	if err == nil || !isRootEscape(err) {
		return err
	}
	p := filepath.ToSlash(name)
	return clientDomain.NewUserError(fmt.Sprintf("refusing to %s %q: a folder on the way to it is a link that leads out of the working copy", verb, p)).
		WithHint("Nipa never writes or deletes through a symbolic link or junction inside the working copy, and a folder was replaced by one while it was writing", "replace the link with a real folder (or move it out of the working copy), then run the command again")
}

func isRootEscape(err error) bool {
	for err != nil {
		next := errors.Unwrap(err)
		if next == nil {
			return err.Error() == rootEscapeMessage
		}
		err = next
	}
	return false
}
