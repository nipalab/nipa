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
// in one rename once every byte is on disk.
type ShelfWriter struct {
	root  string
	tmp   string
	id    string
	dirs  map[string]bool
	bytes int64
}

func (l *LocalRepo) NewShelfWriter() (*ShelfWriter, error) {
	if l.db == nil {
		return nil, errors.New("local repo not initialized")
	}
	root := l.shelvesRoot()
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	sweepShelfTemps(root)
	for attempt := 0; attempt < 8; attempt++ {
		id := strconv.FormatInt(time.Now().UnixNano()+int64(attempt), 36)
		if _, err := os.Stat(filepath.Join(root, id)); err == nil {
			continue
		}
		tmp := filepath.Join(root, shelfTmpPrefix+id)
		if err := os.Mkdir(tmp, 0o755); err != nil {
			if errors.Is(err, fs.ErrExist) {
				continue
			}
			return nil, err
		}
		return &ShelfWriter{root: root, tmp: tmp, id: id, dirs: map[string]bool{}}, nil
	}
	return nil, errors.New("could not allocate a shelf id")
}

func sweepShelfTemps(root string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-staleTempAge)
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), shelfTmpPrefix) {
			continue
		}
		if info, err := e.Info(); err == nil && info.ModTime().Before(cutoff) {
			_ = os.RemoveAll(filepath.Join(root, e.Name()))
		}
	}
}

func (w *ShelfWriter) Bytes() int64 { return w.bytes }

func (w *ShelfWriter) PutChunk(hash serverDomain.Hash, data []byte) error {
	p := shelfObjectPath(w.tmp, hash)
	if _, err := os.Stat(p); err == nil {
		return nil
	}
	dir := filepath.Dir(p)
	if !w.dirs[dir] {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		w.dirs[dir] = true
	}
	if err := writeSynced(p, data); err != nil {
		return err
	}
	w.bytes += int64(len(data))
	return nil
}

func (w *ShelfWriter) Commit(shelf *domain.Shelf) error {
	shelf.ID = w.id
	shelf.SizeBytes = w.bytes
	data, err := json.MarshalIndent(shelf, "", "  ")
	if err != nil {
		return err
	}
	if err := writeSynced(filepath.Join(w.tmp, shelfManifest), data); err != nil {
		return err
	}
	for dir := range w.dirs {
		syncDir(dir)
	}
	syncDir(filepath.Join(w.tmp, ObjectsDir))
	syncDir(w.tmp)
	if err := os.Rename(w.tmp, filepath.Join(w.root, w.id)); err != nil {
		return err
	}
	syncDir(w.root)
	return nil
}

func (w *ShelfWriter) Abort() {
	_ = os.RemoveAll(w.tmp)
}

func writeSynced(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
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

func syncDir(dir string) {
	if runtime.GOOS == "windows" {
		return
	}
	f, err := os.Open(dir)
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
	trash := filepath.Join(l.shelvesRoot(), shelfTmpPrefix+"drop-"+id)
	if err := os.Rename(dir, trash); err != nil {
		return err
	}
	return os.RemoveAll(trash)
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
