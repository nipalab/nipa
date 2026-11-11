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
)

type pushPlanDaemon struct {
	*blobDaemon
	resp  *daemonpb.PushPlanResponse
	err   error
	roots []string
}

func (d *pushPlanDaemon) PushPlan(_ context.Context, req *daemonpb.PushPlanRequest, _ ...grpc.CallOption) (*daemonpb.PushPlanResponse, error) {
	d.roots = append(d.roots, req.GetRoot())
	if d.err != nil {
		return nil, d.err
	}
	return d.resp, nil
}

func TestService_PushChecks(t *testing.T) {
	d := &pushPlanDaemon{blobDaemon: &blobDaemon{opsDaemon: &opsDaemon{fakeDaemon: &fakeDaemon{watchErr: map[string]error{}, changes: make(chan *daemonpb.RepoChange, 4)}}}}
	svc := New(func(context.Context) (Session, error) { return &opsSession{client: d}, nil }, &memStore{}, nil)
	t.Cleanup(func() { _ = svc.ServiceShutdown() })

	d.resp = &daemonpb.PushPlanResponse{
		AssetViolations: []*daemonpb.AssetViolation{{Path: "tex.png", Rule: "textures", Limit: "power_of_two", Actual: "6x4", Message: "6x4 is not a power of two"}},
		AssetImpact: &daemonpb.AssetImpact{
			Broken:      []*daemonpb.BrokenReference{{From: "a.mat", Raw: "guid:x", Target: "tex.png", Reason: "removed"}},
			NewlyUnused: []string{"old.png"},
			Scanned:     12,
			Truncated:   true,
		},
		Warnings: []string{"budget check skipped: bad"},
	}
	out, err := svc.PushChecks(ctx, "/work/game")
	require.NoError(t, err)
	assert.Equal(t, []string{"/work/game"}, d.roots)
	assert.Equal(t, PushChecks{
		Violations:  []BudgetViolation{{Path: "tex.png", Rule: "textures", Limit: "power_of_two", Actual: "6x4", Message: "6x4 is not a power of two"}},
		Broken:      []BrokenReference{{From: "a.mat", Raw: "guid:x", Target: "tex.png", Reason: "removed"}},
		NewlyUnused: []string{"old.png"},
		Truncated:   true,
		Scanned:     12,
		Warnings:    []string{"budget check skipped: bad"},
	}, out)

	d.resp = &daemonpb.PushPlanResponse{}
	out, err = svc.PushChecks(ctx, "/work/game")
	require.NoError(t, err)
	assert.Equal(t, PushChecks{Violations: []BudgetViolation{}, Broken: []BrokenReference{}, NewlyUnused: []string{}, Warnings: []string{}}, out)

	d.err = status.Error(codes.InvalidArgument, "nothing staged to push")
	_, err = svc.PushChecks(ctx, "/work/game")
	assert.ErrorContains(t, err, "nothing staged")
}

func TestService_PushChecksNoDaemon(t *testing.T) {
	svc := New(func(context.Context) (Session, error) { return nil, errors.New("daemon down") }, &memStore{}, nil)
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	_, err := svc.PushChecks(ctx, "/work/game")
	assert.Error(t, err)
}
