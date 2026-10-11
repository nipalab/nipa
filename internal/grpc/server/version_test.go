package server

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/version"
)

func TestGetServerVersion(t *testing.T) {
	srv := New(nil)

	res, err := srv.GetServerVersion(context.Background(), &pb.GetServerVersionRequest{})
	require.NoError(t, err)

	want := version.Resolve()
	assert.Equal(t, want.Version, res.GetVersion())
	assert.Equal(t, want.Revision, res.GetRevision())
	assert.Equal(t, want.GoVersion, res.GetGoVersion())
}
