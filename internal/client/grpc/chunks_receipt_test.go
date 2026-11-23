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
	require.Len(t, srv.lastConfirm.GetHashes(), 2)
	return srv.lastConfirm
}

func TestClient_UploadChunks_SendsTheProxyReceipt(t *testing.T) {
	srv := newChunkTestServer()
	srv.receipts = true
	addr := startChunkTestServer(t, srv)

	confirm := uploadForReceipts(t, srv, addr)

	require.Len(t, confirm.GetReceipts(), 2)
	for _, receipt := range confirm.GetReceipts() {
		require.Equal(t, "receipt-"+receipt.GetHash(), receipt.GetReceipt())
	}
}

func TestClient_UploadChunks_WithoutReceiptsSendsHashesOnly(t *testing.T) {
	srv := newChunkTestServer()
	addr := startChunkTestServer(t, srv)

	confirm := uploadForReceipts(t, srv, addr)

	require.Empty(t, confirm.GetReceipts(), "the server reads direct uploads back")
}

func TestClient_UploadChunks_IgnoresReceiptsFromAbsoluteURLs(t *testing.T) {
	srv := newChunkTestServer()
	srv.receipts = true
	addr := startChunkTestServer(t, srv)
	srv.absoluteBase = "http://" + addr

	confirm := uploadForReceipts(t, srv, addr)

	require.Empty(t, confirm.GetReceipts(), "an object store URL never carries a server receipt")
}

func TestKeepReceipt(t *testing.T) {
	receipt := new(string)
	keepReceipt(chunkTransfer{url: "/api/chunks/o/p/h", receipt: receipt}, "")
	require.Empty(t, *receipt)
	keepReceipt(chunkTransfer{url: "/api/chunks/o/p/h"}, "r")
	keepReceipt(chunkTransfer{url: "https://s3.example.com/k", receipt: receipt}, "r")
	require.Empty(t, *receipt)
	keepReceipt(chunkTransfer{url: "/api/chunks/o/p/h", receipt: receipt}, "r")
	require.Equal(t, "r", *receipt)
}
