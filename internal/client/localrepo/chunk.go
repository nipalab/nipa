package localrepo

import (
	"context"
	"database/sql"
	"errors"

	serverDomain "github.com/nipalab/nipa/internal/domain"
	sqlcLocalrepo "github.com/nipalab/nipa/internal/repository/sqlc/localrepo"
)

func (l *LocalRepo) StoreChunk(hash serverDomain.Hash, data []byte) error {
	if l.db == nil {
		return errors.New("local repo not initialized")
	}
	ctx := context.Background()
	q := sqlcLocalrepo.New(l.db)
	_, err := q.ChunkUpsertContent(ctx, sqlcLocalrepo.ChunkUpsertContentParams{
		Hash:      hash.Bytes(),
		SizeBytes: int64(len(data)),
		Data:      data,
	})
	return err
}

func (l *LocalRepo) LoadChunk(hash serverDomain.Hash) ([]byte, error) {
	if l.db == nil {
		return nil, errors.New("local repo not initialized")
	}
	ctx := context.Background()
	q := sqlcLocalrepo.New(l.db)
	data, err := q.ChunkGetData(ctx, hash.Bytes())
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("chunk not found in cache")
		}
		return nil, err
	}
	if data == nil {
		return nil, errors.New("chunk content not stored")
	}
	return data, nil
}
