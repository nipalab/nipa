package usecase

import (
	"context"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/storage"
)

// ProjectStore is the chunk store serving one project. External content is
// held outside the server's own storage and is never attributed to a ledger;
// a ReadOnly project may be read but takes no new content or commits.
type ProjectStore struct {
	Store    storage.ChunkStore
	External bool
	ReadOnly bool
}

// ChunkStores resolves the chunk store of a project.
type ChunkStores interface {
	ForProject(ctx context.Context, projectID snow.ID) (ProjectStore, error)
}

type staticStores struct {
	store storage.ChunkStore
}

// StaticStores serves every project from one store.
func StaticStores(store storage.ChunkStore) ChunkStores {
	return staticStores{store: store}
}

func (s staticStores) ForProject(context.Context, snow.ID) (ProjectStore, error) {
	return ProjectStore{Store: s.store}, nil
}

// ErrStorageReadOnly is returned for writes to a project whose own storage is
// no longer entitled.
func ErrStorageReadOnly() *domain.Error {
	return domain.NewErrorQuotaExceeded("own storage requires the Own storage add-on")
}

// storageWriteGate refuses writes to read-only projects.
type storageWriteGate interface {
	RequireWritable(ctx context.Context, projectID snow.ID) error
}

// ProjectChunkReader reads chunk content from the store of the project it is
// asked for.
type ProjectChunkReader struct {
	stores ChunkStores
}

func NewProjectChunkReader(stores ChunkStores) *ProjectChunkReader {
	return &ProjectChunkReader{stores: stores}
}

func (r *ProjectChunkReader) Get(ctx context.Context, projectID snow.ID, hash domain.Hash) ([]byte, error) {
	resolved, err := r.stores.ForProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	return resolved.Store.Get(ctx, projectID, hash)
}

// Prefetch looks up the locations of chunks the caller is about to read one
// by one (a merge reading a file), so a store with packs resolves them in one
// query instead of one per chunk. Other stores ignore it.
func (r *ProjectChunkReader) Prefetch(ctx context.Context, projectID snow.ID, hashes []domain.Hash) error {
	resolved, err := r.stores.ForProject(ctx, projectID)
	if err != nil {
		return err
	}
	if packed, ok := resolved.Store.(*packedStore); ok {
		return packed.Prefetch(ctx, projectID, hashes)
	}
	return nil
}
