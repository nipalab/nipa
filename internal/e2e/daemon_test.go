package e2e

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	"github.com/nipalab/nipa/internal/client/daemon"
	clientgrpc "github.com/nipalab/nipa/internal/client/grpc"
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/client/localrepo"
	clientusecase "github.com/nipalab/nipa/internal/client/usecase"
)

// startDaemon boots an in-process `nipa serve` over the test's client stack
// and returns a token-authenticated daemon client.
func startDaemon(t *testing.T, auth *clientusecase.Auth, client *clientgrpc.Client) (daemonpb.NipaDaemonClient, context.Context) {
	t.Helper()
	srv, err := daemon.NewServer(daemon.Options{
		EndpointPath: filepath.Join(t.TempDir(), "daemon.json"),
		Login:        auth.LoginWithUsernamePassword,
		Runners: daemon.Runners{New: func(string) daemon.RepoOps {
			push := clientusecase.NewPush(auth, client, localrepo.NewLocalRepo())
			return daemon.RepoOps{
				Update: clientusecase.NewUpdate(auth, client, localrepo.NewLocalRepo()),
				Push:   push,
				Merge:  clientusecase.NewMerge(auth, client, localrepo.NewLocalRepo(), push),
				Revert: clientusecase.NewRevert(auth, client, localrepo.NewLocalRepo(), push),
				Diff:   clientusecase.NewDiff(auth, client, localrepo.NewLocalRepo()),
				Proxy:  client,
			}
		}},
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	select {
	case <-srv.Ready():
	case err := <-done:
		t.Fatalf("daemon exited before ready: %v", err)
	}

	conn, err := grpc.NewClient(
		fmt.Sprintf("127.0.0.1:%d", srv.Endpoint().Port),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = conn.Close()
		srv.Stop()
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("daemon did not stop")
		}
	})

	dctx := metadata.AppendToOutgoingContext(context.Background(), daemon.TokenHeader, srv.Endpoint().Token)
	return daemonpb.NewNipaDaemonClient(conn), dctx
}

type opEventStream interface {
	Recv() (*daemonpb.OpEvent, error)
}

func collectOpEvents(t *testing.T, stream opEventStream) []*daemonpb.OpEvent {
	t.Helper()
	var events []*daemonpb.OpEvent
	for {
		ev, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		require.NotNil(t, ev)
		events = append(events, ev)
	}
	require.NotEmpty(t, events)
	return events
}

func terminalResult(t *testing.T, events []*daemonpb.OpEvent) *daemonpb.OpResult {
	t.Helper()
	last := events[len(events)-1]
	require.NotNil(t, last.GetResult(), "stream must end with a result, got %v", last)
	return last.GetResult()
}

func firstEventIs(t *testing.T, events []*daemonpb.OpEvent, phase string) {
	t.Helper()
	require.Equal(t, phase, events[0].GetStarted().GetPhase())
}

func progressPhases(events []*daemonpb.OpEvent, phase string) (objects, bytes int64) {
	for _, ev := range events {
		p := ev.GetProgress()
		if p == nil || p.GetPhase() != phase {
			continue
		}
		if p.GetObjectsTotal() > objects {
			objects = p.GetObjectsTotal()
		}
		if p.GetBytesTotal() > bytes {
			bytes = p.GetBytesTotal()
		}
	}
	return objects, bytes
}

func collectDiffData(t *testing.T, stream daemonpb.NipaDaemon_DiffClient) string {
	t.Helper()
	var b strings.Builder
	for {
		ev, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		require.Nil(t, ev.GetFailure(), "unexpected diff failure: %v", ev.GetFailure())
		b.Write(ev.GetData())
	}
	return b.String()
}

func TestEndToEnd_DaemonUpdateStagePush(t *testing.T) {
	ctx := context.Background()
	host := startTestServer(t, openTestDB(t))

	store := newMemoryStore()
	transport := clientgrpc.NewTransport()
	session := clientusecase.NewSession(store, transport, failPrompt{})
	grpcClient := clientgrpc.NewClient(transport, session)
	auth := clientusecase.NewAuth(grpcClient, store, failPrompt{})
	require.NoError(t, grpcClient.Connect(ctx, host))
	loginResult, err := grpcClient.LoginWithUsernamePassword(ctx, host, e2eSuperAdminEmail, e2eSuperAdminPass)
	require.NoError(t, err)
	require.NoError(t, store.SaveToken(loginResult))

	url := "http://" + host + "/" + e2eOrgSlug + "/" + e2eProjectSlug
	repoUc := clientusecase.NewRepo(auth, grpcClient, localrepo.NewLocalRepo())

	// The daemon-served clone is created before the seed push so the first
	// update has real content to fetch.
	target := filepath.Join(t.TempDir(), "work")
	require.NoError(t, repoUc.Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", nil, target))

	seed := filepath.Join(t.TempDir(), "seed")
	require.NoError(t, repoUc.Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", nil, seed))
	writeFile(t, seed, "a.txt", "hello from the seed\n")
	stagePath(t, seed, "a.txt")
	seedPush := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())
	require.NoError(t, seedPush.Run(ctx, seed, "seed a.txt"))

	client, dctx := startDaemon(t, auth, grpcClient)
	_, err = client.WatchRepo(dctx, &daemonpb.WatchRepoRequest{Root: target})
	require.NoError(t, err)

	// Update through the daemon streams progress and materializes the seed.
	updateStream, err := client.Update(dctx, &daemonpb.UpdateRequest{Root: target})
	require.NoError(t, err)
	updateEvents := collectOpEvents(t, updateStream)
	firstEventIs(t, updateEvents, "update")
	sync := terminalResult(t, updateEvents).GetSync()
	require.Equal(t, "main", sync.GetBranch())
	require.NotEmpty(t, sync.GetCommitId())
	downloadObjects, _ := progressPhases(updateEvents, "download")
	require.Greater(t, downloadObjects, int64(0), "the update must stream download progress")
	assertFileContent(t, target, "a.txt", "hello from the seed\n")

	// Stage and push through the daemon.
	writeFile(t, target, "a.txt", "edited locally\n")
	writeFile(t, target, "b.txt", "new file\n")
	st, err := client.Stage(dctx, &daemonpb.StageRequest{Root: target, Add: []string{"a.txt", "b.txt"}})
	require.NoError(t, err)
	require.Equal(t, []string{"a.txt", "b.txt"}, st.GetStaged())

	pushStream, err := client.Push(dctx, &daemonpb.PushRequest{Root: target, Message: "daemon push"})
	require.NoError(t, err)
	pushEvents := collectOpEvents(t, pushStream)
	firstEventIs(t, pushEvents, "push")
	push := terminalResult(t, pushEvents).GetPush()
	require.NotEmpty(t, push.GetCommitId())
	require.NotEmpty(t, push.GetCommitHash())
	require.NotEmpty(t, push.GetTreeHash())
	uploadObjects, uploadBytes := progressPhases(pushEvents, "upload")
	require.Greater(t, uploadObjects, int64(0), "the push must stream upload progress")
	require.Greater(t, uploadBytes, int64(0), "the push must stream uploaded byte counts")

	head, err := grpcClient.GetBranchByName(ctx, e2eOrgSlug, e2eProjectSlug, "main")
	require.NoError(t, err)
	require.NotNil(t, head.CommitID)
	require.Equal(t, head.CommitID.Base36(), push.GetCommitId(), "the daemon push must move the server head")

	// A later server-side change flows back through a second daemon update.
	// The seed clone syncs first: the daemon push moved the branch head.
	seedUpdate := clientusecase.NewUpdate(auth, grpcClient, localrepo.NewLocalRepo())
	require.NoError(t, seedUpdate.Run(ctx, seed))
	writeFile(t, seed, "c.txt", "third\n")
	stagePath(t, seed, "c.txt")
	require.NoError(t, seedPush.Run(ctx, seed, "add c.txt"))

	updateStream, err = client.Update(dctx, &daemonpb.UpdateRequest{Root: target})
	require.NoError(t, err)
	updateEvents = collectOpEvents(t, updateStream)
	require.NotNil(t, terminalResult(t, updateEvents).GetSync().GetCommitId())
	assertFileContent(t, target, "c.txt", "third\n")

	// Diff through the daemon: patch of the working tree against the snapshot.
	writeFile(t, target, "a.txt", "edited after push\n")
	diffStream, err := client.Diff(dctx, &daemonpb.DiffRequest{Root: target})
	require.NoError(t, err)
	patch := collectDiffData(t, diffStream)
	require.Contains(t, patch, "diff --nipa a/a.txt b/a.txt")
	require.Contains(t, patch, "+edited after push")

	// Staged diff in the name_status format.
	writeFile(t, target, "d.txt", "staged file\n")
	_, err = client.Stage(dctx, &daemonpb.StageRequest{Root: target, Add: []string{"a.txt", "d.txt"}})
	require.NoError(t, err)
	diffStream, err = client.Diff(dctx, &daemonpb.DiffRequest{Root: target, Staged: true, Format: "name_status"})
	require.NoError(t, err)
	staged := collectDiffData(t, diffStream)
	require.Contains(t, staged, "M\ta.txt")
	require.Contains(t, staged, "A\td.txt")

	// Two identical revisions produce an empty diff.
	headAfter, err := grpcClient.GetBranchByName(ctx, e2eOrgSlug, e2eProjectSlug, "main")
	require.NoError(t, err)
	require.NotNil(t, headAfter.CommitID)
	diffStream, err = client.Diff(dctx, &daemonpb.DiffRequest{
		Root:      target,
		Revisions: []string{headAfter.CommitID.Base36(), headAfter.CommitID.Base36()},
	})
	require.NoError(t, err)
	require.Empty(t, collectDiffData(t, diffStream))

	// Unwatching drops the root from the daemon registry.
	_, err = client.UnwatchRepo(dctx, &daemonpb.UnwatchRepoRequest{Root: target})
	require.NoError(t, err)
	repos, err := client.ListRepos(dctx, &daemonpb.ListReposRequest{})
	require.NoError(t, err)
	require.Empty(t, repos.GetRepos())
}
