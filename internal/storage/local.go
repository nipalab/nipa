package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"go.opentelemetry.io/otel/attribute"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/obs"
	"github.com/nipalab/nipa/internal/snow"
)

// LocalStore is the default chunk content backend: it writes each chunk to a
// file on the server's local filesystem, one directory per project, sharded by
// the first two hex digits of its hash to keep directories small (ChunkKey).
//
//	<root>/p/<project base36>/<hash[:2]>/<hash[2:]>
type LocalStore struct {
	root string
}

var _ ChunkOpener = (*LocalStore)(nil)
var _ ProjectDeleter = (*LocalStore)(nil)

func NewLocalStore(root string) (*LocalStore, error) {
	if root == "" {
		root = "./chunks"
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("storage: create chunk root %s: %w", root, err)
	}
	return &LocalStore{root: root}, nil
}

func (s *LocalStore) Put(ctx context.Context, projectID snow.ID, hash domain.Hash, data []byte) (err error) {
	_, span := obs.StartSpan(ctx, "storage.local.put",
		attribute.String(obs.AttrChunkHash, hash.String()),
		attribute.Int(obs.AttrChunkSize, len(data)),
		attribute.String(obs.AttrBackend, "local"))
	defer func() { obs.FinishSpan(span, err) }()

	p := s.path(projectID, hash)
	if _, err := os.Stat(p); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".nipa-chunk-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return nil
		}
		return err
	}
	return nil
}

func (s *LocalStore) Get(ctx context.Context, projectID snow.ID, hash domain.Hash) (data []byte, err error) {
	_, span := obs.StartSpan(ctx, "storage.local.get",
		attribute.String(obs.AttrChunkHash, hash.String()),
		attribute.String(obs.AttrBackend, "local"))
	defer func() { obs.FinishSpan(span, err) }()

	b, err := os.ReadFile(s.path(projectID, hash))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, domain.NewErrorNotFound("chunk not found: " + hash.String())
	}
	if err != nil {
		return nil, err
	}
	return b, nil
}

// Open streams the chunk file. It implements ChunkOpener.
func (s *LocalStore) Open(ctx context.Context, projectID snow.ID, hash domain.Hash) (_ io.ReadCloser, err error) {
	_, span := obs.StartSpan(ctx, "storage.local.open",
		attribute.String(obs.AttrChunkHash, hash.String()),
		attribute.String(obs.AttrBackend, "local"))
	defer func() { obs.FinishSpan(span, err) }()

	f, err := os.Open(s.path(projectID, hash))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, domain.NewErrorNotFound("chunk not found: " + hash.String())
	}
	if err != nil {
		return nil, err
	}
	return f, nil
}

func (s *LocalStore) Size(ctx context.Context, projectID snow.ID, hash domain.Hash) (size int64, err error) {
	_, span := obs.StartSpan(ctx, "storage.local.size",
		attribute.String(obs.AttrChunkHash, hash.String()),
		attribute.String(obs.AttrBackend, "local"))
	defer func() { obs.FinishSpan(span, err) }()

	info, err := os.Stat(s.path(projectID, hash))
	if errors.Is(err, fs.ErrNotExist) {
		return 0, domain.NewErrorNotFound("chunk not found: " + hash.String())
	}
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

func (s *LocalStore) Exists(ctx context.Context, projectID snow.ID, hash domain.Hash) (bool, error) {
	_, err := os.Stat(s.path(projectID, hash))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *LocalStore) Close() error {
	return nil
}

// DeleteProject removes the project's directory with every chunk in it. It
// implements ProjectDeleter; a project without content is not an error.
func (s *LocalStore) DeleteProject(_ context.Context, projectID snow.ID) error {
	return os.RemoveAll(filepath.Join(s.root, filepath.FromSlash(ProjectPrefix(projectID))))
}

func (s *LocalStore) path(projectID snow.ID, hash domain.Hash) string {
	return filepath.Join(s.root, filepath.FromSlash(ChunkKey(projectID, hash)))
}
