package daemon

import (
	"context"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/client/usecase"
)

// DiskUsage reports what the clone occupies locally under the shared slot:
// snapshot sizes summed in SQL and one pass over the object cache.
func (s *Server) DiskUsage(ctx context.Context, req *daemonpb.DiskUsageRequest) (*daemonpb.DiskUsageResponse, error) {
	var res *daemonpb.DiskUsageResponse
	err := s.repos.withRepo(ctx, req.GetRoot(), false, func(rp *repo) error {
		usage, err := rp.localRepo.DiskUsage()
		if err != nil {
			return err
		}
		res = &daemonpb.DiskUsageResponse{
			WorkingFiles: usage.WorkingFiles,
			WorkingBytes: usage.WorkingBytes,
			CacheObjects: usage.CacheObjects,
			CacheBytes:   usage.CacheBytes,
			PinnedPaths:  append([]string{}, usage.Pins...),
			Sparse:       append([]string{}, rp.config().Sparse...),
		}
		return nil
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return res, nil
}

// GC prunes the object cache like nipa gc. It queues for the exclusive slot,
// so it never overlaps an operation or the idle pruner, and a completed run
// cancels the pruner's pending request.
func (s *Server) GC(ctx context.Context, req *daemonpb.GCRequest) (*daemonpb.GCResponse, error) {
	var res usecase.PruneResult
	err := s.repos.withRepo(ctx, req.GetRoot(), true, func(rp *repo) error {
		var err error
		res, err = usecase.PruneCache(rp.localRepo)
		if err == nil && !res.Skipped {
			rp.pruner.settle()
		}
		return err
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return &daemonpb.GCResponse{
		Objects:    int64(res.Objects),
		Bytes:      res.Bytes,
		Skipped:    res.Skipped,
		SkipReason: res.SkipReason,
	}, nil
}
