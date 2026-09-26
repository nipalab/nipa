package daemon

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/nipalab/nipa/internal/client/localrepo"
)

// defaultWatchWindow is the debounce window coalescing watcher events into one
// reconciliation batch.
const defaultWatchWindow = 300 * time.Millisecond

// eventSource is the OS notification backend behind the watcher. Tests inject
// fake sources; production uses fsnotify.
type eventSource interface {
	events() <-chan string // repository-relative slash paths
	errors() <-chan error
	Close() error
}

// watcher debounces source events into sorted dirty-path batches. It is a pure
// accelerator: dropped events only cost the next status a stat walk, never
// correctness.
type watcher struct {
	src    eventSource
	window time.Duration

	updatesC chan []string
	errsC    chan error

	stop      chan struct{}
	done      chan struct{}
	closeOnce sync.Once

	dirty map[string]struct{}
}

func newWatcher(src eventSource, window time.Duration) *watcher {
	w := &watcher{
		src:      src,
		window:   window,
		updatesC: make(chan []string, 16),
		errsC:    make(chan error, 16),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
		dirty:    map[string]struct{}{},
	}
	go w.run()
	return w
}

func (w *watcher) updates() <-chan []string {
	return w.updatesC
}

func (w *watcher) errors() <-chan error {
	return w.errsC
}

// Close stops the watcher and its source. Idempotent.
func (w *watcher) Close() error {
	w.closeOnce.Do(func() {
		close(w.stop)
		_ = w.src.Close()
	})
	<-w.done
	return nil
}

func (w *watcher) run() {
	defer close(w.done)
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()

	for {
		select {
		case <-w.stop:
			return
		case path, ok := <-w.src.events():
			if !ok {
				return
			}
			w.dirty[path] = struct{}{}
			if len(w.dirty) == 1 {
				timer.Reset(w.window)
			}
		case err, ok := <-w.src.errors():
			if !ok {
				return
			}
			select {
			case w.errsC <- err:
			default:
			}
		case <-timer.C:
			paths := make([]string, 0, len(w.dirty))
			for p := range w.dirty {
				paths = append(paths, p)
			}
			sort.Strings(paths)
			w.dirty = map[string]struct{}{}
			select {
			case w.updatesC <- paths:
			case <-w.stop:
				return
			}
		}
	}
}

// fsnotifySource watches root recursively, skipping .nipa. Watches are added
// for new directories and dropped for removed or renamed ones.
type fsnotifySource struct {
	root    string
	watcher *fsnotify.Watcher
	eventsC chan string
	errsC   chan error
	stop    chan struct{}
	done    chan struct{}

	mu      sync.Mutex
	watched map[string]bool
}

func newFsnotifySource(root string) (*fsnotifySource, error) {
	backend, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	s := &fsnotifySource{
		root:    root,
		watcher: backend,
		eventsC: make(chan string, 256),
		errsC:   make(chan error, 16),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
		watched: map[string]bool{},
	}
	if err := s.addRecursive(root); err != nil {
		_ = backend.Close()
		return nil, err
	}
	go s.run()
	return s, nil
}

func (s *fsnotifySource) events() <-chan string {
	return s.eventsC
}

func (s *fsnotifySource) errors() <-chan error {
	return s.errsC
}

func (s *fsnotifySource) Close() error {
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
	err := s.watcher.Close()
	<-s.done
	return err
}

func (s *fsnotifySource) run() {
	defer close(s.done)
	for {
		select {
		case <-s.stop:
			return
		case event, ok := <-s.watcher.Events:
			if !ok {
				return
			}
			s.handle(event)
		case err, ok := <-s.watcher.Errors:
			if !ok {
				return
			}
			if err != nil {
				select {
				case s.errsC <- err:
				case <-s.stop:
					return
				}
			}
		}
	}
}

func (s *fsnotifySource) handle(event fsnotify.Event) {
	rel, err := filepath.Rel(s.root, event.Name)
	if err != nil || rel == "." {
		return
	}
	rel = filepath.ToSlash(rel)
	if rel == localrepo.ConfigDir || strings.HasPrefix(rel, localrepo.ConfigDir+"/") {
		return
	}

	switch {
	case event.Op&fsnotify.Create != 0:
		if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
			_ = s.addRecursive(event.Name)
		}
		s.emit(rel)
	case event.Op&fsnotify.Rename != 0:
		if s.removeExact(event.Name) {
			s.pruneDescendants(event.Name)
		}
		s.emit(rel)
	case event.Op&fsnotify.Remove != 0:
		s.removeExact(event.Name)
		s.emit(rel)
	default: // Write, Chmod
		s.emit(rel)
	}
}

func (s *fsnotifySource) emit(rel string) {
	select {
	case s.eventsC <- rel:
	case <-s.stop:
	}
}

func (s *fsnotifySource) addRecursive(dir string) error {
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if d.Name() == localrepo.ConfigDir {
			return filepath.SkipDir
		}
		return s.add(path)
	})
}

func (s *fsnotifySource) add(dir string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.watched[dir] {
		return nil
	}
	if err := s.watcher.Add(dir); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	s.watched[dir] = true
	return nil
}

// removeExact drops the watch for dir, reporting whether it was watched.
func (s *fsnotifySource) removeExact(dir string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.watched[dir] {
		return false
	}
	delete(s.watched, dir)
	_ = s.watcher.Remove(dir)
	return true
}

// pruneDescendants drops the watches under a renamed directory. The scan only
// runs once per renamed watched directory; plain removals delete each watched
// directory as its own remove event arrives.
func (s *fsnotifySource) pruneDescendants(dir string) {
	prefix := dir + string(filepath.Separator)
	s.mu.Lock()
	defer s.mu.Unlock()
	for watched := range s.watched {
		if strings.HasPrefix(watched, prefix) {
			delete(s.watched, watched)
			_ = s.watcher.Remove(watched)
		}
	}
}
