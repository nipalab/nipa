package daemon

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/diff"
)

type stubDiffRunner struct {
	run func(ctx context.Context, root string, revs []string, opts usecase.DiffOptions) ([]diff.FileDiff, error)
}

func (s *stubDiffRunner) Run(ctx context.Context, root string, revs []string, opts usecase.DiffOptions) ([]diff.FileDiff, error) {
	if s.run != nil {
		return s.run(ctx, root, revs, opts)
	}
	return nil, nil
}

// diffTestStream records the events a Diff handler sends. failAt, when
// positive, makes that 1-based send fail.
type diffTestStream struct {
	grpc.ServerStream
	ctx    context.Context
	failAt int

	mu     sync.Mutex
	events []*daemonpb.DiffEvent
}

func (s *diffTestStream) Context() context.Context {
	return s.ctx
}

func (s *diffTestStream) Send(ev *daemonpb.DiffEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failAt > 0 && len(s.events)+1 == s.failAt {
		return errors.New("stream closed")
	}
	s.events = append(s.events, ev)
	return nil
}

func (s *diffTestStream) output() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var b strings.Builder
	for _, ev := range s.events {
		b.Write(ev.GetData())
	}
	return b.String()
}

func (s *diffTestStream) failure() *daemonpb.OpFailure {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.events) == 0 {
		return nil
	}
	return s.events[len(s.events)-1].GetFailure()
}

func (s *diffTestStream) eventCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.events)
}

func addedFileDiff(path, content string) diff.FileDiff {
	return diff.FileDiff{
		Change: diff.Change{
			Path:   path,
			Status: diff.Added,
			New:    diff.Entry{Path: path, Mode: 2, SizeBytes: int64(len(content))},
		},
		New: []byte(content),
	}
}

func runDiff(t *testing.T, srv *Server, req *daemonpb.DiffRequest) *diffTestStream {
	t.Helper()
	stream := &diffTestStream{ctx: context.Background()}
	require.NoError(t, srv.Diff(req, stream))
	return stream
}

func TestServer_DiffPatchStreamsAPatch(t *testing.T) {
	root := newTestClone(t)
	var gotRevs []string
	var gotOpts usecase.DiffOptions
	srv := newOpServer(t, RepoOps{Diff: &stubDiffRunner{run: func(_ context.Context, _ string, revs []string, opts usecase.DiffOptions) ([]diff.FileDiff, error) {
		gotRevs, gotOpts = revs, opts
		return []diff.FileDiff{addedFileDiff("a.txt", "hello\n")}, nil
	}}})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)

	stream := runDiff(t, srv, &daemonpb.DiffRequest{
		Root:    root,
		Paths:   []string{"a.txt"},
		Staged:  true,
		NoCache: true,
		Context: 5,
		Format:  "",
	})
	require.Equal(t, []string{"a.txt"}, gotOpts.Paths)
	require.True(t, gotOpts.Staged)
	require.True(t, gotOpts.NoCache)
	require.Empty(t, gotRevs)
	require.Contains(t, stream.output(), "diff --nipa a/a.txt b/a.txt")
	require.Contains(t, stream.output(), "new file mode 100644")
	require.Contains(t, stream.output(), "+hello")
	require.Nil(t, stream.failure())
}

func TestServer_DiffFormats(t *testing.T) {
	root := newTestClone(t)
	srv := newOpServer(t, RepoOps{Diff: &stubDiffRunner{run: func(context.Context, string, []string, usecase.DiffOptions) ([]diff.FileDiff, error) {
		return []diff.FileDiff{addedFileDiff("a.txt", "hello\n")}, nil
	}}})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)

	tests := []struct {
		format string
		want   string
	}{
		{"stat", " a.txt | 1 +\n"},
		{"stat", "1 file changed, 1 insertion(+), 0 deletions(-)"},
		{"name_only", "a.txt\n"},
		{"name_status", "A\ta.txt\n"},
	}
	for _, tt := range tests {
		t.Run(tt.format+"/"+tt.want[:4], func(t *testing.T) {
			stream := runDiff(t, srv, &daemonpb.DiffRequest{Root: root, Format: tt.format})
			require.Contains(t, stream.output(), tt.want)
		})
	}
}

func TestServer_DiffExpandsRevisionRanges(t *testing.T) {
	root := newTestClone(t)
	var gotRevs []string
	var gotMergeBase bool
	srv := newOpServer(t, RepoOps{Diff: &stubDiffRunner{run: func(_ context.Context, _ string, revs []string, opts usecase.DiffOptions) ([]diff.FileDiff, error) {
		gotRevs, gotMergeBase = revs, opts.MergeBase
		return nil, nil
	}}})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)

	runDiff(t, srv, &daemonpb.DiffRequest{Root: root, Revisions: []string{"a..b"}})
	require.Equal(t, []string{"a", "b"}, gotRevs)
	require.False(t, gotMergeBase)

	runDiff(t, srv, &daemonpb.DiffRequest{Root: root, Revisions: []string{"a...b"}})
	require.Equal(t, []string{"a", "b"}, gotRevs)
	require.True(t, gotMergeBase)

	runDiff(t, srv, &daemonpb.DiffRequest{Root: root, Revisions: []string{"a", "b"}, MergeBase: true})
	require.Equal(t, []string{"a", "b"}, gotRevs)
	require.True(t, gotMergeBase)
}

func TestServer_DiffValidation(t *testing.T) {
	root := newTestClone(t)
	srv := newOpServer(t, RepoOps{Diff: &stubDiffRunner{}})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)

	tests := []struct {
		name string
		req  *daemonpb.DiffRequest
		code int32
	}{
		{"unknown format", &daemonpb.DiffRequest{Root: root, Format: "xml"}, 400},
		{"negative context", &daemonpb.DiffRequest{Root: root, Context: -1}, 400},
		{"staged with revisions", &daemonpb.DiffRequest{Root: root, Staged: true, Revisions: []string{"main"}}, 400},
		{"merge base with one revision", &daemonpb.DiffRequest{Root: root, MergeBase: true, Revisions: []string{"main"}}, 400},
		{"bad range", &daemonpb.DiffRequest{Root: root, Revisions: []string{"a.."}}, 400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stream := runDiff(t, srv, tt.req)
			require.NotNil(t, stream.failure())
			require.Equal(t, tt.code, stream.failure().GetCode())
		})
	}
}

func TestServer_DiffUnwatchedRoot(t *testing.T) {
	srv := newOpServer(t, RepoOps{})
	stream := runDiff(t, srv, &daemonpb.DiffRequest{Root: newTestClone(t)})
	require.NotNil(t, stream.failure())
	require.EqualValues(t, 400, stream.failure().GetCode())
	require.Contains(t, stream.failure().GetMessage(), "not watched")
}

func TestServer_DiffWithoutRunner(t *testing.T) {
	root := newTestClone(t)
	srv := newOpServer(t, RepoOps{})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)

	stream := runDiff(t, srv, &daemonpb.DiffRequest{Root: root})
	require.NotNil(t, stream.failure())
	require.EqualValues(t, 409, stream.failure().GetCode())
	require.Contains(t, stream.failure().GetMessage(), "not configured")
}

func TestServer_DiffRunnerFailureIsMapped(t *testing.T) {
	root := newTestClone(t)
	srv := newOpServer(t, RepoOps{Diff: &stubDiffRunner{run: func(context.Context, string, []string, usecase.DiffOptions) ([]diff.FileDiff, error) {
		return nil, clientDomain.NewTokenError("bad credentials")
	}}})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)

	stream := runDiff(t, srv, &daemonpb.DiffRequest{Root: root})
	require.NotNil(t, stream.failure())
	require.EqualValues(t, 401, stream.failure().GetCode())
	require.Equal(t, "bad credentials", stream.failure().GetMessage())
}

func TestSendDiffLinesChunks(t *testing.T) {
	stream := &diffTestStream{ctx: context.Background()}
	line := strings.Repeat("x", 4096)
	lines := make([]string, 40) // ~160 KiB: at least three chunks
	for i := range lines {
		lines[i] = line
	}
	require.NoError(t, sendDiffLines(stream, lines))
	require.Greater(t, stream.eventCount(), 1, "large output must be split into chunks")

	out := stream.output()
	require.Len(t, out, len(lines)*(len(line)+1))
	require.Equal(t, strings.Repeat(line+"\n", len(lines)), out)
}

func TestSendDiffLinesEmpty(t *testing.T) {
	stream := &diffTestStream{ctx: context.Background()}
	require.NoError(t, sendDiffLines(stream, nil))
	require.Zero(t, stream.eventCount())
}

func TestSendDiffLinesChunkSendFailure(t *testing.T) {
	stream := &diffTestStream{ctx: context.Background(), failAt: 1}
	line := strings.Repeat("x", 4096)
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = line
	}
	require.Error(t, sendDiffLines(stream, lines))
}

func TestServer_DiffSendFailure(t *testing.T) {
	root := newTestClone(t)
	srv := newOpServer(t, RepoOps{Diff: &stubDiffRunner{run: func(context.Context, string, []string, usecase.DiffOptions) ([]diff.FileDiff, error) {
		return []diff.FileDiff{addedFileDiff("a.txt", "hello\n")}, nil
	}}})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)

	stream := &diffTestStream{ctx: context.Background(), failAt: 1}
	require.Error(t, srv.Diff(&daemonpb.DiffRequest{Root: root}, stream))
}

func TestServer_DiffEmptyResult(t *testing.T) {
	root := newTestClone(t)
	srv := newOpServer(t, RepoOps{Diff: &stubDiffRunner{}})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)

	stream := runDiff(t, srv, &daemonpb.DiffRequest{Root: root})
	require.Zero(t, stream.eventCount())
	require.Nil(t, stream.failure())
}

func TestServer_DiffCancelledWhileRunning(t *testing.T) {
	root := newTestClone(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := newOpServer(t, RepoOps{Diff: &stubDiffRunner{run: func(context.Context, string, []string, usecase.DiffOptions) ([]diff.FileDiff, error) {
		cancel() // the client goes away while the runner works
		return []diff.FileDiff{addedFileDiff("a.txt", "hello\n")}, nil
	}}})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)

	stream := &diffTestStream{ctx: ctx}
	require.ErrorIs(t, srv.Diff(&daemonpb.DiffRequest{Root: root}, stream), context.Canceled)
}

func TestServer_DiffAcquireCancel(t *testing.T) {
	root := newTestClone(t)
	srv := newOpServer(t, RepoOps{Diff: &stubDiffRunner{}})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stream := &diffTestStream{ctx: ctx}
	require.NoError(t, srv.Diff(&daemonpb.DiffRequest{Root: root}, stream))
	require.NotNil(t, stream.failure())
	require.EqualValues(t, 499, stream.failure().GetCode())
}

func TestServer_DiffRangeValidation(t *testing.T) {
	root := newTestClone(t)
	srv := newOpServer(t, RepoOps{Diff: &stubDiffRunner{}})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)

	for _, token := range []string{"a...", "...b", "..b"} {
		stream := runDiff(t, srv, &daemonpb.DiffRequest{Root: root, Revisions: []string{token}})
		require.NotNil(t, stream.failure(), token)
		require.EqualValues(t, 400, stream.failure().GetCode(), token)
	}
}
