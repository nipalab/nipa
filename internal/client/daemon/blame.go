package daemon

import (
	"context"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/client/usecase"
)

// BlameRunner attributes the lines of a text file to commits (usecase.Blame).
type BlameRunner interface {
	Run(ctx context.Context, root, path, rev string, progress usecase.BlameProgress) (*usecase.BlameResult, error)
}

type serialBlame struct {
	mu     sync.Mutex
	runner BlameRunner
}

// SerialBlame makes a BlameRunner safe for concurrent admission.
func SerialBlame(runner BlameRunner) BlameRunner {
	return &serialBlame{runner: runner}
}

func (s *serialBlame) Run(ctx context.Context, root, path, rev string, progress usecase.BlameProgress) (*usecase.BlameResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runner.Run(ctx, root, path, rev, progress)
}

// Blame streams progress while it reads the file's history under the shared
// slot, then the attributed lines.
func (s *Server) Blame(req *daemonpb.BlameRequest, stream daemonpb.NipaDaemon_BlameServer) error {
	ctx := stream.Context()
	rp, unref, err := s.repos.ref(req.GetRoot())
	if err != nil {
		return stream.Send(blameFailureEvent(err))
	}
	release, _, err := rp.coord.Acquire(ctx, false)
	if err != nil {
		unref()
		return stream.Send(blameFailureEvent(err))
	}
	defer func() {
		release()
		rp.pruner.request()
		unref()
	}()
	if rp.blame == nil {
		return stream.Send(blameFailureEvent(status.Error(codes.FailedPrecondition, "blame is not configured")))
	}
	progress := func(done, total int) {
		_ = stream.Send(&daemonpb.BlameEvent{Event: &daemonpb.BlameEvent_Progress{
			Progress: &daemonpb.BlameProgress{Done: int32(done), Total: int32(total)},
		}})
	}
	res, err := rp.blame.Run(ctx, rp.root, req.GetPath(), req.GetRevision(), progress)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return stream.Send(blameFailureEvent(err))
	}
	return stream.Send(&daemonpb.BlameEvent{Event: &daemonpb.BlameEvent_Result{Result: blameResult(res)}})
}

func blameResult(res *usecase.BlameResult) *daemonpb.BlameResult {
	out := &daemonpb.BlameResult{Path: res.Path, Revision: res.Revision, Truncated: res.Truncated}
	out.Lines = make([]*daemonpb.BlameLine, len(res.Lines))
	for i, l := range res.Lines {
		out.Lines[i] = &daemonpb.BlameLine{Text: l.Text, CommitId: l.CommitID, Uncommitted: l.Uncommitted}
	}
	for _, c := range res.Commits {
		out.Commits = append(out.Commits, &daemonpb.BlameCommit{
			Id:          c.ID,
			Hash:        c.Hash,
			AuthorName:  c.AuthorName,
			AuthorEmail: c.AuthorEmail,
			Message:     c.Message,
			CreatedAt:   c.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	return out
}

func blameFailureEvent(err error) *daemonpb.BlameEvent {
	return &daemonpb.BlameEvent{Event: &daemonpb.BlameEvent_Failure{
		Failure: &daemonpb.OpFailure{Code: failureCode(err), Message: err.Error()},
	}}
}
