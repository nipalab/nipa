package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

type stubLockDownloadClient struct {
	*stubLockClient
	data  map[serverDomain.Hash][]byte
	calls int
}

func (s *stubLockDownloadClient) DownloadChunks(_ context.Context, _ clientDomain.ChunkScope, hashes []serverDomain.Hash, onChunk func(serverDomain.Hash, []byte) error) error {
	s.calls++
	for _, h := range hashes {
		if data, ok := s.data[h]; ok {
			if err := onChunk(h, data); err != nil {
				return err
			}
		}
	}
	return nil
}

func pinFixture(t *testing.T) *pristineFixture {
	return newPristineFixture(t, map[string]string{
		"art/hero.bin":   "\x00hero texture\x01",
		"artwork/x.bin":  "\x00other texture\x02",
		"notes/read.txt": "plain text notes\n",
	})
}

func (fx *pristineFixture) cached(t *testing.T, name string) bool {
	t.Helper()
	for _, c := range fx.chunks[name] {
		if _, err := fx.lr.LoadChunk(c.Hash); err != nil {
			return false
		}
	}
	return true
}

func TestFileLock_LockPinsAndCapturesCoveredRawFiles(t *testing.T) {
	fx := pinFixture(t)
	fx.dropObjects(t)
	client := &stubLockDownloadClient{stubLockClient: &stubLockClient{lockResult: &clientDomain.FileLock{Path: "/art"}}}

	lock, err := newTestFileLockUsecase(t, fx.lr, client).Lock(context.Background(), fx.root, "art", LockOptions{})
	require.NoError(t, err)
	require.Empty(t, lock.Warning)
	pins, err := fx.lr.ListPins()
	require.NoError(t, err)
	require.Equal(t, []string{"art"}, pins)
	require.True(t, fx.cached(t, "art/hero.bin"), "an unchanged working file is recaptured")
	require.False(t, fx.cached(t, "artwork/x.bin"), "a sibling prefix is not covered")
	require.False(t, fx.cached(t, "notes/read.txt"))
	require.Zero(t, client.calls)
}

func TestFileLock_LockDownloadsEditedFile(t *testing.T) {
	fx := pinFixture(t)
	fx.dropObjects(t)
	writeFileAt(t, fx.root, "art/hero.bin", "\x00edited before locking\x03")
	client := &stubLockDownloadClient{stubLockClient: &stubLockClient{lockResult: &clientDomain.FileLock{Path: "art/hero.bin"}}, data: fx.stored}

	lock, err := newTestFileLockUsecase(t, fx.lr, client).Lock(context.Background(), fx.root, "art/hero.bin", LockOptions{})
	require.NoError(t, err)
	require.Empty(t, lock.Warning)
	require.Equal(t, 1, client.calls)
	require.True(t, fx.cached(t, "art/hero.bin"))
}

func TestFileLock_LockCaptureFailureIsWarning(t *testing.T) {
	fx := pinFixture(t)
	fx.dropObjects(t)
	writeFileAt(t, fx.root, "art/hero.bin", "\x00edited\x03")
	client := &stubLockClient{lockResult: &clientDomain.FileLock{Path: "art/hero.bin"}}

	lock, err := newTestFileLockUsecase(t, fx.lr, client).Lock(context.Background(), fx.root, "art/hero.bin", LockOptions{})
	require.NoError(t, err)
	require.Contains(t, lock.Warning, "content not cached locally")
	pins, err := fx.lr.ListPins()
	require.NoError(t, err)
	require.Equal(t, []string{"art/hero.bin"}, pins, "the pin stays so a later read can still fetch the old version")
}

func TestFileLock_UnlockUnpinsAndPrunes(t *testing.T) {
	fx := pinFixture(t)
	require.NoError(t, fx.lr.PinPaths([]string{"art"}))
	client := &stubLockClient{}

	require.NoError(t, newTestFileLockUsecase(t, fx.lr, client).Unlock(context.Background(), fx.root, "/art/", ""))
	pins, err := fx.lr.ListPins()
	require.NoError(t, err)
	require.Empty(t, pins)
	require.False(t, fx.cached(t, "art/hero.bin"))
	require.True(t, fx.cached(t, "notes/read.txt"), "text stays cached")
}

func TestFileLock_UnlockServerFailureKeepsPin(t *testing.T) {
	fx := pinFixture(t)
	require.NoError(t, fx.lr.PinPaths([]string{"art"}))
	client := &stubLockClient{unlockErr: errors.New("not yours")}

	require.Error(t, newTestFileLockUsecase(t, fx.lr, client).Unlock(context.Background(), fx.root, "art", ""))
	pins, err := fx.lr.ListPins()
	require.NoError(t, err)
	require.Equal(t, []string{"art"}, pins)
}

func TestLockPins_Errors(t *testing.T) {
	boom := errors.New("boom")
	ctx := context.Background()
	cases := map[string]*stubLocalRepo{
		"init":     {initErr: boom},
		"lock":     {lockErr: boom},
		"pin":      {pinErr: boom},
		"snapshot": {snapshotErr: boom},
	}
	for name, repo := range cases {
		t.Run(name, func(t *testing.T) {
			require.ErrorIs(t, NewLockPins(nil, nil, repo).Pin(ctx, t.TempDir(), "a.bin"), boom)
		})
	}
	require.Error(t, NewLockPins(nil, nil, &stubLocalRepo{}).Pin(ctx, t.TempDir(), "../a"))
	require.NoError(t, NewLockPins(nil, nil, &stubLocalRepo{}).Pin(ctx, t.TempDir(), "/"))

	for name, repo := range map[string]*stubLocalRepo{
		"init":  {initErr: boom},
		"lock":  {lockErr: boom},
		"unpin": {unpinErr: boom},
	} {
		t.Run("unpin "+name, func(t *testing.T) {
			require.ErrorIs(t, NewLockPins(nil, nil, repo).Unpin(ctx, t.TempDir(), "a.bin"), boom)
		})
	}
}

func TestFileLock_LockWithoutPinRepoReturnsServerLock(t *testing.T) {
	lock := &clientDomain.FileLock{Path: "a.bin"}
	got, err := newTestFileLockUsecase(t, lockOnlyRepo{}, &stubLockClient{lockResult: lock}).Lock(context.Background(), t.TempDir(), "a.bin", LockOptions{})
	require.NoError(t, err)
	require.Same(t, lock, got)
	require.NoError(t, newTestFileLockUsecase(t, lockOnlyRepo{}, &stubLockClient{}).Unlock(context.Background(), t.TempDir(), "a.bin", ""))
}

type lockOnlyRepo struct{}

func (lockOnlyRepo) Init(string) error { return nil }
func (lockOnlyRepo) LoadConfig() (*clientDomain.Config, error) {
	return &clientDomain.Config{Url: "http://example.com/org/project", Branch: "main"}, nil
}

func TestFileLock_UnlockUnpinFailureOnlyWarns(t *testing.T) {
	local := lockTestLocalRepo()
	local.unpinErr = errors.New("db")
	client := &stubLockClient{}
	require.NoError(t, newTestFileLockUsecase(t, local, client).Unlock(context.Background(), t.TempDir(), "a.bin", ""))
	require.Equal(t, "a.bin", client.unlockPath)
}
