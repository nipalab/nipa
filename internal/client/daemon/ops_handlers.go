package daemon

import (
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/client/usecase"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Update fetches the branch head and materializes it in the working copy.
func (s *Server) Update(req *daemonpb.UpdateRequest, stream daemonpb.NipaDaemon_UpdateServer) error {
	ctx := stream.Context()
	return s.runOp(ctx, req.GetRoot(), "update", stream.Send, func(rp *repo, progress *progressAdapter) (*daemonpb.OpEvent, error) {
		if rp.update == nil {
			return nil, status.Error(codes.FailedPrecondition, "update is not configured")
		}
		if err := rp.update.Run(ctx, rp.root, progress); err != nil {
			return nil, err
		}
		return syncResultEvent(rp.config().Branch, rp.headCommitID()), nil
	})
}

// Switch changes the configured branch and materializes its head, or detaches
// HEAD at a tag when the request carries one.
func (s *Server) Switch(req *daemonpb.SwitchRequest, stream daemonpb.NipaDaemon_SwitchServer) error {
	ctx := stream.Context()
	return s.runOp(ctx, req.GetRoot(), "switch", stream.Send, func(rp *repo, progress *progressAdapter) (*daemonpb.OpEvent, error) {
		if rp.update == nil {
			return nil, status.Error(codes.FailedPrecondition, "switch is not configured")
		}
		if tag := req.GetTag(); tag != "" {
			if err := rp.update.SwitchTag(ctx, rp.root, tag, progress); err != nil {
				return nil, err
			}
			return syncResultEvent(rp.config().Branch, rp.headCommitID()), nil
		}
		if err := rp.update.Switch(ctx, rp.root, req.GetBranch(), progress); err != nil {
			return nil, err
		}
		return syncResultEvent(req.GetBranch(), rp.headCommitID()), nil
	})
}

// Push uploads the staged set and commits it on the configured branch.
func (s *Server) Push(req *daemonpb.PushRequest, stream daemonpb.NipaDaemon_PushServer) error {
	ctx := stream.Context()
	return s.runOp(ctx, req.GetRoot(), "push", stream.Send, func(rp *repo, progress *progressAdapter) (*daemonpb.OpEvent, error) {
		if rp.push == nil {
			return nil, status.Error(codes.FailedPrecondition, "push is not configured")
		}
		if err := rp.push.Run(ctx, rp.root, req.GetMessage(), progress); err != nil {
			return nil, err
		}
		commit, err := rp.localRepo.LoadCommit()
		if err != nil {
			return nil, err
		}
		treeHash := ""
		if snap, err := rp.localRepo.Snapshot(); err == nil {
			treeHash = snap.TreeHash
		}
		return opResultEvent(&daemonpb.OpResult{Outcome: &daemonpb.OpResult_Push{
			Push: &daemonpb.PushResult{
				CommitId:   commit.CommitID,
				CommitHash: commit.CommitHash,
				TreeHash:   treeHash,
			},
		}}), nil
	})
}

// Merge merges a source branch into the configured branch. Conflicts are part
// of the result, not a failure: the working copy keeps merge markers for the
// user to resolve.
func (s *Server) Merge(req *daemonpb.MergeOpRequest, stream daemonpb.NipaDaemon_MergeServer) error {
	ctx := stream.Context()
	return s.runOp(ctx, req.GetRoot(), "merge", stream.Send, func(rp *repo, _ *progressAdapter) (*daemonpb.OpEvent, error) {
		if rp.merge == nil {
			return nil, status.Error(codes.FailedPrecondition, "merge is not configured")
		}
		outcome, err := rp.merge.Run(ctx, rp.root, req.GetSourceBranch(), usecase.MergeOptions{
			Abort:   req.GetAbort(),
			FFOnly:  req.GetFfOnly(),
			NoFF:    req.GetNoFf(),
			Message: req.GetMessage(),
		})
		if err != nil {
			return nil, err
		}
		result := &daemonpb.MergeResult{Aborted: req.GetAbort()}
		if outcome != nil {
			result.UpToDate = outcome.UpToDate
			result.FastForwarded = outcome.FastForwarded
			result.MergeCommitted = outcome.MergeCommitted
			result.Conflicts = outcome.Conflicts
		}
		return opResultEvent(&daemonpb.OpResult{Outcome: &daemonpb.OpResult_Merge{Merge: result}}), nil
	})
}

// Revert reverts one commit or a range, including the continue/skip/abort
// resumption modes.
func (s *Server) Revert(req *daemonpb.RevertOpRequest, stream daemonpb.NipaDaemon_RevertServer) error {
	ctx := stream.Context()
	return s.runOp(ctx, req.GetRoot(), "revert", stream.Send, func(rp *repo, progress *progressAdapter) (*daemonpb.OpEvent, error) {
		if rp.revert == nil {
			return nil, status.Error(codes.FailedPrecondition, "revert is not configured")
		}
		outcome, err := rp.revert.Run(ctx, rp.root, req.GetTarget(), usecase.RevertOptions{
			Abort:    req.GetAbort(),
			Continue: req.GetContinue(),
			Skip:     req.GetSkip(),
			NoCommit: req.GetNoCommit(),
			Mainline: int(req.GetMainline()),
			Message:  req.GetMessage(),
		}, progress)
		if err != nil {
			return nil, err
		}
		result := &daemonpb.RevertResult{Aborted: req.GetAbort(), Skipped: req.GetSkip()}
		if outcome != nil {
			result.Committed = outcome.Committed
			result.NoChange = outcome.NoChange
			result.Conflicts = outcome.Conflicts
		}
		return opResultEvent(&daemonpb.OpResult{Outcome: &daemonpb.OpResult_Revert{Revert: result}}), nil
	})
}
