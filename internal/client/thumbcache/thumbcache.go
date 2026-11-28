// Package thumbcache keeps small rendered thumbnails on disk so a file grid
// does not decode the same image again in every session. Entries are opaque
// byte values under string keys; callers put everything that identifies the
// rendering (content hash or stat fingerprint, size, surface, format
// version) into the key.
//
// The folder may be shared by several daemons at once: entries are written
// to a temporary file and renamed into place, readers treat any missing,
// short or corrupt entry as a miss, and eviction tolerates files another
// process already removed.
package thumbcache

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash/crc32"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// EnvDir overrides the cache folder.
	EnvDir = "NIPA_THUMBNAIL_CACHE_DIR"
	// DefaultMaxBytes caps the folder; the oldest entries are evicted beyond it.
	DefaultMaxBytes int64 = 256 << 20

	tempPrefix = ".tmp-"
	headerSize = 16
	// staleTemp is how old an orphaned temporary file (a writer that died
	// before its rename) must be before eviction removes it.
	staleTemp = time.Hour
)

var magic = [4]byte{'N', 'T', 'C', '1'}

// Cache is a size-capped folder of entries, evicted oldest first by
// modification time; a hit refreshes the entry's time.
type Cache struct {
	dir      string
	maxBytes int64
	now      func() time.Time

	mu    sync.Mutex
	sized bool
	size  int64
}

// DefaultDir is $NIPA_THUMBNAIL_CACHE_DIR or <user cache dir>/nipa/thumbs.
func DefaultDir() (string, error) {
	if dir := strings.TrimSpace(os.Getenv(EnvDir)); dir != "" {
		return dir, nil
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "nipa", "thumbs"), nil
}

// New opens (creating it if needed) the cache folder dir, capped at maxBytes
// (DefaultMaxBytes when not positive).
func New(dir string, maxBytes int64) (*Cache, error) {
	if dir == "" {
		return nil, errors.New("thumbcache: empty folder")
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Cache{dir: dir, maxBytes: maxBytes, now: time.Now}, nil
}

// Dir is the cache folder.
func (c *Cache) Dir() string { return c.dir }

func (c *Cache) path(key string) string {
	sum := sha256.Sum256([]byte(key))
	name := hex.EncodeToString(sum[:])
	return filepath.Join(c.dir, name[:2], name)
}

// Get returns the value stored under key. A missing entry is a miss; an
// unreadable or corrupt one is removed and is a miss too.
func (c *Cache) Get(key string) ([]byte, bool) {
	p := c.path(key)
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, false
	}
	value, ok := decode(data)
	if !ok {
		_ = os.Remove(p)
		return nil, false
	}
	now := c.now()
	_ = os.Chtimes(p, now, now)
	return value, true
}

// Put stores value under key, replacing any previous entry atomically, then
// evicts the oldest entries if the folder grew past its cap. A value larger
// than the whole cap is not stored.
func (c *Cache) Put(key string, value []byte) error {
	data := encode(value)
	size := int64(len(data))
	if size > c.maxBytes {
		return nil
	}
	p := c.path(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(p), tempPrefix+"*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, werr := f.Write(data)
	cerr := f.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		_ = os.Remove(tmp)
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.sized {
		c.evictLocked()
		return nil
	}
	c.size += size
	if c.size > c.maxBytes {
		c.evictLocked()
	}
	return nil
}

type cacheFile struct {
	path  string
	size  int64
	mtime time.Time
}

// evictLocked walks the folder, drops stale temporary files and, when the
// entries exceed the cap, removes the oldest until they fit in 90% of it.
// The walk also resynchronizes the size estimate, which other daemons
// sharing the folder make drift.
func (c *Cache) evictLocked() {
	var files []cacheFile
	var total int64
	now := c.now()
	_ = filepath.WalkDir(c.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if strings.HasPrefix(d.Name(), tempPrefix) {
			if now.Sub(info.ModTime()) > staleTemp {
				_ = os.Remove(p)
			}
			return nil
		}
		files = append(files, cacheFile{path: p, size: info.Size(), mtime: info.ModTime()})
		total += info.Size()
		return nil
	})
	if total > c.maxBytes {
		sort.Slice(files, func(i, j int) bool { return files[i].mtime.Before(files[j].mtime) })
		target := c.maxBytes / 10 * 9
		for _, f := range files {
			if total <= target {
				break
			}
			if err := os.Remove(f.path); err == nil || errors.Is(err, fs.ErrNotExist) {
				total -= f.size
			}
		}
	}
	c.size, c.sized = total, true
}

func encode(value []byte) []byte {
	out := make([]byte, headerSize, headerSize+len(value))
	copy(out, magic[:])
	binary.LittleEndian.PutUint32(out[4:], crc32.ChecksumIEEE(value))
	binary.LittleEndian.PutUint64(out[8:], uint64(len(value)))
	return append(out, value...)
}

func decode(data []byte) ([]byte, bool) {
	if len(data) < headerSize || !bytes.Equal(data[:4], magic[:]) {
		return nil, false
	}
	value := data[headerSize:]
	if binary.LittleEndian.Uint64(data[8:]) != uint64(len(value)) || crc32.ChecksumIEEE(value) != binary.LittleEndian.Uint32(data[4:]) {
		return nil, false
	}
	return value, true
}
