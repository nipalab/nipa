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

type searchDaemon struct {
	*blobDaemon
	res  *daemonpb.SearchFilesResponse
	err  error
	reqs []*daemonpb.SearchFilesRequest
}

func (d *searchDaemon) SearchFiles(_ context.Context, req *daemonpb.SearchFilesRequest, _ ...grpc.CallOption) (*daemonpb.SearchFilesResponse, error) {
	d.reqs = append(d.reqs, req)
	if d.err != nil {
		return nil, d.err
	}
	return d.res, nil
}

func newSearchService(t *testing.T) (*Service, *searchDaemon) {
	t.Helper()
	d := &searchDaemon{blobDaemon: &blobDaemon{opsDaemon: &opsDaemon{fakeDaemon: &fakeDaemon{watchErr: map[string]error{}, changes: make(chan *daemonpb.RepoChange, 4)}}}}
	svc := New(func(context.Context) (Session, error) { return &opsSession{client: d}, nil }, &memStore{}, nil)
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	return svc, d
}

func TestService_SearchFiles(t *testing.T) {
	svc, d := newSearchService(t)
	d.res = &daemonpb.SearchFilesResponse{Truncated: true, Hits: []*daemonpb.SearchFileHit{
		{Path: "Assets/hero.png", Name: "hero.png", SizeBytes: 42, IsBinary: true, Kind: "image"},
	}}
	out, err := svc.SearchFiles(ctx, "/work/game", "hero", 20)
	require.NoError(t, err)
	assert.Equal(t, &FileSearch{Truncated: true, Hits: []FileHit{{Path: "Assets/hero.png", Name: "hero.png", Size: 42, Binary: true, Kind: "image"}}}, out)
	require.Len(t, d.reqs, 1)
	assert.Equal(t, "/work/game", d.reqs[0].GetRoot())
	assert.Equal(t, "hero", d.reqs[0].GetQuery())
	assert.Equal(t, int32(20), d.reqs[0].GetLimit())

	d.res = &daemonpb.SearchFilesResponse{}
	out, err = svc.SearchFiles(ctx, "/work/game", "x", 5000)
	require.NoError(t, err)
	assert.Equal(t, &FileSearch{Hits: []FileHit{}}, out)
	assert.Equal(t, int32(200), d.reqs[1].GetLimit())

	_, err = svc.SearchFiles(ctx, "/work/game", "x", -3)
	require.NoError(t, err)
	assert.Equal(t, int32(0), d.reqs[2].GetLimit())

	d.err = status.Error(codes.InvalidArgument, "repository is not watched")
	_, err = svc.SearchFiles(ctx, "/work/game", "x", 10)
	require.ErrorContains(t, err, "not watched")
}
