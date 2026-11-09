package cli

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/usecase"
)

type fakeLockClient struct {
	connectHost string
	connectErr  error

	lockPath   string
	lockBranch string
	lockTTL    time.Duration
	lockResult *domain.FileLock
	lockErr    error

	unlockPath   string
	unlockBranch string
	unlockErr    error

	listResult []*domain.FileLock
	listErr    error
}

func (f *fakeLockClient) Connect(_ context.Context, host string) error {
	f.connectHost = host
	return f.connectErr
}

func (f *fakeLockClient) LockFile(_ context.Context, _, _, path, branch string, ttl time.Duration) (*domain.FileLock, error) {
	f.lockPath, f.lockBranch, f.lockTTL = path, branch, ttl
	return f.lockResult, f.lockErr
}

func (f *fakeLockClient) UnlockFile(_ context.Context, _, _, path, branch string) error {
	f.unlockPath, f.unlockBranch = path, branch
	return f.unlockErr
}

func (f *fakeLockClient) ListFileLocks(_ context.Context, _, _ string) ([]*domain.FileLock, error) {
	return f.listResult, f.listErr
}

func newLockCli(t *testing.T, client *fakeLockClient) *Cli {
	t.Helper()
	auth := usecase.NewAuth(fakeExecutor{}, &fakeStorage{token: signTestJWT(t)}, &fakeInput{})
	return NewCli(&fakeUsecaseContainer{lock: usecase.NewFileLock(auth, client, localrepo.NewLocalRepo())}, &fakeConnector{})
}

func TestSetupLockCmd_Success(t *testing.T) {
	root := setupRepo(t, "feature")
	client := &fakeLockClient{lockResult: &domain.FileLock{Path: "art/tex.png", Branch: "feature"}}
	cli := newLockCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupLockCmd(), "art/tex.png")
	require.NoError(t, err)
	require.Contains(t, out, `Locked art/tex.png (branch "feature").`)
	require.Equal(t, "art/tex.png", client.lockPath)
	require.Equal(t, "feature", client.lockBranch, "locks default to the current branch")
	require.Zero(t, client.lockTTL, "without --ttl the lock is permanent")
}

func TestSetupLockCmd_TTL(t *testing.T) {
	root := setupRepo(t, "feature")
	expires := time.Now().Add(2 * time.Hour)
	client := &fakeLockClient{lockResult: &domain.FileLock{Path: "art/tex.png", Branch: "feature", ExpiresAt: &expires}}
	cli := newLockCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupLockCmd(), "art/tex.png", "--ttl", "2h")
	require.NoError(t, err)
	require.Equal(t, 2*time.Hour, client.lockTTL)
	require.Contains(t, out, "expires in 2h0m0s")

	_, err = runCmdInDir(t, root, cli.setupLockCmd(), "art/tex.png", "--ttl", "soon")
	require.ErrorContains(t, err, "invalid --ttl")
	_, err = runCmdInDir(t, root, cli.setupLockCmd(), "art/tex.png", "--ttl", "-5m")
	require.ErrorContains(t, err, "invalid --ttl")
}

func TestFormatLockRemaining(t *testing.T) {
	require.Equal(t, "0s", formatLockRemaining(-time.Second))
	require.Equal(t, "42s", formatLockRemaining(42*time.Second+300*time.Millisecond))
	require.Equal(t, "5m0s", formatLockRemaining(5*time.Minute+10*time.Second))
}

func TestSetupLockCmd_BranchFlag(t *testing.T) {
	root := setupRepo(t, "feature")
	client := &fakeLockClient{lockResult: &domain.FileLock{Path: "art/tex.png", Global: true, Branch: "main"}}
	cli := newLockCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupLockCmd(), "art/tex.png", "--branch", "main")
	require.NoError(t, err)
	require.Contains(t, out, "Locked art/tex.png (mainline).")
	require.Equal(t, "main", client.lockBranch)
}

func TestSetupLockCmd_Error(t *testing.T) {
	root := setupRepo(t, "feature")
	wantErr := &domain.Error{Code: 409, Message: "locked by bob"}
	cli := newLockCli(t, &fakeLockClient{lockErr: wantErr})

	_, err := runCmdInDir(t, root, cli.setupLockCmd(), "art/tex.png")
	require.ErrorIs(t, err, wantErr)
}

func TestSetupLockListCmd_Rows(t *testing.T) {
	root := setupRepo(t, "feature")
	mrNumber := int64(3)
	client := &fakeLockClient{listResult: []*domain.FileLock{
		{Path: "art/tex.png", Global: true, HeldByName: "bob", MergeRequestNumber: &mrNumber},
		{Path: "audio/loop.wav", Branch: "feature", HeldByName: "alice"},
	}}
	cli := newLockCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupLockCmd(), "list")
	require.NoError(t, err)
	require.Contains(t, out, "art/tex.png")
	require.Contains(t, out, "mainline")
	require.Contains(t, out, "bob")
	require.Contains(t, out, "MR #3")
	require.Contains(t, out, `branch "feature"`)
	require.Contains(t, out, "never")
}

func TestSetupLockListCmd_Expiry(t *testing.T) {
	root := setupRepo(t, "feature")
	expires := time.Now().Add(10 * time.Minute)
	cli := newLockCli(t, &fakeLockClient{listResult: []*domain.FileLock{
		{Path: "art/tex.png", Global: true, HeldByName: "bob", ExpiresAt: &expires},
	}})

	out, err := runCmdInDir(t, root, cli.setupLockCmd(), "list")
	require.NoError(t, err)
	require.Contains(t, out, "in 10m0s")
	require.NotContains(t, out, "never")
}

func TestSetupLockListCmd_Empty(t *testing.T) {
	root := setupRepo(t, "feature")
	cli := newLockCli(t, &fakeLockClient{})

	out, err := runCmdInDir(t, root, cli.setupLockCmd(), "list")
	require.NoError(t, err)
	require.Contains(t, out, "no locks")
}

func TestSetupUnlockCmd_Success(t *testing.T) {
	root := setupRepo(t, "feature")
	client := &fakeLockClient{}
	cli := newLockCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupUnlockCmd(), "art/tex.png")
	require.NoError(t, err)
	require.Contains(t, out, "Unlocked art/tex.png.")
	require.Equal(t, "art/tex.png", client.unlockPath)
	require.Equal(t, "feature", client.unlockBranch)
}

func TestSetupUnlockCmd_Error(t *testing.T) {
	root := setupRepo(t, "feature")
	wantErr := errors.New("boom")
	cli := newLockCli(t, &fakeLockClient{unlockErr: wantErr})

	_, err := runCmdInDir(t, root, cli.setupUnlockCmd(), "art/tex.png")
	require.ErrorIs(t, err, wantErr)
}

func TestLock_NotARepo(t *testing.T) {
	cli := newLockCli(t, &fakeLockClient{})

	_, err := runCmdInDir(t, t.TempDir(), cli.setupLockCmd(), "art/tex.png")
	require.EqualError(t, err, "not a nipa repository (or any of the parent directories)")
}

func TestSetupLockCmd_PrintsPinWarning(t *testing.T) {
	root := setupRepoWithStoredTree(t, "main", map[string]string{"a.bin": "\x00original\x01"})
	dropObjects(t, root)
	writeFile(t, root, "a.bin", "\x00edited\x02")
	client := &fakeLockClient{lockResult: &domain.FileLock{Path: "a.bin", Global: true}}

	out, err := runCmdInDir(t, root, newLockCli(t, client).setupLockCmd(), "a.bin")
	require.NoError(t, err)
	require.Contains(t, out, "Locked a.bin (mainline).")
	require.Contains(t, out, "warning: locked, but the old version of a.bin was not kept locally")
}
