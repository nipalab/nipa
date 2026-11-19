package usecase

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

type recordingUploadClient struct {
	mu     sync.Mutex
	chunks []serverDomain.Hash
	err    error
}

func (r *recordingUploadClient) UploadChunks(_ context.Context, _ domain.ChunkScope, chunks []*serverDomain.ChunkData, onChunk ...func(*serverDomain.ChunkData)) (int, int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return 0, 0, r.err
	}
	for _, ch := range chunks {
		r.chunks = append(r.chunks, ch.Hash)
		if len(onChunk) > 0 && onChunk[0] != nil {
			onChunk[0](ch)
		}
	}
	return len(chunks), 0, nil
}

func TestChunkUploader_UploadsDistinctChunks(t *testing.T) {
	client := &recordingUploadClient{}
	var seen int
	u := NewChunkUploader(context.Background(), client, domain.ChunkScope{Org: "o", Project: "p"}, func(*serverDomain.ChunkData) { seen++ })
	require.NoError(t, u.Add(&serverDomain.ChunkData{Hash: serverDomain.Hash{1}, Data: []byte("a")}))
	require.NoError(t, u.Add(&serverDomain.ChunkData{Hash: serverDomain.Hash{1}, Data: []byte("a")}))
	require.NoError(t, u.Add(&serverDomain.ChunkData{Hash: serverDomain.Hash{2}, Data: []byte("b")}))
	require.NoError(t, u.Close())
	require.ElementsMatch(t, []serverDomain.Hash{{1}, {2}}, client.chunks)
	require.Equal(t, 2, seen)
}

func TestChunkUploader_ErrorsAndAbort(t *testing.T) {
	client := &recordingUploadClient{err: errors.New("refused")}
	u := NewChunkUploader(context.Background(), client, domain.ChunkScope{}, nil)
	require.NoError(t, u.Add(&serverDomain.ChunkData{Hash: serverDomain.Hash{1}}))
	require.EqualError(t, u.Close(), "refused")

	u = NewChunkUploader(context.Background(), &recordingUploadClient{}, domain.ChunkScope{}, nil)
	require.NoError(t, u.Add(&serverDomain.ChunkData{Hash: serverDomain.Hash{1}}))
	u.Abort()
}
