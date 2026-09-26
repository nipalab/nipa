package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
)

type stubLockClient struct {
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

func (s *stubLockClient) Connect(_ context.Context, host string) error {
	s.connectHost = host
	return s.connectErr
}

func (s *stubLockClient) LockFile(_ context.Context, _, _, path, branch string) (*domain.FileLock, error) {
	s.lockPath, s.lockBranch = path, branch
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

	lock, err := newTestFileLockUsecase(t, local, client).Lock(context.Background(), root, " art/tex.png ", "")
	require.NoError(t, err)
	require.Equal(t, "art/tex.png", lock.Path)
	require.Equal(t, root, local.initTarget)
	require.Equal(t, "example.com", client.connectHost)
	require.Equal(t, "art/tex.png", client.lockPath)
	require.Equal(t, "feature", client.lockBranch)
}

func TestFileLockUsecase_Lock_ExplicitBranch(t *testing.T) {
	client := &stubLockClient{lockResult: &domain.FileLock{Path: "art/tex.png", Global: true}}
	_, err := newTestFileLockUsecase(t, lockTestLocalRepo(), client).Lock(context.Background(), t.TempDir(), "art/tex.png", "main")
	require.NoError(t, err)
	require.Equal(t, "main", client.lockBranch)
}

func TestFileLockUsecase_Lock_Validation(t *testing.T) {
	local := lockTestLocalRepo()
	client := &stubLockClient{}
	uc := newTestFileLockUsecase(t, local, client)

	_, err := uc.Lock(context.Background(), t.TempDir(), "  ", "")
	require.EqualError(t, err, "a path is required")
	require.Empty(t, client.lockPath)
}

func TestFileLockUsecase_Lock_ClientError(t *testing.T) {
	wantErr := &domain.Error{Code: 409, Message: "locked by bob"}
	client := &stubLockClient{lockErr: wantErr}

	_, err := newTestFileLockUsecase(t, lockTestLocalRepo(), client).Lock(context.Background(), t.TempDir(), "a.png", "")
	require.ErrorIs(t, err, wantErr)
}

func TestFileLockUsecase_Lock_ConnectError(t *testing.T) {
	wantErr := errors.New("dial failed")
	client := &stubLockClient{connectErr: wantErr}

	_, err := newTestFileLockUsecase(t, lockTestLocalRepo(), client).Lock(context.Background(), t.TempDir(), "a.png", "")
	require.ErrorIs(t, err, wantErr)
}

func TestFileLockUsecase_Lock_ConfigError(t *testing.T) {
	wantErr := errors.New("missing config")
	local := &stubLocalRepo{configLoadErr: wantErr}

	_, err := newTestFileLockUsecase(t, local, &stubLockClient{}).Lock(context.Background(), t.TempDir(), "a.png", "")
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
