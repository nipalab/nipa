package issuetracker

import (
	"errors"
	"fmt"
	"slices"
	"sync"
)

var ErrUnknownKind = errors.New("unknown issue tracker kind")

var (
	registryMu sync.RWMutex
	factories  = map[string]Factory{}
)

// Register makes a tracker kind available to New. It panics on an empty kind,
// a nil factory or a duplicate registration, like database/sql drivers.
func Register(kind string, f Factory) {
	if kind == "" || f == nil {
		panic("issuetracker: Register needs a kind and a factory")
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, dup := factories[kind]; dup {
		panic("issuetracker: Register called twice for " + kind)
	}
	factories[kind] = f
}

func New(kind string, cfg Config) (Tracker, error) {
	registryMu.RLock()
	f, ok := factories[kind]
	registryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w %q", ErrUnknownKind, kind)
	}
	return f(cfg)
}

// Kinds lists the registered kinds, sorted.
func Kinds() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	kinds := make([]string, 0, len(factories))
	for kind := range factories {
		kinds = append(kinds, kind)
	}
	slices.Sort(kinds)
	return kinds
}
