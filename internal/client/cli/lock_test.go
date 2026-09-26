package cli

import (
	"context"
	"errors"
	"testing"

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

func (f *fakeLockClient) LockFile(_ context.Context, _, _, path, branch string) (*domain.FileLock, error) {
	f.lockPath, f.lockBranch = path, branch
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
