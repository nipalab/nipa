package storage

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/nipalab/nipa/internal/domain"
)

// LocalStore is the default chunk content backend: it writes each chunk to a
// file on the server's local filesystem, sharded by the first two hex digits
// of its hash to keep directories small.
//
//	<root>/<hash[:2]>/<hash[2:]>
type LocalStore struct {
	root string
}

func NewLocalStore(root string) (*LocalStore, error) {
	if root == "" {
		root = "./chunks"
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("storage: create chunk root %s: %w", root, err)
	}
	return &LocalStore{root: root}, nil
}

func (s *LocalStore) Put(ctx context.Context, hash domain.Hash, data []byte) error {
	p := s.path(hash)
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

func (s *LocalStore) Get(ctx context.Context, hash domain.Hash) ([]byte, error) {
	b, err := os.ReadFile(s.path(hash))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, domain.NewErrorNotFound("chunk not found: " + hash.String())
	}
	if err != nil {
		return nil, err
	}
	return b, nil
}

func (s *LocalStore) Exists(ctx context.Context, hash domain.Hash) (bool, error) {
	_, err := os.Stat(s.path(hash))
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

func (s *LocalStore) path(hash domain.Hash) string {
	hex := hash.String()
	return filepath.Join(s.root, hex[:2], hex[2:])
}
