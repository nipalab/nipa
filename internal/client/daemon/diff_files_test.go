package daemon

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/diff"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

type diffFilesTestStream struct {
	grpc.ServerStream
	ctx    context.Context
	failAt int

	mu     sync.Mutex
	events []*daemonpb.DiffFileEvent
}

func (s *diffFilesTestStream) Context() context.Context { return s.ctx }

func (s *diffFilesTestStream) Send(ev *daemonpb.DiffFileEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failAt > 0 && len(s.events)+1 == s.failAt {
		return errors.New("stream closed")
	}
	s.events = append(s.events, ev)
	return nil
}

func (s *diffFilesTestStream) files() []*daemonpb.DiffFile {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*daemonpb.DiffFile
	for _, ev := range s.events {
		if f := ev.GetFile(); f != nil {
			out = append(out, f)
		}
	}
	return out
}

func (s *diffFilesTestStream) failure() *daemonpb.OpFailure {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.events) == 0 {
		return nil
	}
	return s.events[len(s.events)-1].GetFailure()
}

func diffFilesServer(t *testing.T, files ...diff.FileDiff) (*Server, string, *usecase.DiffOptions) {
	t.Helper()
	root := newTestClone(t)
	var gotOpts usecase.DiffOptions
	srv := newOpServer(t, RepoOps{Diff: &stubDiffRunner{run: func(_ context.Context, _ string, _ []string, opts usecase.DiffOptions) ([]diff.FileDiff, error) {
		gotOpts = opts
		return files, nil
	}}})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)
	return srv, root, &gotOpts
}

func runDiffFiles(t *testing.T, srv *Server, req *daemonpb.DiffRequest) *diffFilesTestStream {
	t.Helper()
	stream := &diffFilesTestStream{ctx: context.Background()}
	require.NoError(t, srv.DiffFiles(req, stream))
	return stream
}

func TestServer_DiffFilesTextChange(t *testing.T) {
	hash := serverDomain.Hash{0xab, 0xcd}
	modified := diff.FileDiff{
		Change: diff.Change{
			Path:   "src/main.go",
			Status: diff.Modified,
			Old:    diff.Entry{Path: "src/main.go", Mode: 2, SizeBytes: 8, Hash: serverDomain.Hash{1}},
			New:    diff.Entry{Path: "src/main.go", Mode: 2, SizeBytes: 12, Hash: hash},
		},
		Old: []byte("a\nb\nc\n"),
		New: []byte("a\nB\nc\nd\n"),
	}
	srv, root, gotOpts := diffFilesServer(t, modified)

	stream := runDiffFiles(t, srv, &daemonpb.DiffRequest{Root: root, Paths: []string{"src"}, Staged: true, Format: "stat"})
	require.Nil(t, stream.failure())
	require.Equal(t, []string{"src"}, gotOpts.Paths)
	require.True(t, gotOpts.Staged)

	files := stream.files()
	require.Len(t, files, 1)
	f := files[0]
	require.Equal(t, "src/main.go", f.GetPath())
	require.Equal(t, "modified", f.GetStatus())
	require.Equal(t, "text", f.GetKind())
	require.Equal(t, hash.String(), f.GetNew().GetHash())
	require.EqualValues(t, 8, f.GetOld().GetSizeBytes())
	require.EqualValues(t, 12, f.GetNew().GetSizeBytes())
	require.EqualValues(t, 2, f.GetAdditions())
	require.EqualValues(t, 1, f.GetDeletions())
	require.False(t, f.GetHunksOmitted())

	require.Len(t, f.GetHunks(), 1)
	hunk := f.GetHunks()[0]
	require.EqualValues(t, 1, hunk.GetOldStart())
	var kinds []string
	for _, line := range hunk.GetLines() {
		kinds = append(kinds, line.GetKind()+":"+line.GetText())
		switch line.GetKind() {
		case "add":
			require.Nil(t, line.OldLine)
			require.NotNil(t, line.NewLine)
		case "remove":
			require.NotNil(t, line.OldLine)
			require.Nil(t, line.NewLine)
		default:
			require.NotNil(t, line.OldLine)
			require.NotNil(t, line.NewLine)
		}
	}
	require.Equal(t, []string{"context:a", "remove:b", "add:B", "context:c", "add:d"}, kinds)
}

func TestServer_DiffFilesBinaryAndSides(t *testing.T) {
	files := []diff.FileDiff{
		{
			Change: diff.Change{
				Path: "art/hero.png", Status: diff.Modified,
				Old: diff.Entry{Path: "art/hero.png", IsBinary: true, SizeBytes: 100, Hash: serverDomain.Hash{1}},
				New: diff.Entry{Path: "art/hero.png", IsBinary: true, SizeBytes: 120, Hash: serverDomain.Hash{2}},
			},
			Old: []byte{0x89, 'P', 'N', 'G'}, New: []byte{0x89, 'P', 'N', 'G', 0},
		},
		{
			Change: diff.Change{Path: "sfx/old.wav", Status: diff.Deleted, Old: diff.Entry{Path: "sfx/old.wav", IsBinary: true}},
		},
		addedFileDiff("loc/en.csv", "key,value\nhi,hello\n"),
		{
			Change: diff.Change{
				Path: "models/new.glb", Status: diff.Renamed, Similarity: 87,
				Old: diff.Entry{Path: "models/old.glb", IsBinary: true, Hash: serverDomain.Hash{3}},
				New: diff.Entry{Path: "models/new.glb", IsBinary: true, Hash: serverDomain.Hash{4}},
			},
			NewUnavailable: true,
		},
	}
	srv, root, _ := diffFilesServer(t, files...)

	got := runDiffFiles(t, srv, &daemonpb.DiffRequest{Root: root}).files()
	require.Len(t, got, 4)

	png := got[0]
	require.Equal(t, "image", png.GetKind())
	require.Empty(t, png.GetHunks())
	require.True(t, png.GetOld().GetIsBinary())
	require.Zero(t, png.GetAdditions())

	wav := got[1]
	require.Equal(t, "deleted", wav.GetStatus())
	require.Equal(t, "audio", wav.GetKind())
	require.NotNil(t, wav.GetOld())
	require.Nil(t, wav.GetNew())

	csv := got[2]
	require.Equal(t, "added", csv.GetStatus())
	require.Equal(t, "table", csv.GetKind())
	require.Nil(t, csv.GetOld())
	require.Empty(t, csv.GetNew().GetHash())
	require.EqualValues(t, 2, csv.GetAdditions())

	glb := got[3]
	require.Equal(t, "renamed", glb.GetStatus())
	require.Equal(t, "model", glb.GetKind())
	require.EqualValues(t, 87, glb.GetSimilarity())
	require.Equal(t, "models/old.glb", glb.GetOld().GetPath())
	require.True(t, glb.GetContentUnavailable())
}

func TestServer_DiffFilesOmitsOversizedHunks(t *testing.T) {
	big := strings.Repeat(strings.Repeat("x", 1023)+"\n", 1100)
	srv, root, _ := diffFilesServer(t, addedFileDiff("data/big.json", big))

	got := runDiffFiles(t, srv, &daemonpb.DiffRequest{Root: root}).files()
	require.Len(t, got, 1)
	require.True(t, got[0].GetHunksOmitted())
	require.Empty(t, got[0].GetHunks())
	require.EqualValues(t, 1100, got[0].GetAdditions())
}

func TestServer_DiffFilesOmitsManyShortLines(t *testing.T) {
	many := strings.Repeat("x\n", 100000)
	srv, root, _ := diffFilesServer(t, addedFileDiff("data/many.txt", many))

	got := runDiffFiles(t, srv, &daemonpb.DiffRequest{Root: root}).files()
	require.Len(t, got, 1)
	require.True(t, got[0].GetHunksOmitted())
	require.EqualValues(t, 100000, got[0].GetAdditions())
}

func TestServer_DiffFilesIgnoresWhitespaceOnlyChanges(t *testing.T) {
	ws := diff.FileDiff{
		Change: diff.Change{
			Path: "a.txt", Status: diff.Modified,
			Old: diff.Entry{Path: "a.txt", Hash: serverDomain.Hash{1}},
			New: diff.Entry{Path: "a.txt", Hash: serverDomain.Hash{2}},
		},
		Old: []byte("a b\n"), New: []byte("a  b\n"),
	}
	srv, root, _ := diffFilesServer(t, ws)

	require.Len(t, runDiffFiles(t, srv, &daemonpb.DiffRequest{Root: root}).files(), 1)
	require.Empty(t, runDiffFiles(t, srv, &daemonpb.DiffRequest{Root: root, IgnoreAllSpace: true}).files())
}

func TestServer_DiffFilesFailures(t *testing.T) {
	srv, root, _ := diffFilesServer(t)

	stream := runDiffFiles(t, srv, &daemonpb.DiffRequest{Root: root, Context: -1})
	require.EqualValues(t, 400, stream.failure().GetCode())

	stream = runDiffFiles(t, srv, &daemonpb.DiffRequest{Root: root, Staged: true, Revisions: []string{"main"}})
	require.EqualValues(t, 400, stream.failure().GetCode())

	stream = runDiffFiles(t, srv, &daemonpb.DiffRequest{Root: newTestClone(t)})
	require.Contains(t, stream.failure().GetMessage(), "not watched")

	noRunner := newOpServer(t, RepoOps{})
	_, err := noRunner.repos.watch(root)
	require.NoError(t, err)
	stream = runDiffFiles(t, noRunner, &daemonpb.DiffRequest{Root: root})
	require.EqualValues(t, 409, stream.failure().GetCode())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cancelled := &diffFilesTestStream{ctx: ctx}
	require.NoError(t, srv.DiffFiles(&daemonpb.DiffRequest{Root: root}, cancelled))
	require.EqualValues(t, 499, cancelled.failure().GetCode())
}

func TestServer_DiffFilesRunnerError(t *testing.T) {
	root := newTestClone(t)
	srv := newOpServer(t, RepoOps{Diff: &stubDiffRunner{run: func(context.Context, string, []string, usecase.DiffOptions) ([]diff.FileDiff, error) {
		return nil, errors.New("boom")
	}}})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)

	stream := runDiffFiles(t, srv, &daemonpb.DiffRequest{Root: root})
	require.EqualValues(t, 500, stream.failure().GetCode())
}

func TestServer_DiffFilesStreamErrors(t *testing.T) {
	srv, root, _ := diffFilesServer(t, addedFileDiff("a.txt", "a\n"), addedFileDiff("b.txt", "b\n"))

	failing := &diffFilesTestStream{ctx: context.Background(), failAt: 2}
	require.Error(t, srv.DiffFiles(&daemonpb.DiffRequest{Root: root}, failing))
	require.Len(t, failing.files(), 1)

	ctx, cancel := context.WithCancel(context.Background())
	root2 := newTestClone(t)
	srv2 := newOpServer(t, RepoOps{Diff: &stubDiffRunner{run: func(context.Context, string, []string, usecase.DiffOptions) ([]diff.FileDiff, error) {
		cancel()
		return []diff.FileDiff{addedFileDiff("a.txt", "a\n")}, nil
	}}})
	_, err := srv2.repos.watch(root2)
	require.NoError(t, err)
	require.ErrorIs(t, srv2.DiffFiles(&daemonpb.DiffRequest{Root: root2}, &diffFilesTestStream{ctx: ctx}), context.Canceled)
}
