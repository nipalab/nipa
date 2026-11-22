package grpc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	pb "github.com/nipalab/nipa/internal/grpc/pb"
)

func uploadForReceipts(t *testing.T, srv *chunkTestServer, addr string) *pb.ConfirmChunkUploadsRequest {
	t.Helper()
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))
	uploaded, _, err := c.UploadChunks(context.Background(), domain.ChunkScope{Org: "org", Project: "proj"}, []*serverDomain.ChunkData{
		{Hash: testChunkHash(0x01), Data: []byte("aaaa")},
		{Hash: testChunkHash(0x02), Data: []byte("bbbb")},
	})
	require.NoError(t, err)
	require.Equal(t, 2, uploaded)
	srv.mu.Lock()
	defer srv.mu.Unlock()
	require.NotNil(t, srv.lastConfirm)
	require.Len(t, srv.lastConfirm.GetProofs(), 2)
	return srv.lastConfirm
}

func TestClient_UploadChunks_SendsTheProxyReceipt(t *testing.T) {
	srv := newChunkTestServer()
	srv.receipts = true
	addr := startChunkTestServer(t, srv)

	confirm := uploadForReceipts(t, srv, addr)

	for _, proof := range confirm.GetProofs() {
		require.Equal(t, "receipt-"+proof.GetHash(), proof.GetReceipt())
		require.NotEmpty(t, proof.GetProof(), "the proof stays as the fallback")
		require.NotEmpty(t, proof.GetChallenge())
	}
}

func TestClient_UploadChunks_WithoutReceiptsSendsProofsOnly(t *testing.T) {
	srv := newChunkTestServer()
	addr := startChunkTestServer(t, srv)

	confirm := uploadForReceipts(t, srv, addr)

	for _, proof := range confirm.GetProofs() {
		require.Empty(t, proof.GetReceipt(), "an older server sends no receipt")
		require.NotEmpty(t, proof.GetProof())
	}
}

func TestClient_UploadChunks_IgnoresReceiptsFromAbsoluteURLs(t *testing.T) {
	srv := newChunkTestServer()
	srv.receipts = true
	addr := startChunkTestServer(t, srv)
	srv.absoluteBase = "http://" + addr

	confirm := uploadForReceipts(t, srv, addr)

	for _, proof := range confirm.GetProofs() {
		require.Empty(t, proof.GetReceipt(), "an object store URL never carries a server receipt")
	}
}

func TestKeepReceipt(t *testing.T) {
	proof := &pb.ChunkPossessionProof{}
	keepReceipt(chunkTransfer{url: "/api/chunks/o/p/h", proof: proof}, "")
	require.Empty(t, proof.GetReceipt())
	keepReceipt(chunkTransfer{url: "/api/chunks/o/p/h"}, "r")
	keepReceipt(chunkTransfer{url: "https://s3.example.com/k", proof: proof}, "r")
	require.Empty(t, proof.GetReceipt())
	keepReceipt(chunkTransfer{url: "/api/chunks/o/p/h", proof: proof}, "r")
	require.Equal(t, "r", proof.GetReceipt())
}
