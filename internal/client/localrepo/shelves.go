package localrepo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	sqlcLocalrepo "github.com/nipalab/nipa/internal/repository/sqlc/localrepo"
)

const (
	ShelvesDir     = "shelves"
	shelfManifest  = "manifest.json"
	shelfStateKey  = "shelf_state"
	shelfTmpPrefix = ".tmp-"
)

var shelfIDPattern = regexp.MustCompile(`^[0-9a-z]{1,32}$`)

var errShelfNotFound = errors.New("shelf not found")

func (l *LocalRepo) shelvesRoot() string {
	return filepath.Join(l.target, ConfigDir, ShelvesDir)
}

func (l *LocalRepo) shelfDir(id string) (string, error) {
	if !shelfIDPattern.MatchString(id) {
		return "", domain.NewUserError(fmt.Sprintf("invalid shelf id %q", id))
	}
	return filepath.Join(l.shelvesRoot(), id), nil
}

func shelfObjectPath(dir string, hash serverDomain.Hash) string {
	hex := hash.String()
	return filepath.Join(dir, ObjectsDir, hex[:2], hex[2:])
}

// ShelfWriter fills a shelf in a temporary folder; Commit makes it visible
// in one rename once every byte is on disk. It keeps the verified shelves
// folder open as an os.Root and does every mkdir, write, rename and removal
// through it, so a link swapped in under the shelves folder (or the temp
// folder) while the shelf is written is never followed. The root is closed by
// Commit and Abort.
type ShelfWriter struct {
	root  *os.Root
	tmp   string
	id    string
	dirs  map[string]bool
	bytes int64
}

var errShelfWriterClosed = errors.New("shelf writer is closed")

func (l *LocalRepo) NewShelfWriter() (*ShelfWriter, error) {
	if l.db == nil {
		return nil, errors.New("local repo not initialized")
	}
	root, err := ensureRealDir(l.target, filepath.Join(ConfigDir, ShelvesDir))
	if err != nil {
		return nil, err
	}
	w, err := l.allocateShelf(root)
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	return w, nil
}

func (l *LocalRepo) allocateShelf(root *os.Root) (*ShelfWriter, error) {
	if err := l.sweepShelfTemps(); err != nil {
		return nil, err
	}
	for attempt := 0; attempt < 8; attempt++ {
		id := strconv.FormatInt(time.Now().UnixNano()+int64(attempt), 36)
		if _, err := root.Lstat(id); err == nil {
			continue
		}
		tmp := shelfTmpPrefix + id
		if err := root.Mkdir(tmp, 0o755); err != nil {
			if errors.Is(err, fs.ErrExist) {
				continue
			}
			return nil, err
		}
		return &ShelfWriter{root: root, tmp: tmp, id: id, dirs: map[string]bool{}}, nil
	}
	return nil, errors.New("could not allocate a shelf id")
}

// sweepShelfTemps removes the temp folders of shelves that were never
// committed, through the shelves folder opened as an os.Root; it fails only
// when that folder cannot be opened as a real one.
func (l *LocalRepo) sweepShelfTemps() error {
	root, err := openRealDir(l.target, filepath.Join(ConfigDir, ShelvesDir))
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return nil
	}
	cutoff := time.Now().Add(-staleTempAge)
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), shelfTmpPrefix) {
			continue
		}
		if info, err := e.Info(); err == nil && info.ModTime().Before(cutoff) {
			_ = root.RemoveAll(e.Name())
		}
	}
	return nil
}

func (w *ShelfWriter) Bytes() int64 { return w.bytes }

func (w *ShelfWriter) PutChunk(hash serverDomain.Hash, data []byte) error {
	if w.root == nil {
		return errShelfWriterClosed
	}
	p := shelfObjectPath(w.tmp, hash)
	if _, err := w.root.Stat(p); err == nil {
		return nil
	}
	dir := filepath.Dir(p)
	if !w.dirs[dir] {
		if err := w.root.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		w.dirs[dir] = true
	}
	if err := writeSynced(w.root, p, data); err != nil {
		return err
	}
	w.bytes += int64(len(data))
	return nil
}

// Commit publishes the shelf with one rename and closes the writer. On any
// failure the temp folder is removed, as Abort does.
func (w *ShelfWriter) Commit(shelf *domain.Shelf) error {
	if w.root == nil {
		return errShelfWriterClosed
	}
	if err := w.commit(shelf); err != nil {
		w.Abort()
		return err
	}
	_ = w.root.Close()
	w.root = nil
	return nil
}

func (w *ShelfWriter) commit(shelf *domain.Shelf) error {
	shelf.ID = w.id
	shelf.SizeBytes = w.bytes
	data, err := json.MarshalIndent(shelf, "", "  ")
	if err != nil {
		return err
	}
	if err := writeSynced(w.root, filepath.Join(w.tmp, shelfManifest), data); err != nil {
		return err
	}
	for dir := range w.dirs {
		syncDir(w.root, dir)
	}
	syncDir(w.root, filepath.Join(w.tmp, ObjectsDir))
	syncDir(w.root, w.tmp)
	if err := w.root.Rename(w.tmp, w.id); err != nil {
		return err
	}
	syncDir(w.root, ".")
	return nil
}

// Abort removes the temp folder and closes the writer; it is safe to call
// more than once and after Commit.
func (w *ShelfWriter) Abort() {
	if w.root == nil {
		return
	}
	_ = w.root.RemoveAll(w.tmp)
	_ = w.root.Close()
	w.root = nil
}

func writeSynced(root *os.Root, path string, data []byte) error {
	f, err := root.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func syncDir(root *os.Root, dir string) {
	if runtime.GOOS == "windows" {
		return
	}
	f, err := root.Open(dir)
	if err != nil {
		return
	}
	_ = f.Sync()
	_ = f.Close()
}

// ListShelves returns every complete shelf, newest first. A folder without a
// readable manifest is skipped.
func (l *LocalRepo) ListShelves() ([]domain.Shelf, error) {
	if l.db == nil {
		return nil, errors.New("local repo not initialized")
	}
	entries, err := os.ReadDir(l.shelvesRoot())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []domain.Shelf
	for _, e := range entries {
		if !e.IsDir() || !shelfIDPattern.MatchString(e.Name()) {
			continue
		}
		shelf, err := l.LoadShelf(e.Name())
		if err != nil {
			continue
		}
		out = append(out, *shelf)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID > out[j].ID
	})
	return out, nil
}

func (l *LocalRepo) LoadShelf(id string) (*domain.Shelf, error) {
	dir, err := l.shelfDir(id)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(dir, shelfManifest))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, shelfMissing(id)
	}
	if err != nil {
		return nil, err
	}
	var shelf domain.Shelf
	if err := json.Unmarshal(data, &shelf); err != nil {
		return nil, fmt.Errorf("read shelf %s: %w", id, err)
	}
	shelf.ID = id
	return &shelf, nil
}

func shelfMissing(id string) error {
	e := domain.NewNotFoundError(fmt.Sprintf("shelf %q not found", id))
	e.Cause = errShelfNotFound
	return e
}

func (l *LocalRepo) DropShelf(id string) error {
	dir, err := l.shelfDir(id)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(dir, shelfManifest)); errors.Is(err, fs.ErrNotExist) {
		return shelfMissing(id)
	}
	shelves, err := openRealDir(l.target, filepath.Join(ConfigDir, ShelvesDir))
	if err != nil {
		return err
	}
	defer func() { _ = shelves.Close() }()
	trash := shelfTmpPrefix + "drop-" + id
	if err := shelves.Rename(id, trash); err != nil {
		return err
	}
	return shelves.RemoveAll(trash)
}

func (l *LocalRepo) OpenShelfChunk(id string, hash serverDomain.Hash) (io.ReadCloser, error) {
	dir, err := l.shelfDir(id)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(shelfObjectPath(dir, hash))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("chunk %s not found in shelf %s", hash, id)
	}
	return f, err
}

func (l *LocalRepo) LoadShelfChunk(id string, hash serverDomain.Hash) ([]byte, error) {
	r, err := l.OpenShelfChunk(id, hash)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	return io.ReadAll(r)
}

func (l *LocalRepo) HasShelfChunk(id string, hash serverDomain.Hash) bool {
	dir, err := l.shelfDir(id)
	if err != nil {
		return false
	}
	_, err = os.Stat(shelfObjectPath(dir, hash))
	return err == nil
}

func (l *LocalRepo) SaveShelfState(state *domain.ShelfState) error {
	if l.db == nil {
		return errors.New("local repo not initialized")
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return sqlcLocalrepo.New(l.db).MetaSet(context.Background(), sqlcLocalrepo.MetaSetParams{Key: shelfStateKey, Value: string(data)})
}

func (l *LocalRepo) LoadShelfState() (*domain.ShelfState, error) {
	if l.db == nil {
		return nil, errors.New("local repo not initialized")
	}
	value, err := sqlcLocalrepo.New(l.db).MetaGet(context.Background(), shelfStateKey)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var state domain.ShelfState
	if err := json.Unmarshal([]byte(value), &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func (l *LocalRepo) ClearShelfState() error {
	if l.db == nil {
		return errors.New("local repo not initialized")
	}
	return sqlcLocalrepo.New(l.db).MetaDelete(context.Background(), shelfStateKey)
}

// CreateShelf runs build with a chunk writer and commits the shelf it
// returns; any failure leaves no shelf behind.
func (l *LocalRepo) CreateShelf(build func(put func(serverDomain.Hash, []byte) error) (*domain.Shelf, error)) (*domain.Shelf, error) {
	w, err := l.NewShelfWriter()
	if err != nil {
		return nil, err
	}
	shelf, err := build(w.PutChunk)
	if err == nil {
		err = w.Commit(shelf)
	}
	if err != nil {
		w.Abort()
		return nil, err
	}
	return shelf, nil
}
