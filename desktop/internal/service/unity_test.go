package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
)

type metaDaemon struct {
	*blobDaemon
	warnings []*daemonpb.MetaWarning
	err      error
}

func (d *metaDaemon) MetaWarnings(_ context.Context, req *daemonpb.MetaWarningsRequest, _ ...grpc.CallOption) (*daemonpb.MetaWarningsResponse, error) {
	if d.err != nil {
		return nil, d.err
	}
	return &daemonpb.MetaWarningsResponse{Warnings: d.warnings}, nil
}

func TestService_MetaWarnings(t *testing.T) {
	d := &metaDaemon{blobDaemon: &blobDaemon{opsDaemon: &opsDaemon{fakeDaemon: &fakeDaemon{watchErr: map[string]error{}, changes: make(chan *daemonpb.RepoChange, 4)}}}}
	svc := New(func(context.Context) (Session, error) { return &opsSession{client: d}, nil }, &memStore{}, nil)
	t.Cleanup(func() { _ = svc.ServiceShutdown() })

	d.warnings = []*daemonpb.MetaWarning{{Path: "Assets/a.meta", Kind: "guid-changed", Related: "Assets/a", Message: "GUID changed"}}
	out, err := svc.MetaWarnings(ctx, "/work/game")
	require.NoError(t, err)
	assert.Equal(t, []MetaWarning{{Path: "Assets/a.meta", Kind: "guid-changed", Related: "Assets/a", Message: "GUID changed"}}, out)

	d.warnings = nil
	out, err = svc.MetaWarnings(ctx, "/work/game")
	require.NoError(t, err)
	assert.NotNil(t, out)
	assert.Empty(t, out)

	d.err = status.Error(codes.Internal, "boom")
	_, err = svc.MetaWarnings(ctx, "/work/game")
	assert.ErrorContains(t, err, "boom")
}
