package gitimport

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/grpc"
)

func TestGRPCTransport_ForwardsTheUploadWindow(t *testing.T) {
	type sizer interface{ UploadWindowBytes() int }

	tr, ok := NewGRPCTransport(grpc.NewClient(grpc.NewTransport(), nil, grpc.WithUploadWindowMiB(8))).(sizer)
	require.True(t, ok, "the chunk uploader finds the override through the transport")
	require.Equal(t, 8<<20, tr.UploadWindowBytes())

	plain := NewGRPCTransport(grpc.NewClient(grpc.NewTransport(), nil)).(sizer)
	require.Zero(t, plain.UploadWindowBytes())
}
