package service

import (
	"context"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/client/usecase"
)

type DiskUsage struct {
	WorkingFiles int64    `json:"workingFiles"`
	WorkingBytes int64    `json:"workingBytes"`
	CacheObjects int64    `json:"cacheObjects"`
	CacheBytes   int64    `json:"cacheBytes"`
	Pins         []string `json:"pins"`
	Sparse       []string `json:"sparse"`
}

type CleanUpResult struct {
	Objects int64  `json:"objects"`
	Bytes   int64  `json:"bytes"`
	Skipped bool   `json:"skipped"`
	Reason  string `json:"reason"`
}

// DiskUsage reports the tracked project files, the local object cache and the
// lock pins of a clone.
func (s *Service) DiskUsage(ctx context.Context, root string) (DiskUsage, error) {
	out := DiskUsage{Pins: []string{}, Sparse: []string{}}
	client, err := s.client(ctx, root)
	if err != nil {
		return out, err
	}
	resp, err := client.DiskUsage(ctx, &daemonpb.DiskUsageRequest{Root: root})
	if err != nil {
		s.dropIfDisconnected(err)
		return out, cleanError(err)
	}
	out.WorkingFiles = resp.GetWorkingFiles()
	out.WorkingBytes = resp.GetWorkingBytes()
	out.CacheObjects = resp.GetCacheObjects()
	out.CacheBytes = resp.GetCacheBytes()
	out.Pins = append(out.Pins, resp.GetPinnedPaths()...)
	out.Sparse = append(out.Sparse, resp.GetSparse()...)
	return out, nil
}

// CleanUp drops cached objects the clone no longer needs. Reason is "busy"
// when another process holds the repository and "pending" during a merge or
// revert.
func (s *Service) CleanUp(ctx context.Context, root string) (CleanUpResult, error) {
	client, err := s.client(ctx, root)
	if err != nil {
		return CleanUpResult{}, err
	}
	resp, err := client.GC(ctx, &daemonpb.GCRequest{Root: root})
	if err != nil {
		s.dropIfDisconnected(err)
		return CleanUpResult{}, cleanError(err)
	}
	out := CleanUpResult{Objects: resp.GetObjects(), Bytes: resp.GetBytes(), Skipped: resp.GetSkipped(), Reason: resp.GetSkipReason()}
	switch out.Reason {
	case usecase.PruneSkipBusy:
		out.Reason = "busy"
	case usecase.PruneSkipPending:
		out.Reason = "pending"
	}
	return out, nil
}
