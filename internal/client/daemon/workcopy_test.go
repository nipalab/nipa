package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
)

func writeWorkingFile(t *testing.T, root, rel, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), []byte(content), 0o644))
}

func TestServer_WatchRepoRejectsNonRepo(t *testing.T) {
	srv := startTestServer(t, Options{})
	ctx := authed(context.Background(), srv.Endpoint().Token)

	_, err := srv.client.WatchRepo(ctx, &daemonpb.WatchRepoRequest{Root: t.TempDir()})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestServer_UnwatchRepoRejectsMissingRoot(t *testing.T) {
	srv := startTestServer(t, Options{})
	ctx := authed(context.Background(), srv.Endpoint().Token)

	_, err := srv.client.UnwatchRepo(ctx, &daemonpb.UnwatchRepoRequest{})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Contains(t, status.Convert(err).Message(), "required")
}

func TestServer_StageUnstageRejectsEscapingPath(t *testing.T) {
	root := newTestClone(t)
	srv := startTestServer(t, Options{})
	ctx := authed(context.Background(), srv.Endpoint().Token)
	_, err := srv.client.WatchRepo(ctx, &daemonpb.WatchRepoRequest{Root: root})
	require.NoError(t, err)

	_, err = srv.client.Stage(ctx, &daemonpb.StageRequest{Root: root, Unstage: []string{"../escape"}})
	require.NotEqual(t, codes.OK, status.Code(err))
	require.Contains(t, status.Convert(err).Message(), "outside the repository")
}

func TestServer_StatusAndStageSurfaceHandleErrors(t *testing.T) {
	root := newTestClone(t)
	srv := startTestServer(t, Options{})
	ctx := authed(context.Background(), srv.Endpoint().Token)
	_, err := srv.client.WatchRepo(ctx, &daemonpb.WatchRepoRequest{Root: root})
	require.NoError(t, err)

	rp, unref, err := srv.repos.ref(root)
	require.NoError(t, err)
	defer unref()
	require.NoError(t, rp.localRepo.Close(), "break the handle on purpose")

	_, err = srv.client.Status(ctx, &daemonpb.StatusRequest{Root: root})
	require.NotEqual(t, codes.OK, status.Code(err))

	_, err = srv.client.Stage(ctx, &daemonpb.StageRequest{Root: root})
	require.NotEqual(t, codes.OK, status.Code(err))
}

func TestServer_WatchRepoResolvesSubdirectory(t *testing.T) {
	root := newTestClone(t)
	sub := filepath.Join(root, "assets")
	require.NoError(t, os.MkdirAll(sub, 0o755))

	srv := startTestServer(t, Options{})
	ctx := authed(context.Background(), srv.Endpoint().Token)

	res, err := srv.client.WatchRepo(ctx, &daemonpb.WatchRepoRequest{Root: sub})
	require.NoError(t, err)
	require.Equal(t, root, res.GetRepo().GetRoot())
	require.Equal(t, "main", res.GetRepo().GetBranch())
	require.Equal(t, "https://nipa.example.com/default/default", res.GetRepo().GetUrl())
}

func TestServer_StatusStageFlow(t *testing.T) {
	root := newTestClone(t)
	srv := startTestServer(t, Options{})
	ctx := authed(context.Background(), srv.Endpoint().Token)

	_, err := srv.client.WatchRepo(ctx, &daemonpb.WatchRepoRequest{Root: root})
	require.NoError(t, err)

	st, err := srv.client.Status(ctx, &daemonpb.StatusRequest{Root: root})
	require.NoError(t, err)
	require.Equal(t, []string{"tracked.txt"}, st.GetMissing(), "a tracked file absent from disk is missing")

	writeWorkingFile(t, root, "tracked.txt", "hello")
	writeWorkingFile(t, root, "untracked.txt", "new")

	st, err = srv.client.Status(ctx, &daemonpb.StatusRequest{Root: root})
	require.NoError(t, err)
	require.Equal(t, []string{"tracked.txt"}, st.GetModified())
	require.Equal(t, []string{"untracked.txt"}, st.GetUntracked())
	require.Empty(t, st.GetMissing())

	st, err = srv.client.Status(ctx, &daemonpb.StatusRequest{Root: root, NoCache: true})
	require.NoError(t, err)
	require.Equal(t, []string{"tracked.txt"}, st.GetModified(), "no_cache returns the same answer")

	st, err = srv.client.Stage(ctx, &daemonpb.StageRequest{
		Root: root,
		Add:  []string{"tracked.txt", "untracked.txt"},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"tracked.txt", "untracked.txt"}, st.GetStaged())
	require.Empty(t, st.GetModified())
	require.Empty(t, st.GetUntracked())

	st, err = srv.client.Stage(ctx, &daemonpb.StageRequest{
		Root:    root,
		Unstage: []string{"untracked.txt"},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"tracked.txt"}, st.GetStaged())
	require.Equal(t, []string{"untracked.txt"}, st.GetUntracked())
}

func TestServer_StageRejectsMissingPath(t *testing.T) {
	root := newTestClone(t)
	srv := startTestServer(t, Options{})
	ctx := authed(context.Background(), srv.Endpoint().Token)

	_, err := srv.client.WatchRepo(ctx, &daemonpb.WatchRepoRequest{Root: root})
	require.NoError(t, err)

	_, err = srv.client.Stage(ctx, &daemonpb.StageRequest{Root: root, Add: []string{"nope.txt"}})
	require.NotEqual(t, codes.OK, status.Code(err))
	require.Contains(t, status.Convert(err).Message(), "nope.txt")
}

func TestServer_StatusRequiresWatch(t *testing.T) {
	srv := startTestServer(t, Options{})
	ctx := authed(context.Background(), srv.Endpoint().Token)

	_, err := srv.client.Status(ctx, &daemonpb.StatusRequest{Root: newTestClone(t)})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Contains(t, status.Convert(err).Message(), "not watched")
}

func TestServer_UnwatchRepo(t *testing.T) {
	root := newTestClone(t)
	srv := startTestServer(t, Options{})
	ctx := authed(context.Background(), srv.Endpoint().Token)

	_, err := srv.client.WatchRepo(ctx, &daemonpb.WatchRepoRequest{Root: root})
	require.NoError(t, err)

	_, err = srv.client.UnwatchRepo(ctx, &daemonpb.UnwatchRepoRequest{Root: root})
	require.NoError(t, err)

	repos, err := srv.client.ListRepos(ctx, &daemonpb.ListReposRequest{})
	require.NoError(t, err)
	require.Empty(t, repos.GetRepos())

	_, err = srv.client.Status(ctx, &daemonpb.StatusRequest{Root: root})
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	_, err = srv.client.UnwatchRepo(ctx, &daemonpb.UnwatchRepoRequest{Root: root})
	require.NoError(t, err, "unwatching twice is a no-op")
}

func TestServer_ListRepos(t *testing.T) {
	rootA := newTestClone(t)
	rootB := newTestClone(t)
	srv := startTestServer(t, Options{})
	ctx := authed(context.Background(), srv.Endpoint().Token)

	for _, root := range []string{rootB, rootA} {
		_, err := srv.client.WatchRepo(ctx, &daemonpb.WatchRepoRequest{Root: root})
		require.NoError(t, err)
	}

	res, err := srv.client.ListRepos(ctx, &daemonpb.ListReposRequest{})
	require.NoError(t, err)
	require.Len(t, res.GetRepos(), 2)
	require.Less(t, res.GetRepos()[0].GetRoot(), res.GetRepos()[1].GetRoot())
}
