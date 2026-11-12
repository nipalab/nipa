package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/client/usecase"
)

type storageDaemon struct {
	*blobDaemon
	usage    *daemonpb.DiskUsageResponse
	gc       *daemonpb.GCResponse
	err      error
	usageFor []string
	gcFor    []string
}

func (d *storageDaemon) DiskUsage(_ context.Context, req *daemonpb.DiskUsageRequest, _ ...grpc.CallOption) (*daemonpb.DiskUsageResponse, error) {
	d.usageFor = append(d.usageFor, req.GetRoot())
	if d.err != nil {
		return nil, d.err
	}
	return d.usage, nil
}

func (d *storageDaemon) GC(_ context.Context, req *daemonpb.GCRequest, _ ...grpc.CallOption) (*daemonpb.GCResponse, error) {
	d.gcFor = append(d.gcFor, req.GetRoot())
	if d.err != nil {
		return nil, d.err
	}
	return d.gc, nil
}

func newStorageService(t *testing.T) (*Service, *storageDaemon) {
	t.Helper()
	d := &storageDaemon{blobDaemon: &blobDaemon{opsDaemon: &opsDaemon{fakeDaemon: &fakeDaemon{watchErr: map[string]error{}, changes: make(chan *daemonpb.RepoChange, 4)}}}}
	svc := New(func(context.Context) (Session, error) { return &opsSession{client: d}, nil }, &memStore{}, nil)
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	return svc, d
}

func TestService_DiskUsage(t *testing.T) {
	svc, d := newStorageService(t)
	d.usage = &daemonpb.DiskUsageResponse{WorkingFiles: 3, WorkingBytes: 4096, CacheObjects: 7, CacheBytes: 2048, PinnedPaths: []string{"art/hero.psd"}, Sparse: []string{"code"}}
	out, err := svc.DiskUsage(ctx, "/work/game")
	require.NoError(t, err)
	assert.Equal(t, []string{"/work/game"}, d.usageFor)
	assert.Equal(t, DiskUsage{WorkingFiles: 3, WorkingBytes: 4096, CacheObjects: 7, CacheBytes: 2048, Pins: []string{"art/hero.psd"}, Sparse: []string{"code"}}, out)

	d.usage = &daemonpb.DiskUsageResponse{}
	out, err = svc.DiskUsage(ctx, "/work/game")
	require.NoError(t, err)
	assert.Equal(t, DiskUsage{Pins: []string{}, Sparse: []string{}}, out)

	d.err = status.Error(codes.InvalidArgument, "repository \"/work/game\" is not watched")
	_, err = svc.DiskUsage(ctx, "/work/game")
	assert.ErrorContains(t, err, "is not watched")
}

func TestService_CleanUp(t *testing.T) {
	svc, d := newStorageService(t)
	d.gc = &daemonpb.GCResponse{Objects: 4, Bytes: 1 << 20}
	out, err := svc.CleanUp(ctx, "/work/game")
	require.NoError(t, err)
	assert.Equal(t, []string{"/work/game"}, d.gcFor)
	assert.Equal(t, CleanUpResult{Objects: 4, Bytes: 1 << 20}, out)

	d.gc = &daemonpb.GCResponse{Skipped: true, SkipReason: usecase.PruneSkipBusy}
	out, err = svc.CleanUp(ctx, "/work/game")
	require.NoError(t, err)
	assert.Equal(t, CleanUpResult{Skipped: true, Reason: "busy"}, out)

	d.gc = &daemonpb.GCResponse{Skipped: true, SkipReason: usecase.PruneSkipPending}
	out, err = svc.CleanUp(ctx, "/work/game")
	require.NoError(t, err)
	assert.Equal(t, "pending", out.Reason)

	d.gc = &daemonpb.GCResponse{Skipped: true, SkipReason: "other"}
	out, err = svc.CleanUp(ctx, "/work/game")
	require.NoError(t, err)
	assert.Equal(t, "other", out.Reason)

	d.err = status.Error(codes.Unknown, "disk on fire")
	_, err = svc.CleanUp(ctx, "/work/game")
	assert.ErrorContains(t, err, "disk on fire")
}

func TestService_StorageNoDaemon(t *testing.T) {
	svc := New(func(context.Context) (Session, error) { return nil, errors.New("daemon down") }, &memStore{}, nil)
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	_, err := svc.DiskUsage(ctx, "/work/game")
	assert.Error(t, err)
	_, err = svc.CleanUp(ctx, "/work/game")
	assert.Error(t, err)
}
