package daemon

import (
	"context"
	"strings"
	"sync"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/diff"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// DiffRunner compares working-copy and revision states.
type DiffRunner interface {
	Run(ctx context.Context, root string, revs []string, opts usecase.DiffOptions) ([]diff.FileDiff, error)
}

// serialDiff serializes calls into one DiffRunner. The daemon admits diffs
// concurrently through the shared coordinator slot, but a single
// usecase.Diff shares one localrepo handle whose Init rebinds the SQLite
// connection on every run, so overlapping runs would race and close each
// other's handle.
type serialDiff struct {
	mu     sync.Mutex
	runner DiffRunner
}

// SerialDiff makes a DiffRunner safe for concurrent admission.
func SerialDiff(runner DiffRunner) DiffRunner {
	return &serialDiff{runner: runner}
}

func (s *serialDiff) Run(ctx context.Context, root string, revs []string, opts usecase.DiffOptions) ([]diff.FileDiff, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runner.Run(ctx, root, revs, opts)
}

const (
	diffChunkBytes       = 64 << 10
	diffFormatPatch      = "patch"
	diffFormatStat       = "stat"
	diffFormatNameOnly   = "name_only"
	diffFormatNameStatus = "name_status"
)

// Diff streams the rendered diff. Patch output is rendered file by file and
// sent in bounded chunks; stat/name formats are single payloads. Output is
// always plain text: color and pagination belong to the client.
func (s *Server) Diff(req *daemonpb.DiffRequest, stream daemonpb.NipaDaemon_DiffServer) error {
	ctx := stream.Context()

	revs, rangeMergeBase, err := expandDiffRevisions(req.GetRevisions())
	if err != nil {
		return stream.Send(diffFailureEvent(err))
	}
	mergeBase := req.GetMergeBase() || rangeMergeBase
	if mergeBase && len(revs) != 2 {
		return stream.Send(diffFailureEvent(clientDomain.NewUserError("a three-dot diff requires two revisions")))
	}
	if len(revs) > 0 && req.GetStaged() {
		return stream.Send(diffFailureEvent(clientDomain.NewUserError("staged cannot be combined with revisions")))
	}
	format := req.GetFormat()
	if format == "" {
		format = diffFormatPatch
	}
	switch format {
	case diffFormatPatch, diffFormatStat, diffFormatNameOnly, diffFormatNameStatus:
	default:
		return stream.Send(diffFailureEvent(clientDomain.NewUserError("unknown diff format " + format)))
	}
	if req.GetContext() < 0 {
		return stream.Send(diffFailureEvent(clientDomain.NewUserError("context must not be negative")))
	}
	renderOpts := diff.Options{
		Context:           int(req.GetContext()),
		IgnoreAllSpace:    req.GetIgnoreAllSpace(),
		IgnoreSpaceChange: req.GetIgnoreSpaceChange(),
	}
	if renderOpts.Context == 0 {
		renderOpts.Context = diff.DefaultContext
	}

	rp, unref, err := s.repos.ref(req.GetRoot())
	if err != nil {
		return stream.Send(diffFailureEvent(err))
	}
	defer unref()
	release, _, err := rp.coord.Acquire(ctx, false)
	if err != nil {
		return stream.Send(diffFailureEvent(err))
	}
	defer release()

	if rp.diff == nil {
		return stream.Send(diffFailureEvent(status.Error(codes.FailedPrecondition, "diff is not configured")))
	}
	files, err := rp.diff.Run(ctx, rp.root, revs, usecase.DiffOptions{
		Staged:    req.GetStaged(),
		Paths:     req.GetPaths(),
		MergeBase: mergeBase,
		NoCache:   req.GetNoCache(),
	})
	if err != nil {
		return stream.Send(diffFailureEvent(err))
	}
	files = diff.FilterIgnored(files, renderOpts)

	switch format {
	case diffFormatStat:
		return sendDiffLines(stream, diff.Stat(files))
	case diffFormatNameOnly:
		return sendDiffLines(stream, diff.NameOnly(files))
	case diffFormatNameStatus:
		return sendDiffLines(stream, diff.NameStatus(files))
	default:
		for _, f := range files {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := sendDiffLines(stream, diff.FilePatch(f, renderOpts)); err != nil {
				return err
			}
		}
		return nil
	}
}

func sendDiffLines(stream daemonpb.NipaDaemon_DiffServer, lines []string) error {
	var buf strings.Builder
	for _, line := range lines {
		buf.WriteString(line)
		buf.WriteByte('\n')
		if buf.Len() >= diffChunkBytes {
			if err := sendDiffChunk(stream, buf.String()); err != nil {
				return err
			}
			buf.Reset()
		}
	}
	if buf.Len() == 0 {
		return nil
	}
	return sendDiffChunk(stream, buf.String())
}

func sendDiffChunk(stream daemonpb.NipaDaemon_DiffServer, chunk string) error {
	return stream.Send(&daemonpb.DiffEvent{Event: &daemonpb.DiffEvent_Data{Data: []byte(chunk)}})
}

func diffFailureEvent(err error) *daemonpb.DiffEvent {
	return &daemonpb.DiffEvent{Event: &daemonpb.DiffEvent_Failure{
		Failure: &daemonpb.OpFailure{Code: failureCode(err), Message: err.Error()},
	}}
}

// expandDiffRevisions mirrors the CLI's <a>..<b> / <a>...<b> expansion so API
// clients that send a range get the same semantics.
func expandDiffRevisions(revs []string) ([]string, bool, error) {
	if len(revs) != 1 {
		return revs, false, nil
	}
	token := revs[0]
	if a, b, ok := strings.Cut(token, "..."); ok {
		if a == "" || b == "" {
			return nil, false, clientDomain.NewUserError("a revision range must be <a>...<b>")
		}
		return []string{a, b}, true, nil
	}
	if a, b, ok := strings.Cut(token, ".."); ok {
		if a == "" || b == "" {
			return nil, false, clientDomain.NewUserError("a revision range must be <a>..<b>")
		}
		return []string{a, b}, false, nil
	}
	return revs, false, nil
}
