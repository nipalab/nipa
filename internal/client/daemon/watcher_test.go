package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/localrepo"
)

// fakeSource drives the watcher deterministically in tests.
type fakeSource struct {
	eventsC chan string
	errsC   chan error

	mu     sync.Mutex
	closed bool
}

func newFakeSource() *fakeSource {
	return &fakeSource{
		eventsC: make(chan string, 32),
		errsC:   make(chan error, 32),
	}
}

func (f *fakeSource) events() <-chan string { return f.eventsC }
func (f *fakeSource) errors() <-chan error  { return f.errsC }

func (f *fakeSource) emit(path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return
	}
	f.eventsC <- path
}

func (f *fakeSource) fail(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return
	}
	f.errsC <- err
}

func (f *fakeSource) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func (f *fakeSource) closeEvents() {
	f.mu.Lock()
	defer f.mu.Unlock()
	close(f.eventsC)
}

func (f *fakeSource) closeErrors() {
	f.mu.Lock()
	defer f.mu.Unlock()
	close(f.errsC)
}

func recvBatch(t *testing.T, ch <-chan []string) []string {
	t.Helper()
	select {
	case batch := <-ch:
		return batch
	case <-time.After(2 * time.Second):
		t.Fatal("no watcher batch arrived")
		return nil
	}
}

func recvEvent(t *testing.T, ch <-chan string) string {
	t.Helper()
	select {
	case event := <-ch:
		return event
	case <-time.After(2 * time.Second):
		t.Fatal("no watcher event arrived")
		return ""
	}
}

func TestWatcher_DebouncesIntoOneBatch(t *testing.T) {
	src := newFakeSource()
	w := newWatcher(src, 20*time.Millisecond)
	t.Cleanup(func() { _ = w.Close() })

	src.emit("b/c.txt")
	src.emit("a.txt")
	src.emit("a.txt")

	require.Equal(t, []string{"a.txt", "b/c.txt"}, recvBatch(t, w.updates()),
		"the burst must coalesce into one sorted, deduplicated batch")
}

func TestWatcher_SeparatesQuietBatches(t *testing.T) {
	src := newFakeSource()
	w := newWatcher(src, 20*time.Millisecond)
	t.Cleanup(func() { _ = w.Close() })

	src.emit("a.txt")
	require.Equal(t, []string{"a.txt"}, recvBatch(t, w.updates()))

	src.emit("b.txt")
	require.Equal(t, []string{"b.txt"}, recvBatch(t, w.updates()))
}

func TestWatcher_ForwardsErrors(t *testing.T) {
	src := newFakeSource()
	w := newWatcher(src, 20*time.Millisecond)
	t.Cleanup(func() { _ = w.Close() })

	want := errors.New("queue overflow")
	src.fail(want)
	select {
	case err := <-w.errors():
		require.ErrorIs(t, err, want)
	case <-time.After(2 * time.Second):
		t.Fatal("no watcher error forwarded")
	}
}

func TestWatcher_CloseIsIdempotent(t *testing.T) {
	src := newFakeSource()
	w := newWatcher(src, 20*time.Millisecond)
	src.emit("a.txt")
	require.NoError(t, w.Close())
	require.NoError(t, w.Close())
}

func TestWatcher_StopsWhenSourceEventsClose(t *testing.T) {
	src := newFakeSource()
	w := newWatcher(src, 20*time.Millisecond)
	src.closeEvents()
	require.NoError(t, w.Close())
}

func TestWatcher_StopsWhenSourceErrorsClose(t *testing.T) {
	src := newFakeSource()
	w := newWatcher(src, 20*time.Millisecond)
	src.closeErrors()
	require.NoError(t, w.Close())
}

func TestFsnotifySource_RenamePrunesAndRemoveEmits(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "old", "deep"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "old", "deep", "f.txt"), []byte("x"), 0o644))

	src, err := newFsnotifySource(root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = src.Close() })

	oldDeep := filepath.Join(root, "old", "deep")
	require.Eventually(t, func() bool {
		src.mu.Lock()
		defer src.mu.Unlock()
		return src.watched[oldDeep]
	}, 2*time.Second, 10*time.Millisecond)

	require.NoError(t, os.Rename(filepath.Join(root, "old"), filepath.Join(root, "new")))
	require.Eventually(t, func() bool {
		src.mu.Lock()
		defer src.mu.Unlock()
		return !src.watched[filepath.Join(root, "old")] && !src.watched[oldDeep]
	}, 2*time.Second, 10*time.Millisecond, "renaming a watched directory prunes its watches")

	// Removing a file emits an event even though no watch is attached to it.
	remove := filepath.Join(root, "gone.txt")
	require.NoError(t, os.WriteFile(remove, []byte("x"), 0o644))
	for {
		if event := recvEvent(t, src.events()); event == "gone.txt" {
			break
		}
	}
	require.NoError(t, os.Remove(remove))
	for {
		if event := recvEvent(t, src.events()); event == "gone.txt" {
			break
		}
	}
}

func TestFsnotifySource_AddMissingIsNoop(t *testing.T) {
	src, err := newFsnotifySource(t.TempDir())
	require.NoError(t, err)
	defer func() { _ = src.Close() }()

	require.NoError(t, src.add(filepath.Join(t.TempDir(), "missing")))
}

func TestFsnotifySource_StopsWhenBackendCloses(t *testing.T) {
	src, err := newFsnotifySource(t.TempDir())
	require.NoError(t, err)

	require.NoError(t, src.watcher.Close())
	select {
	case <-src.done:
	case <-time.After(2 * time.Second):
		t.Fatal("the source did not stop when its backend closed")
	}
	_ = src.Close()
}

func TestFsnotifySource_WatchesRecursivelySkipsNipa(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "assets"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, localrepo.ConfigDir), 0o755))

	src, err := newFsnotifySource(root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = src.Close() })

	require.NoError(t, os.WriteFile(filepath.Join(root, "assets", "a.txt"), []byte("x"), 0o644))
	require.Equal(t, "assets/a.txt", recvEvent(t, src.events()))

	// Directories created after the source started are picked up. Keep
	// writing until the recursive watch reports the event: the first writes
	// can land before the directory watch is registered.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "new", "deep"), 0o755))
	require.Eventually(t, func() bool {
		_ = os.WriteFile(filepath.Join(root, "new", "deep", "b.txt"), []byte("x"), 0o644)
		for {
			select {
			case event := <-src.events():
				if event == "new/deep/b.txt" {
					return true
				}
			case <-time.After(50 * time.Millisecond):
				return false
			}
		}
	}, 5*time.Second, 20*time.Millisecond)

	// Writes inside .nipa are never emitted.
	require.NoError(t, os.WriteFile(filepath.Join(root, localrepo.ConfigDir, "ignored.txt"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "marker.txt"), []byte("x"), 0o644))
	for {
		event := recvEvent(t, src.events())
		require.NotContains(t, event, localrepo.ConfigDir)
		if event == "marker.txt" {
			break
		}
	}
}
