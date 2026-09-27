package daemon

import (
	"context"
	"errors"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/client/usecase"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Runners builds the operation graph for a watched root. The daemon calls New
// once per repo: the usecases and the proxy connection hold per-repo state
// (localrepo handles, the server host binding), so roots never share them.
type Runners struct {
	New func(root string) RepoOps
}

// RepoOps is the long-operation and proxy surface of one watched repo.
type RepoOps struct {
	Update UpdateRunner
	Push   PushRunner
	Merge  MergeRunner
	Revert RevertRunner
	Diff   DiffRunner
	Proxy  ProxyConn
}

type UpdateRunner interface {
	Run(ctx context.Context, root string, progress ...usecase.DownloadProgress) error
	Switch(ctx context.Context, root, branch string, progress ...usecase.DownloadProgress) error
}

type PushRunner interface {
	Run(ctx context.Context, root, message string, progress ...usecase.UploadProgress) error
}

type MergeRunner interface {
	Run(ctx context.Context, root, sourceBranch string, opts usecase.MergeOptions) (*usecase.Outcome, error)
}

type RevertRunner interface {
	Run(ctx context.Context, root, target string, opts usecase.RevertOptions, progress ...usecase.UploadProgress) (*usecase.RevertOutcome, error)
}

type opFunc func(rp *repo, progress *progressAdapter) (*daemonpb.OpEvent, error)

// runOp executes one long operation under the root's exclusive slot and turns
// it into an OpEvent stream: queued (when the slot is busy), started, progress
// events, then exactly one terminal result or failure event. The exclusive
// admission is what keeps a status poll from racing a running push.
func (s *Server) runOp(ctx context.Context, root, phase string, send func(*daemonpb.OpEvent) error, fn opFunc) error {
	rp, unref, err := s.repos.ref(root)
	if err != nil {
		return send(opFailureEvent(err))
	}
	defer unref()

	if busy, ahead := rp.coord.pending(); busy {
		if err := send(opQueuedEvent(ahead)); err != nil {
			return err
		}
	}
	release, _, err := rp.coord.Acquire(ctx, true)
	if err != nil {
		return send(opFailureEvent(err))
	}
	defer release()

	if err := send(opStartedEvent(phase)); err != nil {
		return err
	}

	progress := newProgressAdapter()
	done := make(chan *daemonpb.OpEvent, 1)
	go func() {
		result, err := fn(rp, progress)
		if err != nil {
			done <- opFailureEvent(err)
			return
		}
		done <- result
	}()

	for {
		select {
		case <-ctx.Done():
			// The stream is gone; still emit the terminal failure so the
			// contract holds. The send may fail and that is fine. Do not
			// release the exclusive slot before the operation stops: the
			// usecases only observe ctx at network boundaries (hash loops do
			// not at all), so releasing early would let the next operation
			// race an abandoned one on the same working copy.
			_ = send(opFailureEvent(ctx.Err()))
			<-done
			return nil
		case ev := <-progress.events():
			if err := send(ev); err != nil {
				<-done // the stream is gone, but the operation is still running
				return err
			}
		case terminal := <-done:
			for {
				select {
				case ev := <-progress.events():
					if err := send(ev); err != nil {
						return err
					}
				default:
					return send(terminal)
				}
			}
		}
	}
}

func opQueuedEvent(ahead int) *daemonpb.OpEvent {
	return &daemonpb.OpEvent{Event: &daemonpb.OpEvent_Queued{
		Queued: &daemonpb.OpQueued{Ahead: int32(ahead)},
	}}
}

func opStartedEvent(phase string) *daemonpb.OpEvent {
	return &daemonpb.OpEvent{Event: &daemonpb.OpEvent_Started{
		Started: &daemonpb.OpStarted{Phase: phase},
	}}
}

func opProgressEvent(phase string, objectsDone, objectsTotal, bytesDone, bytesTotal int64) *daemonpb.OpEvent {
	return &daemonpb.OpEvent{Event: &daemonpb.OpEvent_Progress{
		Progress: &daemonpb.OpProgress{
			Phase:        phase,
			ObjectsDone:  objectsDone,
			ObjectsTotal: objectsTotal,
			BytesDone:    bytesDone,
			BytesTotal:   bytesTotal,
		},
	}}
}

func opResultEvent(result *daemonpb.OpResult) *daemonpb.OpEvent {
	return &daemonpb.OpEvent{Event: &daemonpb.OpEvent_Result{Result: result}}
}

func opFailureEvent(err error) *daemonpb.OpEvent {
	return &daemonpb.OpEvent{Event: &daemonpb.OpEvent_Failure{
		Failure: &daemonpb.OpFailure{Code: failureCode(err), Message: err.Error()},
	}}
}

// failureCode maps an error onto the HTTP-style code OpFailure carries.
func failureCode(err error) int32 {
	var domErr *clientDomain.Error
	switch {
	case errors.As(err, &domErr):
		return int32(domErr.Code)
	case errors.Is(err, context.Canceled):
		return 499
	case errors.Is(err, context.DeadlineExceeded):
		return 504
	}
	if st, ok := status.FromError(err); ok {
		switch st.Code() {
		case codes.InvalidArgument:
			return 400
		case codes.Unauthenticated:
			return 401
		case codes.PermissionDenied:
			return 403
		case codes.NotFound:
			return 404
		case codes.FailedPrecondition:
			return 409
		case codes.Unimplemented:
			return 501
		}
	}
	return 500
}

func syncResultEvent(branch, commitID string) *daemonpb.OpEvent {
	return opResultEvent(&daemonpb.OpResult{Outcome: &daemonpb.OpResult_Sync{
		Sync: &daemonpb.SyncResult{Branch: branch, CommitId: commitID},
	}})
}
