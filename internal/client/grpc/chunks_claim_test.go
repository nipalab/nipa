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

func proofHashes(req *pb.ConfirmChunkUploadsRequest) []string {
	hashes := make([]string, 0, len(req.GetProofs()))
	for _, proof := range req.GetProofs() {
		hashes = append(hashes, proof.GetHash())
	}
	return hashes
}

var testScope = domain.ChunkScope{Org: "org", Project: "proj"}

func TestClient_UploadChunks_ClaimsOnlyChunksWithoutAnUploadURL(t *testing.T) {
	shared, fresh, linked := testChunkHash(0x01), testChunkHash(0x02), testChunkHash(0x03)
	sharedData, freshData := []byte("shared"), []byte("fresh")

	srv := newChunkTestServer()
	srv.put(shared.String(), sharedData) // held for another project: no URL
	srv.put(linked.String(), []byte("linked"))
	srv.link(linked.String())
	c := connectChunkClient(t, srv)

	uploaded, skipped, err := c.UploadChunks(context.Background(), testScope, []*serverDomain.ChunkData{
		{Hash: shared, Data: sharedData},
		{Hash: fresh, Data: freshData},
		{Hash: linked, Data: []byte("linked")},
	})
	require.NoError(t, err)
	require.Equal(t, 1, uploaded)
	require.Equal(t, 2, skipped)

	confirms := srv.confirmLog()
	require.Len(t, confirms, 2, "one claim round, one final confirm")
	claim := confirms[0]
	require.Equal(t, []string{shared.String()}, claim.GetHashes(), "only the chunk without an upload URL is claimed")
	require.Equal(t, []string{shared.String()}, proofHashes(claim))
	require.Equal(t, chunkproof.Proof("challenge-"+shared.String(), sharedData), claim.GetProofs()[0].GetProof())

	final := confirms[1]
	require.ElementsMatch(t, []string{shared.String(), fresh.String(), linked.String()}, final.GetHashes())
	require.Equal(t, []string{fresh.String()}, proofHashes(final), "the uploaded chunk is confirmed with its proof")
	require.Equal(t, chunkproof.Proof("challenge-"+fresh.String(), freshData), final.GetProofs()[0].GetProof())

	require.Equal(t, 1, srv.putsFor(fresh.String()))
	require.Zero(t, srv.putsFor(shared.String()), "claimed content is never sent")
	require.True(t, srv.isLinked(shared.String()))
	require.True(t, srv.isLinked(fresh.String()))
}

func TestClient_UploadChunks_NoClaimRoundWhenEveryChunkGetsAURL(t *testing.T) {
	h1, h2 := testChunkHash(0x01), testChunkHash(0x02)

	srv := newChunkTestServer()
	c := connectChunkClient(t, srv)

	uploaded, skipped, err := c.UploadChunks(context.Background(), testScope, []*serverDomain.ChunkData{
		{Hash: h1, Data: []byte("aaaa")},
		{Hash: h2, Data: []byte("bbbb")},
	})
	require.NoError(t, err)
	require.Equal(t, 2, uploaded)
	require.Zero(t, skipped)

	confirms := srv.confirmLog()
	require.Len(t, confirms, 1, "new chunks are not claimed before the upload")
	require.ElementsMatch(t, []string{h1.String(), h2.String()}, proofHashes(confirms[0]))
}

func TestClient_UploadChunks_StoredMeanwhileIsConfirmedWithItsProof(t *testing.T) {
	h1 := testChunkHash(0x01)
	data := []byte("aaaa")

	srv := newChunkTestServer()
	srv.hookBeforePut = func() { srv.put(h1.String(), data) }
	c := connectChunkClient(t, srv)

	uploaded, skipped, err := c.UploadChunks(context.Background(), testScope, []*serverDomain.ChunkData{{Hash: h1, Data: data}})
	require.NoError(t, err)
	require.Equal(t, 1, uploaded, "a 412 counts as uploaded once the confirm accepts the proof")
	require.Zero(t, skipped)
	require.Equal(t, 1, srv.rejectedPuts)

	confirms := srv.confirmLog()
	require.Len(t, confirms, 1)
	require.Equal(t, []string{h1.String()}, proofHashes(confirms[0]))
	require.True(t, srv.isLinked(h1.String()))
}

func TestClient_UploadChunks_GarbageBehindAnUploadURLIsUploadedAgain(t *testing.T) {
	h1 := testChunkHash(0x01)
	data := []byte("aaaa")

	srv := newChunkTestServer()
	srv.urlForStored = true
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
	require.Len(t, confirms, 2, "one final confirm per attempt, no claim rounds")
	require.Equal(t, []string{h1.String()}, proofHashes(confirms[0]))
	require.Equal(t, []string{h1.String()}, proofHashes(confirms[1]))
	got, _ := srv.storedChunk(h1.String())
	require.Equal(t, data, got)
	require.True(t, srv.isLinked(h1.String()))
}

func TestClient_UploadChunks_GarbageBehindAClaimIsReportedMissingAndUploaded(t *testing.T) {
	h1 := testChunkHash(0x01)
	data := []byte("aaaa")

	srv := newChunkTestServer()
	srv.put(h1.String(), []byte("planted garbage"))
	srv.garbageOnce = map[string]bool{h1.String(): true}
	c := connectChunkClient(t, srv)

	uploaded, _, err := c.UploadChunks(context.Background(), testScope, []*serverDomain.ChunkData{{Hash: h1, Data: data}})
	require.NoError(t, err)
	require.Equal(t, 1, uploaded)

	confirms := srv.confirmLog()
	require.GreaterOrEqual(t, len(confirms), 2)
	require.Equal(t, []string{h1.String()}, confirms[0].GetHashes(), "the first attempt claims the stored object")
	require.Equal(t, 1, srv.putsFor(h1.String()), "the next attempt gets a URL and uploads")
	last := confirms[len(confirms)-1]
	require.Equal(t, []string{h1.String()}, proofHashes(last))
	got, _ := srv.storedChunk(h1.String())
	require.Equal(t, data, got)
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
