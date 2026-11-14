package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
)

type stubLockClient struct {
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

func (s *stubLockClient) Connect(_ context.Context, host string) error {
	s.connectHost = host
	return s.connectErr
}

func (s *stubLockClient) LockFile(_ context.Context, _, _, path, branch string, ttl time.Duration) (*domain.FileLock, error) {
	s.lockPath, s.lockBranch, s.lockTTL = path, branch, ttl
	return s.lockResult, s.lockErr
}

func (s *stubLockClient) UnlockFile(_ context.Context, _, _, path, branch string) error {
	s.unlockPath, s.unlockBranch = path, branch
	return s.unlockErr
}

func (s *stubLockClient) ListFileLocks(_ context.Context, _, _ string) ([]*domain.FileLock, error) {
	return s.listResult, s.listErr
}

func newTestFileLockUsecase(t *testing.T, local lockLocalRepo, client lockClient) *FileLock {
	t.Helper()
	token := signTestToken(t, "secret")
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: token}}
	auth := NewAuth(nil, storage, nil)
	return NewFileLock(auth, client, local)
}

func lockTestLocalRepo() *stubLocalRepo {
	return &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "feature"},
	}
}

func TestFileLockUsecase_Lock_DefaultsToCurrentBranch(t *testing.T) {
	root := t.TempDir()
	local := lockTestLocalRepo()
	client := &stubLockClient{lockResult: &domain.FileLock{Path: "art/tex.png", Branch: "feature"}}

	lock, err := newTestFileLockUsecase(t, local, client).Lock(context.Background(), root, " art/tex.png ", LockOptions{})
	require.NoError(t, err)
	require.Equal(t, "art/tex.png", lock.Path)
	require.Equal(t, root, local.initTarget)
	require.Equal(t, "http://example.com", client.connectHost)
	require.Equal(t, "art/tex.png", client.lockPath)
	require.Equal(t, "feature", client.lockBranch)
}

func TestFileLockUsecase_Lock_ExplicitBranch(t *testing.T) {
	client := &stubLockClient{lockResult: &domain.FileLock{Path: "art/tex.png", Global: true}}
	_, err := newTestFileLockUsecase(t, lockTestLocalRepo(), client).Lock(context.Background(), t.TempDir(), "art/tex.png", LockOptions{Branch: "main"})
	require.NoError(t, err)
	require.Equal(t, "main", client.lockBranch)
}

func TestFileLockUsecase_Lock_Validation(t *testing.T) {
	local := lockTestLocalRepo()
	client := &stubLockClient{}
	uc := newTestFileLockUsecase(t, local, client)

	_, err := uc.Lock(context.Background(), t.TempDir(), "  ", LockOptions{})
	require.EqualError(t, err, "a path is required")
	require.Empty(t, client.lockPath)
}

func TestFileLockUsecase_Lock_ClientError(t *testing.T) {
	wantErr := &domain.Error{Code: 409, Message: "locked by bob"}
	client := &stubLockClient{lockErr: wantErr}

	_, err := newTestFileLockUsecase(t, lockTestLocalRepo(), client).Lock(context.Background(), t.TempDir(), "a.png", LockOptions{})
	require.ErrorIs(t, err, wantErr)
}

func TestFileLockUsecase_Lock_ConnectError(t *testing.T) {
	wantErr := errors.New("dial failed")
	client := &stubLockClient{connectErr: wantErr}

	_, err := newTestFileLockUsecase(t, lockTestLocalRepo(), client).Lock(context.Background(), t.TempDir(), "a.png", LockOptions{})
	require.ErrorIs(t, err, wantErr)
}

func TestFileLockUsecase_Lock_ConfigError(t *testing.T) {
	wantErr := errors.New("missing config")
	local := &stubLocalRepo{configLoadErr: wantErr}

	_, err := newTestFileLockUsecase(t, local, &stubLockClient{}).Lock(context.Background(), t.TempDir(), "a.png", LockOptions{})
	require.ErrorIs(t, err, wantErr)
}

func TestFileLockUsecase_Unlock(t *testing.T) {
	client := &stubLockClient{}
	require.NoError(t, newTestFileLockUsecase(t, lockTestLocalRepo(), client).
		Unlock(context.Background(), t.TempDir(), "art/tex.png", ""))
	require.Equal(t, "art/tex.png", client.unlockPath)
	require.Equal(t, "feature", client.unlockBranch)

	err := newTestFileLockUsecase(t, lockTestLocalRepo(), &stubLockClient{}).
		Unlock(context.Background(), t.TempDir(), " ", "")
	require.EqualError(t, err, "a path is required")
}

func TestFileLockUsecase_Unlock_Error(t *testing.T) {
	wantErr := &domain.Error{Code: 404, Message: "no lock"}
	client := &stubLockClient{unlockErr: wantErr}

	err := newTestFileLockUsecase(t, lockTestLocalRepo(), client).
		Unlock(context.Background(), t.TempDir(), "a.png", "main")
	require.ErrorIs(t, err, wantErr)
	require.Equal(t, "main", client.unlockBranch)
}

func TestFileLockUsecase_List(t *testing.T) {
	client := &stubLockClient{listResult: []*domain.FileLock{{Path: "a.png"}}}

	locks, err := newTestFileLockUsecase(t, lockTestLocalRepo(), client).List(context.Background(), t.TempDir())
	require.NoError(t, err)
	require.Len(t, locks, 1)

	wantErr := errors.New("boom")
	_, err = newTestFileLockUsecase(t, lockTestLocalRepo(), &stubLockClient{listErr: wantErr}).
		List(context.Background(), t.TempDir())
	require.ErrorIs(t, err, wantErr)
}

func TestFileLockUsecase_Lock_PassesTTL(t *testing.T) {
	client := &stubLockClient{lockResult: &domain.FileLock{Path: "a.png"}}
	_, err := newTestFileLockUsecase(t, lockTestLocalRepo(), client).
		Lock(context.Background(), t.TempDir(), "a.png", LockOptions{TTL: 2 * time.Hour})
	require.NoError(t, err)
	require.Equal(t, 2*time.Hour, client.lockTTL)
	require.Equal(t, "feature", client.lockBranch)
}

type stubRequestClient struct {
	*stubLockClient
	lockID  string
	message string
	err     error
}

func (s *stubRequestClient) RequestFileLockRelease(_ context.Context, _, _, lockID, message string) (*domain.FileLockRequest, error) {
	s.lockID, s.message = lockID, message
	if s.err != nil {
		return nil, s.err
	}
	return &domain.FileLockRequest{ID: "r1", Message: message}, nil
}

func TestFileLockUsecase_RequestRelease(t *testing.T) {
	locks := []*domain.FileLock{
		{ID: "g", Path: "art", Global: true, HeldByName: "bob"},
		{ID: "f", Path: "art/a.psd", Branch: "feature"},
		{ID: "o", Path: "art/a.psd", Branch: "other"},
		nil,
	}
	client := &stubRequestClient{stubLockClient: &stubLockClient{listResult: locks}}
	uc := newTestFileLockUsecase(t, lockTestLocalRepo(), client)

	lock, request, err := uc.RequestRelease(context.Background(), t.TempDir(), "art/a.psd", "", " please ")
	require.NoError(t, err)
	require.Equal(t, "f", lock.ID, "the clone's branch lock wins over the global one")
	require.Equal(t, "f", client.lockID)
	require.Equal(t, "please", request.Message)

	lock, _, err = uc.RequestRelease(context.Background(), t.TempDir(), "art/b.psd", "feature", "")
	require.NoError(t, err)
	require.Equal(t, "g", lock.ID, "a directory lock covers the path")

	_, _, err = uc.RequestRelease(context.Background(), t.TempDir(), "docs/x.md", "", "")
	require.ErrorContains(t, err, "docs/x.md is not locked")
	_, _, err = uc.RequestRelease(context.Background(), t.TempDir(), " ", "", "")
	require.ErrorContains(t, err, "a path is required")

	client.err = errors.New("boom")
	_, _, err = uc.RequestRelease(context.Background(), t.TempDir(), "art/a.psd", "", "")
	require.EqualError(t, err, "boom")

	client.listErr = errors.New("list down")
	_, _, err = uc.RequestRelease(context.Background(), t.TempDir(), "art/a.psd", "", "")
	require.EqualError(t, err, "list down")

	client.connectErr = errors.New("offline")
	_, _, err = uc.RequestRelease(context.Background(), t.TempDir(), "art/a.psd", "", "")
	require.EqualError(t, err, "offline")

	_, _, err = newTestFileLockUsecase(t, lockTestLocalRepo(), &stubLockClient{}).RequestRelease(context.Background(), t.TempDir(), "a", "", "")
	require.ErrorContains(t, err, "not supported")
}
