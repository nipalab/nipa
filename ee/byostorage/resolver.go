package byostorage

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/storage"
	"github.com/nipalab/nipa/internal/usecase"
)

const (
	// ResolverTTL bounds how long a project's server row is reused, so key
	// edits reach every server process.
	ResolverTTL       = 60 * time.Second
	maxCachedProjects = 10000
)

var _ usecase.ChunkStores = (*Resolver)(nil)
var _ usecase.ProjectStorageAssigner = (*Service)(nil)

type projectServers interface {
	ProjectServer(ctx context.Context, projectID snow.ID) (*domain.StorageServer, error)
}

type projectEntry struct {
	server  *domain.StorageServer
	fetched time.Time
}

type storeEntry struct {
	updatedAt time.Time
	store     storage.ChunkStore
}

// Resolver serves each project from its assigned server, or from the
// server's own store when it has none. Opened stores are kept per server and
// replaced when the server row changes.
type Resolver struct {
	svc      *Service
	repo     projectServers
	fallback storage.ChunkStore
	ttl      time.Duration
	now      func() time.Time

	mu       sync.Mutex
	projects map[snow.ID]projectEntry
	stores   map[snow.ID]storeEntry
}

// Resolver returns the per-project store resolver over fallback, the store
// of projects without an own server.
func (s *Service) Resolver(fallback storage.ChunkStore) *Resolver {
	return &Resolver{
		svc:      s,
		repo:     s.repo,
		fallback: fallback,
		ttl:      ResolverTTL,
		now:      time.Now,
		projects: map[snow.ID]projectEntry{},
		stores:   map[snow.ID]storeEntry{},
	}
}

func (r *Resolver) ForProject(ctx context.Context, projectID snow.ID) (usecase.ProjectStore, error) {
	server, err := r.projectServer(ctx, projectID)
	if err != nil {
		return usecase.ProjectStore{}, err
	}
	if server == nil {
		return usecase.ProjectStore{Store: r.fallback}, nil
	}
	store, err := r.store(ctx, server)
	if err != nil {
		return usecase.ProjectStore{}, err
	}
	active, err := entitled(ctx, r.svc.entitlement, server.OwnerUserID)
	if err != nil {
		return usecase.ProjectStore{}, err
	}
	return usecase.ProjectStore{Store: store, External: true, ReadOnly: !active}, nil
}

func (r *Resolver) projectServer(ctx context.Context, projectID snow.ID) (*domain.StorageServer, error) {
	now := r.now()
	r.mu.Lock()
	entry, ok := r.projects[projectID]
	r.mu.Unlock()
	if ok && now.Sub(entry.fetched) < r.ttl {
		return entry.server, nil
	}
	server, err := r.repo.ProjectServer(ctx, projectID)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.projects) >= maxCachedProjects {
		for id, cached := range r.projects {
			if now.Sub(cached.fetched) >= r.ttl {
				delete(r.projects, id)
			}
		}
	}
	r.projects[projectID] = projectEntry{server: server, fetched: now}
	return server, nil
}

func (r *Resolver) store(ctx context.Context, server *domain.StorageServer) (storage.ChunkStore, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cached, ok := r.stores[server.ID]; ok && cached.updatedAt.Equal(server.UpdatedAt) {
		return cached.store, nil
	}
	secret, err := r.svc.openSecret(server)
	if err != nil {
		return nil, unavailable(server, err)
	}
	store, err := r.svc.open(ctx, r.svc.storeConfig(*server, secret))
	if err != nil {
		return nil, unavailable(server, err)
	}
	if previous, ok := r.stores[server.ID]; ok {
		_ = previous.store.Close()
	}
	r.stores[server.ID] = storeEntry{updatedAt: server.UpdatedAt, store: store}
	return store, nil
}

func unavailable(server *domain.StorageServer, err error) error {
	slog.Warn("storage server unavailable", "storage_server_id", server.ID.Base36(), "err", err)
	return &domain.Error{
		Code:            502,
		Message:         "storage server unavailable",
		InternalMessage: err.Error(),
		Cause:           err,
	}
}
