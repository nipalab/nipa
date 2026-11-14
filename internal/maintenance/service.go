package maintenance

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nipalab/nipa/internal/snow"
)

type Scope string

const (
	ScopeServer  Scope = "server"
	ScopeProject Scope = "project"
)

const (
	SourceDB   = "db"
	SourceFile = "file"
)

// Snapshot is the immutable maintenance state requests read. ByID holds the
// stored windows only; a file override replaces Server but never enters ByID.
type Snapshot struct {
	Server    *Window
	Source    string
	Projects  map[string]*Window
	ByID      map[snow.ID]*Window
	LoadedAt  time.Time
	FileError string
}

// NewSnapshot indexes stored windows: the one without a project is the server
// window, the others are keyed by "org/project".
func NewSnapshot(windows []Window, loadedAt time.Time) *Snapshot {
	snap := &Snapshot{
		Source:   SourceDB,
		Projects: make(map[string]*Window, len(windows)),
		ByID:     make(map[snow.ID]*Window, len(windows)),
		LoadedAt: loadedAt,
	}
	for i := range windows {
		w := &windows[i]
		snap.ByID[w.ID] = w
		if key := w.Key(); key != "" {
			snap.Projects[key] = w
		} else {
			snap.Server = w
		}
	}
	return snap
}

// Windows returns the stored windows of the snapshot.
func (s *Snapshot) Windows() []Window {
	out := make([]Window, 0, len(s.ByID))
	for _, w := range s.ByID {
		out = append(out, *w)
	}
	return out
}

func (s *Snapshot) frozen(now time.Time) bool {
	return s.Server.Active(now) && s.Server.Mode == Frozen
}

type Service struct {
	mu    sync.Mutex
	snap  atomic.Pointer[Snapshot]
	clock func() time.Time
	hooks []func(context.Context) error
}

func NewService() *Service {
	s := &Service{clock: time.Now}
	s.snap.Store(NewSnapshot(nil, time.Time{}))
	return s
}

func (s *Service) WithClock(clock func() time.Time) *Service {
	s.clock = clock
	return s
}

func (s *Service) Now() time.Time {
	return s.clock()
}

func (s *Service) Snapshot() *Snapshot {
	return s.snap.Load()
}

// For returns the effective window of a request: the stricter of the active
// server window and the active window of org/project. Without one the window
// mode is Off and the scope empty.
func (s *Service) For(org, project string) (Window, Scope) {
	snap := s.snap.Load()
	now := s.clock()
	best, scope := Window{Mode: Off}, Scope("")
	if snap.Server.Active(now) {
		best, scope = *snap.Server, ScopeServer
	}
	if org == "" || project == "" {
		return best, scope
	}
	if w := snap.Projects[ProjectKey(org, project)]; w.Active(now) && w.Mode.rank() > best.Mode.rank() {
		best, scope = *w, ScopeProject
	}
	return best, scope
}

func (s *Service) Frozen() bool {
	return s.snap.Load().frozen(s.clock())
}

// OnResume registers a hook that must succeed before a snapshot that leaves
// frozen mode is published.
func (s *Service) OnResume(hook func(context.Context) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hooks = append(s.hooks, hook)
}

// Apply publishes snap. When it would leave frozen mode the resume hooks run
// first; if one fails the frozen snapshot stays and the error is returned.
func (s *Service) Apply(ctx context.Context, snap *Snapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock()
	if s.snap.Load().frozen(now) && !snap.frozen(now) {
		for _, hook := range s.hooks {
			if err := hook(ctx); err != nil {
				return fmt.Errorf("resume after frozen maintenance: %w", err)
			}
		}
	}
	s.snap.Store(snap)
	return nil
}
