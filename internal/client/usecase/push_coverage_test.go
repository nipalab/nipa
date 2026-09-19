package usecase

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

func pushFixture(t *testing.T) (string, *stubLocalRepo, *stubPushClient) {
	t.Helper()
	root := t.TempDir()
	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		snapshot:   &domain.Snapshot{},
	}
	client := &stubPushClient{manifest: &serverDomain.TreeNode{Name: "root"}}
	return root, local, client
}

func TestPush_Run_ErrorPaths(t *testing.T) {
	ctx := context.Background()

	t.Run("init error", func(t *testing.T) {
		local := &stubLocalRepo{initErr: errors.New("init failed")}
		pusher := newTestPush(t, local, &stubPushClient{})
		err := pusher.Run(ctx, t.TempDir(), "message")
		require.ErrorContains(t, err, "init failed")
	})

	t.Run("config error", func(t *testing.T) {
		local := &stubLocalRepo{configLoadErr: errors.New("config failed")}
		pusher := newTestPush(t, local, &stubPushClient{})
		err := pusher.Run(ctx, t.TempDir(), "message")
		require.ErrorContains(t, err, "config failed")
	})

	t.Run("invalid url", func(t *testing.T) {
		local := &stubLocalRepo{loadConfig: &domain.Config{Url: "not a url", Branch: "main"}}
		pusher := newTestPush(t, local, &stubPushClient{})
		err := pusher.Run(ctx, t.TempDir(), "message")
		require.Error(t, err)
	})

	t.Run("connect error", func(t *testing.T) {
		root, local, client := pushFixture(t)
		client.connectErr = errors.New("connect failed")
		pusher := newTestPush(t, local, client)
		err := pusher.Run(ctx, root, "message")
		require.ErrorContains(t, err, "connect failed")
	})

	t.Run("merge state load error", func(t *testing.T) {
		root, local, client := pushFixture(t)
		local.mergeStateErr = errors.New("merge state failed")
		pusher := newTestPush(t, local, client)
		err := pusher.Run(ctx, root, "message")
		require.ErrorContains(t, err, "merge state failed")
	})

	t.Run("revert state load error", func(t *testing.T) {
		root, local, client := pushFixture(t)
		local.revertStateErr = errors.New("revert state failed")
		pusher := newTestPush(t, local, client)
		err := pusher.Run(ctx, root, "message")
		require.ErrorContains(t, err, "revert state failed")
	})

	t.Run("clear merge state error", func(t *testing.T) {
		root, local, client := pushFixture(t)
		writeRepoFile(t, root, "a.txt", "hello")
		local.staged = []string{"a.txt"}
		local.clearMergeErr = errors.New("clear merge failed")
		pusher := newTestPush(t, local, client)
		err := pusher.Run(ctx, root, "message")
		require.ErrorContains(t, err, "clear merge failed")
	})

	t.Run("snapshot error", func(t *testing.T) {
		root, local, client := pushFixture(t)
		local.snapshotErr = errors.New("snapshot failed")
		pusher := newTestPush(t, local, client)
		err := pusher.Run(ctx, root, "message")
		require.ErrorContains(t, err, "snapshot failed")
	})

	t.Run("staged list error", func(t *testing.T) {
		root, local, client := pushFixture(t)
		local.stagedErr = errors.New("staged failed")
		pusher := newTestPush(t, local, client)
		err := pusher.Run(ctx, root, "message")
		require.ErrorContains(t, err, "staged failed")
	})

	t.Run("save commit error", func(t *testing.T) {
		root, local, client := pushFixture(t)
		writeRepoFile(t, root, "a.txt", "hello")
		local.staged = []string{"a.txt"}
		local.saveCommitErr = errors.New("save commit failed")
		pusher := newTestPush(t, local, client)
		err := pusher.Run(ctx, root, "message")
		require.ErrorContains(t, err, "save commit failed")
	})

	t.Run("clear staged error", func(t *testing.T) {
		root, local, client := pushFixture(t)
		writeRepoFile(t, root, "a.txt", "hello")
		local.staged = []string{"a.txt"}
		local.clearStagedErr = errors.New("clear staged failed")
		pusher := newTestPush(t, local, client)
		err := pusher.Run(ctx, root, "message")
		require.ErrorContains(t, err, "clear staged failed")
	})
}

func TestPush_Run_Error_StagedPaths(t *testing.T) {
	ctx := context.Background()

	t.Run("path inside nipa dir", func(t *testing.T) {
		root, local, client := pushFixture(t)
		local.staged = []string{".nipa/secret"}
		pusher := newTestPush(t, local, client)
		err := pusher.Run(ctx, root, "message")
		require.ErrorContains(t, err, "cannot push path inside")
	})

	t.Run("staged directory", func(t *testing.T) {
		root, local, client := pushFixture(t)
		require.NoError(t, os.MkdirAll(filepath.Join(root, "dir"), 0o755))
		local.staged = []string{"dir"}
		pusher := newTestPush(t, local, client)
		err := pusher.Run(ctx, root, "message")
		require.ErrorContains(t, err, "not a regular file")
	})

	t.Run("stat error", func(t *testing.T) {
		root, local, client := pushFixture(t)
		writeRepoFile(t, root, "a.txt", "hello")
		local.staged = []string{"a.txt/sub"}
		pusher := newTestPush(t, local, client)
		err := pusher.Run(ctx, root, "message")
		require.Error(t, err)
		require.False(t, local.clearedStaged)
	})
}

func TestPush_Run_ReportsUploadProgress_EmptyFile(t *testing.T) {
	root, local, client := pushFixture(t)
	writeRepoFile(t, root, "empty.txt", "")
	local.staged = []string{"empty.txt"}
	pusher := newTestPush(t, local, client)
	prog := &stubProgress{}

	require.NoError(t, pusher.Run(context.Background(), root, "add empty", prog))

	require.Equal(t, []progressStart{{objects: 0, bytes: 0}}, prog.starts)
	require.True(t, prog.endCalled)
}
