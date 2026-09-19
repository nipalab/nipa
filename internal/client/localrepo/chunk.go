package localrepo

import (
	"errors"
	"io"

	serverDomain "github.com/nipalab/nipa/internal/domain"
)

func (l *LocalRepo) StoreChunk(hash serverDomain.Hash, data []byte) error {
	return l.StoreChunks([]*serverDomain.ChunkData{{Hash: hash, Data: data}})
}

func (l *LocalRepo) StoreChunks(chunks []*serverDomain.ChunkData) error {
	if l.db == nil {
		return errors.New("local repo not initialized")
	}
	for _, c := range chunks {
		if err := l.putObject(c.Hash, c.Data); err != nil {
			return err
		}
	}
	return nil
}

func (l *LocalRepo) LoadChunk(hash serverDomain.Hash) ([]byte, error) {
	rc, err := l.OpenChunk(hash)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	return io.ReadAll(rc)
}
