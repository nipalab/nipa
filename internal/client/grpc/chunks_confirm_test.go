package grpc

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunkproof"
	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	pb "github.com/nipalab/nipa/internal/grpc/pb"
)

func connectChunkClient(t *testing.T, srv *chunkTestServer) *Client {
	t.Helper()
	addr := startChunkTestServer(t, srv)
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))
	return c
}

func (s *chunkTestServer) confirmLog() []*pb.ConfirmChunkUploadsRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*pb.ConfirmChunkUploadsRequest(nil), s.confirms...)
}

func (s *chunkTestServer) putsFor(hash string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.putAttempts[hash]
}

var testScope = domain.ChunkScope{Org: "org", Project: "proj"}

func TestClient_UploadChunks_OneConfirmPerAttempt(t *testing.T) {
	h1, h2, stored := testChunkHash(0x01), testChunkHash(0x02), testChunkHash(0x03)

	srv := newChunkTestServer()
	srv.link(stored.String())
	srv.put(stored.String(), []byte("cccc"))
	c := connectChunkClient(t, srv)

	uploaded, skipped, err := c.UploadChunks(context.Background(), testScope, []*serverDomain.ChunkData{
		{Hash: h1, Data: []byte("aaaa")},
		{Hash: h2, Data: []byte("bbbb")},
		{Hash: stored, Data: []byte("cccc")},
	})
	require.NoError(t, err)
	require.Equal(t, 2, uploaded)
	require.Equal(t, 1, skipped)

	confirms := srv.confirmLog()
	require.Len(t, confirms, 1, "no claim round: one confirm after the PUTs")
	require.ElementsMatch(t, []string{h1.String(), h2.String(), stored.String()}, confirms[0].GetHashes(),
		"already stored chunks are confirmed too, which clears their GC marks")
	require.Zero(t, srv.putsFor(stored.String()))
}

func TestClient_UploadChunks_StoredMeanwhileIsConfirmed(t *testing.T) {
	h1 := testChunkHash(0x01)
	data := []byte("aaaa")

	srv := newChunkTestServer()
	srv.hookBeforePut = func() { srv.put(h1.String(), data) }
	c := connectChunkClient(t, srv)

	uploaded, skipped, err := c.UploadChunks(context.Background(), testScope, []*serverDomain.ChunkData{{Hash: h1, Data: data}})
	require.NoError(t, err)
	require.Equal(t, 1, uploaded, "a 412 counts as uploaded once the confirm accepts the object")
	require.Zero(t, skipped)
	require.Equal(t, 1, srv.rejectedPuts)

	confirms := srv.confirmLog()
	require.Len(t, confirms, 1)
	require.Equal(t, []string{h1.String()}, confirms[0].GetHashes())
	require.True(t, srv.isLinked(h1.String()))
}

func TestClient_UploadChunks_GarbageBehindAnUploadURLIsUploadedAgain(t *testing.T) {
	h1 := testChunkHash(0x01)
	data := []byte("aaaa")

	srv := newChunkTestServer()
	srv.put(h1.String(), []byte("planted garbage"))
	srv.garbageOnce = map[string]bool{h1.String(): true}
	c := connectChunkClient(t, srv)

	uploaded, skipped, err := c.UploadChunks(context.Background(), testScope, []*serverDomain.ChunkData{{Hash: h1, Data: data}})
	require.NoError(t, err)
	require.Equal(t, 1, uploaded)
	require.Zero(t, skipped)
	require.Equal(t, 2, srv.putsFor(h1.String()), "the first PUT hits the garbage (412), the retry stores the chunk")
	require.Equal(t, 1, srv.rejectedPuts)

	confirms := srv.confirmLog()
	require.Len(t, confirms, 2, "one confirm per attempt")
	require.Equal(t, []string{h1.String()}, confirms[0].GetHashes())
	require.Equal(t, []string{h1.String()}, confirms[1].GetHashes())
	got, _ := srv.storedChunk(h1.String())
	require.Equal(t, data, got)
	require.True(t, srv.isLinked(h1.String()))
}

func TestClient_UploadChunks_GivesUpAfterMaxAttempts(t *testing.T) {
	h1 := testChunkHash(0x01)
	srv := newChunkTestServer()
	srv.confirmMissing = []string{h1.String()}
	c := connectChunkClient(t, srv)

	_, _, err := c.UploadChunks(context.Background(), testScope, []*serverDomain.ChunkData{{Hash: h1, Data: []byte("aaaa")}})
	require.ErrorContains(t, err, "upload incomplete")
	require.Len(t, srv.confirmLog(), maxUploadAttempts)
}

func TestClient_UploadChunks_RefusesAnEmptyUploadURL(t *testing.T) {
	h1 := testChunkHash(0x01)
	srv := newChunkTestServer()
	srv.emptyURLs = true
	c := connectChunkClient(t, srv)

	_, _, err := c.UploadChunks(context.Background(), testScope, []*serverDomain.ChunkData{{Hash: h1, Data: []byte("aaaa")}})
	require.ErrorContains(t, err, "no upload URL")
}

// TestClient_ChunkRPCs_SendTheProtocolVersion checks that every chunk RPC
// carries chunk_protocol, so a server can refuse an older client.
func TestClient_ChunkRPCs_SendTheProtocolVersion(t *testing.T) {
	h1 := testChunkHash(0x01)
	srv := newChunkTestServer()
	c := connectChunkClient(t, srv)

	_, _, err := c.UploadChunks(context.Background(), testScope, []*serverDomain.ChunkData{{Hash: h1, Data: []byte("aaaa")}})
	require.NoError(t, err)
	require.NoError(t, c.DownloadChunks(context.Background(), testScope, []serverDomain.Hash{h1}, func(serverDomain.Hash, []byte) error { return nil }))

	srv.mu.Lock()
	defer srv.mu.Unlock()
	require.Len(t, srv.requestProtocols, 3, "presign, confirm and download")
	for _, version := range srv.requestProtocols {
		require.Equal(t, chunkproof.Protocol, version)
	}
}

// TestClient_ChunkRPCs_RefuseAnOlderServer checks that a server answering
// without the current chunk_protocol (one from before per-project chunks) is
// reported with an upgrade hint instead of being used.
func TestClient_ChunkRPCs_RefuseAnOlderServer(t *testing.T) {
	h1 := testChunkHash(0x01)
	srv := newChunkTestServer()
	srv.oldProtocol = true
	srv.put(h1.String(), []byte("aaaa"))
	c := connectChunkClient(t, srv)

	requireOldServer := func(t *testing.T, err error) {
		t.Helper()
		var domainErr *domain.Error
		require.ErrorAs(t, err, &domainErr)
		require.Equal(t, 409, domainErr.Code)
		require.Contains(t, domainErr.Hint, "upgrade the nipa server")
	}
	_, _, err := c.UploadChunks(context.Background(), testScope, []*serverDomain.ChunkData{{Hash: h1, Data: []byte("aaaa")}})
	requireOldServer(t, err)
	require.Zero(t, srv.putsFor(h1.String()), "nothing is sent to an older server")
	requireOldServer(t, c.DownloadChunks(context.Background(), testScope, []serverDomain.Hash{h1}, func(serverDomain.Hash, []byte) error { return nil }))
	_, err = c.confirmChunks(context.Background(), mustServiceClient(t, c), testScope, []string{h1.String()}, nil)
	requireOldServer(t, err)
}

func mustServiceClient(t *testing.T, c *Client) pb.NipaServiceClient {
	t.Helper()
	client, err := c.transport.NipaServiceClient()
	require.NoError(t, err)
	return client
}

func TestCheckServerChunkProtocol(t *testing.T) {
	require.NoError(t, checkServerChunkProtocol(chunkproof.Protocol))
	require.NoError(t, checkServerChunkProtocol(chunkproof.Protocol+1))
	require.Error(t, checkServerChunkProtocol(0))
}

func TestClient_UploadChunks_BoundsConcurrentConfirms(t *testing.T) {
	srv := newChunkTestServer()
	srv.confirmDelay = 50 * time.Millisecond
	c := connectChunkClient(t, srv)

	const windows = 8
	var wg sync.WaitGroup
	errs := make(chan error, windows)
	for i := 0; i < windows; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			hash := testChunkHash(byte(0x10 + i))
			_, _, err := c.UploadChunks(context.Background(), testScope, []*serverDomain.ChunkData{
				{Hash: hash, Data: []byte(fmt.Sprintf("window-%d", i))},
			})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	srv.mu.Lock()
	peak := srv.confirmsPeak
	srv.mu.Unlock()
	require.LessOrEqual(t, peak, maxConcurrentConfirms)
	require.Greater(t, peak, 1, "overlapping windows confirm concurrently")
}

func TestClient_ConfirmChunks_StopsWaitingWhenTheContextEnds(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	for i := 0; i < maxConcurrentConfirms; i++ {
		c.confirmSlots <- struct{}{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// The RPC client is never reached: every slot is taken.
	_, err := c.confirmChunks(ctx, nil, testScope, []string{testChunkHash(0x01).String()}, nil)
	require.ErrorIs(t, err, context.Canceled)
}

func TestClient_ConfirmChunks_ReleasesTheSlotAfterAnError(t *testing.T) {
	srv := newChunkTestServer()
	srv.confirmErr = fmt.Errorf("boom")
	c := connectChunkClient(t, srv)

	for i := 0; i < maxConcurrentConfirms+1; i++ {
		_, _, err := c.UploadChunks(context.Background(), testScope, []*serverDomain.ChunkData{{Hash: testChunkHash(0x01), Data: []byte("a")}})
		require.Error(t, err)
	}
	require.Empty(t, c.confirmSlots, "every confirm gives its slot back")
}

func TestClient_UploadWindowBytes(t *testing.T) {
	require.Zero(t, NewClient(NewTransport(), nil).UploadWindowBytes(), "no override by default")
	require.Equal(t, 64<<20, NewClient(NewTransport(), nil, WithUploadWindowMiB(64)).UploadWindowBytes())
	require.Zero(t, NewClient(NewTransport(), nil, WithUploadWindowMiB(0)).UploadWindowBytes())
}
