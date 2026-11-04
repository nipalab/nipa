package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/grpc/pb"
)

type lockDaemon struct {
	*opsDaemon
	status    *daemonpb.LockStatusResponse
	statusErr error
	lockErr   map[string]error
	locked    []*pb.LockFileRequest
	unlocked  []*pb.UnlockFileRequest
	unlockErr error
}

func (d *lockDaemon) LockStatus(context.Context, *daemonpb.LockStatusRequest, ...grpc.CallOption) (*daemonpb.LockStatusResponse, error) {
	return d.status, d.statusErr
}

func (d *lockDaemon) ProxyLockFile(_ context.Context, req *daemonpb.ProxyLockFileRequest, _ ...grpc.CallOption) (*daemonpb.ProxyLockFileResponse, error) {
	d.locked = append(d.locked, req.GetRequest())
	if err := d.lockErr[req.GetRequest().GetPath()]; err != nil {
		return nil, err
	}
	return &daemonpb.ProxyLockFileResponse{}, nil
}

func (d *lockDaemon) ProxyUnlockFile(_ context.Context, req *daemonpb.ProxyUnlockFileRequest, _ ...grpc.CallOption) (*daemonpb.ProxyUnlockFileResponse, error) {
	d.unlocked = append(d.unlocked, req.GetRequest())
	return &daemonpb.ProxyUnlockFileResponse{}, d.unlockErr
}

func newLocks(t *testing.T) (*Service, *lockDaemon) {
	t.Helper()
	d := &lockDaemon{opsDaemon: &opsDaemon{fakeDaemon: &fakeDaemon{watchErr: map[string]error{}, changes: make(chan *daemonpb.RepoChange, 4)}}, lockErr: map[string]error{}}
	svc := New(func(context.Context) (Session, error) { return &opsSession{client: d}, nil }, &memStore{}, nil)
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	return svc, d
}

func TestService_LockStatus(t *testing.T) {
	svc, d := newLocks(t)
	at := time.Date(2026, 11, 4, 9, 30, 0, 0, time.UTC)
	mr := int64(12)
	d.status = &daemonpb.LockStatusResponse{
		UserId: "me", Branch: "main", DefaultBranch: true, CanReleaseAny: true,
		Locks: []*pb.FileLockDetail{
			{Id: "l1", Path: "art/hero.psd", Global: true, HeldBy: "me", HeldByName: "Me", AcquiredAt: timestamppb.New(at)},
			{Id: "l2", Path: "audio", Branch: "feature", HeldBy: "bob", HeldByName: "Bob", MergeRequestNumber: &mr},
		},
		Files: []*daemonpb.FileLockState{{Path: "art/hero.psd", State: "mine", LockPath: "art/hero.psd", HeldByName: "Me", Tracked: true}},
	}
	out, err := svc.LockStatus(ctx, "/work/game")
	require.NoError(t, err)
	assert.Equal(t, "me", out.UserID)
	assert.True(t, out.DefaultBranch)
	assert.True(t, out.CanReleaseAny)
	assert.Equal(t, []FileLock{
		{ID: "l1", Path: "art/hero.psd", Global: true, HeldBy: "me", HeldByName: "Me", Mine: true, AcquiredAt: "2026-11-04T09:30:00Z", InScope: true},
		{ID: "l2", Path: "audio", Branch: "feature", HeldBy: "bob", HeldByName: "Bob", MergeRequest: 12},
	}, out.Locks)
	assert.Equal(t, []FileLockState{{Path: "art/hero.psd", State: "mine", LockPath: "art/hero.psd", HeldByName: "Me", Tracked: true}}, out.Files)

	d.status = &daemonpb.LockStatusResponse{Branch: "feature", Locks: []*pb.FileLockDetail{{Path: "audio", Branch: "feature"}, {Path: "x", Global: true}}}
	out, err = svc.LockStatus(ctx, "/work/game")
	require.NoError(t, err)
	assert.True(t, out.Locks[0].InScope)
	assert.False(t, out.Locks[0].Mine)
	assert.False(t, out.Locks[1].InScope)
	assert.Empty(t, out.Files)
	assert.NotNil(t, out.Files)

	d.statusErr = status.Error(codes.Unavailable, "server down")
	_, err = svc.LockStatus(ctx, "/work/game")
	assert.EqualError(t, err, "server down")
}

func TestService_LockAndUnlock(t *testing.T) {
	svc, d := newLocks(t)
	d.lockErr["art/sky.psd"] = status.Error(codes.AlreadyExists, `"art/sky.psd" is locked by Bob`)
	out, err := svc.Lock(ctx, "/work/game", "main", []string{"art/hero.psd", "art/sky.psd"})
	require.NoError(t, err)
	assert.Equal(t, []string{"art/hero.psd"}, out.Locked)
	assert.Equal(t, []LockFailure{{Path: "art/sky.psd", Message: `"art/sky.psd" is locked by Bob`}}, out.Failed)
	assert.Equal(t, "main", d.locked[0].GetBranch())

	d.lockErr["art/hero.psd"] = status.Error(codes.Unavailable, "daemon gone")
	_, err = svc.Lock(ctx, "/work/game", "main", []string{"art/hero.psd"})
	assert.EqualError(t, err, "daemon gone")

	require.NoError(t, svc.Unlock(ctx, "/work/game", "", "art/hero.psd"))
	assert.Equal(t, "art/hero.psd", d.unlocked[0].GetPath())
	assert.Equal(t, "", d.unlocked[0].GetBranch())
	d.unlockErr = status.Error(codes.PermissionDenied, "no permission")
	assert.EqualError(t, svc.Unlock(ctx, "/work/game", "feature", "x"), "no permission")
}

func TestService_LocksNeedTheDaemon(t *testing.T) {
	svc := New(func(context.Context) (Session, error) { return nil, errors.New("no daemon") }, &memStore{}, nil)
	_, err := svc.LockStatus(ctx, "/r")
	assert.Error(t, err)
	_, err = svc.Lock(ctx, "/r", "", []string{"a"})
	assert.Error(t, err)
	assert.Error(t, svc.Unlock(ctx, "/r", "", "a"))
}

func TestService_PickAndOpenFile(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "art"), 0o755))
	svc := New(nil, &memStore{}, nil)

	_, err := svc.PickFile(ctx, root)
	assert.EqualError(t, err, "file dialogs are not available")
	assert.EqualError(t, svc.OpenFile(ctx, root, "a.psd"), "opening files is not available")

	var startedIn string
	picked := filepath.Join(root, "art", "hero.psd")
	svc.PickFileFunc = func(dir string) (string, error) { startedIn = dir; return picked, nil }
	rel, err := svc.PickFile(ctx, root)
	require.NoError(t, err)
	assert.Equal(t, "art/hero.psd", rel)
	assert.Equal(t, root, startedIn)

	for _, bad := range []string{filepath.Join(filepath.Dir(root), "elsewhere.psd"), root, filepath.Join(root, ".nipa", "nipa.db")} {
		picked = bad
		_, err = svc.PickFile(ctx, root)
		assert.EqualError(t, err, "choose a file inside this clone", bad)
	}
	picked = ""
	rel, err = svc.PickFile(ctx, root)
	require.NoError(t, err)
	assert.Empty(t, rel)
	svc.PickFileFunc = func(string) (string, error) { return "", errors.New("dialog crashed") }
	_, err = svc.PickFile(ctx, root)
	assert.EqualError(t, err, "dialog crashed")

	var opened string
	svc.OpenFunc = func(p string) error { opened = p; return nil }
	require.NoError(t, svc.OpenFile(ctx, root, "art/hero.psd"))
	assert.Equal(t, filepath.Join(root, "art", "hero.psd"), opened)
	assert.EqualError(t, svc.OpenFile(ctx, root, "../escape.psd"), "the file is outside this clone")
}
