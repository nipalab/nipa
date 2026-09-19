package localrepo

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	serverDomain "github.com/nipalab/nipa/internal/domain"
)

func (l *LocalRepo) objectPath(hash serverDomain.Hash) string {
	hex := hash.String()
	return filepath.Join(l.target, ConfigDir, ObjectsDir, hex[:2], hex[2:])
}

func (l *LocalRepo) putObject(hash serverDomain.Hash, data []byte) error {
	p := l.objectPath(hash)
	if _, err := os.Stat(p); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return atomicWrite(p, data, 0o644)
}

func (l *LocalRepo) OpenChunk(hash serverDomain.Hash) (io.ReadCloser, error) {
	if l.db == nil {
		return nil, errors.New("local repo not initialized")
	}
	f, err := os.Open(l.objectPath(hash))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, errors.New("chunk not found in cache")
	}
	if err != nil {
		return nil, err
	}
	return f, nil
}
