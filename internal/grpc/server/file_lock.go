package server

import (
	"context"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/usecase"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (n *nipaServer) LockFile(ctx context.Context, req *pb.LockFileRequest) (*pb.LockFileResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}
	var opts []usecase.AcquireOption
	if req.TtlSeconds != nil {
		opts = append(opts, usecase.LockTTLSeconds(req.GetTtlSeconds()))
	}
	lock, err := n.uc.FileLock().Acquire(ctx, project.ID, req.GetBranch(), req.GetPath(), opts...)
	if err != nil {
		return nil, handleError(err)
	}
	return &pb.LockFileResponse{Lock: domainFileLockToPB(lock)}, nil
}

func (n *nipaServer) UnlockFile(ctx context.Context, req *pb.UnlockFileRequest) (*pb.UnlockFileResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}
	if err := n.uc.FileLock().Release(ctx, project.ID, req.GetBranch(), req.GetPath()); err != nil {
		return nil, handleError(err)
	}
	return &pb.UnlockFileResponse{}, nil
}

func (n *nipaServer) ListFileLocks(ctx context.Context, req *pb.ListFileLocksRequest) (*pb.ListFileLocksResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}
	locks, err := n.uc.FileLock().List(ctx, project.ID)
	if err != nil {
		return nil, handleError(err)
	}
	resp := &pb.ListFileLocksResponse{Locks: make([]*pb.FileLockDetail, 0, len(locks))}
	for _, lock := range locks {
		resp.Locks = append(resp.Locks, domainFileLockToPB(lock))
	}
	return resp, nil
}

func domainFileLockToPB(lock *domain.FileLock) *pb.FileLockDetail {
	detail := &pb.FileLockDetail{
		Id:         lock.ID.Base36(),
		Path:       lock.Path,
		Branch:     lock.Branch,
		Global:     lock.BranchID == nil,
		HeldBy:     lock.HeldBy.Base36(),
		HeldByName: lock.HeldByName,
		AcquiredAt: timestamppb.New(lock.AcquiredAt),
	}
	if lock.MergeRequestNumber != nil {
		detail.MergeRequestNumber = lock.MergeRequestNumber
	}
	if lock.ExpiresAt != nil {
		detail.ExpiresAt = timestamppb.New(*lock.ExpiresAt)
	}
	return detail
}
