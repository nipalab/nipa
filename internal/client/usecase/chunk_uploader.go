package usecase

import (
	"context"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

type ChunkUploadClient interface {
	UploadChunks(ctx context.Context, scope domain.ChunkScope, chunks []*serverDomain.ChunkData, onChunk ...func(ch *serverDomain.ChunkData)) (int, int, error)
}

type ChunkUploader struct {
	u *chunkUploader
}

func NewChunkUploader(ctx context.Context, client ChunkUploadClient, scope domain.ChunkScope, onChunk func(*serverDomain.ChunkData)) *ChunkUploader {
	return &ChunkUploader{u: newChunkUploader(ctx, client, nil, scope, onChunk)}
}

func (c *ChunkUploader) Add(ch *serverDomain.ChunkData) error {
	return c.u.add(ch, false)
}

func (c *ChunkUploader) Close() error {
	return c.u.close()
}

func (c *ChunkUploader) Abort() {
	c.u.abort()
}
