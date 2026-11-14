package daemon

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/client/usecase"
)

type blameTestStream struct {
	grpc.ServerStream
	ctx  context.Context
	mu   sync.Mutex
	sent []*daemonpb.BlameEvent
}

func (s *blameTestStream) Context() context.Context { return s.ctx }

func (s *blameTestStream) Send(ev *daemonpb.BlameEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, ev)
	return nil
}

type stubBlamer struct {
	res   *usecase.BlameResult
	err   error
	calls []string
	ctx   func()
}

func (s *stubBlamer) Run(_ context.Context, root, path, rev string, progress usecase.BlameProgress) (*usecase.BlameResult, error) {
	s.calls = append(s.calls, path+"@"+rev)
	progress(1, 2)
	progress(2, 2)
	if s.ctx != nil {
		s.ctx()
	}
	return s.res, s.err
}

func TestServer_BlameStreamsProgressThenResult(t *testing.T) {
	root := newTestClone(t)
	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	blamer := &stubBlamer{res: &usecase.BlameResult{
		Path:     "tracked.txt",
		Revision: "abc",
		Lines:    []usecase.BlameLine{{Text: "a", CommitID: "c1"}, {Text: "b", Uncommitted: true}},
		Commits:  []usecase.BlameCommit{{ID: "c1", Hash: "ff", AuthorName: "Ana", AuthorEmail: "ana@x", Message: "m", CreatedAt: when}},
	}}
	srv := newOpServer(t, RepoOps{Blame: SerialBlame(blamer)})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)

	stream := &blameTestStream{ctx: context.Background()}
	require.NoError(t, srv.Blame(&daemonpb.BlameRequest{Root: root, Path: "tracked.txt", Revision: "abc"}, stream))
	require.Len(t, stream.sent, 3)
	require.EqualValues(t, 1, stream.sent[0].GetProgress().GetDone())
	require.EqualValues(t, 2, stream.sent[1].GetProgress().GetTotal())
	res := stream.sent[2].GetResult()
	require.Equal(t, "tracked.txt", res.GetPath())
	require.Equal(t, "abc", res.GetRevision())
	require.Len(t, res.GetLines(), 2)
	require.Equal(t, "c1", res.GetLines()[0].GetCommitId())
	require.True(t, res.GetLines()[1].GetUncommitted())
	require.Equal(t, "2026-01-02T03:04:05Z", res.GetCommits()[0].GetCreatedAt())
	require.Equal(t, "Ana", res.GetCommits()[0].GetAuthorName())
	require.Equal(t, []string{"tracked.txt@abc"}, blamer.calls)
}

func TestServer_BlameFailures(t *testing.T) {
	root := newTestClone(t)
	blamer := &stubBlamer{err: clientDomain.NewUserError("not a text file")}
	srv := newOpServer(t, RepoOps{Blame: blamer})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)

	stream := &blameTestStream{ctx: context.Background()}
	require.NoError(t, srv.Blame(&daemonpb.BlameRequest{Root: root, Path: "img.png"}, stream))
	last := stream.sent[len(stream.sent)-1]
	require.EqualValues(t, 400, last.GetFailure().GetCode())

	ctx, cancel := context.WithCancel(context.Background())
	blamer.err = errors.New("cancelled")
	blamer.ctx = cancel
	stream = &blameTestStream{ctx: ctx}
	require.ErrorIs(t, srv.Blame(&daemonpb.BlameRequest{Root: root, Path: "a"}, stream), context.Canceled)

	stream = &blameTestStream{ctx: context.Background()}
	require.NoError(t, srv.Blame(&daemonpb.BlameRequest{Root: t.TempDir(), Path: "a"}, stream))
	require.NotNil(t, stream.sent[0].GetFailure())

	stream = &blameTestStream{ctx: ctx}
	require.NoError(t, srv.Blame(&daemonpb.BlameRequest{Root: root, Path: "a"}, stream))
	require.EqualValues(t, 499, stream.sent[0].GetFailure().GetCode())
}

func TestServer_BlameWithoutRunner(t *testing.T) {
	root := newTestClone(t)
	srv := newOpServer(t, RepoOps{})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)

	stream := &blameTestStream{ctx: context.Background()}
	require.NoError(t, srv.Blame(&daemonpb.BlameRequest{Root: root, Path: "a"}, stream))
	require.EqualValues(t, 409, stream.sent[0].GetFailure().GetCode())
}
