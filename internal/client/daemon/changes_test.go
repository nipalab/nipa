package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
)

func TestChangeHub_CoalescesUntilRead(t *testing.T) {
	hub := newChangeHub()
	sub, unsubscribe := hub.subscribe()
	defer unsubscribe()

	require.Nil(t, sub.take(), "nothing pending yet")
	hub.publish([]string{"b.txt", "a.txt"})
	hub.publish([]string{"a.txt"})
	require.Len(t, sub.signal, 1)
	require.Equal(t, []string{"a.txt", "b.txt"}, sub.take().GetPaths())
	require.Nil(t, sub.take())

	hub.publish([]string{"a.txt"})
	hub.publish(nil)
	hub.publish([]string{"c.txt"})
	require.Empty(t, sub.take().GetPaths(), "a refresh-everything notice absorbs path notices")

	many := make([]string, maxChangePaths+1)
	for i := range many {
		many[i] = fmt.Sprintf("f%04d", i)
	}
	hub.publish(many)
	change := sub.take()
	require.NotNil(t, change)
	require.Empty(t, change.GetPaths(), "an oversized batch collapses into a full refresh")
}

func TestChangeHub_CloseEndsSubscribers(t *testing.T) {
	hub := newChangeHub()
	sub, unsubscribe := hub.subscribe()
	hub.close()
	<-sub.done
	unsubscribe()
	hub.publish([]string{"ignored"})

	late, unsubscribeLate := hub.subscribe()
	<-late.done
	unsubscribeLate()
}

func TestPumpChanges(t *testing.T) {
	hub := newChangeHub()
	sub, unsubscribe := hub.subscribe()
	defer unsubscribe()
	hub.publish([]string{"a"})

	sendErr := errors.New("gone")
	err := pumpChanges(context.Background(), nil, sub, func(*daemonpb.RepoChange) error { return sendErr })
	require.ErrorIs(t, err, sendErr)

	stop := make(chan struct{})
	close(stop)
	require.NoError(t, pumpChanges(context.Background(), stop, sub, nil))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.NoError(t, pumpChanges(ctx, nil, sub, nil))

	sub.signal <- struct{}{}
	go unsubscribe()
	require.NoError(t, pumpChanges(context.Background(), nil, sub, func(*daemonpb.RepoChange) error {
		t.Fatal("a spurious signal without pending changes sends nothing")
		return nil
	}))
}

func recvChange(t *testing.T, stream daemonpb.NipaDaemon_WatchChangesClient) *daemonpb.RepoChange {
	t.Helper()
	type result struct {
		change *daemonpb.RepoChange
		err    error
	}
	got := make(chan result, 1)
	go func() {
		change, err := stream.Recv()
		got <- result{change, err}
	}()
	select {
	case r := <-got:
		require.NoError(t, r.err)
		return r.change
	case <-time.After(5 * time.Second):
		t.Fatal("no change notice")
		return nil
	}
}

func TestServer_WatchChangesStreamsWorkingCopyChanges(t *testing.T) {
	root := newTestClone(t)
	srv := startTestServer(t, Options{})
	ctx := authed(context.Background(), srv.Endpoint().Token)
	_, err := srv.client.WatchRepo(ctx, &daemonpb.WatchRepoRequest{Root: root})
	require.NoError(t, err)

	stream, err := srv.client.WatchChanges(ctx, &daemonpb.WatchChangesRequest{Root: root})
	require.NoError(t, err)
	_, err = stream.Header()
	require.NoError(t, err)

	writeWorkingFile(t, root, "fresh.txt", "new")
	require.Contains(t, recvChange(t, stream).GetPaths(), "fresh.txt")

	_, err = srv.client.Stage(ctx, &daemonpb.StageRequest{Root: root, Add: []string{"fresh.txt"}})
	require.NoError(t, err)
	for len(recvChange(t, stream).GetPaths()) > 0 {
	}

	_, err = srv.client.UnwatchRepo(ctx, &daemonpb.UnwatchRepoRequest{Root: root})
	require.NoError(t, err)
	for {
		_, err = stream.Recv()
		if err != nil {
			break
		}
	}
	require.ErrorIs(t, err, io.EOF, "unwatching ends the stream")
}

func TestServer_WatchChangesRejectsUnwatchedRoot(t *testing.T) {
	srv := startTestServer(t, Options{})
	ctx := authed(context.Background(), srv.Endpoint().Token)
	stream, err := srv.client.WatchChanges(ctx, &daemonpb.WatchChangesRequest{Root: newTestClone(t)})
	require.NoError(t, err)
	_, err = stream.Recv()
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestServer_WatchChangesEndsOnShutdown(t *testing.T) {
	root := newTestClone(t)
	srv := startTestServer(t, Options{})
	ctx := authed(context.Background(), srv.Endpoint().Token)
	_, err := srv.client.WatchRepo(ctx, &daemonpb.WatchRepoRequest{Root: root})
	require.NoError(t, err)
	stream, err := srv.client.WatchChanges(ctx, &daemonpb.WatchChangesRequest{Root: root})
	require.NoError(t, err)
	_, err = stream.Header()
	require.NoError(t, err)

	_, err = srv.client.Shutdown(ctx, &daemonpb.ShutdownRequest{})
	require.NoError(t, err)
	_, err = stream.Recv()
	require.ErrorIs(t, err, io.EOF, "an open stream must not block a graceful stop")
}

func TestServer_OperationPublishesFullRefresh(t *testing.T) {
	root := newTestClone(t)
	srv := newOpServer(t, RepoOps{Update: &stubUpdateRunner{}})
	rp, err := srv.repos.watch(root)
	require.NoError(t, err)
	sub, unsubscribe := rp.changes.subscribe()
	defer unsubscribe()

	stream := &opTestStream{ctx: context.Background()}
	require.NoError(t, srv.Update(&daemonpb.UpdateRequest{Root: root}, stream))
	change := sub.take()
	require.NotNil(t, change)
	require.Empty(t, change.GetPaths())
}
