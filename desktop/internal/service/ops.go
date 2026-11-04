package service

import (
	"context"
	"errors"
	"io"

	"google.golang.org/grpc"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
)

const EventOpProgress = "op:progress"

// OpProgress reports a running push or update. Phase is "queued" while
// another operation holds the clone, then the daemon's phase names (push,
// update, upload, download).
type OpProgress struct {
	Root         string `json:"root"`
	Op           string `json:"op"`
	Phase        string `json:"phase"`
	Ahead        int    `json:"ahead"`
	ObjectsDone  int64  `json:"objectsDone"`
	ObjectsTotal int64  `json:"objectsTotal"`
	BytesDone    int64  `json:"bytesDone"`
	BytesTotal   int64  `json:"bytesTotal"`
}

type OpOutcome struct {
	Branch     string `json:"branch,omitempty"`
	CommitID   string `json:"commitId,omitempty"`
	CommitHash string `json:"commitHash,omitempty"`
}

// Push uploads the staged files and commits them with message. Cancelling the
// call stops the push.
func (s *Service) Push(ctx context.Context, root, message string) (OpOutcome, error) {
	return s.runOp(ctx, root, "push", func(c daemonpb.NipaDaemonClient) (grpc.ServerStreamingClient[daemonpb.OpEvent], error) {
		return c.Push(ctx, &daemonpb.PushRequest{Root: root, Message: message})
	})
}

// Update gets the latest version of the branch into the working copy.
func (s *Service) Update(ctx context.Context, root string) (OpOutcome, error) {
	return s.runOp(ctx, root, "update", func(c daemonpb.NipaDaemonClient) (grpc.ServerStreamingClient[daemonpb.OpEvent], error) {
		return c.Update(ctx, &daemonpb.UpdateRequest{Root: root})
	})
}

func (s *Service) runOp(ctx context.Context, root, op string, open func(daemonpb.NipaDaemonClient) (grpc.ServerStreamingClient[daemonpb.OpEvent], error)) (OpOutcome, error) {
	client, err := s.client(ctx, root)
	if err != nil {
		return OpOutcome{}, err
	}
	return s.streamOp(ctx, client, root, op, open)
}

func (s *Service) streamOp(ctx context.Context, client daemonpb.NipaDaemonClient, root, op string, open func(daemonpb.NipaDaemonClient) (grpc.ServerStreamingClient[daemonpb.OpEvent], error)) (OpOutcome, error) {
	stream, err := open(client)
	if err != nil {
		s.dropIfDisconnected(err)
		return OpOutcome{}, cleanError(err)
	}
	for {
		ev, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return OpOutcome{}, errors.New(op + " ended without a result")
		}
		if err != nil {
			if ctx.Err() != nil {
				return OpOutcome{}, ctx.Err()
			}
			s.dropIfDisconnected(err)
			return OpOutcome{}, cleanError(err)
		}
		progress := OpProgress{Root: root, Op: op}
		switch {
		case ev.GetQueued() != nil:
			progress.Phase = "queued"
			progress.Ahead = int(ev.GetQueued().GetAhead())
		case ev.GetStarted() != nil:
			progress.Phase = ev.GetStarted().GetPhase()
		case ev.GetProgress() != nil:
			p := ev.GetProgress()
			progress.Phase = p.GetPhase()
			progress.ObjectsDone, progress.ObjectsTotal = p.GetObjectsDone(), p.GetObjectsTotal()
			progress.BytesDone, progress.BytesTotal = p.GetBytesDone(), p.GetBytesTotal()
		case ev.GetFailure() != nil:
			return OpOutcome{}, errors.New(ev.GetFailure().GetMessage())
		default:
			return toOutcome(ev.GetResult()), nil
		}
		s.emit(EventOpProgress, progress)
	}
}

func toOutcome(result *daemonpb.OpResult) OpOutcome {
	if push := result.GetPush(); push != nil {
		return OpOutcome{CommitID: push.GetCommitId(), CommitHash: push.GetCommitHash()}
	}
	sync := result.GetSync()
	return OpOutcome{Branch: sync.GetBranch(), CommitID: sync.GetCommitId()}
}
