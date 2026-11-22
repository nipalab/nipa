package usecase

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

// sizedUploadClient reports its own upload window size and records the bytes
// of every window it receives.
type sizedUploadClient struct {
	window int

	mu      sync.Mutex
	windows []int
}

func (c *sizedUploadClient) UploadWindowBytes() int { return c.window }

func (c *sizedUploadClient) UploadChunks(_ context.Context, _ domain.ChunkScope, chunks []*serverDomain.ChunkData, _ ...func(ch *serverDomain.ChunkData)) (int, int, error) {
	var size int
	for _, ch := range chunks {
		size += len(ch.Data)
	}
	c.mu.Lock()
	c.windows = append(c.windows, size)
	c.mu.Unlock()
	return len(chunks), 0, nil
}

// plainUploadClient has no window size of its own.
type plainUploadClient struct{}

func (plainUploadClient) UploadChunks(context.Context, domain.ChunkScope, []*serverDomain.ChunkData, ...func(ch *serverDomain.ChunkData)) (int, int, error) {
	return 0, 0, nil
}

func TestUploadWindowDefaults(t *testing.T) {
	require.Equal(t, 32<<20, uploadWindowBytes)
	require.Equal(t, 4, uploadWindowWorkers)
	require.Equal(t, 2, uploadWindowQueue)
}

func TestWindowBytesFor(t *testing.T) {
	require.Equal(t, uploadWindowBytes, windowBytesFor(plainUploadClient{}), "clients without an override use the default")
	require.Equal(t, uploadWindowBytes, windowBytesFor(&sizedUploadClient{}), "a zero override keeps the default")
	require.Equal(t, 64<<20, windowBytesFor(&sizedUploadClient{window: 64 << 20}))
}

func TestChunkUploader_UsesTheClientWindowSize(t *testing.T) {
	client := &sizedUploadClient{window: 250}
	uploader := NewChunkUploader(context.Background(), client, domain.ChunkScope{Org: "o", Project: "p"}, nil)
	for i := 0; i < 7; i++ {
		var h serverDomain.Hash
		h[0] = byte(i + 1)
		require.NoError(t, uploader.Add(&serverDomain.ChunkData{Hash: h, Data: make([]byte, 100)}))
	}
	require.NoError(t, uploader.Close())

	client.mu.Lock()
	defer client.mu.Unlock()
	require.ElementsMatch(t, []int{300, 300, 100}, client.windows, "a window is handed over once it reaches the client's size")
}
