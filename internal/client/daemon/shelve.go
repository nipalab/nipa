package daemon

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/diff"
	"github.com/nipalab/nipa/internal/preview"
)

func (r *repo) shelve() *usecase.Shelve {
	return usecase.NewShelve(r.localRepo, r.wc, r.root)
}

// ShelveCreate parks the selected changes in a new shelf and puts those
// files back to the synced version, under the exclusive slot.
func (s *Server) ShelveCreate(req *daemonpb.ShelveCreateRequest, stream daemonpb.NipaDaemon_ShelveCreateServer) error {
	ctx := stream.Context()
	return s.runOp(ctx, req.GetRoot(), "shelve", stream.Send, func(rp *repo, _ *progressAdapter) (*daemonpb.OpEvent, error) {
		shelf, err := rp.shelve().Create(ctx, usecase.ShelveOptions{Name: req.GetName(), Paths: req.GetPaths(), IncludeUntracked: req.GetIncludeUntracked()})
		if err != nil {
			return nil, err
		}
		return opResultEvent(&daemonpb.OpResult{Outcome: &daemonpb.OpResult_Shelve{Shelve: &daemonpb.ShelveResult{Shelf: shelfToPB(*shelf)}}}), nil
	})
}

// ShelveApply merges a shelf into the working copy, or with abort stops the
// apply waiting on conflicts. Conflicts are part of the result.
func (s *Server) ShelveApply(req *daemonpb.ShelveApplyRequest, stream daemonpb.NipaDaemon_ShelveApplyServer) error {
	ctx := stream.Context()
	return s.runOp(ctx, req.GetRoot(), "shelve", stream.Send, func(rp *repo, _ *progressAdapter) (*daemonpb.OpEvent, error) {
		sh := rp.shelve()
		if req.GetAbort() {
			state, err := rp.localRepo.LoadShelfState()
			if err != nil {
				return nil, err
			}
			if err := sh.Abort(ctx); err != nil {
				return nil, err
			}
			return shelveApplyEvent(&daemonpb.ShelveApplyResult{ShelfId: state.ShelfID, Name: state.ShelfName, Aborted: true}), nil
		}
		res, err := sh.Apply(ctx, req.GetId(), req.GetKeep())
		if err != nil {
			return nil, err
		}
		return shelveApplyEvent(&daemonpb.ShelveApplyResult{ShelfId: res.Shelf.ID, Name: res.Shelf.Name, Conflicts: res.Conflicts, Dropped: res.Dropped}), nil
	})
}

func shelveApplyEvent(res *daemonpb.ShelveApplyResult) *daemonpb.OpEvent {
	return opResultEvent(&daemonpb.OpResult{Outcome: &daemonpb.OpResult_ShelveApply{ShelveApply: res}})
}

func (s *Server) ShelveList(ctx context.Context, req *daemonpb.ShelveListRequest) (*daemonpb.ShelveListResponse, error) {
	resp := &daemonpb.ShelveListResponse{}
	err := s.repos.withRepo(ctx, req.GetRoot(), false, func(rp *repo) error {
		shelves, err := rp.shelve().List()
		if err != nil {
			return err
		}
		for _, sh := range shelves {
			resp.Shelves = append(resp.Shelves, shelfToPB(sh))
		}
		state, err := rp.localRepo.LoadShelfState()
		if err != nil {
			return err
		}
		if state != nil {
			resp.Applying = state.ShelfID
		}
		return nil
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return resp, nil
}

func (s *Server) ShelveDrop(ctx context.Context, req *daemonpb.ShelveDropRequest) (*daemonpb.ShelveDropResponse, error) {
	err := s.repos.withRepo(ctx, req.GetRoot(), false, func(rp *repo) error {
		return rp.shelve().Drop(req.GetId())
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return &daemonpb.ShelveDropResponse{}, nil
}

// ShelveFiles describes a shelf's changes like DiffFiles, so GUIs reuse
// their viewers with the shelf_base and shelf blob sources.
func (s *Server) ShelveFiles(ctx context.Context, req *daemonpb.ShelveFilesRequest) (*daemonpb.ShelveFilesResponse, error) {
	resp := &daemonpb.ShelveFilesResponse{}
	err := s.repos.withRepo(ctx, req.GetRoot(), false, func(rp *repo) error {
		shelf, files, err := rp.shelve().Show(req.GetId())
		if err != nil {
			return err
		}
		resp.Shelf = shelfToPB(*shelf)
		for _, f := range files {
			resp.Files = append(resp.Files, diffFileToPB(f, diff.Options{}))
		}
		return nil
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return resp, nil
}

// SyncPlan lists the local changes an update, branch switch or tag checkout
// would replace, so a GUI can offer to shelve them first.
func (s *Server) SyncPlan(ctx context.Context, req *daemonpb.SyncPlanRequest) (*daemonpb.SyncPlanResponse, error) {
	resp := &daemonpb.SyncPlanResponse{}
	err := s.repos.withRepo(ctx, req.GetRoot(), false, func(rp *repo) error {
		if rp.update == nil {
			return status.Error(codes.FailedPrecondition, "update is not configured")
		}
		target := usecase.SyncTarget{Branch: req.GetBranch(), Tag: req.GetTag()}
		tree, err := rp.update.TargetTree(ctx, rp.root, target)
		if err != nil {
			return err
		}
		st, err := rp.wc.Status(ctx)
		if err != nil {
			return err
		}
		snapshot, err := rp.localRepo.Snapshot()
		if err != nil {
			return err
		}
		resp.Overwritten = usecase.OverwrittenPaths(snapshot, tree, st, target.Branch != "" || target.Tag != "")
		return nil
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return resp, nil
}

func shelfToPB(s clientDomain.Shelf) *daemonpb.Shelf {
	out := &daemonpb.Shelf{
		Id:           s.ID,
		Name:         s.Name,
		CreatedAt:    s.CreatedAt.Unix(),
		Branch:       s.Branch,
		BaseCommitId: s.BaseCommitID,
		SizeBytes:    s.SizeBytes,
	}
	if s.Head != nil {
		out.Tag = s.Head.Name
	}
	for _, e := range s.Entries {
		entry := &daemonpb.ShelfEntry{Path: e.Path, Status: e.Status, Staged: e.Staged, Untracked: e.Untracked, Locked: e.Locked}
		side := e.File
		if side == nil {
			side = e.Base
		}
		if side != nil {
			entry.SizeBytes, entry.IsBinary = side.SizeBytes, side.IsBinary
		}
		entry.Kind = string(preview.Classify(e.Path, entry.IsBinary))
		out.Entries = append(out.Entries, entry)
	}
	return out
}
