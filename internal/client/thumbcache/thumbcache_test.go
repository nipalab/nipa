package thumbcache

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newCache(t *testing.T, maxBytes int64) *Cache {
	t.Helper()
	c, err := New(filepath.Join(t.TempDir(), "thumbs"), maxBytes)
	require.NoError(t, err)
	return c
}

func entryFiles(t *testing.T, dir string) (entries, temps []string) {
	t.Helper()
	require.NoError(t, filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		require.NoError(t, err)
		switch {
		case d.IsDir():
		case strings.HasPrefix(d.Name(), tempPrefix):
			temps = append(temps, p)
		default:
			entries = append(entries, p)
		}
		return nil
	}))
	return entries, temps
}

func TestCache_PutGet(t *testing.T) {
	c := newCache(t, 0)
	require.Equal(t, DefaultMaxBytes, c.maxBytes)

	_, ok := c.Get("a")
	require.False(t, ok)
	require.NoError(t, c.Put("a", []byte("alpha")))
	got, ok := c.Get("a")
	require.True(t, ok)
	require.Equal(t, "alpha", string(got))

	require.NoError(t, c.Put("a", []byte("alpha 2")))
	got, ok = c.Get("a")
	require.True(t, ok)
	require.Equal(t, "alpha 2", string(got))

	require.NoError(t, c.Put("empty", nil))
	got, ok = c.Get("empty")
	require.True(t, ok)
	require.Empty(t, got)

	_, temps := entryFiles(t, c.Dir())
	require.Empty(t, temps, "writes leave no temporary files")
}

func TestCache_KeysAreSeparate(t *testing.T) {
	c := newCache(t, 0)
	keys := []string{"h|abc|128|0|0|v1", "h|abc|256|0|0|v1", "h|abc|128|1|0|v1", "h|abc|128|0|1|v1", "h|abc|128|0|0|v2"}
	for i, k := range keys {
		require.NoError(t, c.Put(k, []byte{byte(i)}))
	}
	for i, k := range keys {
		got, ok := c.Get(k)
		require.True(t, ok, k)
		require.Equal(t, []byte{byte(i)}, got, k)
	}
}

func TestCache_CorruptEntryIsMissAndRemoved(t *testing.T) {
	c := newCache(t, 0)
	for name, corrupt := range map[string]func([]byte) []byte{
		"short":    func(b []byte) []byte { return b[:5] },
		"magic":    func(b []byte) []byte { b[0] = 'X'; return b },
		"checksum": func(b []byte) []byte { b[len(b)-1] ^= 0xff; return b },
		"length":   func(b []byte) []byte { return append(b, 1) },
	} {
		key := "k-" + name
		require.NoError(t, c.Put(key, []byte("payload")))
		p := c.path(key)
		data, err := os.ReadFile(p)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(p, corrupt(data), 0o600))

		_, ok := c.Get(key)
		require.False(t, ok, name)
		_, err = os.Stat(p)
		require.ErrorIs(t, err, os.ErrNotExist, name)
	}
}

func TestCache_HitRefreshesTime(t *testing.T) {
	c := newCache(t, 0)
	require.NoError(t, c.Put("a", []byte("x")))
	old := time.Now().Add(-48 * time.Hour)
	require.NoError(t, os.Chtimes(c.path("a"), old, old))
	_, ok := c.Get("a")
	require.True(t, ok)
	info, err := os.Stat(c.path("a"))
	require.NoError(t, err)
	require.WithinDuration(t, time.Now(), info.ModTime(), time.Minute)
}

func TestCache_EvictsOldestBeyondCap(t *testing.T) {
	value := bytes.Repeat([]byte{7}, 1000)
	entry := int64(len(encode(value)))
	c := newCache(t, 5*entry)
	base := time.Now().Add(-time.Hour)
	for i := range 5 {
		key := fmt.Sprint("k", i)
		require.NoError(t, c.Put(key, value))
		at := base.Add(time.Duration(i) * time.Minute)
		require.NoError(t, os.Chtimes(c.path(key), at, at))
	}
	// k0 is the oldest, but a hit makes it the newest.
	_, ok := c.Get("k0")
	require.True(t, ok)

	require.NoError(t, c.Put("k5", value))
	entries, _ := entryFiles(t, c.Dir())
	require.Len(t, entries, 4, "evicted down to 90%% of the cap")
	for _, gone := range []string{"k1", "k2"} {
		_, ok := c.Get(gone)
		require.False(t, ok, gone)
	}
	for _, kept := range []string{"k0", "k3", "k4", "k5"} {
		_, ok := c.Get(kept)
		require.True(t, ok, kept)
	}
	require.LessOrEqual(t, c.size, c.maxBytes)
}

func TestCache_EvictionResyncsWithOtherWriters(t *testing.T) {
	value := bytes.Repeat([]byte{1}, 1000)
	entry := int64(len(encode(value)))
	dir := filepath.Join(t.TempDir(), "thumbs")
	a, err := New(dir, 3*entry)
	require.NoError(t, err)
	b, err := New(dir, 3*entry)
	require.NoError(t, err)
	for i := range 6 {
		w := a
		if i%2 == 1 {
			w = b
		}
		require.NoError(t, w.Put(fmt.Sprint("k", i), value))
		at := time.Now().Add(time.Duration(i-10) * time.Minute)
		require.NoError(t, os.Chtimes(w.path(fmt.Sprint("k", i)), at, at))
	}
	entries, _ := entryFiles(t, dir)
	require.LessOrEqual(t, int64(len(entries))*entry, 3*entry)
	_, ok := a.Get("k5")
	require.True(t, ok, "the newest entry survives")
}

func TestCache_TooLargeValueIsSkipped(t *testing.T) {
	c := newCache(t, 64)
	require.NoError(t, c.Put("big", bytes.Repeat([]byte{1}, 100)))
	_, ok := c.Get("big")
	require.False(t, ok)
}

func TestCache_StaleTempFilesAreCleaned(t *testing.T) {
	c := newCache(t, 0)
	sub := filepath.Join(c.Dir(), "ab")
	require.NoError(t, os.MkdirAll(sub, 0o700))
	stale := filepath.Join(sub, tempPrefix+"stale")
	fresh := filepath.Join(sub, tempPrefix+"fresh")
	require.NoError(t, os.WriteFile(stale, []byte("x"), 0o600))
	require.NoError(t, os.WriteFile(fresh, []byte("x"), 0o600))
	old := time.Now().Add(-2 * staleTemp)
	require.NoError(t, os.Chtimes(stale, old, old))

	require.NoError(t, c.Put("a", []byte("v"))) // first put scans the folder
	_, temps := entryFiles(t, c.Dir())
	require.Equal(t, []string{fresh}, temps, "a writer still in flight keeps its file")
}

func TestCache_ConcurrentPutSameKey(t *testing.T) {
	c := newCache(t, 0)
	values := make([][]byte, 16)
	for i := range values {
		values[i] = bytes.Repeat([]byte{byte(i)}, 4096)
	}
	var wg sync.WaitGroup
	for i := range values {
		wg.Add(2)
		go func() {
			defer wg.Done()
			// Windows may refuse a rename over an entry a reader holds open;
			// a failed write is only a lost cache entry.
			_ = c.Put("same", values[i])
		}()
		go func() {
			defer wg.Done()
			if got, ok := c.Get("same"); ok {
				assert.Len(t, got, 4096)
				assert.Equal(t, bytes.Repeat(got[:1], 4096), got, "a reader never sees a torn entry")
			}
		}()
	}
	wg.Wait()
	got, ok := c.Get("same")
	require.True(t, ok)
	require.Contains(t, values, got)
	entries, temps := entryFiles(t, c.Dir())
	require.Len(t, entries, 1)
	require.Empty(t, temps)
}

func TestCache_WriteFailuresReturnErrors(t *testing.T) {
	c := newCache(t, 0)
	// A file where the shard folder should be makes the write fail.
	p := c.path("a")
	require.NoError(t, os.WriteFile(filepath.Dir(p), []byte("x"), 0o600))
	require.Error(t, c.Put("a", []byte("v")))

	// A folder where the entry should be makes the rename fail and cleans up.
	q := c.path("b")
	require.NoError(t, os.MkdirAll(filepath.Join(q, "child"), 0o700))
	require.Error(t, c.Put("b", []byte("v")))
	_, temps := entryFiles(t, filepath.Dir(q))
	require.Empty(t, temps)
	_, ok := c.Get("b")
	require.False(t, ok)
}

func TestNewAndDefaultDir(t *testing.T) {
	_, err := New("", 0)
	require.Error(t, err)
	blocked := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(blocked, nil, 0o600))
	_, err = New(filepath.Join(blocked, "thumbs"), 0)
	require.Error(t, err)

	t.Setenv(EnvDir, "/custom/thumbs")
	dir, err := DefaultDir()
	require.NoError(t, err)
	require.Equal(t, "/custom/thumbs", dir)

	t.Setenv(EnvDir, "")
	t.Setenv("XDG_CACHE_HOME", "/xdg")
	t.Setenv("HOME", "/home/someone")
	dir, err = DefaultDir()
	require.NoError(t, err)
	require.True(t, strings.HasSuffix(dir, filepath.Join("nipa", "thumbs")), dir)
}
